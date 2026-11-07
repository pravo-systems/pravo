package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// protectedRule refuses to delete or replace a resource that carried the label
// protected when its record was last written.
const protectedRule = `changes: [...pravo.#Change & {
	action: string
	addr:   string
	before_labels?: {protected?: string, ...}
	if (action == "delete" || action == "replace") && before_labels != _|_ {
		if before_labels.protected != _|_ {
			error("\(addr) is protected: remove the label in a reviewed change first")
		}
	}
}]`

// labelledRoot has a policy that protects labelled resources, and the resources vault
// (labelled) and scratch.
func labelledRoot(t *testing.T, rt *runContext) *policyRoot {
	t.Helper()
	r := newPolicyRoot(t, rt, map[string]string{"keep_protected": protectedRule})
	r.append(t, `
vault: $local.#Resource.local_file & {
	filename: "vault.txt"
	content:  "x"
	$labels: protected: "true"
}
scratch: $local.#Resource.local_file & {filename: "scratch.txt", content: "x"}
`)
	return r
}

func (r *policyRoot) append(t *testing.T, extra string) {
	t.Helper()
	main := filepath.Join(r.dir, "main.cue")
	b, err := os.ReadFile(main)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(main, append(b, extra...), 0o644); err != nil {
		t.Fatal(err)
	}
}

func (r *policyRoot) replace(t *testing.T, old, new string) {
	t.Helper()
	main := filepath.Join(r.dir, "main.cue")
	b, err := os.ReadFile(main)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), old) {
		t.Fatalf("main.cue has no %q:\n%s", old, b)
	}
	if err := os.WriteFile(main, []byte(strings.Replace(string(b), old, new, 1)), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestLabelsReachThePlanDocumentBeforeAndAfter(t *testing.T) {
	rt := &runContext{}
	r := labelledRoot(t, rt)
	text, code := runPravo(t, rt, r.dir, "plan", "--json")
	if code != 0 || !strings.Contains(text, `"labels": {`) {
		t.Fatalf("plan --json: exit %d\n%s", code, text)
	}
	if strings.Contains(text, "before_labels") {
		t.Fatalf("a create has no before_labels:\n%s", text)
	}
	if text, code = r.apply(t, rt); code != 0 {
		t.Fatalf("apply: exit %d\n%s", code, text)
	}
	// Taking the block away shows the recorded label as before_labels, and the policy refuses.
	r.replace(t, "vault: $local", "#vault: $local")
	text, code = runPravo(t, rt, r.dir, "plan", "--json")
	if !strings.Contains(text, `"before_labels"`) || !strings.Contains(text, `"protected": "true"`) {
		t.Fatalf("plan --json after taking the block away: exit %d\n%s", code, text)
	}
	text, code = r.apply(t, rt)
	if code == 0 || !strings.Contains(text, "vault is protected") {
		t.Fatalf("the policy should refuse: exit %d\n%s", code, text)
	}
	// A resource with no label is not held.
	r.replace(t, "#vault: $local", "vault: $local")
	r.replace(t, "scratch: $local", "#scratch: $local")
	if text, code = r.apply(t, rt); code != 0 {
		t.Fatalf("deleting scratch: exit %d\n%s", code, text)
	}
}

func TestAChangedLabelIsRecordedAndSeenAsBefore(t *testing.T) {
	rt := &runContext{}
	r := labelledRoot(t, rt)
	if text, code := r.apply(t, rt); code != 0 {
		t.Fatalf("apply: exit %d\n%s", code, text)
	}
	// Taking the label off is a change of the record, even though the attributes stay.
	r.replace(t, `$labels: protected: "true"`, `$labels: protected: "false"`)
	text, code := runPravo(t, rt, r.dir, "plan", "--json", "--include-skipped")
	if code != 0 || !strings.Contains(text, `"protected": "false"`) {
		t.Fatalf("plan --json: exit %d\n%s", code, text)
	}
	if text, code = r.apply(t, rt); code != 0 {
		t.Fatalf("apply: exit %d\n%s", code, text)
	}
	text, code = runPravo(t, rt, r.dir, "plan", "--json")
	if code != 0 || !strings.Contains(text, `"changes": []`) {
		t.Fatalf("the plan after the apply should be empty: exit %d\n%s", code, text)
	}
}

// A rule that reads an attribute which is null in some changes.
const nullRule = `changes: [...pravo.#Change & {
	action: string
	after?: {content_base64?: {x: string}, ...}
}]`

const fixedRule = `changes: [...pravo.#Change & {
	action: string
	after?: {content_base64?: null | {x: string}, ...}
}]`

func TestAFaultyPolicyNamesItsFileAndLineAndCanBeFixed(t *testing.T) {
	rt := &runContext{}
	r := newPolicyRoot(t, rt, map[string]string{"faulty": nullRule}, "a")
	if text, code := r.apply(t, rt); code != 0 {
		t.Fatalf("first apply: exit %d\n%s", code, text)
	}
	r.declare(t, map[string]string{"faulty": nullRule}, "a", "b")
	text, code := runPravo(t, rt, r.dir, "plan")
	if code == 0 {
		t.Fatalf("the faulty rule should refuse:\n%s", text)
	}
	for _, want := range []string{"main.cue:", "in the field changes of the policy faulty", "Hint: an attribute that is not set is null", "field?: null | {...}"} {
		if !strings.Contains(text, want) {
			t.Errorf("plan output lacks %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "faulty:") && !strings.Contains(text, "policy faulty@") {
		t.Errorf("a generated name is left in the positions:\n%s", text)
	}
	// The fix together with the resource change is judged by the version in force, and
	// says how to leave.
	r.declare(t, map[string]string{"faulty": fixedRule}, "a", "b")
	text, code = r.apply(t, rt)
	if code == 0 || !strings.Contains(text, "This plan also replaces the policy faulty") {
		t.Fatalf("the plan that replaces the policy: exit %d\n%s", code, text)
	}
	if !strings.Contains(text, "version 1, replaced by version 2") {
		// The apply refused before the text; the plan prints the entry.
		if plan, _ := runPravo(t, rt, r.dir, "plan"); !strings.Contains(plan, "faulty (version 1, replaced by version 2") {
			t.Errorf("the plan should say what version the policy becomes:\n%s", plan)
		}
	}
	// The fix alone is not judged, and the change then passes under the new version.
	r.declare(t, map[string]string{"faulty": fixedRule}, "a")
	if text, code = r.apply(t, rt); code != 0 {
		t.Fatalf("applying the fix alone: exit %d\n%s", code, text)
	}
	r.declare(t, map[string]string{"faulty": fixedRule}, "a", "b")
	if text, code = r.apply(t, rt); code != 0 {
		t.Fatalf("applying b under the fixed policy: exit %d\n%s", code, text)
	}
}

func TestAnUndeclaredOptionalFieldGetsAHint(t *testing.T) {
	rt := &runContext{}
	spec := "package infra\n\nimport \"pravo.dev/pravo\"\n\nreads_before: pravo.#Policy & {rule: {\n\tchanges: [...{action: string, if action == \"delete\" {x: before.id}}]\n}}\n"
	dir := specDir(t, map[string]string{"main.cue": spec})
	text, code := runPravo(t, rt, dir, "init")
	if code == 0 || !strings.Contains(text, "main.cue:6") || !strings.Contains(text, "Hint: before and after are absent from some changes") {
		t.Fatalf("plan: exit %d\n%s", code, text)
	}
}
