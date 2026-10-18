//go:build darwin

package command

import (
	"bytes"

	"golang.org/x/sys/unix"
)

// p_stat values from <sys/proc.h>.
const (
	darwinProcStopped = 4 // SSTOP
	darwinProcZombie  = 5 // SZOMB
)

// listProcs reads every process with sysctl KERN_PROC_ALL.
func listProcs() (map[int]procInfo, error) {
	kps, err := unix.SysctlKinfoProcSlice("kern.proc.all")
	if err != nil {
		return nil, err
	}
	procs := make(map[int]procInfo, len(kps))
	for i := range kps {
		info := kinfoToProcInfo(&kps[i])
		procs[info.pid] = info
	}
	return procs, nil
}

// readProc reads one process with sysctl KERN_PROC_PID.
func readProc(pid int) (procInfo, error) {
	kp, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
	if err != nil {
		return procInfo{}, err
	}
	if int(kp.Proc.P_pid) != pid {
		return procInfo{}, unix.ESRCH
	}
	return kinfoToProcInfo(kp), nil
}

func kinfoToProcInfo(kp *unix.KinfoProc) procInfo {
	start := kp.Proc.P_starttime
	return procInfo{
		pid:     int(kp.Proc.P_pid),
		ppid:    int(kp.Eproc.Ppid),
		pgid:    int(kp.Eproc.Pgid),
		uid:     kp.Eproc.Ucred.Uid,
		start:   uint64(start.Sec)*1_000_000 + uint64(start.Usec),
		stopped: kp.Proc.P_stat == darwinProcStopped,
		zombie:  kp.Proc.P_stat == darwinProcZombie,
	}
}

// procEnvContains reports whether the exec-time environment of pid contains
// the exact entry. It reads sysctl KERN_PROCARGS2: a 32-bit argc followed by
// NUL-separated exec path, argv and envp strings. Every string is compared;
// an argv entry equal to the marker can only come from a process that passes
// it on, which makes it a tree member too. It is false when the arguments are
// unreadable (another user's process).
func procEnvContains(pid int, entry string) bool {
	buf, err := unix.SysctlRaw("kern.procargs2", pid)
	if err != nil || len(buf) < 4 {
		return false
	}
	for _, kv := range bytes.Split(buf[4:], []byte{0}) {
		if string(kv) == entry {
			return true
		}
	}
	return false
}
