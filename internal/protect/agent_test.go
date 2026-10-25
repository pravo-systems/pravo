//go:build !windows

package protect

import (
	"crypto/ed25519"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pravo-systems/pravo/internal/env"
)

// openSSHAgent starts a private OpenSSH agent for the test, never the user's, and
// loads one new Ed25519 key into it. It skips the test when OpenSSH is not there.
func openSSHAgent(t *testing.T) (sock string, pub ed25519.PublicKey) {
	t.Helper()
	agent, err1 := exec.LookPath("ssh-agent")
	add, err2 := exec.LookPath("ssh-add")
	kg, err3 := exec.LookPath("ssh-keygen")
	if err1 != nil || err2 != nil || err3 != nil {
		t.Skip("OpenSSH tools not installed")
	}
	dir, err := os.MkdirTemp("/tmp", "pa")
	if err != nil {
		t.Skip(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	sock = filepath.Join(dir, "s")
	cmd := exec.Command(agent, "-D", "-a", sock)
	if err := cmd.Start(); err != nil {
		t.Skip(err)
	}
	t.Cleanup(func() { cmd.Process.Kill(); cmd.Wait() })
	for i := 0; i < 50; i++ {
		if _, err := os.Stat(sock); err == nil {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	key := filepath.Join(dir, "k")
	if out, err := exec.Command(kg, "-q", "-t", "ed25519", "-N", "", "-C", "test-key", "-f", key).CombinedOutput(); err != nil {
		t.Skip(string(out))
	}
	c := exec.Command(add, key)
	c.Env = append(os.Environ(), "SSH_AUTH_SOCK="+sock)
	if out, err := c.CombinedOutput(); err != nil {
		t.Skip(string(out))
	}
	pubLine, _ := os.ReadFile(key + ".pub")
	pub, err = ParseSSHPublicLine(string(pubLine))
	if err != nil {
		t.Fatal(err)
	}
	return sock, pub
}

func TestAgentAgainstOpenSSH(t *testing.T) {
	sock, want := openSSHAgent(t)
	conn, err := net.Dial("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	keys, err := AgentList(conn)
	if err != nil || len(keys) != 1 || !keys[0].Pub.Equal(want) || !strings.Contains(keys[0].Comment, "test-key") {
		t.Fatalf("list: %v %v", keys, err)
	}
	msg := []byte("pravo/test message")
	sig, err := AgentSign(conn, want, msg)
	if err != nil || !ed25519.Verify(want, msg, sig) {
		t.Fatalf("sign: %v", err)
	}
	sig2, _ := AgentSign(conn, want, msg)
	if string(sig) != string(sig2) {
		t.Error("Ed25519 signatures should be deterministic")
	}
	other, _, _ := ed25519.GenerateKey(nil)
	if _, err := AgentSign(conn, other, msg); err == nil {
		t.Error("signed with a key the agent does not hold")
	}
}

// An identity sealed under a real agent opens again with it: OpenSSH signs the
// challenge the same way every time, which the key derivation depends on.
func TestAnIdentityUnderAnOpenSSHAgent(t *testing.T) {
	sock, pub := openSSHAgent(t)
	t.Setenv("PRAVO_SSH_AUTH_SOCK", sock) // ahead of the IdentityAgent of the ssh configuration of whoever runs the test
	SetAgentEnv(env.OS())
	id, err := NewIdentity("ada-mac", "")
	if err != nil {
		t.Fatal(err)
	}
	defer id.Close()
	file, err := id.MarshalAgentSealed(pub)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		got, err := ParseIdentity(file, nil)
		if err != nil {
			t.Fatalf("open %d: %v", i, err)
		}
		if got.Line() != id.Line() {
			t.Errorf("open %d: the identity changed", i)
		}
		got.Close()
	}
	keys, err := AgentKeys()
	if err != nil || len(keys) != 1 || !keys[0].Pub.Equal(pub) {
		t.Fatalf("AgentKeys = %v, %v", keys, err)
	}
	// An agent without the key refuses to open it.
	t.Setenv("PRAVO_SSH_AUTH_SOCK", filepath.Join(t.TempDir(), "gone"))
	SetAgentEnv(env.OS())
	if _, err := ParseIdentity(file, nil); err == nil || !strings.Contains(err.Error(), "not reachable") {
		t.Errorf("no agent: %v", err)
	}
}
