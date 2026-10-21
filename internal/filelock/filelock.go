// Package filelock coordinates processes that change the same files. Exclusive
// serialises the commands that read an object and then replace or delete it.
// Rename and Remove do what os.Rename and os.Remove do, and on Windows, where a
// file that another process has open cannot be replaced or deleted, they try
// again for a moment first.
package filelock

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// Exclusive waits for the exclusive lock that goes with name and returns the
// function that releases it. The name is a path: a command that takes the lock
// of one path waits for any other that takes it, in this process or another on
// the same machine. The lock is not on the file at the path, which the callers
// replace by a rename; it is on a file of its own in the cache directory of the
// user, which nothing replaces. A system that cannot lock a file gives a lock
// that holds nothing.
func Exclusive(name string) (unlock func()) {
	if abs, err := filepath.Abs(name); err == nil {
		name = abs
	}
	if real, err := filepath.EvalSymlinks(name); err == nil {
		name = real
	} else if real, err := filepath.EvalSymlinks(filepath.Dir(name)); err == nil {
		name = filepath.Join(real, filepath.Base(name))
	}
	if runtime.GOOS == "windows" {
		name = strings.ToLower(name)
	}
	sum := sha256.Sum256([]byte(name))
	dir, err := os.UserCacheDir()
	if err != nil {
		dir = os.TempDir()
	}
	dir = filepath.Join(dir, "pravo", "mutex")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return func() {}
	}
	f, err := os.OpenFile(filepath.Join(dir, hex.EncodeToString(sum[:16])), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return func() {}
	}
	if err := lock(f); err != nil {
		_ = f.Close()
		return func() {}
	}
	return func() { _ = f.Close() }
}
