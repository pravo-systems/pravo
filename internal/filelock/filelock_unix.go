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

// unlockFile releases the lock on f before it is closed. The system does it
// when the file is closed, which is enough here.
func unlockFile(f *os.File) {}

// ReadFile is os.ReadFile.
func ReadFile(path string) ([]byte, error) { return os.ReadFile(path) }

// Rename is os.Rename.
func Rename(oldpath, newpath string) error { return os.Rename(oldpath, newpath) }

// Remove is os.Remove.
func Remove(path string) error { return os.Remove(path) }
