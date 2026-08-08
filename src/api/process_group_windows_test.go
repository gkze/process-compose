//go:build windows

package api

import "os/exec"

func configureProcessGroup(_ *exec.Cmd) {}

func killProcessGroup(command *exec.Cmd) error {
	return command.Process.Kill()
}
