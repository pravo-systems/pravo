//go:build !windows

package main

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// A named pipe among the identity files must not hold up the choice of the
// identity: its public part is read without opening anything that waits.
func TestIdentityDirectoryWithANamedPipe(t *testing.T) {
	s := sealedBucket(t)
	t.Setenv("USER", "bob")
	dir := t.TempDir()
	putIdentity(t, dir, "ada", s.identityFile(t, "ada"))
	_, eve := newIdentityFile(t, "eve", true)
	putIdentity(t, dir, "eve", eve)
	if err := syscall.Mkfifo(filepath.Join(dir, "aaa"), 0o600); err != nil {
		t.Skip("no named pipes here:", err)
	}
	t.Setenv("PRAVO_IDENTITY_DIR", dir)
	resetIdentityState(t)
	done := make(chan string, 1)
	go func() {
		text, code := runPravo(t, s.dir, "protect", "status")
		if code != 0 {
			text = "exit " + text
		}
		done <- text
	}()
	select {
	case text := <-done:
		if !strings.Contains(text, "You          ada · reader key") {
			t.Fatalf("status:\n%s", text)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("choosing the identity waits for a named pipe in the identity directory")
	}
	// A pipe that is the identity asked for is refused as well.
	resetIdentityState(t)
	if _, err := os.Stat(filepath.Join(dir, "aaa")); err != nil {
		t.Fatal(err)
	}
	go func() {
		text, _ := runPravo(t, s.dir, "protect", "status", "--identity", "aaa")
		done <- text
	}()
	select {
	case text := <-done:
		if !strings.Contains(text, "not a regular file") {
			t.Fatalf("--identity aaa:\n%s", text)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("reading an identity that is a named pipe waits")
	}
}
