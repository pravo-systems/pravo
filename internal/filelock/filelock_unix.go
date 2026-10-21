//go:build unix

package filelock

import (
	"os"
	"syscall"
)

// lock waits for an exclusive lock on f, which the system releases when f is
// closed.
func lock(f *os.File) error {
	for {
		err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX)
		if err == syscall.EINTR {
			continue
		}
		return err
	}
}

// Rename is os.Rename.
func Rename(oldpath, newpath string) error { return os.Rename(oldpath, newpath) }

// Remove is os.Remove.
func Remove(path string) error { return os.Remove(path) }
