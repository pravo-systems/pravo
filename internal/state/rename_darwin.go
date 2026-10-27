//go:build darwin

package state

import "golang.org/x/sys/unix"

// renameExcl renames old to new unless new exists.
func renameExcl(from, to string) error {
	return unix.RenamexNp(from, to, unix.RENAME_EXCL)
}
