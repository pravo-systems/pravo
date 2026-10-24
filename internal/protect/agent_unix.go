//go:build !windows

package protect

import (
	"errors"
	"io"
	"net"
)

// DialAgent connects to the ssh-agent (see AgentAddress).
func DialAgent() (io.ReadWriteCloser, error) {
	sock := AgentAddress()
	if sock == "" {
		return nil, errors.New("no ssh-agent: SSH_AUTH_SOCK is not set and ssh_config names no IdentityAgent")
	}
	return net.Dial("unix", sock)
}
