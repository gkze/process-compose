package command

import (
	"os/exec"
	"strconv"

	"github.com/rs/zerolog/log"
)

// Stop terminates the process with taskkill. Windows has no process groups or
// signals: KillModeGroup and KillModeTree both terminate the process tree.
func (c *CmdWrapper) Stop(sig int, mode KillMode) error {
	pid := c.Pid()
	if pid <= 0 {
		return nil
	}
	log.
		Debug().
		Int("pid", pid).
		Str("killMode", string(mode)).
		Msg("Stop Windows process.")

	args := []string{"/F", "/PID", strconv.Itoa(pid)}
	if mode != KillModeParent {
		args = append([]string{"/T"}, args...)
	}

	// Try taskkill
	kill := exec.Command("taskkill", args...)
	_ = kill.Run()

	// Direct kill as fallback
	if c.cmd.Process != nil {
		_ = c.cmd.Process.Kill()
	}

	return nil
}

// IsTreeAlive is not supported on Windows: taskkill /T already terminated the
// tree, so it reports false.
func (c *CmdWrapper) IsTreeAlive() bool {
	return false
}

func (c *CmdWrapper) SetCmdArgs() {
	//empty for windows
}
