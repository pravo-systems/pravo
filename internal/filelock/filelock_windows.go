//go:build windows

package filelock

import (
	"os"

	"golang.org/x/sys/windows"
)

// Lock waits for an exclusive lock on f. The lock covers the first byte of the
// file, which is enough for the callers: they all lock the same byte.
func Lock(f *os.File) error {
	return windows.LockFileEx(windows.Handle(f.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK, 0, 1, 0, new(windows.Overlapped))
}
