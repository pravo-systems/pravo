package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A plan that refreshes a record cannot write it when its author may only read the
// state. The stored plan must still apply: what it expects of the record is the
// revision that the state holds, not the one the refresh would have written.
func TestStoredPlanOfARefreshThatWasNotWrittenIsNotStale(t *testing.T) {
	rt := &runContext{}
	t.Cleanup(func() { stateInMemory = false }) // --no-write sets it for the process

	prov := newDemoProvider(t)
	dir := demoEstate(t, rt, "keep: $demo.#Resource.demo_server & {name: \"keep\"}\nlost: $demo.#Resource.demo_server & {name: \"lost\"}\n")
	if text, code := runPravo(t, rt, dir, "apply", "--auto-approve"); code != 0 {
		t.Fatalf("apply exited %d:\n%s", code, text)
	}
	prov.remove("srv-lost")

	text, code := runPravo(t, rt, dir, "drift", "--plan", "--store", "--no-write")
	if code != 2 || !strings.Contains(text, "Stored the plan as") {
		t.Fatalf("drift --plan --store --no-write exited %d:\n%s", code, text)
	}
	id := storedID.FindString(text)
	if text, code = runPravo(t, rt, dir, "apply", id); code != 0 || !strings.Contains(text, "Apply complete!") {
		t.Fatalf("apply of %s exited %d:\n%s", id, code, text)
	}
	if !prov.has("srv-lost") {
		t.Fatalf("the apply did not put the server back:\n%s", text)
	}
}

// The layout of a team: the state is in a bucket, sealed, and the keys have roles. A reader
// plans and stores the plan, a writer applies it by its id. The plan refreshed a resource
// that had drifted, which the reader could not write, and a resource that the change leaves
// alone is read by one that it creates, through json.Marshal.
func TestAWriterAppliesWhatAReaderStoredAfterARefreshAndAJSONRead(t *testing.T) {
	rt := &runContext{}

	requireSealing(t)
	prov := newDemoProvider(t)
	ids := isolateIdentities(t, rt)
	e := newS3Env(t, false)
	_, adaLine := person(t, ids, "ada")
	_, danLine := person(t, ids, "dan")
	e.readers(t, adaLine+" role:writer", danLine+" role:reader")
	settings, err := os.ReadFile(filepath.Join(e.dir, "main.cue"))
	if err != nil {
		t.Fatal(err)
	}
	spec := func(body string) string {
		head := strings.Replace(string(settings), "package t\n", "package t\n\nimport (\n\t\"encoding/json\"\n\tdemo \"pravo.dev/providers/demo/demo\"\n)\n", 1)
		return head + "\n$demo: demo.#Provider & {$path: " + fmt.Sprintf("%q", demoBinary) + "}\n_unused: json.Marshal({})\n" + body
	}
	base := "net: $demo.#Resource.demo_server & {name: \"net\"}\ndns: {for k in [\"apps\", \"other\"] {(k): $demo.#Resource.demo_server & {name: k, size: net.id}}}\ndb: $demo.#Resource.demo_server & {name: \"db\"}\n"
	mustWrite(t, e.dir, "main.cue", spec(base))
	for _, args := range [][]string{{"init"}, {"protect", "init", "--encrypt", "--yes"}, {"--identity", "ada", "apply", "--auto-approve"}} {
		if text, code := runPravo(t, rt, e.dir, args...); code != 0 {
			t.Fatalf("%v exited %d:\n%s", args, code, text)
		}
	}

	// The database drifts, and the change adds a task that encodes the zone as JSON.
	prov.remove("srv-db")
	mustWrite(t, e.dir, "main.cue", spec(base+"cp: worker: def: $demo.#Resource.demo_server & {name: \"def\", size: json.Marshal([{z: dns.apps.id, n: net.id}])}\nfor i in [1, 2, 3] {\"extra\\(i)\": $demo.#Resource.demo_server & {name: \"extra\\(i)\", size: cp.worker.def.id}}\n"))
	before := map[string]string{}
	for k, v := range e.f.Objects {
		before[k] = string(v)
	}
	text, code := runPravo(t, rt, e.dir, "--identity", "dan", "plan", "--store", "--refresh")
	if code != 0 && code != 2 || !strings.Contains(text, "plan-") {
		t.Fatalf("a reader's plan --store exited %d:\n%s", code, text)
	}
	for k, v := range e.f.Objects {
		if old, ok := before[k]; (!ok || old != string(v)) && !strings.Contains(k, "/plans/") {
			t.Errorf("the plan of a reader wrote %s", k)
		}
	}
	id := storedID.FindString(text)
	text, code = runPravo(t, rt, e.dir, "--identity", "ada", "apply", id)
	if code != 0 || !strings.Contains(text, "Apply complete!") {
		t.Fatalf("a writer's apply of %s exited %d:\n%s", id, code, text)
	}
	if !prov.has("srv-db") || !prov.has("srv-def") {
		t.Fatalf("the apply did not create the database and the worker: %v\n%s", prov.catalog(), text)
	}
}
