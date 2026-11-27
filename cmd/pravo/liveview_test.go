package main

import (
	"strings"
	"testing"
)

// An apply that is printed as lines and one that is drawn as a live view say the same
// things about the same resources: the view leaves out the lines of the engine, and
// never an attribute.
func TestApplyProgressModes(t *testing.T) {
	const spec = `
net: {
	vpc:    $demo.#Resource.demo_server & {name: "net-vpc"}
	subnet: $demo.#Resource.demo_server & {name: "net-subnet"}
}
`
	rt := &runContext{}
	newDemoProvider(t)
	dir := demoEstate(t, rt, spec)

	live, code := runPravo(t, rt, dir, "apply", "--auto-approve", "--progress=live")
	if code != 0 {
		t.Fatalf("apply exited %d:\n%s", code, live)
	}
	contains(t, live, "net.vpc", "net.subnet", "created", "2/2", "Apply complete! Resources: 2 added")
	lacks(t, live, "Creating...", "Creation complete")

	if text, code := runPravo(t, rt, dir, "destroy", "--auto-approve", "--progress=plain"); code != 0 {
		t.Fatalf("destroy exited %d:\n%s", code, text)
	}
	plain, code := runPravo(t, rt, dir, "apply", "--auto-approve", "--progress=plain")
	if code != 0 {
		t.Fatalf("apply exited %d:\n%s", code, plain)
	}
	contains(t, plain, "net.vpc: Creating...", "net.vpc: Creation complete after")
	lacks(t, plain, "2/2", "\x1b[")

	// Not a terminal, so auto prints lines.
	if text, code := runPravo(t, rt, dir, "destroy", "--auto-approve"); code != 0 || !strings.Contains(text, "Destruction complete") {
		t.Fatalf("destroy exited %d, and its lines are those of the engine:\n%s", code, text)
	}
}
