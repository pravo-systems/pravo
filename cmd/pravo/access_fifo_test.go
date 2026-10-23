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

// A readers file that is a named pipe must be refused, not waited for.
func TestReadersFileThatIsAPipe(t *testing.T) {
	isolateIdentities(t)
	root := t.TempDir()
	dir := filepath.Join(root, ".pravo", "access")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Mkfifo(filepath.Join(dir, "readers.txt"), 0o600); err != nil {
		t.Skip("no named pipes here:", err)
	}
	done := make(chan readersFile, 1)
	go func() { done <- loadReadersFile(root, "") }()
	select {
	case rf := <-done:
		if !rf.Found || rf.Err == nil || !strings.Contains(rf.Err.Error(), "not a regular file") {
			t.Fatalf("load = %+v, want a refusal", rf)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("loading a readers file that is a named pipe blocks")
	}
}
