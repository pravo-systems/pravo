//go:build windows

package filelock

import (
	"errors"
	"math/rand/v2"
	"os"
	"time"

	"golang.org/x/sys/windows"
)

// lock waits for an exclusive lock on the first byte of f, which the system
// releases when f is closed.
func lock(f *os.File) error {
	return windows.LockFileEx(windows.Handle(f.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK, 0, 1, 0, new(windows.Overlapped))
}

// unlockFile releases the lock on f. Closing the file releases it too, but the
// system may take its time over that, and the next command waits for it meanwhile.
func unlockFile(f *os.File) {
	_ = windows.UnlockFileEx(windows.Handle(f.Fd()), 0, 1, 0, new(windows.Overlapped))
}

// busy reports whether err says that another process has the file open.
func busy(err error) bool {
	return errors.Is(err, windows.ERROR_ACCESS_DENIED) || errors.Is(err, windows.ERROR_SHARING_VIOLATION) || errors.Is(err, windows.ERROR_LOCK_VIOLATION)
}

// retry runs fn until it succeeds or fails for another reason than a busy file,
// for up to ten seconds. The waits grow and differ from caller to caller, so
// that callers that collide do not collide again at the same moment.
func retry(fn func() error) error {
	deadline := time.Now().Add(10 * time.Second)
	for wait := 2 * time.Millisecond; ; {
		err := fn()
		if err == nil || !busy(err) || time.Now().After(deadline) {
			return err
		}
		time.Sleep(wait/2 + time.Duration(rand.Int64N(int64(wait))))
		if wait < 100*time.Millisecond {
			wait *= 2
		}
	}
}

// ReadFile is os.ReadFile, tried again while another process replaces the file.
func ReadFile(path string) (data []byte, err error) {
	err = retry(func() error {
		var rerr error
		data, rerr = os.ReadFile(path)
		return rerr
	})
	return data, err
}

// Rename is os.Rename, tried again while the file at newpath is open elsewhere.
func Rename(oldpath, newpath string) error {
	return retry(func() error { return os.Rename(oldpath, newpath) })
}

// Remove is os.Remove, tried again while the file is open elsewhere.
func Remove(path string) error {
	return retry(func() error { return os.Remove(path) })
}
