package filelock

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// A cache directory that cannot be written does not leave the commands of a machine
// without a lock: the lock file is then in the temporary directory, and a second caller
// waits for the first.
func TestExclusiveHoldsWhenTheCacheDirectoryCannotBeWritten(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)
	t.Setenv("TMP", tmp)
	t.Setenv("TEMP", tmp)
	blocked := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(blocked, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	old := cacheDir.Load()
	SetDir(blocked)
	t.Cleanup(func() { cacheDir.Store(old) })

	name := filepath.Join(t.TempDir(), "state", "resources", "a.json")
	unlock := Exclusive(name)
	if files, _ := filepath.Glob(filepath.Join(tmp, "pravo", "mutex", "*")); len(files) != 1 {
		t.Fatalf("lock files in the temporary directory: %v, want one", files)
	}
	took := make(chan struct{})
	go func() {
		release := Exclusive(name)
		close(took)
		release()
	}()
	select {
	case <-took:
		t.Fatal("a second caller took the lock while the first held it")
	case <-time.After(150 * time.Millisecond):
	}
	unlock()
	select {
	case <-took:
	case <-time.After(10 * time.Second):
		t.Fatal("the second caller did not take the lock after the first released it")
	}
}
