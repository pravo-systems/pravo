package main

import (
	"strings"
	"testing"
)

// drift and drift --due print the report as Markdown for an issue: the resources
// that changed, what happened, and with --plan --store the plan that puts them
// back and how to apply it.
func TestDriftMarkdown(t *testing.T) {
	prov := newDemoProvider(t)
	dir := demoEstate(t, "keep: $demo.#Resource.demo_server & {name: \"keep\"}\nlost: $demo.#Resource.demo_server & {name: \"lost\"}\n")
	if text, code := runPravo(t, dir, "apply", "--auto-approve"); code != 0 {
		t.Fatalf("apply exited %d:\n%s", code, text)
	}
	text, code := runPravo(t, dir, "drift", "--markdown")
	if code != 0 || !strings.HasPrefix(text, "### pravo drift: no drift\n") {
		t.Fatalf("drift --markdown without drift exited %d:\n%s", code, text)
	}

	prov.remove("srv-lost")
	text, code = runPravo(t, dir, "drift", "--markdown")
	if code != 2 {
		t.Fatalf("drift --markdown exited %d:\n%s", code, text)
	}
	for _, want := range []string{"### pravo drift: 1 of 2 resources changed outside pravo", "| `lost` | the object does not exist |"} {
		if !strings.Contains(text, want) {
			t.Errorf("the report lacks %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "Drift.") {
		t.Errorf("the report has the text form:\n%s", text)
	}

	text, code = runPravo(t, dir, "drift", "--plan", "--store", "--markdown")
	id := storedID.FindString(text)
	if code != 2 || id == "" {
		t.Fatalf("drift --plan --store --markdown exited %d:\n%s", code, text)
	}
	for _, want := range []string{"### pravo drift: 1 of 2", "### pravo plan that puts them back: 1 to add", "Stored as `" + id + "`", "pravo apply " + id} {
		if !strings.Contains(text, want) {
			t.Errorf("the proposal lacks %q:\n%s", want, text)
		}
	}

	text, code = runPravo(t, dir, "drift", "--due", "--markdown")
	if !strings.HasPrefix(text, "### pravo drift:") || (code != 0 && code != 2) {
		t.Errorf("drift --due --markdown exited %d:\n%s", code, text)
	}
	if text, code = runPravo(t, dir, "drift", "--markdown", "--json"); code == 0 || !strings.Contains(text, "give one of them") {
		t.Errorf("--markdown with --json exited %d:\n%s", code, text)
	}
}

// discover show --markdown lists the unmanaged objects with the command that
// adopts each.
func TestDiscoverShowMarkdown(t *testing.T) {
	_, dir := demoWorld(t)
	text, code := runPravo(t, dir, "discover", "show", "--markdown")
	if code != 0 || !strings.Contains(text, "### pravo discover vms: nothing unmanaged") && !strings.Contains(text, "Not run yet") {
		t.Fatalf("discover show --markdown before a run exited %d:\n%s", code, text)
	}
	if text, code = runPravo(t, dir, "discover"); code != 0 {
		t.Fatalf("discover exited %d:\n%s", code, text)
	}
	text, code = runPravo(t, dir, "discover", "show", "--markdown", "--exit-code")
	if code != 2 {
		t.Fatalf("discover show --markdown --exit-code exited %d:\n%s", code, text)
	}
	for _, want := range []string{
		"### pravo discover vms: 3 unmanaged",
		"`demo_server` via",
		"3 unmanaged · 0 adopting · 0 acknowledged · 1 managed",
		"| Object | Name | Adopt with |",
		"| `srv-web-old` | web-old | `pravo discover adopt vms srv-web-old` |",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("the listing lacks %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "srv-us-1") || strings.Contains(text, "UNMANAGED") {
		t.Errorf("the listing has an object of another scope or the text form:\n%s", text)
	}
}
