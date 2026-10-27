//go:build !darwin && !linux

package state

import "syscall"

// renameExcl is not available on this system; the caller links the file.
func renameExcl(from, to string) error { return syscall.ENOTSUP }
