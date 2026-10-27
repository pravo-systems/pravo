package serve

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pravo-systems/pravo/internal/config"
	"github.com/pravo-systems/pravo/internal/state"
)

const discoverySpecCUE = `package demo

$fake: {
	$kind:   "provider"
	$source: "example.test/x/fake"
	#Resource: {
		fake_db: {
			$kind:   "resource"
			$type:   "fake_db"
			$source: "example.test/x/fake"
			$import?: string
			name:    string
			...
		}
	}
	#Discovery: {
		fake_db: {
			$kind:   "discovery"
			$type:   "fake_db"
			$source: "example.test/x/fake"
			$limit?: int & >0
			$ack?: [...{ids?: [...string], names?: [...string], reason?: string}]
			region?: string
		}
	}
}

vms: $fake.#Discovery.fake_db & {region: "eu"}

a:         $fake.#Resource.fake_db & {name: "a"}
adopting:  $fake.#Resource.fake_db & {name: "adopting-one", $import: "xx"}
`

// A discovery project: the specification above, a record for a, and the writable
// store.
func newDiscoveryProject(t *testing.T) *project {
	t.Helper()
	dir := t.TempDir()
	writeSpec(t, dir, discoverySpecCUE)
	st, err := state.Open(filepath.Join(dir, "pravo.state"))
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Set(record("a", "fake_db", `{"name":"a"}`), "txn"); err != nil {
		t.Fatal(err)
	}
	return start(t, dir, st, Options{})
}

// scopeDigest is the digest the specification gives the discovery now.
func scopeDigest(t *testing.T, dir, name string) string {
	t.Helper()
	root, err := config.Load(dir, config.LoadOptions{})
	if err != nil {
		t.Fatal(err)
	}
	d := root.DiscoveryByName(name)
	if d == nil {
		t.Fatalf("no discovery %s", name)
	}
	return d.Digest
}

// acknowledge writes $ack entries into the discovery of the specification, as a
// person would, and leaves the scope as it is.
func (p *project) acknowledge(entries string) {
	p.t.Helper()
	spec, err := os.ReadFile(filepath.Join(p.dir, "main.cue"))
	if err != nil {
		p.t.Fatal(err)
	}
	next := strings.Replace(string(spec), `{region: "eu"}`, `{region: "eu", $ack: [`+entries+`]}`, 1)
	if next == string(spec) {
		p.t.Fatal("the discovery is not in the specification as the test wrote it")
	}
	writeSpec(p.t, p.dir, next)
}

func listedItems() []state.Listed {
	return []state.Listed{
		state.ListedOf(map[string]any{"name": "a", "region": "eu"}, "a"),
		state.ListedOf(map[string]any{"name": "stray-1", "region": "eu"}, "stray-1"),
		state.ListedOf(map[string]any{"name": "batch-1", "region": "eu"}, "batch-1"),
		state.ListedOf(map[string]any{"id": "xx"}, "being adopted"),
	}
}

func (p *project) putRun(digest string, items []state.Listed) {
	p.t.Helper()
	now := time.Now().UTC()
	run := &state.DiscoveryRun{Name: "vms", Type: "fake_db", Provider: "example.test/x/fake", ProviderKey: "fake", Digest: digest,
		Scope: map[string]any{"region": "eu"}, Started: now, Finished: now, Actor: "marcin", Status: state.RunComplete, Items: items}
	if err := p.store.PutDiscovery(run); err != nil {
		p.t.Fatal(err)
	}
}

// discovery reads GET /api/discovery once it is of the revision that the graph has.
func (p *project) discovery() Discovery {
	p.t.Helper()
	var d Discovery
	if err := json.Unmarshal(p.get("/api/discovery"), &d); err != nil {
		p.t.Fatal(err)
	}
	return d
}

// waitDiscovery waits until the document satisfies the condition.
func (p *project) waitDiscovery(what string, cond func(Discovery) bool) Discovery {
	p.t.Helper()
	var d Discovery
	waitFor(p.t, what, func() bool { d = p.discovery(); return cond(d) })
	return d
}

func find(d Discovery, name string) *DiscoveryInfo {
	for i := range d.Discoveries {
		if d.Discoveries[i].Name == name {
			return &d.Discoveries[i]
		}
	}
	return nil
}

func TestDiscoveryDocumentSetsTheRunAgainstTheState(t *testing.T) {
	p := newDiscoveryProject(t)
	d := p.discovery()
	v := find(d, "vms")
	if !d.SpecOK || v == nil || v.Freshness != "never" || v.Run != nil || v.Type != "fake_db" || !v.Declared || len(v.Items) != 0 || d.Unmanaged != 0 || d.Command != "pravo discover" {
		t.Fatalf("a discovery that was not run: %+v", d)
	}
	if v.AckAll != "" {
		t.Errorf("nothing is unmanaged, so there is nothing to acknowledge all of: %q", v.AckAll)
	}
	if v.Command != "pravo discover vms" || v.Source == nil || v.Source.Line == 0 || len(v.Scope) != 1 || v.Scope[0].Name != "region" || v.Scope[0].Value != "eu" {
		t.Errorf("%+v", v)
	}

	// The acknowledgement is written in the specification and covers by name.
	p.acknowledge(`{names: ["batch-*"], reason: "nightly"}`)
	p.putRun(scopeDigest(t, p.dir, "vms"), listedItems())
	d = p.waitDiscovery("the run", func(d Discovery) bool { v := find(d, "vms"); return v != nil && v.Run != nil })
	v = find(d, "vms")
	if v.Freshness != "current" || v.Run.Total != 4 || v.Run.Actor != "marcin" || v.Run.Status != "complete" {
		t.Errorf("%+v %+v", v, v.Run)
	}
	if v.Counts != (DiscoveryCounts{Unmanaged: 1, Adopting: 1, Acknowledged: 1, Managed: 1}) || d.Unmanaged != 1 {
		t.Errorf("counts %+v, total unmanaged %d", v.Counts, d.Unmanaged)
	}
	// The managed object is counted and is not in the list; the rest are, unmanaged first.
	var got []string
	for _, it := range v.Items {
		got = append(got, it.Status+":"+it.Name)
	}
	if strings.Join(got, ",") != "unmanaged:stray-1,adopting:being adopted,acknowledged:batch-1" {
		t.Errorf("items %v", got)
	}
	by := map[string]DiscoveredInfo{}
	for _, it := range v.Items {
		by[it.Name] = it
	}
	if by["being adopted"].By != "adopting" {
		t.Errorf("%+v", by)
	}
	if ack := by["batch-1"]; ack.Reason != "nightly" || ack.By != "$ack[0]" {
		t.Errorf("%+v", ack)
	}
	if len(by["stray-1"].Identity) != 2 || by["stray-1"].Identity[0].Name != "name" || by["stray-1"].Identity[0].Value != "stray-1" {
		t.Errorf("%+v", by["stray-1"].Identity)
	}
	// What is unmanaged is offered as an $ack entry to paste, with the reason left to give.
	for _, want := range []string{"$ack", `ids: ["name=stray-1,region=eu"]`, `reason: "<why>"`} {
		if !strings.Contains(v.AckAll, want) {
			t.Errorf("ack_all lacks %q:\n%s", want, v.AckAll)
		}
	}
	if g := find(d, "gone"); g != nil && g.AckAll != "" {
		t.Errorf("a discovery that is not declared cannot be acknowledged: %q", g.AckAll)
	}
	if len(v.Acks) != 1 || v.Acks[0].Covers != 1 || v.Acks[0].Names[0] != "batch-*" || v.Acks[0].Ref != "$ack[0]" || v.Acks[0].Reason != "nightly" {
		t.Errorf("%+v", v.Acks)
	}

	// The scope changes: the run answers the old question.
	spec, _ := os.ReadFile(filepath.Join(p.dir, "main.cue"))
	writeSpec(t, p.dir, strings.Replace(string(spec), `region: "eu"`, `region: "us"`, 1))
	d = p.waitDiscovery("the changed scope", func(d Discovery) bool { v := find(d, "vms"); return v != nil && v.Freshness == "stale" })
	v = find(d, "vms")
	if v.Scope[0].Value != "us" || v.Counts.Unmanaged != 1 || len(v.Items) != 3 {
		t.Errorf("a stale run still shows what it found, with the scope that is written now: %+v", v)
	}
}

// A discovery with no scope sends an empty list, not a missing field: the page reads
// its length.
func TestDiscoveryWithoutAScopeSendsAnEmptyList(t *testing.T) {
	p := newDiscoveryProject(t)
	spec, _ := os.ReadFile(filepath.Join(p.dir, "main.cue"))
	writeSpec(t, p.dir, strings.Replace(string(spec), `{region: "eu"}`, `{}`, 1))
	p.waitDiscovery("the new scope", func(d Discovery) bool { v := find(d, "vms"); return v != nil && v.Declared && len(v.Scope) == 0 })
	var doc struct {
		Discoveries []map[string]json.RawMessage `json:"discoveries"`
	}
	if err := json.Unmarshal(p.get("/api/discovery"), &doc); err != nil {
		t.Fatal(err)
	}
	for _, d := range doc.Discoveries {
		for _, field := range []string{"scope", "items", "counts"} {
			if v, ok := d[field]; !ok || string(v) == "null" {
				t.Errorf("a discovery has no %q: %s", field, d["name"])
			}
		}
	}
}

func TestDiscoveryOfAnotherKind(t *testing.T) {
	p := newDiscoveryProject(t)
	p.putRun("anything", listedItems())
	// A run of a discovery that the specification lacks.
	now := time.Now().UTC()
	if err := p.store.PutDiscovery(&state.DiscoveryRun{Name: "gone", Type: "fake_db", Provider: "example.test/x/fake", Started: now, Finished: now, Status: state.RunTruncated, Limit: 1,
		Items: []state.Listed{state.ListedOf(map[string]any{"id": "g1"}, "g1")}}); err != nil {
		t.Fatal(err)
	}
	d := p.waitDiscovery("the runs", func(d Discovery) bool { return find(d, "gone") != nil })
	if v := find(d, "vms"); v.Freshness != "stale" {
		t.Errorf("a run with another digest than the scope: %s", v.Freshness)
	}
	g := find(d, "gone")
	if g.Declared || g.Freshness != "undeclared" || g.Run.Status != "truncated" || g.Source != nil || g.Counts.Unmanaged != 1 {
		t.Errorf("%+v", g)
	}
	if d.Unmanaged != 3 {
		t.Errorf("the unmanaged objects of both runs count (two of vms, one of gone): %d", d.Unmanaged)
	}
}

func TestDiscoveryWithASpecificationThatDoesNotLoad(t *testing.T) {
	p := newDiscoveryProject(t)
	p.putRun("d", listedItems())
	writeSpec(t, p.dir, "package demo\n\nbroken: {a: 1, a: 2}\n")
	d := p.waitDiscovery("the broken spec", func(d Discovery) bool { return !d.SpecOK })
	v := find(d, "vms")
	if v == nil || v.Freshness != "unknown" || v.Declared || len(v.Items) == 0 {
		t.Errorf("the runs are shown as the state holds them: %+v", v)
	}
}

func TestDiscoveryTextsAreMaskedAndBounded(t *testing.T) {
	p := newDiscoveryProject(t)
	// A record whose sensitive attribute is a string; a display name that quotes it.
	rec := record("secretive", "fake_db", `{"name":"s","api_token":"`+secretToken+`"}`)
	if err := p.store.Set(rec, "txn"); err != nil {
		t.Fatal(err)
	}
	items := []state.Listed{
		state.ListedOf(map[string]any{"id": "i-1"}, "named "+secretBlob+" \u202e reversed\x07"),
		state.ListedOf(map[string]any{"id": "i-2"}, strings.Repeat("long", 200)),
	}
	p.putRun("d", items)
	p.acknowledge(`{ids: ["i-1"], reason: "because ` + secretBlob + `"}`)
	// secretBlob is a sensitive value of the state only when a record keeps it so.
	if err := p.store.Set(record("holder", "fake_db", `{"name":"h","blob":"`+secretBlob+`"}`), "txn"); err != nil {
		t.Fatal(err)
	}
	d := p.waitDiscovery("the run", func(d Discovery) bool { v := find(d, "vms"); return v != nil && v.Run != nil && len(v.Acks) == 1 })
	text, _ := json.Marshal(d)
	// The blob is sensitive in the schema of fake_db only when the cached schema says
	// so: assert on what the page must never see in any case.
	noSecrets(t, "/api/discovery", text, secretToken, privateBlob, configHash)
	v := find(d, "vms")
	for _, it := range v.Items {
		if strings.ContainsAny(it.Name, "\x07\u202e") {
			t.Errorf("control and direction characters are kept: %q", it.Name)
		}
		if len(it.Name) > maxDiscoveryText+10 {
			t.Errorf("a text of %d bytes is sent", len(it.Name))
		}
	}
}

func TestDiscoveryListIsBounded(t *testing.T) {
	p := newDiscoveryProject(t)
	var items []state.Listed
	for i := range maxDiscoveryItems + 300 {
		items = append(items, state.ListedOf(map[string]any{"id": fmt.Sprintf("i-%05d", i)}, fmt.Sprintf("n%05d", i)))
	}
	p.putRun("d", items)
	d := p.waitDiscovery("the run", func(d Discovery) bool { v := find(d, "vms"); return v != nil && v.Run != nil })
	v := find(d, "vms")
	if len(v.Items) != maxDiscoveryItems || v.ItemsOmitted != 300+0 || v.Counts.Unmanaged != maxDiscoveryItems+300 || v.Run.Total != maxDiscoveryItems+300 {
		t.Errorf("%d items, %d omitted, counts %+v, total %d", len(v.Items), v.ItemsOmitted, v.Counts, v.Run.Total)
	}
}

func TestAdoptEndpoint(t *testing.T) {
	p := newDiscoveryProject(t)
	p.acknowledge(`{names: ["batch-*"], reason: "nightly"}`)
	items := append(listedItems(), state.ListedOf(map[string]any{"id": "weird id; rm -rf /"}, "ugly"), state.ListedOf(map[string]any{"id": "id\nnewline"}, "newline"))
	p.putRun(scopeDigest(t, p.dir, "vms"), items)
	d := p.waitDiscovery("the run", func(d Discovery) bool { v := find(d, "vms"); return v != nil && v.Run != nil })
	keyOf := func(name string) string {
		for _, it := range find(d, "vms").Items {
			if it.Name == name {
				return it.Key
			}
		}
		t.Fatalf("no %s", name)
		return ""
	}
	adopt := func(name, key string) (Adopt, int) {
		resp, body := p.do("GET", "/api/adopt?discovery="+queryEscape(name)+"&key="+queryEscape(key), nil)
		var a Adopt
		_ = json.Unmarshal(body, &a)
		return a, resp.StatusCode
	}

	a, code := adopt("vms", keyOf("stray-1"))
	if code != 200 || a.Status != "unmanaged" || a.ID != "name=stray-1,region=eu" {
		t.Fatalf("%d %+v", code, a)
	}
	// The object is adopted by its identity: no id is told from it, and no placeholder is left.
	for _, want := range []string{"$fake.#Resource.fake_db & {", "$import: {", `name:   "stray-1"`, `region: "eu"`} {
		if !strings.Contains(a.Snippet, want) {
			t.Errorf("the snippet lacks %q:\n%s", want, a.Snippet)
		}
	}
	var cmds []string
	var placeholders int
	for _, s := range a.Steps {
		if s.Kind == "command" {
			cmds = append(cmds, s.Text)
			if s.Placeholder {
				placeholders++
			}
		}
	}
	if len(cmds) != 1 || cmds[0] != "pravo discover adopt vms name=stray-1,region=eu --read" || placeholders != 0 {
		t.Errorf("steps %+v", a.Steps)
	}
	// Saying that it is known is code too: an entry to add to the $ack of the discovery.
	for _, want := range []string{`ids: ["name=stray-1,region=eu"]`, `reason: "<why>"`} {
		if !strings.Contains(a.Ack, want) {
			t.Errorf("the $ack entry lacks %q:\n%s", want, a.Ack)
		}
	}

	// An object with an id gets the id.
	a, code = adopt("vms", keyOf("being adopted"))
	if code != 200 || a.Status != "adopting" || !strings.Contains(a.Snippet, `id: "xx"`) || len(a.Steps) != 1 || a.Steps[0].Kind != "note" {
		t.Errorf("%d %+v", code, a)
	}
	a, code = adopt("vms", keyOf("batch-1"))
	if code != 200 || a.Status != "acknowledged" {
		t.Fatalf("%d %+v", code, a)
	}
	if a.Ack != "" {
		t.Errorf("an object that was acknowledged is offered an acknowledgement: %s", a.Ack)
	}

	// A command is made only from what a shell can be given safely.
	a, code = adopt("vms", keyOf("ugly"))
	if code != 200 {
		t.Fatal(code)
	}
	for _, s := range a.Steps {
		if s.Kind == "command" && strings.Contains(s.Text, "rm -rf") && !strings.Contains(s.Text, "'weird id; rm -rf /'") {
			t.Errorf("an id is put in a command unquoted: %q", s.Text)
		}
	}
	a, _ = adopt("vms", keyOf("newline"))
	for _, s := range a.Steps {
		if s.Kind == "command" {
			t.Errorf("an id with a newline is put in a command: %q", s.Text)
		}
	}
	if !strings.Contains(a.Snippet, `"id\nnewline"`) && !strings.Contains(a.Snippet, `id\nnewline`) {
		t.Errorf("the snippet quotes the id as CUE does:\n%s", a.Snippet)
	}

	// What is not there, or is managed, is not found.
	for _, c := range [][2]string{{"vms", "name=a,region=eu"}, {"vms", "nope"}, {"nope", "x"}, {"", ""}} {
		if _, code := adopt(c[0], c[1]); code != 404 {
			t.Errorf("%v: %d", c, code)
		}
	}
	if resp, _ := p.do("GET", "/api/adopt?discovery="+strings.Repeat("a", 600)+"&key=x", nil); resp.StatusCode != http.StatusBadRequest {
		t.Errorf("a name that is too long: %d", resp.StatusCode)
	}
}

// The viewer reads the discoveries and writes nothing: not a record, not a run.
func TestDiscoveryViewerWritesNothing(t *testing.T) {
	p := newDiscoveryProject(t)
	p.putRun(scopeDigest(t, p.dir, "vms"), listedItems())
	p.acknowledge(`{ids: ["xx"], reason: "x"}`)
	p.waitDiscovery("the run", func(d Discovery) bool { v := find(d, "vms"); return v != nil && v.Run != nil })
	before := digestTree(t, p.dir)
	d := p.discovery()
	for _, it := range find(d, "vms").Items {
		p.get("/api/adopt?discovery=vms&key=" + queryEscape(it.Key))
	}
	p.get("/api/graph")
	time.Sleep(200 * time.Millisecond)
	if after := digestTree(t, p.dir); after != before {
		t.Error("reading the discoveries changed the project directory or the state")
	}
}

func TestDiscoveryRequiresTheToken(t *testing.T) {
	p := newDiscoveryProject(t)
	for _, path := range []string{"/api/discovery", "/api/adopt?discovery=vms&key=x"} {
		req, err := http.NewRequest(http.MethodGet, p.base+path, nil)
		if err != nil {
			t.Fatal(err)
		}
		resp, _ := p.send(req)
		if resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("%s without a token: %d", path, resp.StatusCode)
		}
	}
}

func TestWatcherSeesDiscoveries(t *testing.T) {
	dir := t.TempDir()
	st, err := state.Open(filepath.Join(dir, "pravo.state"))
	if err != nil {
		t.Fatal(err)
	}
	w := newWatcher(time.Hour, dir, "")
	w.setState(filepath.Join(dir, "pravo.state"), nil)
	sub, cancel := w.bus.subscribe()
	defer cancel()
	w.sample()
	expect := func(what string, change func()) {
		t.Helper()
		before := w.revision()
		change()
		w.sample()
		if w.revision() == before {
			t.Fatalf("%s did not move the revision", what)
		}
		select {
		case ev := <-sub:
			if len(ev.What) != 1 || ev.What[0] != "discovery" {
				t.Errorf("%s: event %+v", what, ev)
			}
		case <-time.After(time.Second):
			t.Fatalf("%s: no event", what)
		}
	}
	expect("a run", func() { _ = st.PutDiscovery(&state.DiscoveryRun{Name: "vms", Status: "complete"}) })
	expect("a run written again", func() {
		time.Sleep(10 * time.Millisecond)
		_ = st.PutDiscovery(&state.DiscoveryRun{Name: "vms", Status: "truncated", Items: []state.Listed{{Key: "k", ID: "k"}}})
	})
	// Nothing written, no change.
	rev := w.revision()
	for range 5 {
		if w.sample(); w.revision() != rev {
			t.Fatal("the revision moved with nothing written")
		}
	}
}

func TestRemoteDiscoverySignature(t *testing.T) {
	st, err := state.Open(filepath.Join(t.TempDir(), "pravo.state"))
	if err != nil {
		t.Fatal(err)
	}
	b := st.Backend()
	sig := func() string { return remoteDiscoverySignature(context.Background(), b) }
	empty := sig()
	_ = st.PutDiscovery(&state.DiscoveryRun{Name: "vms", Status: "complete"})
	one := sig()
	if one == empty {
		t.Error("a run was not seen")
	}
	// The key stays and the body changes, as it does in a bucket.
	_ = st.PutDiscovery(&state.DiscoveryRun{Name: "vms", Status: "truncated"})
	if sig() == one {
		t.Error("a run that was replaced under its key was not seen")
	}
	if first, again := sig(), sig(); first != again {
		t.Error("the signature of an unchanged state moves")
	}
}
