//go:build !windows

package protect

import "os"

func chmodPrivate(path string, mode os.FileMode) error { return os.Chmod(path, mode) }

// looseMode reports whether group or others have any access to path.
func looseMode(path string) (bool, os.FileMode) {
	st, err := os.Stat(path)
	if err != nil {
		return false, 0
	}
	m := st.Mode().Perm()
	return m&0o077 != 0, m
}
