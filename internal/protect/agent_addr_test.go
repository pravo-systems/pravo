package protect

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestAgentAddressOrder(t *testing.T) {
	old := agentFromConfig
	defer func() { agentFromConfig = old }()
	config := ""
	agentFromConfig = func() string { return config }

	t.Setenv("PRAVO_SSH_AUTH_SOCK", "")
	t.Setenv("SSH_AUTH_SOCK", "")
	if got := AgentAddress(); got != "" {
		t.Errorf("nothing names an agent: %q", got)
	}
	t.Setenv("SSH_AUTH_SOCK", "/run/system-agent")
	if got := AgentAddress(); got != "/run/system-agent" {
		t.Errorf("the variable: %q", got)
	}
	// ssh takes the IdentityAgent of its configuration before the variable, which
	// is how 1Password is set up.
	config = "/run/1password/agent.sock"
	if got := AgentAddress(); got != config {
		t.Errorf("the configuration: %q", got)
	}
	t.Setenv("PRAVO_SSH_AUTH_SOCK", "/run/chosen")
	if got := AgentAddress(); got != "/run/chosen" {
		t.Errorf("PRAVO_SSH_AUTH_SOCK: %q", got)
	}
}

func TestIdentityAgentOf(t *testing.T) {
	for name, tc := range map[string]struct{ out, want string }{
		"a path":                   {"user marcinw\nidentityagent /run/agent.sock\nport 22\n", "/run/agent.sock"},
		"a path with spaces":       {"identityagent /Users/ada/Library/Group Containers/x.com.1password/t/agent.sock\n", "/Users/ada/Library/Group Containers/x.com.1password/t/agent.sock"},
		"none":                     {"identityagent none\n", ""},
		"the variable":             {"identityagent SSH_AUTH_SOCK\n", ""},
		"not configured":           {"user ada\nidentityfile ~/.ssh/id_ed25519\n", ""},
		"another word that starts": {"identityagentx /run/other\n", ""},
		"carriage returns":         {"identityagent /run/agent.sock\r\nport 22\r\n", "/run/agent.sock"},
		"nothing":                  {"", ""},
	} {
		if got := identityAgentOf([]byte(tc.out)); got != tc.want {
			t.Errorf("%s: %q, want %q", name, got, tc.want)
		}
	}
}

// Against the real ssh, with a configuration of the test's own.
func TestConfiguredAgentReadsTheSSHConfiguration(t *testing.T) {
	if _, err := exec.LookPath("ssh"); err != nil {
		t.Skip("OpenSSH is not installed")
	}
	dir := t.TempDir()
	write := func(name, text string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	sock := filepath.Join(dir, "Group Containers", "agent.sock")
	named := write("named", "Host *\n\tIdentityAgent \""+sock+"\"\n")
	if got := configuredAgent("-F", named); got != sock {
		t.Errorf("a configuration that names an agent: %q, want %q", got, sock)
	}
	if got := configuredAgent("-F", write("none", "Host *\n\tIdentityAgent none\n")); got != "" {
		t.Errorf("none: %q", got)
	}
	if got := configuredAgent("-F", write("empty", "Host *\n\tUser ada\n")); got != "" {
		t.Errorf("a configuration without one: %q", got)
	}
	// An entry for another host is not the entry of this one.
	if got := configuredAgent("-F", write("other", "Host example.com\n\tIdentityAgent /run/other\n")); got != "" {
		t.Errorf("another host: %q", got)
	}
	if got := configuredAgent("-F", filepath.Join(dir, "missing")); got != "" {
		t.Errorf("a configuration that is not there: %q", got)
	}
}
