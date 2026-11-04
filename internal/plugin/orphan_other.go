//go:build !linux && !windows

package plugin

import (
	"os/exec"
	"syscall"
)

func guardChildOS(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true
}

func watchChildOS(cmd *exec.Cmd) func() { return watchWithShell(cmd) }
