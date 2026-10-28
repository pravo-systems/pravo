package protect

import (
	"bytes"
	"context"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/pravo-systems/pravo/internal/env"
)

// Where the ssh-agent is. ssh takes the IdentityAgent of its configuration before
// $SSH_AUTH_SOCK, and that is how the agent of 1Password is set up: ~/.ssh/config
// names its socket, and the variable is left to the agent of the system, which
// holds no key. pravo asks the same way, in this order:
//
//  1. $PRAVO_SSH_AUTH_SOCK;
//  2. the IdentityAgent that `ssh -G` reports for a host with no entry of its own;
//  3. $SSH_AUTH_SOCK.

// agentProbeTimeout is how long a run waits for ssh to report its configuration. The agent may
// not be needed, so a run that finds ssh slow goes on without the IdentityAgent.
const agentProbeTimeout = 3 * time.Second

// agentFromConfig is the IdentityAgent of the ssh configuration, found once for a
// process. A test replaces it.
var agentFromConfig = sync.OnceValue(func() string { return configuredAgent(agentProbeTimeout) })

// agentEnv is the environment the address of the agent is read from: the one
// SetAgentEnv gave. The agent is reached from deep in the signing of a
// transaction, which carries no environment, so the setting is process-wide.
var agentEnv atomic.Pointer[env.Env]

// SetAgentEnv sets the environment that PRAVO_SSH_AUTH_SOCK and SSH_AUTH_SOCK are
// read from. Before it is called no agent is found.
func SetAgentEnv(e env.Env) { agentEnv.Store(&e) }

// AgentAddress is the socket (on Windows, the pipe) of the ssh-agent, or "" when
// nothing names one.
func AgentAddress() string {
	var e env.Env
	if p := agentEnv.Load(); p != nil {
		e = *p
	}
	if v := e.Get("PRAVO_SSH_AUTH_SOCK"); v != "" {
		return v
	}
	if v := agentFromConfig(); v != "" {
		return v
	}
	return e.Get("SSH_AUTH_SOCK")
}

// configuredAgent asks ssh for its IdentityAgent, and waits for it no longer than timeout. args
// come before the host name (a test gives -F). Any failure is "": there is no ssh to ask, or it
// has none, or it was too slow.
func configuredAgent(timeout time.Duration, args ...string) string {
	path, err := exec.LookPath("ssh")
	if err != nil {
		return ""
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, path, append(append([]string{"-G"}, args...), "pravo")...).Output()
	if err != nil {
		return ""
	}
	return identityAgentOf(out)
}

// identityAgentOf reads the identityagent line of the output of ssh -G: a path,
// which may hold spaces, or none, or the name of the variable. The last two say
// that the configuration names no socket.
func identityAgentOf(out []byte) string {
	for _, line := range bytes.Split(out, []byte("\n")) {
		rest, ok := strings.CutPrefix(strings.TrimRight(string(line), "\r"), "identityagent ")
		if !ok {
			continue
		}
		switch v := strings.TrimSpace(rest); v {
		case "", "none", "SSH_AUTH_SOCK":
			return ""
		default:
			return v
		}
	}
	return ""
}
