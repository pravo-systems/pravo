package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// pravo policy test says which plans a rule would refuse, exits 5 for one that would be, 6 for a rule
// that cannot be used, and leaves the files and the state as they were.
func TestPolicyTestHoldsADraftAgainstAPlanFile(t *testing.T) {
	r := newPolicyRoot(t, nil, "a", "b", "c")
	if text, code := r.apply(t); code != 0 {
		t.Fatalf("apply: exit %d\n%s", code, text)
	}
	r.declare(t, nil, "c")
	plan := filepath.Join(t.TempDir(), "p.plan")
	if text, code := runPravo(t, r.dir, "plan", "--out", plan); code != 0 {
		t.Fatalf("plan: exit %d\n%s", code, text)
	}
	draft := func(name, text string) string {
		p := filepath.Join(t.TempDir(), name)
		if err := os.WriteFile(p, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	before, _ := os.ReadFile(filepath.Join(r.dir, "pravo.state"))

	text, code := runPravo(t, r.dir, "policy", "test", draft("few.cue", "summary: destroy: <=1"), "--plan", plan)
	if code != 5 || !strings.Contains(text, "would be refused") || !strings.Contains(text, "summary.destroy") {
		t.Errorf("a rule that refuses: exit %d\n%s", code, text)
	}
	text, code = runPravo(t, r.dir, "policy", "test", draft("many.cue", "summary: destroy: <=2"), "--plan", plan)
	if code != 0 || !strings.Contains(text, "holds") {
		t.Errorf("a rule that holds: exit %d\n%s", code, text)
	}
	text, code = runPravo(t, r.dir, "policy", "test", draft("bad.cue", "summary: destroy: <="), "--plan", plan)
	if code != 6 || !strings.Contains(text, "cannot be used") {
		t.Errorf("a rule that is invalid: exit %d\n%s", code, text)
	}
	if text, code = runPravo(t, r.dir, "policy", "test", draft("few.cue", "summary: destroy: <=1"), "--plan", plan, "--json"); code != 5 || !strings.Contains(text, `"outcome": "refused"`) {
		t.Errorf("--json: exit %d\n%s", code, text)
	}
	if after, _ := os.ReadFile(filepath.Join(r.dir, "pravo.state")); string(after) != string(before) {
		t.Errorf("policy test changed the state")
	}
	if _, err := os.Stat(filepath.Join(r.dir, "a.txt")); err != nil {
		t.Errorf("policy test applied the plan: %v", err)
	}
}
