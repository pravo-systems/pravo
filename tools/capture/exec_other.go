//go:build !unix

package main

import (
	"context"
	"os/exec"
	"time"
)

// configureCmd bounds the wait for output pipes after the command is killed.
func configureCmd(cmd *exec.Cmd) {
	cmd.WaitDelay = 3 * time.Second
}

// killGroup kills the command; there are no process groups to kill here.
func killGroup(cmd *exec.Cmd) {
	if cmd.Process != nil {
		cmd.Process.Kill()
	}
}

// lockFile does nothing here: pages that name the same port still run one
// after another inside a process, but two capture processes are not kept
// apart.
func lockFile(ctx context.Context, path string) (func(), error) {
	return func() {}, ctx.Err()
}
