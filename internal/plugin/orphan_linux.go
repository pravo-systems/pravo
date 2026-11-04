//go:build linux

package plugin

import (
	"os/exec"
	"syscall"
)

// Pdeathsig fires when the OS thread that started the child exits. The Go
// runtime does not end threads of ordinary goroutines, so in practice that is
// when pravo exits.
func guardChildOS(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Pdeathsig = syscall.SIGKILL
}

func watchChildOS(*exec.Cmd) func() { return func() {} }
