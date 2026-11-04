//go:build !linux && !windows

package plugin

import "os/exec"

func guardChildOS(*exec.Cmd) {}

func watchChildOS(cmd *exec.Cmd) func() { return watchWithShell(cmd) }
