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
	// A group of its own, so that the watcher can kill what the provider started.
	cmd.SysProcAttr.Setpgid = true
}

// Pdeathsig reaches the provider only. The watcher also kills its group, which
// holds the processes the provider started (plugin subprocesses, helpers).
func watchChildOS(cmd *exec.Cmd) func() { return watchWithShell(cmd) }
