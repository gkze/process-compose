//go:build !windows

package command

import (
	"errors"
	"fmt"
	"os"
	"syscall"

	"github.com/rs/zerolog/log"
)

// procInfo is a snapshot of one process-table entry.
type procInfo struct {
	pid  int
	ppid int
	pgid int
	uid  uint32
	// start is an opaque start-time token; a different token for the same
	// PID means the PID was reused.
	start   uint64
	stopped bool
	zombie  bool
}

// maxTreeWalks bounds the freeze-and-rewalk loop of a tree stop. Each walk
// freezes the members it finds, so only processes forked before their parent
// was frozen can appear in a later walk.
const maxTreeWalks = 10

// stopTree delivers sig to every member of the process tree rooted at the
// launched process:
//
//  1. Walk the process table from the members (see treeState.collect) and freeze
//     each new member with SIGSTOP, so it cannot fork, exit or move away.
//  2. Rewalk until a walk finds no new member.
//  3. Signal every frozen member whose start time still matches, then resume
//     the members this stop froze with SIGCONT.
//
// It returns an errProcTable error when the process table cannot be read;
// the caller then falls back to group mode.
func (c *CmdWrapper) stopTree(sig syscall.Signal) error {
	t := &c.tree
	t.mu.Lock()
	defer t.mu.Unlock()

	marker := treeMarker(c.cmd.Env)
	frozen := map[int]procInfo{}
	resume := !isStopSignal(sig)
	defer func() {
		for _, info := range frozen {
			// Leave processes that were stopped before this stop as they were.
			if resume && !info.stopped && isSameProcess(info) {
				_ = syscall.Kill(info.pid, syscall.SIGCONT)
			}
		}
	}()

	stable := false
	for range maxTreeWalks {
		procs, err := listProcs()
		if err != nil {
			return fmt.Errorf("%w: %w", errProcTable, err)
		}
		grew := false
		for _, info := range t.collect(procs, c.Pid(), marker) {
			if _, ok := frozen[info.pid]; ok {
				continue
			}
			grew = true
			frozen[info.pid] = info
			if !info.stopped {
				_ = syscall.Kill(info.pid, syscall.SIGSTOP)
			}
		}
		if !grew {
			stable = true
			break
		}
	}
	if !stable {
		log.Warn().Int("pid", c.Pid()).Int("members", len(frozen)).
			Msgf("process tree still growing after %d walks, signalling the members found", maxTreeWalks)
	}

	var sigErr error
	signalled := 0
	for _, info := range frozen {
		if !isSameProcess(info) {
			continue
		}
		if err := syscall.Kill(info.pid, sig); err != nil {
			if !errors.Is(err, syscall.ESRCH) && sigErr == nil {
				sigErr = err
			}
			continue
		}
		signalled++
	}
	log.Debug().Int("pid", c.Pid()).Int("signal", int(sig)).Int("members", signalled).Msg("Signalled process tree.")
	return sigErr
}

// isTreeAlive reports whether any member of the process tree is still running.
func (c *CmdWrapper) isTreeAlive() (bool, error) {
	t := &c.tree
	t.mu.Lock()
	defer t.mu.Unlock()
	procs, err := listProcs()
	if err != nil {
		return false, err
	}
	return len(t.collect(procs, c.Pid(), treeMarker(c.cmd.Env))) > 0, nil
}

// collect returns the live members of the tree rooted at root in procs and
// records them as known. The tree is seeded from:
//
//   - root, and its process group when root leads one;
//   - members found by earlier stops (reparented once root exits);
//   - same-user processes whose environment carries marker (descendants that
//     changed group or session and were reparented).
//
// and extended with every descendant of a seed by parent links.
func (t *treeState) collect(procs map[int]procInfo, root int, marker string) []procInfo {
	if t.known == nil {
		t.known = map[int]uint64{}
		t.marked = map[procKey]bool{}
	}
	self, parent := os.Getpid(), os.Getppid()
	euid := uint32(os.Geteuid())

	children := map[int][]int{}
	for pid, info := range procs {
		children[info.ppid] = append(children[info.ppid], pid)
	}

	var seeds []int
	if info, ok := procs[root]; ok && (!t.rootSeen || info.start == t.rootStart) {
		t.rootSeen, t.rootStart = true, info.start
		seeds = append(seeds, root)
		if info.pgid == root {
			for pid, member := range procs {
				if member.pgid == root {
					seeds = append(seeds, pid)
				}
			}
		}
	}
	for pid, start := range t.known {
		if info, ok := procs[pid]; ok && info.start == start {
			seeds = append(seeds, pid)
		} else {
			delete(t.known, pid)
		}
	}
	if marker != "" {
		for key := range t.marked {
			if info, ok := procs[key.pid]; !ok || info.start != key.start {
				delete(t.marked, key)
			}
		}
		for pid, info := range procs {
			if info.uid != euid || info.zombie || pid == self || pid == parent {
				continue
			}
			key := procKey{pid: pid, start: info.start}
			marked, ok := t.marked[key]
			if !ok {
				marked = procEnvContains(pid, marker)
				t.marked[key] = marked
			}
			if marked {
				seeds = append(seeds, pid)
			}
		}
	}

	var members []procInfo
	visited := map[int]bool{}
	for len(seeds) > 0 {
		pid := seeds[len(seeds)-1]
		seeds = seeds[:len(seeds)-1]
		if visited[pid] || pid <= 1 || pid == self || pid == parent {
			continue
		}
		visited[pid] = true
		info, ok := procs[pid]
		if !ok || info.zombie {
			continue
		}
		t.known[pid] = info.start
		members = append(members, info)
		seeds = append(seeds, children[pid]...)
	}
	return members
}

// isSameProcess reports whether info's PID still names the same live process.
func isSameProcess(info procInfo) bool {
	current, err := readProc(info.pid)
	return err == nil && current.start == info.start && !current.zombie
}

func isStopSignal(sig syscall.Signal) bool {
	switch sig {
	case syscall.SIGSTOP, syscall.SIGTSTP, syscall.SIGTTIN, syscall.SIGTTOU:
		return true
	}
	return false
}
