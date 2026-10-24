package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/pravo-systems/pravo/internal/engine"
	"github.com/pravo-systems/pravo/internal/state"
)

const demoSpec = `
known: $demo.#Resource.demo_server & {name: "known"}
vms: $demo.#Discovery.demo_server & {region: "eu"}
`

func demoWorld(t *testing.T) (*demoProvider, string) {
	t.Helper()
	p := newDemoProvider(t,
		demoObject{"srv-web-old", "web-old", "eu"},
		demoObject{"srv-batch-1", "batch-1", "eu"},
		demoObject{"srv-batch-2", "batch-2", "eu"},
		demoObject{"srv-us-1", "us-one", "us"},
	)
	dir := demoEstate(t, demoSpec)
	if text, code := runPravo(t, dir, "apply", "-auto-approve"); code != 0 {
		t.Fatalf("apply exited %d:\n%s", code, text)
	}
	return p, dir
}

// The life of a discovery: it finds what exists, leaves alone what the state
// has, is quiet about what was acknowledged, asks for the rest to be adopted, and
// runs again when its scope changes.
func TestDiscoverLifecycle(t *testing.T) {
	p, dir := demoWorld(t)

	text, code := runPravo(t, dir, "discover", "show")
	if code != 0 {
		t.Fatalf("show exited %d:\n%s", code, text)
	}
	contains(t, text, "vms", "demo_server", "not run yet", "pravo discover vms")

	text, code = runPravo(t, dir, "discover")
	if code != 0 {
		t.Fatalf("discover exited %d:\n%s", code, text)
	}
	contains(t, text, "listing demo_server", "vms", "3 unmanaged", "0 adopting", "0 acknowledged", "1 managed",
		"UNMANAGED", "srv-web-old", "web-old", "pravo discover adopt vms srv-web-old", "srv-batch-1")
	lacks(t, text, "srv-us-1", "ACKNOWLEDGED", "\n  MANAGED")
	if p.listCalls() != 1 {
		t.Errorf("the provider listed %d times", p.listCalls())
	}

	// A discovery that is current is not run again.
	text, code = runPravo(t, dir, "discover")
	if code != 0 || !strings.Contains(text, "Every discovery is current") || p.listCalls() != 1 {
		t.Errorf("discover again exited %d, the provider listed %d times:\n%s", code, p.listCalls(), text)
	}
	if text, code := runPravo(t, dir, "discover", "--all"); code != 0 || p.listCalls() != 2 {
		t.Errorf("--all exited %d, the provider listed %d times:\n%s", code, p.listCalls(), text)
	}

	// Nothing about it was written in the resources, the transactions or the outputs.
	st, err := state.Open(filepath.Join(dir, "pravo.state"))
	if err != nil {
		t.Fatal(err)
	}
	if st.Len() != 1 {
		t.Errorf("the state holds %d records: %v", st.Len(), st.Addresses())
	}

	// A script can tell.
	if _, code := runPravo(t, dir, "discover", "show", "--exit-code"); code != int(engine.ExitChanges) {
		t.Errorf("show --exit-code exited %d with objects unmanaged", code)
	}

	// Acknowledge two of them with one entry, written in the discovery of the specification.
	spec, _ := os.ReadFile(filepath.Join(dir, "main.cue"))
	// An entry is not part of the scope, so the run is still current and the
	// provider is not asked again.
	entry := "\n\t{\n\t\tnames: [\"batch-*\"]\n\t\treason: \"the nightly jobs\"\n\t}\n"
	acked := strings.Replace(string(spec), `{region: "eu"}`, "{region: \"eu\", $ack: ["+entry+"]}", 1)
	mustWrite(t, dir, "main.cue", acked)
	calls := p.listCalls()
	text, _ = runPravo(t, dir, "discover", "show")
	contains(t, text, "1 unmanaged", "2 acknowledged", "UNMANAGED", "srv-web-old", "ACKNOWLEDGED", "srv-batch-1", "srv-batch-2", "the nightly jobs", "$ack[0]")
	lacks(t, text, "scope changed")
	if i, j := strings.Index(text, "UNMANAGED"), strings.Index(text, "ACKNOWLEDGED"); i < 0 || j < i {
		t.Errorf("the objects that ask for adoption come first:\n%s", text)
	}
	if text, code := runPravo(t, dir, "discover"); code != 0 || !strings.Contains(text, "Every discovery is current") || p.listCalls() != calls {
		t.Errorf("a changed acknowledgement ran the discovery again (%d calls, was %d): exit %d\n%s", p.listCalls(), calls, code, text)
	}
	var doc struct {
		Discoveries []struct {
			Acks []struct {
				Ref    string `json:"ref"`
				Reason string `json:"reason"`
				Covers int    `json:"covers"`
			} `json:"acks"`
		} `json:"discoveries"`
	}
	jtext, _ := runPravo(t, dir, "--json", "discover", "show")
	if err := json.Unmarshal([]byte(jtext), &doc); err != nil || len(doc.Discoveries) != 1 || len(doc.Discoveries[0].Acks) != 1 ||
		doc.Discoveries[0].Acks[0].Reason != "the nightly jobs" || doc.Discoveries[0].Acks[0].Covers != 2 {
		t.Fatalf("show --json: %v\n%s", err, jtext)
	}

	// The scope changes: the result answers the old one until it runs again.
	mustWrite(t, dir, "main.cue", strings.Replace(acked, `region: "eu"`, `region: "us"`, 1))
	text, _ = runPravo(t, dir, "discover", "show")
	contains(t, text, "the scope changed since the last run", "pravo discover vms")
	before := p.listCalls()
	text, code = runPravo(t, dir, "discover")
	if code != 0 || p.listCalls() != before+1 {
		t.Fatalf("a changed scope did not run the discovery (%d): exit %d\n%s", p.listCalls(), code, text)
	}
	contains(t, text, "1 unmanaged", "srv-us-1")
	lacks(t, text, "srv-web-old", "scope changed")

	// Taking the entry out of the specification withdraws it.
	mustWrite(t, dir, "main.cue", strings.Replace(string(spec), `region: "eu"`, `region: "us"`, 1))
	if text, _ := runPravo(t, dir, "discover", "show"); !strings.Contains(text, "1 unmanaged") || strings.Contains(text, "ACKNOWLEDGED") {
		t.Errorf("%s", text)
	}
}

func TestDiscoverAdoptThenManage(t *testing.T) {
	_, dir := demoWorld(t)
	if text, code := runPravo(t, dir, "discover"); code != 0 {
		t.Fatalf("%d\n%s", code, text)
	}

	snippet, code := runPravo(t, dir, "discover", "adopt", "vms", "srv-web-old")
	if code != 0 {
		t.Fatalf("adopt exited %d:\n%s", code, snippet)
	}
	contains(t, snippet, "web_old: $demo.#Resource.demo_server & {", `$import: "srv-web-old"`, "// Adopts the demo_server")

	// With --read the provider says what the object is.
	read, code := runPravo(t, dir, "discover", "adopt", "vms", "srv-web-old", "--read")
	if code != 0 {
		t.Fatalf("adopt --read exited %d:\n%s", code, read)
	}
	contains(t, read, `$import: "srv-web-old"`, "is sensitive: write its value yourself")
	if !regexp.MustCompile(`name:\s+"web-old"`).MatchString(read) || !regexp.MustCompile(`size:\s+"large"`).MatchString(read) {
		t.Errorf("the attributes of the object are in the resource:\n%s", read)
	}
	lacks(t, read, "id:", "hunter2")

	// The snippet goes into the specification. Until the next plan reads the object,
	// the discovery knows that something is about to adopt it.
	spec, _ := os.ReadFile(filepath.Join(dir, "main.cue"))
	mustWrite(t, dir, "main.cue", string(spec)+"\n"+read)
	text, _ := runPravo(t, dir, "discover", "show")
	contains(t, text, "1 adopting", "ADOPTING", "by web_old", "2 unmanaged")
	lacks(t, text, "pravo discover adopt vms srv-web-old")
	if text, code := runPravo(t, dir, "plan"); code != 0 {
		t.Fatalf("plan exited %d:\n%s", code, text)
	}
	if text, code := runPravo(t, dir, "apply", "-auto-approve"); code != 0 {
		t.Fatalf("apply exited %d:\n%s", code, text)
	}
	// Once it is a record, the object is managed.
	text, _ = runPravo(t, dir, "discover", "--all")
	contains(t, text, "2 managed", "2 unmanaged", "0 adopting")
	if _, code := runPravo(t, dir, "discover", "adopt", "vms", "srv-web-old"); code != int(engine.ExitInvalid) {
		t.Errorf("adopting what is managed exited %d", code)
	}
}

func TestDiscoverJSON(t *testing.T) {
	_, dir := demoWorld(t)
	text, code := runPravo(t, dir, "--json", "discover")
	if code != 0 {
		t.Fatal(text)
	}
	var doc struct {
		Discoveries []struct {
			Name      string         `json:"name"`
			Type      string         `json:"type"`
			Freshness string         `json:"freshness"`
			Declared  bool           `json:"declared"`
			Scope     map[string]any `json:"scope"`
			Counts    map[string]int `json:"counts"`
			Items     []struct {
				ID       string         `json:"id"`
				Name     string         `json:"name"`
				Status   string         `json:"status"`
				ImportID string         `json:"import_id"`
				Identity map[string]any `json:"identity"`
			} `json:"items"`
			LastRun map[string]any `json:"last_run"`
		} `json:"discoveries"`
	}
	if err := json.Unmarshal([]byte(text), &doc); err != nil {
		t.Fatalf("%v\n%s", err, text)
	}
	if len(doc.Discoveries) != 1 {
		t.Fatal(text)
	}
	d := doc.Discoveries[0]
	if d.Name != "vms" || d.Type != "demo_server" || d.Freshness != "current" || !d.Declared || d.Scope["region"] != "eu" ||
		d.Counts["unmanaged"] != 3 || d.Counts["managed"] != 1 || d.LastRun == nil {
		t.Errorf("%+v", d)
	}
	// The managed object is counted and not listed; the others are, with their identity.
	if len(d.Items) != 3 || d.Items[0].Status != "unmanaged" || d.Items[0].ImportID == "" || d.Items[0].Identity["region"] != "eu" {
		t.Errorf("%+v", d.Items)
	}
	text, _ = runPravo(t, dir, "--json", "discover", "show", "--managed")
	if err := json.Unmarshal([]byte(text), &doc); err != nil || len(doc.Discoveries[0].Items) != 4 {
		t.Errorf("--managed lists the managed object too: %v\n%s", err, text)
	}
	text, code = runPravo(t, dir, "--json", "discover", "adopt", "vms", "srv-batch-1")
	var ad map[string]any
	if code != 0 || json.Unmarshal([]byte(text), &ad) != nil || !strings.Contains(ad["snippet"].(string), "$import") || ad["import_id"] != "srv-batch-1" {
		t.Errorf("adopt --json: %d\n%s", code, text)
	}
}

func TestDiscoverFailureKeepsTheLastResult(t *testing.T) {
	p, dir := demoWorld(t)
	if text, code := runPravo(t, dir, "discover"); code != 0 {
		t.Fatal(text)
	}
	p.failWith("the API said no")
	text, code := runPravo(t, dir, "discover", "--all")
	if code == 0 || !strings.Contains(text, "the API said no") || !strings.Contains(text, "1 discovery failed") {
		t.Errorf("a provider that fails: exit %d\n%s", code, text)
	}
	text, _ = runPravo(t, dir, "discover", "show")
	contains(t, text, "3 unmanaged", "srv-web-old")
}

func TestDiscoverNamesAndMistakes(t *testing.T) {
	_, dir := demoWorld(t)
	for _, c := range []struct {
		args []string
		code int
		want string
	}{
		{[]string{"discover", "nope"}, int(engine.ExitInvalid), `there is no discovery "nope"`},
		{[]string{"discover", "show", "nope"}, int(engine.ExitInvalid), "it has vms"},
		{[]string{"discover", "adopt", "vms", "x"}, int(engine.ExitInvalid), "has not run"},
		{[]string{"discover", "--all", "vms"}, 1, "--all runs every discovery"},
	} {
		text, code := runPravo(t, dir, c.args...)
		if code != c.code || !strings.Contains(text, c.want) {
			t.Errorf("pravo %s: exit %d, want %d and %q:\n%s", strings.Join(c.args, " "), code, c.code, c.want, text)
		}
	}
	if _, code := runPravo(t, dir, "discover"); code != 0 {
		t.Fatal("discover")
	}
	if text, code := runPravo(t, dir, "discover", "adopt", "vms", "nope"); code != int(engine.ExitInvalid) || !strings.Contains(text, `did not find "nope"`) {
		t.Errorf("%d\n%s", code, text)
	}
	// An $ack entry that covers everything is refused when the specification loads.
	spec, _ := os.ReadFile(filepath.Join(dir, "main.cue"))
	mustWrite(t, dir, "main.cue", strings.Replace(string(spec), `{region: "eu"}`, `{region: "eu", $ack: [{names: ["*"], reason: "x"}]}`, 1))
	if text, code := runPravo(t, dir, "discover", "show"); code != int(engine.ExitInvalid) || !strings.Contains(text, "matches everything") {
		t.Errorf("%d\n%s", code, text)
	}
}

func TestSpecificationWithoutDiscoveries(t *testing.T) {
	newDemoProvider(t)
	dir := demoEstate(t, `known: $demo.#Resource.demo_server & {name: "known"}`)
	for _, args := range [][]string{{"discover"}, {"discover", "show"}} {
		if text, code := runPravo(t, dir, args...); code != 0 || !strings.Contains(text, "no discoveries") {
			t.Errorf("pravo %s: %d\n%s", strings.Join(args, " "), code, text)
		}
	}
}

func TestSchemaPrintsADiscovery(t *testing.T) {
	newDemoProvider(t)
	dir := demoEstate(t, `known: $demo.#Resource.demo_server & {name: "known"}`)
	text, code := runPravo(t, dir, "schema", "--discovery", "demo/demo_server")
	if code != 0 {
		t.Fatalf("%d\n%s", code, text)
	}
	contains(t, text, "discovery demo_server", "#Discovery.demo_server", "region?:", `$kind:   "discovery"`)
	lacks(t, text, "name:")
	if text, code := runPravo(t, dir, "schema", "--discovery", "demo/nope"); code == 0 {
		t.Errorf("a type that cannot be listed: %s", text)
	}
}

// A discovery lists and manages nothing: no plan, apply or validate asks the provider to
// list, counts the discovery as a resource, or records it.
func TestDiscoveriesAreNotPartOfAPlan(t *testing.T) {
	p, dir := demoWorld(t)
	calls := p.listCalls()
	text, code := runPravo(t, dir, "plan")
	if code != 0 || !strings.Contains(text, "No changes") && !strings.Contains(text, "Plan: 0 to add") {
		t.Fatalf("plan exited %d:\n%s", code, text)
	}
	lacks(t, text, "servers", "vms")
	if text, code := runPravo(t, dir, "validate"); code != 0 || !strings.Contains(text, "1 resources") {
		t.Errorf("validate counts a discovery as a resource or fails: %d\n%s", code, text)
	}
	if text, code := runPravo(t, dir, "apply", "-auto-approve"); code != 0 {
		t.Fatalf("apply exited %d:\n%s", code, text)
	}
	if text, _ := runPravo(t, dir, "state", "list"); strings.TrimSpace(text) != "known" {
		t.Errorf("the state lists %q", text)
	}
	if p.listCalls() != calls {
		t.Errorf("a plan, a validate or an apply asked the provider to list: %d calls, was %d", p.listCalls(), calls)
	}
}

// A specification that has a discovery and nothing else still loads, plans and says
// what the discovery found.
func TestSpecificationOfOnlyADiscovery(t *testing.T) {
	p := newDemoProvider(t, demoObject{"srv-1", "one", "eu"})
	dir := demoEstate(t, `vms: $demo.#Discovery.demo_server & {}`)
	if text, code := runPravo(t, dir, "plan"); code != 0 {
		t.Fatalf("plan exited %d:\n%s", code, text)
	}
	text, code := runPravo(t, dir, "discover")
	if code != 0 {
		t.Fatalf("discover exited %d:\n%s", code, text)
	}
	contains(t, text, "1 unmanaged", "srv-1")
	lacks(t, text, "scope:")
	_ = p
}
