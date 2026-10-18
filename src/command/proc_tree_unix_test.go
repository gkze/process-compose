//go:build linux || darwin

package command

import (
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// The tests re-execute the test binary as the processes of a tree. A helper is
// selected by the treeHelperEnv variable and handled in TestMain.
const (
	treeHelperEnv        = "PC_TREE_TEST_HELPER"
	treeHelperPidFileEnv = "PC_TREE_TEST_PID_FILE"
	treeHelperIgnoreTerm = "PC_TREE_TEST_IGNORE_TERM"
)

func TestMain(m *testing.M) {
	if helper := os.Getenv(treeHelperEnv); helper != "" {
		runTreeHelper(helper)
		os.Exit(0)
	}
	os.Exit(m.Run())
}

// runTreeHelper runs one process of a test tree:
//
//   - escapee: writes its PID to the PID file and sleeps.
//   - escape: starts an escapee child in a new session (setsid) and sleeps as
//     its parent.
//   - orphan: starts an orphan-parent child, waits for it to exit and sleeps.
//   - orphan-parent: starts an escapee child in a new session and exits, so
//     the child is reparented.
//
// Every helper ignores SIGTERM when treeHelperIgnoreTerm is set, before the
// escapee reports its PID.
func runTreeHelper(helper string) {
	if os.Getenv(treeHelperIgnoreTerm) != "" {
		signal.Ignore(syscall.SIGTERM)
	}
	switch helper {
	case "escapee":
		pidFile := os.Getenv(treeHelperPidFileEnv)
		if err := os.WriteFile(pidFile+".tmp", []byte(strconv.Itoa(os.Getpid())), 0o600); err != nil {
			os.Exit(1)
		}
		if err := os.Rename(pidFile+".tmp", pidFile); err != nil {
			os.Exit(1)
		}
	case "escape":
		startTreeHelper("escapee", true)
	case "orphan":
		child := treeHelperCmd("orphan-parent", false)
		if err := child.Run(); err != nil {
			os.Exit(1)
		}
	case "orphan-parent":
		startTreeHelper("escapee", true)
		return
	}
	time.Sleep(time.Minute)
}

func treeHelperCmd(helper string, setsid bool) *exec.Cmd {
	cmd := exec.Command(os.Args[0], "-test.run=^$")
	cmd.Env = append(os.Environ(), treeHelperEnv+"="+helper)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: setsid}
	return cmd
}

func startTreeHelper(helper string, setsid bool) {
	cmd := treeHelperCmd(helper, setsid)
	if err := cmd.Start(); err != nil {
		os.Exit(1)
	}
}

type treeFixture struct {
	cmd    *CmdWrapper
	waited chan struct{}
	// escapee is the descendant that left the session of the launched process.
	escapee procInfo
}

// startTree launches helper the way a process is launched in tree mode: in
// its own process group, with a tree marker unless unmarked is set. It waits
// until the helper has started its escapee.
func startTree(t *testing.T, helper string, ignoreTerm, unmarked bool) *treeFixture {
	t.Helper()
	pidFile := filepath.Join(t.TempDir(), "escapee.pid")
	env := append(os.Environ(), treeHelperEnv+"="+helper, treeHelperPidFileEnv+"="+pidFile)
	if ignoreTerm {
		env = append(env, treeHelperIgnoreTerm+"=1")
	}
	if !unmarked {
		env = WithTreeMarker(env)
	}
	cmd := BuildCommand(os.Args[0], []string{"-test.run=^$"})
	cmd.SetCmdArgs()
	cmd.SetEnv(env)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	f := &treeFixture{cmd: cmd, waited: make(chan struct{})}
	go func() {
		_ = cmd.Wait()
		close(f.waited)
	}()
	t.Cleanup(func() {
		_ = syscall.Kill(-cmd.Pid(), syscall.SIGKILL)
		if f.escapee.pid != 0 && isSameProcess(f.escapee) {
			_ = syscall.Kill(f.escapee.pid, syscall.SIGKILL)
		}
		<-f.waited
	})

	waitFor(t, "escapee PID file", func() bool {
		_, err := os.Stat(pidFile)
		return err == nil
	})
	data, err := os.ReadFile(pidFile)
	if err != nil {
		t.Fatal(err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		t.Fatal(err)
	}
	f.escapee, err = readProc(pid)
	if err != nil {
		t.Fatalf("escapee %d: %v", pid, err)
	}
	if f.escapee.pgid == cmd.Pid() {
		t.Fatalf("escapee %d is still in the process group of %d", pid, cmd.Pid())
	}
	return f
}

func (f *treeFixture) waitRootExited(t *testing.T) {
	t.Helper()
	select {
	case <-f.waited:
	case <-time.After(5 * time.Second):
		t.Fatalf("launched process %d did not exit", f.cmd.Pid())
	}
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func waitGone(t *testing.T, info procInfo) {
	t.Helper()
	waitFor(t, fmt.Sprintf("process %d to exit", info.pid), func() bool {
		return !isSameProcess(info)
	})
}

// assertSurvives checks that info is still running a moment after a stop.
func assertSurvives(t *testing.T, info procInfo) {
	t.Helper()
	time.Sleep(300 * time.Millisecond)
	if !isSameProcess(info) {
		t.Fatalf("process %d exited, want it to survive", info.pid)
	}
}

func TestStopTree_SetsidGrandchild(t *testing.T) {
	f := startTree(t, "escape", false, false)

	if err := f.cmd.Stop(int(syscall.SIGTERM), KillModeTree); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	f.waitRootExited(t)
	waitGone(t, f.escapee)
}

func TestStopTree_ReparentedOrphan(t *testing.T) {
	f := startTree(t, "orphan", false, false)
	// The escapee's parent exited: it is reachable only through the marker.
	waitFor(t, "escapee to be reparented", func() bool {
		info, err := readProc(f.escapee.pid)
		if err != nil {
			return false
		}
		parent, err := readProc(info.ppid)
		return err != nil || parent.pgid != f.cmd.Pid()
	})

	if err := f.cmd.Stop(int(syscall.SIGTERM), KillModeTree); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	f.waitRootExited(t)
	waitGone(t, f.escapee)
}

// TestStopTree_SigkillAfterRootExit covers the SIGKILL fallback: a member that
// ignores SIGTERM outlives the launched process, is still reported alive, and
// is killed by a tree stop after the launched process was reaped.
func TestStopTree_SigkillAfterRootExit(t *testing.T) {
	f := startTree(t, "orphan", true, false)

	if err := f.cmd.Stop(int(syscall.SIGTERM), KillModeTree); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	// the launched process ignores SIGTERM too: kill only it
	if err := f.cmd.Stop(int(syscall.SIGKILL), KillModeParent); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	f.waitRootExited(t)
	assertSurvives(t, f.escapee)
	if !f.cmd.IsTreeAlive() {
		t.Fatal("IsTreeAlive = false while the escapee runs")
	}

	if err := f.cmd.Stop(int(syscall.SIGKILL), KillModeTree); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	waitGone(t, f.escapee)
	if f.cmd.IsTreeAlive() {
		t.Fatal("IsTreeAlive = true after the tree was killed")
	}
}

// TestStopTree_GroupModeMissesEscapee is the control for the tests above: group
// mode does not reach a descendant in another session.
func TestStopTree_GroupModeMissesEscapee(t *testing.T) {
	f := startTree(t, "escape", false, true)

	if err := f.cmd.Stop(int(syscall.SIGTERM), KillModeGroup); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	f.waitRootExited(t)
	assertSurvives(t, f.escapee)
}

// TestStopTree_ResumesMembers checks that members frozen during the walk are
// resumed, so a SIGTERM handler can run.
func TestStopTree_ResumesMembers(t *testing.T) {
	f := startTree(t, "escape", true, false)

	if err := f.cmd.Stop(int(syscall.SIGTERM), KillModeTree); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	time.Sleep(100 * time.Millisecond)
	for _, info := range []procInfo{f.escapee, mustReadProc(t, f.cmd.Pid())} {
		current := mustReadProc(t, info.pid)
		if current.stopped {
			t.Errorf("process %d left stopped", info.pid)
		}
	}
}

func mustReadProc(t *testing.T, pid int) procInfo {
	t.Helper()
	info, err := readProc(pid)
	if err != nil {
		t.Fatalf("process %d: %v", pid, err)
	}
	return info
}

func TestWithTreeMarker(t *testing.T) {
	inherited := TreeMarkerEnvName + "=outer"
	env := WithTreeMarker([]string{"A=1", inherited})
	if len(env) != 2 || env[0] != "A=1" {
		t.Fatalf("WithTreeMarker = %v", env)
	}
	marker := treeMarker(env)
	if marker == "" || marker == inherited {
		t.Fatalf("treeMarker = %q, want a fresh marker", marker)
	}
	if other := treeMarker(WithTreeMarker(nil)); other == "" || other == marker {
		t.Fatalf("WithTreeMarker(nil) marker = %q, want a different fresh marker", other)
	}
}
