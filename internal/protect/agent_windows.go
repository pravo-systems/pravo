//go:build windows

package protect

import (
	"io"
	"os"
)

// DialAgent opens the OpenSSH agent's named pipe. NOT TESTED on Windows: opening
// a named pipe as a file is the usual way to talk to it without a dependency;
// agents that only offer Pageant (PuTTY) are not supported.
func DialAgent() (io.ReadWriteCloser, error) {
	pipe := os.Getenv("SSH_AUTH_SOCK")
	if pipe == "" {
		pipe = `\\.\pipe\openssh-ssh-agent`
	}
	return os.OpenFile(pipe, os.O_RDWR, 0)
}
