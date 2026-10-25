package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A plan writes nothing to the state for what the specification declares about it: a
// record that $from moves, an object that $import brings in. It says what it will do, and
// the apply does it. These tests run the command against a real provider process.

const movedSpec = `
new: $demo.#Resource.demo_server & {name: "keep", $from: "old"}
`

func stateList(t *testing.T, dir string) string {
	t.Helper()
	text, code := runPravo(t, dir, "state", "list")
	if code != 0 {
		t.Fatalf("state list exited %d:\n%s", code, text)
	}
	return strings.TrimSpace(text)
}

func TestPlanDoesNotMoveARecord(t *testing.T) {
	newDemoProvider(t)
	dir := demoEstate(t, `old: $demo.#Resource.demo_server & {name: "keep"}`)
	if text, code := runPravo(t, dir, "apply", "-auto-approve"); code != 0 {
		t.Fatalf("apply exited %d:\n%s", code, text)
	}
	mustWrite(t, dir, "main.cue", strings.Replace(readFile(t, dir, "main.cue"), `old: $demo.#Resource.demo_server & {name: "keep"}`, movedSpec, 1))

	for i := 0; i < 2; i++ {
		text, code := runPravo(t, dir, "plan")
		if code != 0 {
			t.Fatalf("plan %d exited %d:\n%s", i+1, code, text)
		}
		contains(t, text, "old will be moved to new", "1 to move", "to move 1")
		lacks(t, text, "was moved")
		if got := stateList(t, dir); got != "old" {
			t.Fatalf("a plan moved the record (plan %d): %q", i+1, got)
		}
	}
	// A move is no change to an object: --detailed-exitcode says there are none.
	if _, code := runPravo(t, dir, "plan", "--detailed-exitcode"); code != 0 {
		t.Errorf("a plan with a move exited %d", code)
	}

	text, code := runPravo(t, dir, "apply", "-auto-approve")
	if code != 0 {
		t.Fatalf("apply exited %d:\n%s", code, text)
	}
	contains(t, text, "old will be moved to new", "old: moved to new")
	if got := stateList(t, dir); got != "new" {
		t.Errorf("the apply left %q", got)
	}
	if text, code := runPravo(t, dir, "plan", "--detailed-exitcode"); code != 0 || !strings.Contains(text, "No changes.") {
		t.Errorf("a plan after the move: %d\n%s", code, text)
	}
}

func TestSavedPlanMovesWhenItIsApplied(t *testing.T) {
	newDemoProvider(t)
	dir := demoEstate(t, `old: $demo.#Resource.demo_server & {name: "keep"}`)
	if text, code := runPravo(t, dir, "apply", "-auto-approve"); code != 0 {
		t.Fatalf("apply exited %d:\n%s", code, text)
	}
	mustWrite(t, dir, "main.cue", strings.Replace(readFile(t, dir, "main.cue"), `old: $demo.#Resource.demo_server & {name: "keep"}`, movedSpec, 1))
	file := filepath.Join(dir, "move.plan")
	if text, code := runPravo(t, dir, "plan", "-out", file); code != 0 {
		t.Fatalf("plan -out exited %d:\n%s", code, text)
	}
	if got := stateList(t, dir); got != "old" {
		t.Fatalf("saving a plan moved the record: %q", got)
	}
	// What the file lists is shown again, from the file.
	if text, code := runPravo(t, dir, "show", file); code != 0 || !strings.Contains(text, "old will be moved to new") {
		t.Errorf("show: %d\n%s", code, text)
	}
	text, code := runPravo(t, dir, "apply", file)
	if code != 0 {
		t.Fatalf("apply of the file exited %d:\n%s", code, text)
	}
	if got := stateList(t, dir); got != "new" {
		t.Errorf("the apply of the file left %q", got)
	}
	// The file is spent.
	if text, code := runPravo(t, dir, "apply", file); !strings.Contains(text, "nothing left to apply") && code == 0 {
		t.Errorf("a second apply: %d\n%s", code, text)
	}
}

func TestSavedPlanThatMovesIsStaleWhenTheRecordChanged(t *testing.T) {
	newDemoProvider(t)
	dir := demoEstate(t, `old: $demo.#Resource.demo_server & {name: "keep"}`)
	if text, code := runPravo(t, dir, "apply", "-auto-approve"); code != 0 {
		t.Fatalf("apply exited %d:\n%s", code, text)
	}
	mustWrite(t, dir, "main.cue", strings.Replace(readFile(t, dir, "main.cue"), `old: $demo.#Resource.demo_server & {name: "keep"}`, movedSpec, 1))
	file := filepath.Join(dir, "move.plan")
	if text, code := runPravo(t, dir, "plan", "-out", file); code != 0 {
		t.Fatalf("plan -out exited %d:\n%s", code, text)
	}
	// Somebody removes the record that the plan moves.
	if text, code := runPravo(t, dir, "state", "rm", "old"); code != 0 {
		t.Fatalf("state rm exited %d:\n%s", code, text)
	}
	text, code := runPravo(t, dir, "apply", file)
	if code == 0 || !strings.Contains(text, "old changed in state") {
		t.Errorf("a plan whose record is gone: %d\n%s", code, text)
	}
	if got := stateList(t, dir); got != "" {
		t.Errorf("the state holds %q", got)
	}
}

func TestPlanDoesNotImportAndTheApplyUpdatesWhatDiffers(t *testing.T) {
	newDemoProvider(t, demoObject{"srv-web-old", "web-old", "eu"})
	dir := demoEstate(t, `web: $demo.#Resource.demo_server & {name: "renamed", $import: "srv-web-old"}`)
	text, code := runPravo(t, dir, "plan")
	if code != 0 {
		t.Fatalf("plan exited %d:\n%s", code, text)
	}
	contains(t, text, "web will be imported from id", "srv-web-old", "will be updated in-place", "1 to import")
	if got := stateList(t, dir); got != "" {
		t.Fatalf("a plan wrote %q", got)
	}
	file := filepath.Join(dir, "adopt.plan")
	if text, code := runPravo(t, dir, "plan", "-out", file); code != 0 {
		t.Fatalf("plan -out exited %d:\n%s", code, text)
	}
	if got := stateList(t, dir); got != "" {
		t.Fatalf("saving a plan wrote %q", got)
	}
	// The file carries the object that the plan read: the apply of it imports and updates.
	if text, code := runPravo(t, dir, "apply", file); code != 0 {
		t.Fatalf("apply of the file exited %d:\n%s", code, text)
	}
	if got := stateList(t, dir); got != "web" {
		t.Errorf("the apply left %q", got)
	}
	if text, _ := runPravo(t, dir, "state", "show", "web"); !strings.Contains(text, `"renamed"`) {
		t.Errorf("the update was not applied:\n%s", text)
	}
	if text, code := runPravo(t, dir, "plan"); code != 0 || !strings.Contains(text, "No changes.") {
		t.Errorf("a plan after the apply: %d\n%s", code, text)
	}
}

func readFile(t *testing.T, dir, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// What depends on a record that moves is told of it in the plan, and not before: the apply
// writes the new address into the records that name the old one.
func TestMoveOfARecordThatOthersReadKeepsTheGraphWhole(t *testing.T) {
	newDemoProvider(t)
	before := `
old: $demo.#Resource.demo_server & {name: "base"}
user: $demo.#Resource.demo_server & {name: "uses-\(old.id)"}
`
	dir := demoEstate(t, before)
	if text, code := runPravo(t, dir, "apply", "-auto-approve"); code != 0 {
		t.Fatalf("apply exited %d:\n%s", code, text)
	}
	after := `
new: $demo.#Resource.demo_server & {name: "base", $from: "old"}
user: $demo.#Resource.demo_server & {name: "uses-\(new.id)"}
`
	mustWrite(t, dir, "main.cue", strings.Replace(readFile(t, dir, "main.cue"), before, after, 1))
	text, code := runPravo(t, dir, "plan")
	if code != 0 {
		t.Fatalf("plan exited %d:\n%s", code, text)
	}
	contains(t, text, "old will be moved to new", "1 to move")
	if got := stateList(t, dir); got != "old\nuser" {
		t.Fatalf("a plan wrote: %q", got)
	}
	if text, code := runPravo(t, dir, "apply", "-auto-approve"); code != 0 {
		t.Fatalf("apply exited %d:\n%s", code, text)
	}
	if got := stateList(t, dir); got != "new\nuser" {
		t.Errorf("the apply left %q", got)
	}
	if text, code := runPravo(t, dir, "plan", "--detailed-exitcode"); code != 0 || !strings.Contains(text, "No changes.") {
		t.Errorf("a plan after the move: %d\n%s", code, text)
	}
	// What the state says of the dependency now.
	if text, _ := runPravo(t, dir, "state", "show", "user"); strings.Contains(text, "old") && strings.Contains(text, "depends") {
		t.Errorf("a record still depends on the old address:\n%s", text)
	}
}

// A provider $from renames the provider of the records. The plan lists it and writes nothing;
// the apply writes the label.
func TestPlanDoesNotRelabelAProvider(t *testing.T) {
	newDemoProvider(t)
	dir := demoEstate(t, `item: $demo.#Resource.demo_server & {name: "keep"}`)
	if text, code := runPravo(t, dir, "apply", "-auto-approve"); code != 0 {
		t.Fatalf("apply exited %d:\n%s", code, text)
	}
	shown, _ := runPravo(t, dir, "state", "show", "item")
	cur := ""
	for _, w := range strings.Fields(shown) {
		if strings.HasPrefix(w, "registry.") || strings.HasPrefix(w, "pravo.dev/") || strings.Contains(w, "/demo") {
			cur = w
			break
		}
	}
	if cur == "" {
		t.Fatalf("no provider in:\n%s", shown)
	}
	// The record names another provider than the specification does.
	old := "registry.example.com/acme/demo"
	patched := false
	err := filepath.Walk(filepath.Join(dir, "pravo.state"), func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || !strings.HasSuffix(path, ".json") {
			return err
		}
		b, _ := os.ReadFile(path)
		if strings.Contains(string(b), `"provider": "`+cur+`"`) || strings.Contains(string(b), `"provider":"`+cur+`"`) {
			s := strings.ReplaceAll(string(b), cur, old)
			patched = true
			return os.WriteFile(path, []byte(s), 0o644)
		}
		return nil
	})
	if err != nil || !patched {
		t.Fatalf("could not change the provider of the record: %v (patched %v)", err, patched)
	}
	if text, code := runPravo(t, dir, "plan"); code == 0 {
		t.Fatalf("a plan did not ask for $from:\n%s", text)
	}
	spec := readFile(t, dir, "main.cue")
	mustWrite(t, dir, "main.cue", strings.Replace(spec, "demo.#Provider & {", `demo.#Provider & {$from: "`+old+`", `, 1))

	for i := 0; i < 2; i++ {
		text, code := runPravo(t, dir, "plan")
		if code != 0 {
			t.Fatalf("plan %d exited %d:\n%s", i+1, code, text)
		}
		contains(t, text, "item will have its provider changed from "+old+" to "+cur)
		lacks(t, text, "No changes.")
		if got, _ := runPravo(t, dir, "state", "show", "item"); !strings.Contains(got, old) {
			t.Fatalf("a plan relabelled the record (plan %d):\n%s", i+1, got)
		}
	}
	if _, code := runPravo(t, dir, "plan", "--detailed-exitcode"); code != 0 {
		t.Errorf("a relabel is no change to an object, but the plan exited %d", code)
	}
	if text, code := runPravo(t, dir, "apply", "-auto-approve"); code != 0 {
		t.Fatalf("apply exited %d:\n%s", code, text)
	}
	if got, _ := runPravo(t, dir, "state", "show", "item"); !strings.Contains(got, cur) || strings.Contains(got, old) {
		t.Errorf("the apply left the record as:\n%s", got)
	}
	if text, code := runPravo(t, dir, "plan"); code != 0 || !strings.Contains(text, "No changes.") {
		t.Errorf("a plan after the relabel: %d\n%s", code, text)
	}
}
