//go:build !windows

package plugin

import (
	"os"
	"os/exec"
	"strconv"
	"syscall"
)

// watchWithShell starts a shell that reads standard input to its end and then
// kills the provider's process group (the provider has a group of its own, with
// the processes it started). Its standard input is a pipe whose write end only pravo
// holds (and never passes on), so the end comes when pravo dies. The watcher is
// stopped on a normal close, before the provider's pid can be reused.
func watchWithShell(cmd *exec.Cmd) func() {
	if cmd.Process == nil {
		return func() {}
	}
	r, w, err := os.Pipe()
	if err != nil {
		return func() {}
	}
	pid := strconv.Itoa(cmd.Process.Pid)
	w2 := exec.Command("/bin/sh", "-c", `cat >/dev/null; kill -s KILL -- "-$1" 2>/dev/null`, "sh", pid)
	w2.Stdin = r
	w2.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	err = w2.Start()
	r.Close()
	if err != nil {
		w.Close()
		return func() {}
	}
	go func() { _ = w2.Wait() }()
	return func() {
		// Kill the watcher's group first so closing the pipe cannot make it fire.
		_ = syscall.Kill(-w2.Process.Pid, syscall.SIGKILL)
		w.Close()
	}
}

// killGroupOS signals the group of the provider, which guardChildOS made a group
// of its own, so its pgid is the pid of the provider. The group outlives the
// provider as long as one member runs; with none, the signal fails.
func killGroupOS(cmd *exec.Cmd) {
	if cmd.Process != nil {
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
}
