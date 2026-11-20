package main

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/pravo-systems/pravo/internal/engine"
)

// A plan is stored when applying it would change the state, whatever it changes: resources,
// outputs, moves, imports. These tests run the command against the demo provider, and
// against no provider at all for the outputs.

const outputOnlyBody = "package estate\n\nimport \"pravo.dev/pravo\"\n\ngreeting: pravo.#Output & {value: \"%s\"}\n"

func outputRoot(t *testing.T, rt *runContext, value string) string {
	t.Helper()
	dir := specDir(t, map[string]string{"main.cue": strings.Replace(outputOnlyBody, "%s", value, 1)})
	if text, code := runPravo(t, rt, dir, "init"); code != 0 {
		t.Fatalf("init exited %d:\n%s", code, text)
	}
	return dir
}

// storedKinds reads the kinds of a stored plan from "pravo plan show --json" and from the list.
func storedKinds(t *testing.T, rt *runContext, dir, id string) []string {
	t.Helper()
	text, code := runPravo(t, rt, dir, "plan", "show", id, "--json")
	if code != 0 {
		t.Fatalf("plan show --json exited %d:\n%s", code, text)
	}
	var doc struct {
		Stored struct {
			Kinds []string `json:"kinds"`
		} `json:"stored"`
		Plan struct {
			Kinds []string `json:"kinds"`
		} `json:"plan"`
	}
	if err := json.Unmarshal([]byte(text), &doc); err != nil {
		t.Fatalf("plan show --json: %v\n%s", err, text)
	}
	if strings.Join(doc.Stored.Kinds, ",") != strings.Join(doc.Plan.Kinds, ",") {
		t.Errorf("the stored plan says %v, the plan %v", doc.Stored.Kinds, doc.Plan.Kinds)
	}
	return doc.Plan.Kinds
}

func wantKinds(t *testing.T, got []string, want ...string) {
	t.Helper()
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("kinds %v, want %v", got, want)
	}
}

func TestAPlanThatOnlyChangesAnOutputIsStoredAndApplied(t *testing.T) {
	rt := &runContext{}

	dir := outputRoot(t, rt, "hi")
	if text, code := runPravo(t, rt, dir, "apply", "-auto-approve"); code != 0 {
		t.Fatalf("apply exited %d:\n%s", code, text)
	}
	mustWrite(t, dir, "main.cue", strings.Replace(outputOnlyBody, "%s", "hello", 1))

	text, code := runPravo(t, rt, dir, "plan", "--store")
	if code != 0 || !strings.Contains(text, "Stored the plan as") || strings.Contains(text, "Nothing to store") {
		t.Fatalf("exit %d:\n%s", code, text)
	}
	contains(t, text, "The plan changes: outputs")
	id := storedID.FindString(text[strings.Index(text, "Stored the plan as"):])
	wantKinds(t, storedKinds(t, rt, dir, id), "outputs")
	if text, code := runPravo(t, rt, dir, "plan", "show", id); code != 0 || !strings.Contains(text, "changes: outputs") {
		t.Errorf("plan show: exit %d:\n%s", code, text)
	}
	if text, code := runPravo(t, rt, dir, "plan", "list", "--json"); code != 0 || !strings.Contains(text, `"kinds":["outputs"]`) && !strings.Contains(text, `"outputs"`) {
		t.Errorf("plan list --json: exit %d:\n%s", code, text)
	}
	if text, _ := runPravo(t, rt, dir, "output", "greeting"); strings.TrimSpace(text) != `"hi"` {
		t.Fatalf("the plan wrote the output: %s", text)
	}
	if text, code := runPravo(t, rt, dir, "apply", id); code != 0 {
		t.Fatalf("apply %s exited %d:\n%s", id, code, text)
	}
	if text, _ := runPravo(t, rt, dir, "output", "greeting"); strings.TrimSpace(text) != `"hello"` {
		t.Errorf("the output after the apply: %s", text)
	}
	// What is applied is not stored again.
	if text, code := runPravo(t, rt, dir, "plan", "--store"); code != 0 || !strings.Contains(text, "Nothing to store: the plan has no changes.") {
		t.Errorf("a plan with nothing to do: exit %d:\n%s", code, text)
	}
}

func TestAStoredPlanOfAnOutputIsStaleWhenTheConfigurationChanges(t *testing.T) {
	rt := &runContext{}

	dir := outputRoot(t, rt, "hi")
	if text, code := runPravo(t, rt, dir, "apply", "-auto-approve"); code != 0 {
		t.Fatalf("apply exited %d:\n%s", code, text)
	}
	mustWrite(t, dir, "main.cue", strings.Replace(outputOnlyBody, "%s", "hello", 1))
	id := storePlanAs(t, rt, dir, "alice")
	mustWrite(t, dir, "main.cue", strings.Replace(outputOnlyBody, "%s", "again", 1))
	text, code := runPravo(t, rt, dir, "apply", id)
	if code != int(engine.ExitStale) || !strings.Contains(text, "the configuration changed since the plan was made") {
		t.Errorf("exit %d:\n%s", code, text)
	}
	if text, _ := runPravo(t, rt, dir, "output", "greeting"); strings.TrimSpace(text) != `"hi"` {
		t.Errorf("a stale plan wrote the output: %s", text)
	}
}

func TestAStoredPlanOfAnOutputIsStaleWhenWhatItReadsChanged(t *testing.T) {
	rt := &runContext{}

	newDemoProvider(t)
	const head = "package estate\n\nimport (\n\t\"pravo.dev/pravo\"\n\tdemo \"pravo.dev/providers/demo/demo\"\n)\n\n"
	spec := func(value string) string {
		return head + "$demo: demo.#Provider & {$path: " + fmt.Sprintf("%q", demoBinary) + "}\n" +
			`srv: $demo.#Resource.demo_server & {name: "one"}` + "\n" +
			`label: pravo.#Output & {value: ` + value + `}` + "\n"
	}
	dir := specDir(t, map[string]string{"main.cue": spec("srv.name")})
	if text, code := runPravo(t, rt, dir, "init"); code != 0 {
		t.Fatalf("init exited %d:\n%s", code, text)
	}
	if text, code := runPravo(t, rt, dir, "apply", "-auto-approve"); code != 0 {
		t.Fatalf("apply exited %d:\n%s", code, text)
	}
	// Another actor changes the record that the output reads, after the plan was stored.
	mustWrite(t, dir, "main.cue", spec(`"fixed-" + srv.name`))
	id := storePlanAs(t, rt, dir, "alice")
	wantKinds(t, storedKinds(t, rt, dir, id), "outputs")
	if text, code := runPravo(t, rt, dir, "state", "rm", "srv"); code != 0 {
		t.Fatalf("state rm exited %d:\n%s", code, text)
	}
	text, code := runPravo(t, rt, dir, "apply", id)
	if code != int(engine.ExitStale) || !strings.Contains(text, "srv changed in state") {
		t.Errorf("exit %d:\n%s", code, text)
	}
}

func TestAPlanThatOnlyMovesARecordIsStoredAndApplied(t *testing.T) {
	rt := &runContext{}

	newDemoProvider(t)
	dir := demoEstate(t, rt, `old: $demo.#Resource.demo_server & {name: "keep"}`)
	if text, code := runPravo(t, rt, dir, "apply", "-auto-approve"); code != 0 {
		t.Fatalf("apply exited %d:\n%s", code, text)
	}
	mustWrite(t, dir, "main.cue", strings.Replace(readFile(t, dir, "main.cue"), `old: $demo.#Resource.demo_server & {name: "keep"}`, movedSpec, 1))
	id := storePlanAs(t, rt, dir, "alice")
	wantKinds(t, storedKinds(t, rt, dir, id), "moves")
	if got := stateList(t, rt, dir); got != "old" {
		t.Fatalf("storing the plan moved the record: %q", got)
	}
	if text, code := runPravo(t, rt, dir, "apply", id); code != 0 {
		t.Fatalf("apply %s exited %d:\n%s", id, code, text)
	}
	if got := stateList(t, rt, dir); got != "new" {
		t.Errorf("the apply left %q", got)
	}
}

func TestAStoredPlanOfAMoveIsStale(t *testing.T) {
	rt := &runContext{}

	newDemoProvider(t)
	dir := demoEstate(t, rt, `old: $demo.#Resource.demo_server & {name: "keep"}`)
	if text, code := runPravo(t, rt, dir, "apply", "-auto-approve"); code != 0 {
		t.Fatalf("apply exited %d:\n%s", code, text)
	}
	mustWrite(t, dir, "main.cue", strings.Replace(readFile(t, dir, "main.cue"), `old: $demo.#Resource.demo_server & {name: "keep"}`, movedSpec, 1))
	byState := storePlanAs(t, rt, dir, "alice")
	if text, code := runPravo(t, rt, dir, "state", "rm", "old"); code != 0 {
		t.Fatalf("state rm exited %d:\n%s", code, text)
	}
	if text, code := runPravo(t, rt, dir, "apply", byState); code != int(engine.ExitStale) || !strings.Contains(text, "old changed in state") {
		t.Errorf("a record that is gone: exit %d:\n%s", code, text)
	}
	// And by the configuration: a plan made for the move is no good for another specification.
	dir = demoEstate(t, rt, `old: $demo.#Resource.demo_server & {name: "keep"}`)
	if text, code := runPravo(t, rt, dir, "apply", "-auto-approve"); code != 0 {
		t.Fatalf("apply exited %d:\n%s", code, text)
	}
	mustWrite(t, dir, "main.cue", strings.Replace(readFile(t, dir, "main.cue"), `old: $demo.#Resource.demo_server & {name: "keep"}`, movedSpec, 1))
	byConfig := storePlanAs(t, rt, dir, "alice")
	mustWrite(t, dir, "main.cue", readFile(t, dir, "main.cue")+"\n// another change\n")
	if text, code := runPravo(t, rt, dir, "apply", byConfig); code != int(engine.ExitStale) || !strings.Contains(text, "the configuration changed since the plan was made") {
		t.Errorf("a changed configuration: exit %d:\n%s", code, text)
	}
	if got := stateList(t, rt, dir); got != "old" {
		t.Errorf("a stale plan moved the record: %q", got)
	}
}

func TestAPlanThatOnlyImportsAnObjectIsStoredAndApplied(t *testing.T) {
	rt := &runContext{}

	newDemoProvider(t, demoObject{"srv-web-old", "web-old", "eu"})
	dir := demoEstate(t, rt, `web: $demo.#Resource.demo_server & {name: "web-old", size: "large", secret: "hunter2", $import: "srv-web-old"}`)
	text, code := runPravo(t, rt, dir, "plan", "--store", "--actor", "alice")
	if code != 0 || !strings.Contains(text, "Stored the plan as") {
		t.Fatalf("exit %d:\n%s", code, text)
	}
	id := storedID.FindString(text[strings.Index(text, "Stored the plan as"):])
	wantKinds(t, storedKinds(t, rt, dir, id), "imports")
	if got := stateList(t, rt, dir); got != "" {
		t.Fatalf("storing the plan imported %q", got)
	}
	if text, code := runPravo(t, rt, dir, "apply", id); code != 0 {
		t.Fatalf("apply %s exited %d:\n%s", id, code, text)
	}
	if got := stateList(t, rt, dir); got != "web" {
		t.Errorf("the apply left %q", got)
	}
}

func TestAStoredPlanOfAnImportIsStale(t *testing.T) {
	rt := &runContext{}

	newDemoProvider(t, demoObject{"srv-web-old", "web-old", "eu"})
	dir := demoEstate(t, rt, `web: $demo.#Resource.demo_server & {name: "web-old", size: "large", secret: "hunter2", $import: "srv-web-old"}`)
	byConfig := storePlanAs(t, rt, dir, "alice")
	mustWrite(t, dir, "main.cue", readFile(t, dir, "main.cue")+"\n// another change\n")
	if text, code := runPravo(t, rt, dir, "apply", byConfig); code != int(engine.ExitStale) || !strings.Contains(text, "the configuration changed since the plan was made") {
		t.Errorf("a changed configuration: exit %d:\n%s", code, text)
	}
	if got := stateList(t, rt, dir); got != "" {
		t.Errorf("a stale plan imported %q", got)
	}
}

func TestAPlanThatChangesNothingIsNotStoredWhatEverItDeclares(t *testing.T) {
	rt := &runContext{}

	newDemoProvider(t)
	dir := demoEstate(t, rt, `srv: $demo.#Resource.demo_server & {name: "keep"}`)
	if text, code := runPravo(t, rt, dir, "apply", "-auto-approve"); code != 0 {
		t.Fatalf("apply exited %d:\n%s", code, text)
	}
	text, code := runPravo(t, rt, dir, "plan", "--store")
	if code != 0 || !strings.Contains(text, "Nothing to store: the plan has no changes.") {
		t.Errorf("exit %d:\n%s", code, text)
	}
	if got := planObjects(t, dir); len(got) != 0 {
		t.Errorf("stored %v", got)
	}
}

func TestAnOutputThatKeepsItsValueIsNoKindOfChange(t *testing.T) {
	rt := &runContext{}

	newDemoProvider(t)
	const head = "package estate\n\nimport (\n\t\"pravo.dev/pravo\"\n\tdemo \"pravo.dev/providers/demo/demo\"\n)\n\n"
	spec := func(size string) string {
		return head + "$demo: demo.#Provider & {$path: " + fmt.Sprintf("%q", demoBinary) + "}\n" +
			`srv: $demo.#Resource.demo_server & {name: "one", size: "` + size + `"}` + "\n" +
			"label: pravo.#Output & {value: srv.name}\n"
	}
	dir := specDir(t, map[string]string{"main.cue": spec("small")})
	if text, code := runPravo(t, rt, dir, "init"); code != 0 {
		t.Fatalf("init exited %d:\n%s", code, text)
	}
	if text, code := runPravo(t, rt, dir, "apply", "-auto-approve"); code != 0 {
		t.Fatalf("apply exited %d:\n%s", code, text)
	}
	// The output reads a resource that the plan updates, and its value stays.
	mustWrite(t, dir, "main.cue", spec("large"))
	id := storePlanAs(t, rt, dir, "alice")
	wantKinds(t, storedKinds(t, rt, dir, id), "resources")
}

func TestAPlanThatOnlyRemovesAnOutputIsStoredAndApplied(t *testing.T) {
	rt := &runContext{}

	dir := specDir(t, map[string]string{"main.cue": "package estate\n\nimport \"pravo.dev/pravo\"\n\ngreeting: pravo.#Output & {value: \"hi\"}\nfarewell: pravo.#Output & {value: \"bye\"}\n"})
	if text, code := runPravo(t, rt, dir, "init"); code != 0 {
		t.Fatalf("init exited %d:\n%s", code, text)
	}
	if text, code := runPravo(t, rt, dir, "apply", "-auto-approve"); code != 0 {
		t.Fatalf("apply exited %d:\n%s", code, text)
	}
	mustWrite(t, dir, "main.cue", "package estate\n\nimport \"pravo.dev/pravo\"\n\ngreeting: pravo.#Output & {value: \"hi\"}\n")
	id := storePlanAs(t, rt, dir, "alice")
	wantKinds(t, storedKinds(t, rt, dir, id), "outputs")
	if text, code := runPravo(t, rt, dir, "apply", id); code != 0 {
		t.Fatalf("apply %s exited %d:\n%s", id, code, text)
	}
	if text, code := runPravo(t, rt, dir, "output", "farewell"); code == 0 {
		t.Errorf("the removed output is still there: %s", text)
	}
}
