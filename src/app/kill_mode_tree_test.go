//go:build linux || darwin

package app

import (
	"errors"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/f1bonacc1/process-compose/src/command"
	"github.com/f1bonacc1/process-compose/src/types"
)

// treeEscapeeEnv selects the escapee helper of the test binary. It holds the
// path of the PID file to write.
const treeEscapeeEnv = "PC_APP_TREE_TEST_ESCAPEE"

func TestMain(m *testing.M) {
	if pidFile := os.Getenv(treeEscapeeEnv); pidFile != "" {
		runTreeEscapee(pidFile)
		os.Exit(0)
	}
	os.Exit(m.Run())
}

// runTreeEscapee leaves its session and process group, ignores SIGTERM, and
// writes its PID to pidFile.
func runTreeEscapee(pidFile string) {
	if _, err := syscall.Setsid(); err != nil {
		os.Exit(1)
	}
	signal.Ignore(syscall.SIGTERM)
	if err := os.WriteFile(pidFile+".tmp", []byte(strconv.Itoa(os.Getpid())), 0o600); err != nil {
		os.Exit(1)
	}
	if err := os.Rename(pidFile+".tmp", pidFile); err != nil {
		os.Exit(1)
	}
	time.Sleep(time.Minute)
}

// TestSystem_KillModeTreeKillsOrphanedEscapee stops a process whose descendant
// left its session, was reparented when its parent exited, and ignores
// SIGTERM. In tree kill mode the stop reaches it, and the shutdown timeout
// SIGKILLs it after the launched process has exited.
func TestSystem_KillModeTreeKillsOrphanedEscapee(t *testing.T) {
	name := "tree"
	pidFile := filepath.Join(t.TempDir(), "escapee.pid")
	shell := command.DefaultShellConfig()
	// The subshell exits right after starting the escapee, which is reparented.
	script := fmt.Sprintf(`( %q -test.run='^$' >/dev/null 2>&1 & ); sleep 60`, os.Args[0])
	project := &types.Project{
		Processes: map[string]types.ProcessConfig{
			name: {
				Name:        name,
				ReplicaName: name,
				Executable:  shell.ShellCommand,
				Args:        []string{shell.ShellArgument, script},
				Environment: types.Environment{treeEscapeeEnv + "=" + pidFile},
				ShutDownParams: types.ShutDownParams{
					ShutDownTimeout: 2,
					Signal:          int(syscall.SIGTERM),
					KillMode:        command.KillModeTree,
				},
			},
		},
		ShellConfig: shell,
	}
	runner, err := NewProjectRunner(&ProjectOpts{project: project})
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		if err := runner.Run(); err != nil {
			t.Errorf("%s", err)
		}
	}()

	escapee := 0
	waitUntil(t, "escapee PID file", func() bool {
		data, err := os.ReadFile(pidFile)
		if err != nil {
			return false
		}
		escapee, err = strconv.Atoi(strings.TrimSpace(string(data)))
		return err == nil
	})
	t.Cleanup(func() { _ = syscall.Kill(escapee, syscall.SIGKILL) })
	proc := runner.getRunningProcess(name)
	assertProcessStatus(t, proc, name, types.ProcessStateRunning)

	if err := runner.StopProcess(name); err != nil {
		t.Fatal(err)
	}
	waitUntil(t, "escapee to exit", func() bool {
		return errors.Is(syscall.Kill(escapee, 0), syscall.ESRCH)
	})
	assertProcessStatus(t, proc, name, types.ProcessStateCompleted)
}

func waitUntil(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(50 * time.Millisecond)
	}
}
