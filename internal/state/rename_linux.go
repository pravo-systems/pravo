//go:build linux

package state

import "golang.org/x/sys/unix"

// renameExcl renames old to new unless new exists.
func renameExcl(old, new string) error {
	return unix.Renameat2(unix.AT_FDCWD, old, unix.AT_FDCWD, new, unix.RENAME_NOREPLACE)
}
