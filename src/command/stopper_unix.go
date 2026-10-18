//go:build !windows

package command

import (
	"errors"
	"syscall"

	"github.com/rs/zerolog/log"
)

const (
	min_sig = 1
	max_sig = 31
)

// errProcTable marks tree stop failures that leave the tree unsignalled.
var errProcTable = errors.New("cannot read the process table")

func (c *CmdWrapper) Stop(sig int, mode KillMode) error {
	if c.cmd == nil || c.cmd.Process == nil {
		return nil
	}
	if sig < min_sig || sig > max_sig {
		sig = int(syscall.SIGTERM)
	}

	log.
		Debug().
		Int("pid", c.Pid()).
		Int("signal", sig).
		Str("killMode", string(mode)).
		Msg("Stop Unix process.")

	switch mode {
	case KillModeParent:
		return c.cmd.Process.Signal(syscall.Signal(sig))
	case KillModeTree:
		err := c.stopTree(syscall.Signal(sig))
		if !errors.Is(err, errProcTable) {
			return err
		}
		log.Warn().Err(err).Int("pid", c.Pid()).Msg("Process tree stop failed, signalling the process group")
	}

	pgid, err := syscall.Getpgid(c.Pid())
	if err == nil {
		return syscall.Kill(-pgid, syscall.Signal(sig))
	}

	return err
}

// IsTreeAlive reports whether any member of the process tree of a process
// launched in tree kill mode is still running, including members that were
// reparented after the launched process exited.
func (c *CmdWrapper) IsTreeAlive() bool {
	if c.cmd == nil || c.cmd.Process == nil {
		return false
	}
	alive, err := c.isTreeAlive()
	if err != nil {
		log.Debug().Err(err).Int("pid", c.Pid()).Msg("Process tree liveness check failed")
		return false
	}
	return alive
}

func (c *CmdWrapper) SetCmdArgs() {
	c.cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}
