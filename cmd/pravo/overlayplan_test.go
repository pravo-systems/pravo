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
	// The dependencies are read from the evaluator, and from the syntax alone, which a
	// graph falls back to when the evaluator's edges make a cycle.
	for _, mode := range []string{"evaluator", "syntax"} {
		t.Run(mode, func(t *testing.T) {
			t.Setenv("PRAVO_DEPENDENCIES", mode)
			writerAppliesWhatAReaderStored(t)
		})
	}
}

func writerAppliesWhatAReaderStored(t *testing.T) {
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
		head := strings.Replace(string(settings), "package t\n", "package t\n\nimport (\n\t\"encoding/json\"\n\t\"pravo.dev/pravo\"\n\tdemo \"pravo.dev/providers/demo/demo\"\n)\n", 1)
		return head + "\n$demo: demo.#Provider & {$path: " + fmt.Sprintf("%q", demoBinary) + "}\n_unused: json.Marshal({})\nnameServers: pravo.#Output & {value: {apps: dns.apps.id, other: dns.other.id}}\n_unusedHex: hex.Encode(sha256.Sum256(\"\"))\n" + body
	}
	base := "net: $demo.#Resource.demo_server & {name: \"net\"}\ndns: {for k in [\"apps\", \"other\"] {(k): $demo.#Resource.demo_server & {name: k, size: net.id}}}\ndb: $demo.#Resource.demo_server & {name: \"db\"}\n"
	// The shape of the control plane of a real estate: a task definition that encodes, with
	// json.Marshal, an environment read through a let from a zone that the change leaves
	// alone and from resources that the change creates, one through a function.
	cp := func(events bool) string {
		env := `{ZONE: dns.apps.id, NET: net.id`
		out := `
cp: {
	let environment = [for k, v in ` + env
		if events {
			env = `, BUS: events.busArnValue, SHA: events.keySha256`
			out += env
		}
		out += `} {name: k, value: v}]
	#Service: {
		role: string
		def: $demo.#Resource.demo_server & {
			name: "def-\(role)"
			size: json.Marshal([{name: role, "environment": list.Concat([environment, [{name: "ID", value: role}]])}])
		}
		let _def = def
		service: $demo.#Resource.demo_server & {name: "svc-\(role)", size: _def.id}
	}
	worker: #Service & {role: "worker"}
}
`
		if events {
			out += `
events: {
	bus: $demo.#Resource.demo_server & {name: "bus"}
	let _bus = bus
	key: $demo.#Resource.demo_server & {name: "key"}
	let _key = key
	busArnValue: _bus.id
	keySha256:   hex.Encode(sha256.Sum256(_key.id))
}
`
		}
		return out
	}
	imports := func(text string) string {
		return strings.Replace(text, "\"encoding/json\"", "\"encoding/json\"\n\t\"encoding/hex\"\n\t\"crypto/sha256\"\n\t\"list\"", 1)
	}
	mustWrite(t, e.dir, "main.cue", imports(spec(base+cp(false))))
	for _, args := range [][]string{{"init"}, {"protect", "init", "--encrypt", "--yes"}, {"--identity", "ada", "apply", "--auto-approve"}} {
		if text, code := runPravo(t, rt, e.dir, args...); code != 0 {
			t.Fatalf("%v exited %d:\n%s", args, code, text)
		}
	}
	// The database drifts, and the change adds the events and tells the control plane of them.
	prov.remove("srv-db")
	mustWrite(t, e.dir, "main.cue", imports(spec(base+cp(true))))
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
	if !prov.has("srv-db") || !prov.has("srv-def-worker") {
		t.Fatalf("the apply did not create the database and the worker: %v\n%s", prov.catalog(), text)
	}
}
