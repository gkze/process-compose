//go:build linux

package command

import (
	"bytes"
	"fmt"
	"os"
	"strconv"
	"strings"
	"syscall"
)

// listProcs reads every process from procfs.
func listProcs() (map[int]procInfo, error) {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil, err
	}
	procs := make(map[int]procInfo, len(entries))
	for _, entry := range entries {
		pid, err := strconv.Atoi(entry.Name())
		if err != nil {
			continue
		}
		// a process that exits between ReadDir and readProc is skipped
		if info, err := readProc(pid); err == nil {
			procs[pid] = info
		}
	}
	return procs, nil
}

// readProc reads one process from /proc/<pid>/stat (see proc_pid_stat(5)).
func readProc(pid int) (procInfo, error) {
	dir := fmt.Sprintf("/proc/%d", pid)
	data, err := os.ReadFile(dir + "/stat")
	if err != nil {
		return procInfo{}, err
	}
	// The command name (field 2) is parenthesized and may contain spaces and
	// parentheses, so fields are counted from the last ')'.
	end := bytes.LastIndexByte(data, ')')
	if end < 0 {
		return procInfo{}, fmt.Errorf("malformed %s/stat", dir)
	}
	// fields[0] is field 3 (state); field N is fields[N-3].
	fields := strings.Fields(string(data[end+1:]))
	if len(fields) < 20 {
		return procInfo{}, fmt.Errorf("malformed %s/stat", dir)
	}
	ppid, err := strconv.Atoi(fields[1])
	if err != nil {
		return procInfo{}, err
	}
	pgid, err := strconv.Atoi(fields[2])
	if err != nil {
		return procInfo{}, err
	}
	start, err := strconv.ParseUint(fields[19], 10, 64)
	if err != nil {
		return procInfo{}, err
	}
	fi, err := os.Stat(dir)
	if err != nil {
		return procInfo{}, err
	}
	var uid uint32
	if st, ok := fi.Sys().(*syscall.Stat_t); ok {
		uid = st.Uid
	}
	state := fields[0]
	return procInfo{
		pid:     pid,
		ppid:    ppid,
		pgid:    pgid,
		uid:     uid,
		start:   start,
		stopped: state == "T" || state == "t",
		zombie:  state == "Z" || state == "X",
	}, nil
}

// procEnvContains reports whether the initial environment of pid contains the
// exact entry. It is false when the environment is unreadable.
func procEnvContains(pid int, entry string) bool {
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/environ", pid))
	if err != nil {
		return false
	}
	for _, kv := range bytes.Split(data, []byte{0}) {
		if string(kv) == entry {
			return true
		}
	}
	return false
}
