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

// threeFiles is a root that creates three files that do not depend on one another.
const threeFiles = "a: $local.#Resource.local_file & {filename: \"a.txt\", content: \"a\\n\"}\n" +
	"b: $local.#Resource.local_file & {filename: \"b.txt\", content: \"b\\n\"}\n" +
	"c: $local.#Resource.local_file & {filename: \"c.txt\", content: \"c\\n\"}\n"

// storedThenMoved stores a plan that creates a, b and c as alice, and then makes
// the record of a move behind its back: a plan that creates only a is applied,
// with the configuration that has b and c left out, and the configuration is put
// back. It returns the directory and the id.
func storedThenMoved(t *testing.T) (string, string) {
	t.Helper()
	dir := localRoot(t, threeFiles)
	id := storePlanAs(t, dir, "alice")
	moveA(t, dir)
	return dir, id
}

func moveA(t *testing.T, dir string) {
	t.Helper()
	main := filepath.Join(dir, "main.cue")
	original, err := os.ReadFile(main)
	if err != nil {
		t.Fatal(err)
	}
	var kept []string
	for _, line := range strings.Split(string(original), "\n") {
		if !strings.HasPrefix(line, "b:") && !strings.HasPrefix(line, "c:") {
			kept = append(kept, line)
		}
	}
	if err := os.WriteFile(main, []byte(strings.Join(kept, "\n")), 0o644); err != nil {
		t.Fatal(err)
	}
	text, code := runPravo(t, dir, "apply", "--auto-approve")
	if err := os.WriteFile(main, original, 0o644); err != nil {
		t.Fatal(err)
	}
	if code != 0 || !strings.Contains(text, "1 added") {
		t.Fatalf("applying a alone: exit %d:\n%s", code, text)
	}
}

func fileExists(dir, name string) bool {
	_, err := os.Stat(filepath.Join(dir, name))
	return err == nil
}

func TestAPlanWhoseRecordMovedIsAppliedInPart(t *testing.T) {
	dir, id := storedThenMoved(t)

	// The plan listing says that part of the plan conflicts, and how to apply the rest.
	text, code := runPravo(t, dir, "plan", "list")
	if code != 0 || !regexp.MustCompile(id+`\s+partly-stale\s+alice`).MatchString(text) ||
		!strings.Contains(text, "1 of 3 unapplied changes conflict: a changed in state (revision 1, plan expected 0); apply with --only b,c") {
		t.Fatalf("list: exit %d:\n%s", code, text)
	}

	// show prints the conflicts and the command for the part that can be applied.
	text, code = runPravo(t, dir, "plan", "show", id)
	if code != 0 || !strings.Contains(text, "(partly-stale)") || !strings.Contains(text, "apply:   pravo apply "+id+" --only b,c\n") ||
		!strings.Contains(text, "1 of 3 unapplied changes conflict") || !strings.Contains(text, "  - a changed in state (revision 1, plan expected 0)") ||
		strings.Contains(text, "can no longer be applied") {
		t.Errorf("show: exit %d:\n%s", code, text)
	}

	// --json says the same.
	type view struct {
		ID               string
		Status           string
		Conflicts        map[string][]string
		ApplyOnly        []string `json:"apply_only"`
		ApplyOnlyCommand string   `json:"apply_only_command"`
		StaleReasons     []string `json:"stale_reasons"`
		ApplyCommand     string   `json:"apply_command"`
	}
	text, code = runPravo(t, dir, "--json", "plan", "list")
	var list struct{ Plans []view }
	if err := json.Unmarshal([]byte(text), &list); err != nil || code != 0 || len(list.Plans) != 1 {
		t.Fatalf("list --json: exit %d: %v\n%s", code, err, text)
	}
	v := list.Plans[0]
	if v.ID != id || v.Status != "partly-stale" || len(v.Conflicts) != 1 || len(v.Conflicts["a"]) != 1 || !strings.HasPrefix(v.Conflicts["a"][0], "a changed in state") ||
		strings.Join(v.ApplyOnly, ",") != "b,c" || v.ApplyOnlyCommand != "pravo apply "+id+" --only b,c" || v.ApplyCommand != "pravo apply "+id || len(v.StaleReasons) != 0 {
		t.Errorf("list --json: %+v", v)
	}
	text, code = runPravo(t, dir, "--json", "plan", "show", id)
	var shown struct {
		Stored view
		Stale  *engine.ErrStalePlan
	}
	if err := json.Unmarshal([]byte(text), &shown); err != nil || code != 0 {
		t.Fatalf("show --json: exit %d: %v\n%s", code, err, text)
	}
	if shown.Stored.Status != "partly-stale" || strings.Join(shown.Stored.ApplyOnly, ",") != "b,c" || len(shown.Stored.Conflicts["a"]) != 1 || shown.Stale == nil {
		t.Errorf("show --json: %+v (stale %v)", shown.Stored, shown.Stale)
	}

	// The plan as a whole is stale, as it was; nothing is applied.
	text, code = runPravo(t, dir, "apply", id)
	if code != int(engine.ExitStale) || !strings.Contains(text, "a changed in state") {
		t.Errorf("apply: exit %d:\n%s", code, text)
	}
	if text, code = runPravo(t, dir, "apply", id, "--only", "a"); code != int(engine.ExitStale) {
		t.Errorf("apply --only a: exit %d:\n%s", code, text)
	}
	if fileExists(dir, "b.txt") || fileExists(dir, "c.txt") {
		t.Error("a refused apply created a file")
	}

	// A collection does not mark it, however often it runs.
	for i := 0; i < 2; i++ {
		if text, code = runPravo(t, dir, "plan", "gc"); code != 0 || !strings.Contains(text, "No stored plan needs to be removed.") {
			t.Errorf("gc: exit %d:\n%s", code, text)
		}
	}
	if text, code = runPravo(t, dir, "plan", "gc", "--dry-run"); code != 0 || !strings.Contains(text, "No stored plan needs to be removed.") {
		t.Errorf("gc --dry-run: exit %d:\n%s", code, text)
	}

	// The part that is live is applied, and what is left of the plan is partly stale still.
	text, code = runPravo(t, dir, "apply", id, "--only", "b")
	if code != 0 || !strings.Contains(text, "Apply complete!") || !strings.Contains(text, "1 added") {
		t.Fatalf("apply --only b: exit %d:\n%s", code, text)
	}
	if !fileExists(dir, "b.txt") || fileExists(dir, "c.txt") {
		t.Error("b.txt should exist and c.txt should not")
	}
	text, _ = runPravo(t, dir, "plan", "list")
	if !regexp.MustCompile(id+`\s+partly-stale\s+alice`).MatchString(text) ||
		!strings.Contains(text, "1 of 2 unapplied changes conflict: a changed in state (revision 1, plan expected 0); apply with --only c") {
		t.Errorf("list after b:\n%s", text)
	}
	text, _ = runPravo(t, dir, "plan", "show", id)
	if !strings.Contains(text, "1 of 3 changes applied") || !strings.Contains(text, "apply:   pravo apply "+id+" --only c\n") {
		t.Errorf("show after b:\n%s", text)
	}
	if text, code = runPravo(t, dir, "plan", "gc"); code != 0 || !strings.Contains(text, "No stored plan needs to be removed.") {
		t.Errorf("gc after b: exit %d:\n%s", code, text)
	}

	// The rest is applied with the flag that the listing gave.
	text, _ = runPravo(t, dir, "plan", "list")
	flag := regexp.MustCompile(`apply with --only (\S+)`).FindStringSubmatch(text)
	if flag == nil {
		t.Fatalf("no flag in:\n%s", text)
	}
	if text, code = runPravo(t, dir, "apply", id, "--only", flag[1]); code != 0 || !strings.Contains(text, "1 added") {
		t.Fatalf("apply --only %s: exit %d:\n%s", flag[1], code, text)
	}
	if !fileExists(dir, "c.txt") {
		t.Error("c.txt should exist")
	}

	// Only the change that conflicts is left: the plan is stale now, and a collection marks it, as it does a plan that is.
	text, _ = runPravo(t, dir, "plan", "list", "--all")
	if !regexp.MustCompile(id + `\s+stale\s+alice\s.*a changed in state`).MatchString(text) {
		t.Errorf("list --all at the end:\n%s", text)
	}
	if text, code = runPravo(t, dir, "plan", "gc"); code != 0 || !strings.Contains(text, "1 plan can no longer be applied") || !strings.Contains(text, id) {
		t.Errorf("gc at the end: exit %d:\n%s", code, text)
	}
}

func TestTheStatusColumnIsAsWideAsTheLongestStatus(t *testing.T) {
	dir, id := storedThenMoved(t)
	other := storePlanAs(t, dir, "bob")
	text, code := runPravo(t, dir, "plan", "list")
	if code != 0 {
		t.Fatalf("exit %d:\n%s", code, text)
	}
	column := map[string]int{}
	for _, line := range strings.Split(text, "\n") {
		for _, who := range []string{"alice", "bob"} {
			if strings.HasPrefix(line, id) && who == "alice" || strings.HasPrefix(line, other) && who == "bob" {
				column[who] = strings.Index(line, who)
			}
		}
	}
	if column["alice"] <= 0 || column["alice"] != column["bob"] {
		t.Errorf("the author column is at %v:\n%s", column, text)
	}
	if !regexp.MustCompile(other + `\s+pending\s+bob`).MatchString(text) {
		t.Errorf("the other plan is not pending:\n%s", text)
	}
}
