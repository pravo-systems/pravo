package main

import (
	"strings"
	"testing"
)

// A parameter that a definition declares inside the body of a resource, and that
// an instance fills with a reference to another resource, was written as null:
// validate said Success and apply stored nothing.
func TestParameterInsideTheBodyReadsTheReference(t *testing.T) {
	rt := &runContext{}

	newDemoProvider(t)
	dir := demoEstate(t, rt, `
base: $demo.#Resource.demo_server & {name: "base"}

#Server: $demo.#Resource.demo_server & {
	#from: _
	name:  "second"
	size:  #from
}
second: #Server & {#from: base.id}
`)
	if text, code := runPravo(t, rt, dir, "plan"); code != 0 || !strings.Contains(text, "(known after apply)") {
		t.Fatalf("plan exited %d:\n%s", code, text)
	}
	if text, code := runPravo(t, rt, dir, "apply", "-auto-approve"); code != 0 {
		t.Fatalf("apply exited %d:\n%s", code, text)
	}
	text, code := runPravo(t, rt, dir, "state", "show", "second")
	if code != 0 {
		t.Fatalf("state show exited %d:\n%s", code, text)
	}
	contains(t, text, `size:   "srv-base"`)
	if text, code := runPravo(t, rt, dir, "plan", "--detailed-exitcode"); code != 0 {
		t.Errorf("a plan after the apply exited %d:\n%s", code, text)
	}
}

// eval shows a value that the specification gives differently than the state
// records, and fails for a specification that does not evaluate.
func TestEvalConflictIsAnError(t *testing.T) {
	rt := &runContext{}

	newDemoProvider(t)
	dir := demoEstate(t, rt, `
base: $demo.#Resource.demo_server & {name: "base"}
second: $demo.#Resource.demo_server & {name: "second", size: "\(base.id)-x"}
`)
	if text, code := runPravo(t, rt, dir, "apply", "-auto-approve"); code != 0 {
		t.Fatalf("apply exited %d:\n%s", code, text)
	}
	mustWrite(t, dir, "main.cue", strings.Replace(readFile(t, dir, "main.cue"), `-x"`, `-y"`, 1))
	text, code := runPravo(t, rt, dir, "eval", "-e", "second.size")
	if code != 0 {
		t.Fatalf("eval of a changed expression exited %d:\n%s", code, text)
	}
	contains(t, text, `"srv-base-y"`)
	lacks(t, text, "_|_")

	mustWrite(t, dir, "broken.cue", "package estate\n\nbroken: 1 & 2\n")
	text, code = runPravo(t, rt, dir, "eval")
	if code == 0 {
		t.Errorf("eval of a conflict exited 0:\n%s", text)
	}
	contains(t, text, "conflicting values")
}
