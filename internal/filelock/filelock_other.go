//go:build !unix && !windows

package filelock

import "os"

// lock takes no lock on a system without file locks.
func lock(f *os.File) error { return nil }

// unlockFile has nothing to release.
func unlockFile(f *os.File) {}

// ReadFile is os.ReadFile.
func ReadFile(path string) ([]byte, error) { return os.ReadFile(path) }

// Rename is os.Rename.
func Rename(oldpath, newpath string) error { return os.Rename(oldpath, newpath) }

// Remove is os.Remove.
func Remove(path string) error { return os.Remove(path) }
