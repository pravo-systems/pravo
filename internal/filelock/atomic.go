package filelock

import (
	"fmt"
	"os"
	"path/filepath"
)

// AtomicOptions say how WriteFileAtomic writes.
type AtomicOptions struct {
	// Mode is the mode of the new file. Zero is 0600.
	Mode os.FileMode
	// Sync flushes the data to disk before the rename, so that a crash leaves the old file or
	// the whole new one.
	Sync bool
	// TempPattern is the name of the temporary file, with %s for the base name of the file and
	// a * where the random part goes. Zero is ".%s.tmp-*". A file in a directory that something
	// lists must say here how that listing tells a temporary file from a real one.
	TempPattern string
}

// WriteFileAtomic writes data to a new file next to path and renames it over path, so that a
// reader sees the old file or the new one and two writers do not share a file. The rename
// tries again on Windows while the file at path is open elsewhere. The directory must exist.
// This is the one place a file is replaced: the callers differ in what they ask for (mode,
// Sync, the temporary name), not in how it is done.
func WriteFileAtomic(path string, data []byte, o AtomicOptions) error {
	mode := o.Mode
	if mode == 0 {
		mode = 0o600
	}
	pattern := o.TempPattern
	if pattern == "" {
		pattern = ".%s.tmp-*"
	}
	f, err := os.CreateTemp(filepath.Dir(path), fmt.Sprintf(pattern, filepath.Base(path)))
	if err != nil {
		return err
	}
	tmp := f.Name()
	_, err = f.Write(data)
	if err == nil {
		err = f.Chmod(mode)
	}
	if err == nil && o.Sync {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = Rename(tmp, path)
	}
	if err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}
