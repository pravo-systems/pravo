//go:build !unix && !windows

package filelock

import "os"

// Lock takes no lock on a system without file locks.
func Lock(f *os.File) error { return nil }
