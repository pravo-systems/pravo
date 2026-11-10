// Package filelock coordinates processes that change the same files. Exclusive
// serialises the commands that read an object and then replace or delete it.
// ReadFile, Rename and Remove do what os.ReadFile, os.Rename and os.Remove do,
// and on Windows, where a file that another process has open cannot be replaced
// or deleted, and a file that is being replaced cannot be opened, they try again
// for a moment first.
package filelock

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
)

// cacheDir is the directory the lock files are kept under, the cache directory of
// the user. Until SetDir is called it is the temporary directory.
var cacheDir atomic.Pointer[string]

// SetDir sets the directory the lock files of Exclusive are kept under: the
// cache directory of the user, which the caller reads from its environment.
func SetDir(dir string) { cacheDir.Store(&dir) }

// Exclusive waits for the exclusive lock that goes with name and returns the
// function that releases it. The name is a path: a command that takes the lock
// of one path waits for any other that takes it, in this process or another on
// the same machine. The lock is not on the file at the path, which the callers
// replace by a rename; it is on a file of its own in the cache directory of the
// user, which nothing replaces. When that directory cannot be written (a home
// that is read-only, as in some containers) the file is in the temporary directory,
// so that the commands of one machine still wait for each other. A system that
// cannot lock a file gives a lock that holds nothing.
func Exclusive(name string) (unlock func()) {
	if abs, err := filepath.Abs(name); err == nil {
		name = abs
	}
	if resolved, err := filepath.EvalSymlinks(name); err == nil {
		name = resolved
	} else if dir, err := filepath.EvalSymlinks(filepath.Dir(name)); err == nil {
		name = filepath.Join(dir, filepath.Base(name))
	}
	if runtime.GOOS == "windows" {
		name = strings.ToLower(name)
	}
	sum := sha256.Sum256([]byte(name))
	f := openLockFile(hex.EncodeToString(sum[:16]))
	if f == nil {
		return func() {}
	}
	if err := lock(f); err != nil {
		_ = f.Close()
		return func() {}
	}
	return func() {
		unlockFile(f)
		_ = f.Close()
	}
}

// openLockFile opens the lock file base in the first of the cache directory and the
// temporary directory that takes it, or returns nil.
func openLockFile(base string) *os.File {
	dirs := []string{os.TempDir()}
	if d := cacheDir.Load(); d != nil && *d != dirs[0] {
		dirs = []string{*d, dirs[0]}
	}
	for _, dir := range dirs {
		dir = filepath.Join(dir, "pravo", "mutex")
		if err := os.MkdirAll(dir, 0o700); err != nil {
			continue
		}
		if f, err := os.OpenFile(filepath.Join(dir, base), os.O_CREATE|os.O_RDWR, 0o600); err == nil {
			return f
		}
	}
	return nil
}
