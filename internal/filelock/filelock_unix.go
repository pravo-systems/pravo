//go:build unix

package filelock

import (
	"os"
	"syscall"
)

// Lock waits for an exclusive lock on f.
func Lock(f *os.File) error {
	for {
		err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX)
		if err == syscall.EINTR {
			continue
		}
		return err
	}
}
