package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/pravo-systems/pravo/internal/engine"
)

// failingRoot is a root whose second resource cannot be created while "blocker"
// is a file: its directory is not one. The first is made before it, and the third
// waits for it.
func failingRoot(t *testing.T) string {
	t.Helper()
	dir := localRoot(t, `first: $local.#Resource.local_file & {filename: "first.txt", content: "first\n"}
bad: $local.#Resource.local_file & {filename: "blocker/x.txt", content: "bad \(first.id)\n"}
after: $local.#Resource.local_file & {filename: "after.txt", content: "after \(bad.id)\n"}
`)
	if err := os.WriteFile(filepath.Join(dir, "blocker"), []byte("a file\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func exists(dir, name string) bool {
	_, err := os.Stat(filepath.Join(dir, name))
	return err == nil
}

// An apply of a stored plan that fails records the changes it made in the plan:
// the plan is partial, and the same command applies the rest once the cause is gone.
func TestFailedApplyOfAStoredPlanRecordsWhatItApplied(t *testing.T) {
	dir := failingRoot(t)
	id := storePlanAs(t, dir, "alice")
	text, code := runPravo(t, dir, "apply", id)
	if code != 1 || !strings.Contains(text, "Error:") {
		t.Fatalf("the apply exited %d, want 1:\n%s", code, text)
	}
	note := "1 change was applied before the error and is recorded in " + id + `; run "pravo apply ` + id + `" again to apply the rest.`
	if !strings.Contains(text, note) {
		t.Errorf("the apply did not say %q:\n%s", note, text)
	}
	if strings.Index(text, note) > strings.Index(text, "Error:") || strings.Contains(text, "Apply complete!") {
		t.Errorf("the note comes after the error, or the apply says it completed:\n%s", text)
	}
	if !exists(dir, "first.txt") || exists(dir, "after.txt") {
		t.Errorf("first.txt exists: %v, after.txt exists: %v", exists(dir, "first.txt"), exists(dir, "after.txt"))
	}
	text, _ = runPravo(t, dir, "plan", "list")
	if !regexp.MustCompile(id+`\s+partial\s+alice`).MatchString(text) || !strings.Contains(text, "1 of 3 changes applied") {
		t.Errorf("list after the failed apply:\n%s", text)
	}
	text, _ = runPravo(t, dir, "plan", "show", id)
	if !strings.Contains(text, "1 of 3 changes applied") || !strings.Contains(text, "(already applied in txn") {
		t.Errorf("show after the failed apply:\n%s", text)
	}

	// The cause is gone: the rest of the plan applies, and first is not made again.
	if err := os.Remove(filepath.Join(dir, "blocker")); err != nil {
		t.Fatal(err)
	}
	text, code = runPravo(t, dir, "apply", id)
	if code != 0 || !strings.Contains(text, "Apply complete!") || !strings.Contains(text, "2 added, 0 changed, 0 destroyed") {
		t.Fatalf("the rest: exit %d:\n%s", code, text)
	}
	if !exists(dir, "blocker/x.txt") || !exists(dir, "after.txt") {
		t.Error("the rest of the plan was not applied")
	}
	if text, _ = runPravo(t, dir, "plan", "list", "--all"); !regexp.MustCompile(id + `\s+applied`).MatchString(text) {
		t.Errorf("list --all after all of it:\n%s", text)
	}
}

// An apply of a plan file that fails marks the changes it made in the file, and
// with --json the note goes to the error stream and the output stays a stream of
// JSON lines.
func TestFailedApplyOfAPlanFileMarksWhatItApplied(t *testing.T) {
	dir := failingRoot(t)
	if text, code := runPravo(t, dir, "plan", "--out", "change.plan"); code != 0 {
		t.Fatalf("plan --out exited %d:\n%s", code, text)
	}
	text, code := runPravo(t, dir, "--json", "apply", "change.plan")
	if code != 1 {
		t.Fatalf("the apply exited %d, want 1:\n%s", code, text)
	}
	// runPravo adds the error of the command after the output.
	stream, _, _ := strings.Cut(text, "Error: ")
	for _, line := range strings.Split(strings.TrimSpace(stream), "\n") {
		var v map[string]any
		if err := json.Unmarshal([]byte(line), &v); err != nil {
			t.Errorf("stdout holds a line that is not JSON: %q", line)
		}
	}
	file := filepath.Join(dir, "change.plan")
	pf, err := engine.ReadPlanFile(file)
	if err != nil {
		t.Fatal(err)
	}
	marked := map[string]string{}
	for _, sc := range pf.Changes {
		if sc.Applied {
			marked[sc.Addr] = sc.AppliedTxn
		}
	}
	if len(marked) != 1 || marked["first"] == "" || pf.ApplyTxn != "" {
		t.Errorf("marked %v, apply txn %q; want only first, and no apply that finished", marked, pf.ApplyTxn)
	}
	if text, code = runPravo(t, dir, "show", "change.plan"); code != 0 || !strings.Contains(text, "first") {
		t.Errorf("show of the marked file exited %d:\n%s", code, text)
	}

	// A human reads the note: the same apply, without --json, on a file that is
	// marked again.
	text, code = runPravo(t, dir, "apply", "change.plan")
	if code != 1 || strings.Contains(text, "applied before the error") {
		t.Errorf("the second apply exited %d; it applied nothing, so it has nothing to record:\n%s", code, text)
	}
	if err := os.Remove(filepath.Join(dir, "blocker")); err != nil {
		t.Fatal(err)
	}
	text, code = runPravo(t, dir, "apply", "change.plan")
	if code != 0 || !strings.Contains(text, "Apply complete!") || !strings.Contains(text, "2 added, 0 changed, 0 destroyed") {
		t.Fatalf("the rest: exit %d:\n%s", code, text)
	}
	if text, code = runPravo(t, dir, "apply", "change.plan"); code != 0 || !strings.Contains(text, "nothing left") {
		t.Errorf("a third apply exited %d:\n%s", code, text)
	}
}

// A failed apply that was limited with --only says to run the same limited command again.
func TestTheNoteAfterAFailedOnlyApplyKeepsTheSelection(t *testing.T) {
	dir := failingRoot(t)
	id := storePlanAs(t, dir, "alice")
	text, code := runPravo(t, dir, "apply", id, "--only", "first,bad")
	if code != 1 {
		t.Fatalf("exit %d:\n%s", code, text)
	}
	if !strings.Contains(text, `run "pravo apply `+id+` --only first,bad" again`) {
		t.Errorf("the note drops the selection:\n%s", text)
	}
}
