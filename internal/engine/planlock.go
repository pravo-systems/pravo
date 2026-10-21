package engine

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/pravo-systems/pravo/internal/filelock"
)

// updatePlanFile reads the plan file under an exclusive lock, lets edit change
// it, and writes it back whole by a rename, so that a reader sees the file
// before the edit or after it and never half of it. Two updates of one file
// take turns: the second edits what the first wrote.
//
// The lock is not on the file, which the rename replaces; see filelock.Exclusive.
// A filesystem that has no file locks gets none.
func updatePlanFile(path string, edit func(*PlanFile) error) error {
	if real, err := filepath.EvalSymlinks(path); err == nil {
		path = real
	}
	defer filelock.Exclusive(path)()
	data, err := os.ReadFile(path)
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
	if info, err := os.Stat(path); err == nil {
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
	if err := filelock.Rename(tmp.Name(), path); err != nil {
		_ = os.Remove(tmp.Name())
		return err
	}
	return nil
}
