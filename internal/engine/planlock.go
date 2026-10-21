package engine

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"pravo.dev/pravo/internal/filelock"
)

// updatePlanFile reads the plan file under an exclusive lock, lets edit change
// it, and writes it back whole by a rename, so that a reader sees the file
// before the edit or after it and never half of it. Two updates of one file
// take turns: the second edits what the first wrote.
//
// The lock is on the file that was opened and not on its name, which a rename
// replaces; a lock that was taken on a file that has been replaced since is
// given up and taken again on the new one. A filesystem that has no file locks
// gets none.
func updatePlanFile(path string, edit func(*PlanFile) error) error {
	if real, err := filepath.EvalSymlinks(path); err == nil {
		path = real
	}
	f, err := lockPlanFile(path)
	if err != nil {
		return err
	}
	defer f.Close()
	data, err := io.ReadAll(f)
	if err != nil {
		return err
	}
	var cur PlanFile
	if err := json.Unmarshal(data, &cur); err != nil {
		return fmt.Errorf("%s is not a pravo plan file: %w", path, err)
	}
	if err := edit(&cur); err != nil {
		return err
	}
	out, err := indentJSON(&cur)
	if err != nil {
		return err
	}
	perm := os.FileMode(0o600)
	if info, err := f.Stat(); err == nil {
		perm = info.Mode().Perm()
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*")
	if err != nil {
		// The directory of the file cannot be written: write the file itself,
		// still under the lock.
		return os.WriteFile(path, out, perm)
	}
	_, werr := tmp.Write(out)
	if werr == nil {
		werr = tmp.Chmod(perm)
	}
	if werr == nil {
		werr = tmp.Sync()
	}
	if cerr := tmp.Close(); werr == nil {
		werr = cerr
	}
	if werr != nil {
		_ = os.Remove(tmp.Name())
		return werr
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		_ = os.Remove(tmp.Name())
		return err
	}
	return nil
}

// lockPlanFile opens the plan file and takes an exclusive lock on it. The
// caller closes the file, which releases the lock.
func lockPlanFile(path string) (*os.File, error) {
	for {
		f, err := os.Open(path)
		if err != nil {
			return nil, err
		}
		_ = filelock.Lock(f)
		held, herr := f.Stat()
		now, nerr := os.Stat(path)
		switch {
		case herr != nil || nerr != nil:
			_ = f.Close()
			return nil, errors.Join(herr, nerr)
		case os.SameFile(held, now):
			return f, nil
		}
		_ = f.Close()
	}
}
