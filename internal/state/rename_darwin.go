//go:build darwin

package state

import "golang.org/x/sys/unix"

// renameExcl renames old to new unless new exists.
func renameExcl(old, new string) error {
	return unix.RenamexNp(old, new, unix.RENAME_EXCL)
}
