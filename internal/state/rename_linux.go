//go:build linux

package state

import "golang.org/x/sys/unix"

// renameExcl renames from to to unless to exists.
func renameExcl(from, to string) error {
	return unix.Renameat2(unix.AT_FDCWD, from, unix.AT_FDCWD, to, unix.RENAME_NOREPLACE)
}
