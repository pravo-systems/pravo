//go:build !windows

package protect

import (
	"errors"
	"io"
	"net"
	"os"
)

// DialAgent connects to the agent named by SSH_AUTH_SOCK.
func DialAgent() (io.ReadWriteCloser, error) {
	sock := os.Getenv("SSH_AUTH_SOCK")
	if sock == "" {
		return nil, errors.New("no ssh-agent: SSH_AUTH_SOCK is not set")
	}
	return net.Dial("unix", sock)
}
