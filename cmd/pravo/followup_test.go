package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// deferringSpec is a cluster and an app inside it: the provider of the app is configured
// with the id of the cluster, which the first plan does not know, so it defers the app.
func deferringSpec(t *testing.T) string {
	t.Helper()
	newDemoProvider(t)
	return demoEstate(t, `
cluster: $demo.#Resource.demo_server & {name: "cluster"}
$inside: demo.#Provider & {$path: `+fmt.Sprintf("%q", demoBinary)+`, token: cluster.id}
app: $inside.#Resource.demo_server & {name: "app"}
`)
}

func catalogNames(t *testing.T) []string {
	t.Helper()
	b, _ := os.ReadFile(filepath.Join(os.Getenv("FAKEPROVIDER_DIR"), "catalog.json"))
	return regexp.MustCompile(`"name":"([a-z]+)"`).FindAllString(string(b), -1)
}

// An apply that deferred a change plans it again by itself and, approved, applies it.
func TestApplyPlansWhatItDeferredAgain(t *testing.T) {
	dir := deferringSpec(t)
	text, code := runPravo(t, dir, "plan")
	if code != 0 {
		t.Fatalf("plan exited %d:\n%s", code, text)
	}
	contains(t, text, "# app (demo_server) is deferred to the next plan", "1 to add, 0 to change, 0 to destroy, 1 deferred to the next plan.")
	text, code = runPravo(t, dir, "apply", "--auto-approve")
	if code != 0 {
		t.Fatalf("apply exited %d:\n%s", code, text)
	}
	contains(t, text, "1 change was deferred to the next plan.", "Planned again for the 1 change that the apply deferred:", "# app (demo_server) will be created")
	if n := strings.Count(text, "Apply complete! Resources: 1 added"); n != 2 {
		t.Fatalf("%d applies of one resource each, want 2:\n%s", n, text)
	}
	if got := catalogNames(t); len(got) != 2 {
		t.Fatalf("the provider holds %v, want the cluster and the app", got)
	}
	if text, code = runPravo(t, dir, "plan"); code != 0 || !strings.Contains(text, "No changes.") {
		t.Fatalf("plan after the apply exited %d:\n%s", code, text)
	}
}

// The apply of a plan file that deferred a change writes the plan of it next to the file.
func TestApplyOfAPlanFileSavesTheNextPlan(t *testing.T) {
	dir := deferringSpec(t)
	if text, code := runPravo(t, dir, "plan", "--out", "first.plan"); code != 0 {
		t.Fatalf("plan exited %d:\n%s", code, text)
	}
	text, code := runPravo(t, dir, "apply", "first.plan")
	if code != 0 {
		t.Fatalf("apply exited %d:\n%s", code, text)
	}
	contains(t, text, "1 change was deferred to the next plan.", "Saved the plan to first.next.plan.", "pravo apply first.next.plan")
	if len(catalogNames(t)) != 1 {
		t.Fatalf("the apply of a plan file applied the follow-up as well")
	}
	if text, code = runPravo(t, dir, "apply", "first.next.plan"); code != 0 || !strings.Contains(text, "Apply complete! Resources: 1 added") {
		t.Fatalf("apply of the next plan exited %d:\n%s", code, text)
	}
	if got := catalogNames(t); len(got) != 2 {
		t.Fatalf("the provider holds %v, want the cluster and the app", got)
	}
}

// The apply of a stored plan that deferred a change stores the plan of it.
func TestApplyOfAStoredPlanStoresTheNextPlan(t *testing.T) {
	dir := deferringSpec(t)
	text, code := runPravo(t, dir, "plan", "--store")
	if code != 0 {
		t.Fatalf("plan exited %d:\n%s", code, text)
	}
	id := regexp.MustCompile(`plan-[0-9a-f]{8}`).FindString(text)
	text, code = runPravo(t, dir, "apply", id)
	if code != 0 {
		t.Fatalf("apply exited %d:\n%s", code, text)
	}
	contains(t, text, "1 change was deferred to the next plan.", "Stored the plan as plan-")
	next := ""
	for _, m := range regexp.MustCompile(`plan-[0-9a-f]{8}`).FindAllString(text, -1) {
		if m != id {
			next = m
		}
	}
	if next == "" {
		t.Fatalf("no new stored plan:\n%s", text)
	}
	if text, code = runPravo(t, dir, "apply", next); code != 0 || !strings.Contains(text, "Apply complete! Resources: 1 added") {
		t.Fatalf("apply of the next plan exited %d:\n%s", code, text)
	}
	if got := catalogNames(t); len(got) != 2 {
		t.Fatalf("the provider holds %v, want the cluster and the app", got)
	}
}
