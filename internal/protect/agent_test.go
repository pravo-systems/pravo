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
)

// Runs against a private OpenSSH agent started for the test, never the user's.
func TestAgentAgainstOpenSSH(t *testing.T) {
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
	defer os.RemoveAll(dir)
	sock := filepath.Join(dir, "s")
	cmd := exec.Command(agent, "-D", "-a", sock)
	if err := cmd.Start(); err != nil {
		t.Skip(err)
	}
	defer func() { cmd.Process.Kill(); cmd.Wait() }()
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
	want, err := ParseSSHPublicLine(string(pubLine))
	if err != nil {
		t.Fatal(err)
	}
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
