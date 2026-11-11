package engine

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/pravo-systems/pravo/internal/filelock"
	"github.com/pravo-systems/pravo/internal/state"
)

// updatePlanFile reads the plan file under an exclusive lock, lets edit change
// it, and writes it back whole by a rename, so that a reader sees the file
// before the edit or after it and never half of it. Two updates of one file
// take turns: the second edits what the first wrote.
//
// The lock is not on the file, which the rename replaces; see filelock.Exclusive.
// A filesystem that has no file locks gets none.
func updatePlanFile(path string, sl state.Sealer, edit func(*PlanFile) error) error {
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		path = resolved
	}
	defer filelock.Exclusive(path)()
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	// A sealed file is opened and sealed again, with the key that read it.
	plain, sealed, err := openPlanFile(path, data, sl)
	if err != nil {
		return err
	}
	var cur PlanFile
	if err := json.Unmarshal(plain, &cur); err != nil {
		return fmt.Errorf("%s is not a pravo plan file: %w", path, err)
	}
	if err := edit(&cur); err != nil {
		return err
	}
	out, err := indentJSON(&cur)
	if err != nil {
		return err
	}
	if sealed {
		if out, err = sealPlanFile(sl, out); err != nil {
			return err
		}
	}
	perm := os.FileMode(0o600)
	if info, err := os.Stat(path); err == nil {
		perm = info.Mode().Perm()
	}
	err = filelock.WriteFileAtomic(path, out, filelock.AtomicOptions{Mode: perm, Sync: true, TempPattern: ".%s.*"})
	if errors.Is(err, fs.ErrPermission) {
		// The directory of the file cannot be written: write the file itself,
		// still under the lock.
		return os.WriteFile(path, out, perm)
	}
	return err
}
