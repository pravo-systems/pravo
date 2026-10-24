package engine

import (
	"context"
	"encoding/json"
	"regexp"
	"strings"
	"testing"

	"cuelang.org/go/cue/cuecontext"
	"github.com/hashicorp/terraform-plugin-go/tftypes"

	"github.com/pravo-systems/pravo/internal/config"
	"github.com/pravo-systems/pravo/internal/plugin"
	"github.com/pravo-systems/pravo/internal/state"
)

func listed(id, region, name string) plugin.ListedResource {
	return plugin.ListedResource{Identity: map[string]any{"id": id, "region": region}, DisplayName: name}
}

func discoverySpec(extra string) spec {
	return spec{cue: "vms: {region: \"eu\"}\n" + extra, discoveries: []string{"vms"}}
}

func TestDiscoverListsInTheScope(t *testing.T) {
	h := newHarness(t)
	h.fake.list("fake_thing", listed("i-3", "eu", "web-3"), listed("i-1", "eu", "web-1"), listed("i-9", "us", "web-9"), listed("i-2", "eu", "db-2"))
	e := h.engine(discoverySpec(""), Options{Actor: "marcin"})
	d := e.Root.DiscoveryByName("vms")
	run, err := e.Discover(context.Background(), d)
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, it := range run.Items {
		ids = append(ids, it.ID)
	}
	if strings.Join(ids, ",") != "i-1,i-2,i-3" {
		t.Errorf("the scope is the region, and the items are in key order: %v", ids)
	}
	if run.Name != "vms" || run.Type != "fake_thing" || run.Digest != d.Digest || run.Status != state.RunComplete || run.Actor != "marcin" || run.Scope["region"] != "eu" {
		t.Errorf("%+v", run)
	}
	if run.Provider != "example.test/x/fake" || run.ProviderKey != "fake" || run.Finished.Before(run.Started) {
		t.Errorf("%+v", run)
	}
	if h.fake.called("ListResource") != 1 || h.fake.called("ReadResource") != 0 || h.fake.called("PlanResourceChange") != 0 {
		t.Errorf("a discovery only lists: %v", h.fake.calls)
	}
	// Nothing is written: a run is kept by the caller.
	if h.state.Len() != 0 {
		t.Errorf("the state holds %d records", h.state.Len())
	}
	if got, _ := h.state.Discovery("vms"); got != nil {
		t.Error("Discover stored the run")
	}
	// The scope reached the provider as the schema types it.
	var m map[string]tftypes.Value
	var region string
	h.fake.mu.Lock()
	cfg := h.fake.lists[0]
	h.fake.mu.Unlock()
	if cfg.As(&m) != nil || m["region"].As(&region) != nil || region != "eu" || !m["name_prefix"].IsNull() {
		t.Errorf("scope as the provider got it: %v", cfg)
	}
}

func TestDiscoverIsCutOffAtTheLimit(t *testing.T) {
	h := newHarness(t)
	h.fake.list("fake_thing", listed("i-1", "eu", "a"), listed("i-2", "eu", "b"), listed("i-3", "eu", "c"))
	run := func(limit string) *state.DiscoveryRun {
		t.Helper()
		e := h.engine(spec{cue: "vms: {region: \"eu\", $limit: " + limit + "}", discoveries: []string{"vms"}}, Options{})
		r, err := e.Discover(context.Background(), e.Root.DiscoveryByName("vms"))
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	if r := run("2"); r.Status != state.RunTruncated || len(r.Items) != 2 || r.Limit != 2 {
		t.Errorf("a list longer than the limit: %+v", r)
	}
	if r := run("3"); r.Status != state.RunComplete || len(r.Items) != 3 {
		t.Errorf("a list that ends at the limit is complete: %+v", r)
	}
	if r := run("10"); r.Status != state.RunComplete || len(r.Items) != 3 {
		t.Errorf("%+v", r)
	}
}

func TestDiscoverKeepsWhatItCanAndWarnsOfTheRest(t *testing.T) {
	h := newHarness(t)
	h.fake.list("fake_thing", listed("i-1", "eu", "a"), listed("i-1", "eu", "a again"), plugin.ListedResource{DisplayName: "no identity"})
	e := h.engine(spec{cue: "vms: {}", discoveries: []string{"vms"}}, Options{})
	run, err := e.Discover(context.Background(), e.Root.DiscoveryByName("vms"))
	if err != nil {
		t.Fatal(err)
	}
	if len(run.Items) != 1 || len(run.Warnings) != 2 {
		t.Errorf("%+v", run)
	}
}

func TestDiscoverFailures(t *testing.T) {
	h := newHarness(t)
	h.fake.list("fake_thing", listed("i-1", "eu", "a"))

	h.fake.listFails = "the API said no"
	e := h.engine(discoverySpec(""), Options{})
	if _, err := e.Discover(context.Background(), e.Root.DiscoveryByName("vms")); err == nil || !strings.Contains(err.Error(), "the API said no") || !strings.Contains(err.Error(), "vms") {
		t.Errorf("a provider that fails: %v", err)
	}
	h.fake.listFails = ""

	// A scope that has no value yet is an error of the discovery.
	e = h.engine(spec{cue: "vms: {region: string}", discoveries: []string{"vms"}}, Options{})
	if _, err := e.Discover(context.Background(), e.Root.DiscoveryByName("vms")); err == nil || !strings.Contains(err.Error(), "region") {
		t.Errorf("a scope that is not concrete: %v", err)
	}

	// A provider that cannot list the type says what it can.
	e = h.engine(discoverySpec(""), Options{})
	e.Schemas["fake"].ListResources = nil
	if _, err := e.Discover(context.Background(), e.Root.DiscoveryByName("vms")); err == nil || !strings.Contains(err.Error(), "cannot list") {
		t.Errorf("a provider without the list resource: %v", err)
	}

	// A scope that is refused before the provider is asked does not reach it.
	h.fake.resetCalls()
	e = h.engine(spec{cue: "vms: {region: string}", discoveries: []string{"vms"}}, Options{})
	_, _ = e.Discover(context.Background(), e.Root.DiscoveryByName("vms"))
	if h.fake.called("ListResource") != 0 {
		t.Error("the provider was asked with a scope that is not concrete")
	}
}

// Everything below is the report: the run set against the state.

func thingRecord(t *testing.T, h *harness, addr string) *state.Resource {
	t.Helper()
	r := h.state.Get(addr)
	if r == nil {
		t.Fatalf("%s is not in the state", addr)
	}
	return r
}

func recordID(t *testing.T, r *state.Resource) string {
	t.Helper()
	var attrs map[string]any
	if err := json.Unmarshal(r.Attributes, &attrs); err != nil {
		t.Fatal(err)
	}
	return attrs["id"].(string)
}

func TestReportSetsTheRunAgainstTheState(t *testing.T) {
	h := newHarness(t)
	h.apply(spec{
		cue:       "a: " + thing("a", "") + "\nb: " + thing("b", ""),
		resources: []string{"a", "b"},
	})
	idA := recordID(t, thingRecord(t, h, "a"))

	// The specification: the discovery, and a resource that adopts i-adopt.
	s := spec{
		cue: `vms: {region: "eu"}
a: ` + thing("a", "") + `
b: ` + thing("b", "") + `
adopt: ` + thing("adopt", `$import: "i-adopt"`) + `
adopt2: ` + thing("adopt2", `$import: {id: "i-adopt2", region: "eu"}`),
		resources:   []string{"a", "b", "adopt", "adopt2"},
		discoveries: []string{"vms"},
	}
	e := h.engine(s, Options{})
	e.Root.Imports["adopt"] = &config.Import{Addr: "adopt", ID: "i-adopt"}
	// The second adopts by identity, which the specification gave without the region too.
	e.Root.Imports["adopt2"] = &config.Import{Addr: "adopt2", Identity: map[string]any{"id": "i-adopt2"}}

	h.fake.list("fake_thing",
		listed(idA, "eu", "managed one"),
		listed("i-adopt", "eu", "being adopted"),
		listed("i-adopt2", "eu", "being adopted by identity"),
		listed("i-ack-1", "eu", "batch-1"),
		listed("i-ack-2", "eu", "batch-2"),
		listed("i-stray", "eu", "stray"),
		listed("i-loose", "eu", "loose"),
	)
	run, err := e.Discover(context.Background(), e.Root.DiscoveryByName("vms"))
	if err != nil {
		t.Fatal(err)
	}
	e.Root.DiscoveryByName("vms").Acks = []*config.Ack{{Index: 0, Names: []string{"batch-*"}, Reason: "the nightly jobs"}}
	rep := BuildDiscoveryReport(DiscoveryInputs{Root: e.Root, Runs: map[string]*state.DiscoveryRun{"vms": run}, Records: h.state.List()})

	v := rep.View("vms")
	if v == nil || v.Freshness != FreshCurrent || !v.Declared || len(rep.Views) != 1 {
		t.Fatalf("%+v", v)
	}
	want := map[string]DiscoveryStatus{
		idA: Managed, "i-adopt": Adopting, "i-adopt2": Adopting, "i-ack-1": Acknowledged, "i-ack-2": Acknowledged, "i-stray": Unmanaged, "i-loose": Unmanaged,
	}
	got := map[string]DiscoveryStatus{}
	for _, it := range v.Items {
		got[it.ID] = it.Status
	}
	for id, st := range want {
		if got[id] != st {
			t.Errorf("%s is %q, want %q", id, got[id], st)
		}
	}
	if v.Counts[Unmanaged] != 2 || v.Counts[Acknowledged] != 2 || v.Counts[Adopting] != 2 || v.Counts[Managed] != 1 {
		t.Errorf("counts %v", v.Counts)
	}
	// Unmanaged first, then adopting, acknowledged, managed.
	order := []DiscoveryStatus{Unmanaged, Unmanaged, Adopting, Adopting, Acknowledged, Acknowledged, Managed}
	for i, it := range v.Items {
		if it.Status != order[i] {
			t.Errorf("item %d is %s, want %s: %+v", i, it.Status, order[i], v.Items)
			break
		}
	}
	for _, it := range v.Items {
		switch it.Status {
		case Managed:
			if it.By != "a" {
				t.Errorf("the record is %q", it.By)
			}
		case Adopting:
			if it.By != "adopt" && it.By != "adopt2" {
				t.Errorf("the adopter is %q", it.By)
			}
		case Acknowledged:
			if it.By != "$ack[0]" || it.Reason != "the nightly jobs" {
				t.Errorf("%+v", it)
			}
		}
	}
	if us := v.Unmanaged(); len(us) != 2 || us[0].Name != "loose" || us[1].Name != "stray" {
		t.Errorf("unmanaged, by name: %+v", us)
	}
	// Reading the report touched no provider.
	if h.fake.called("ListResource") != 1 {
		t.Errorf("%v", h.fake.calls)
	}
}

func TestReportFreshness(t *testing.T) {
	h := newHarness(t)
	h.fake.list("fake_thing", listed("i-1", "eu", "a"))
	e := h.engine(discoverySpec(""), Options{})
	run, err := e.Discover(context.Background(), e.Root.DiscoveryByName("vms"))
	if err != nil {
		t.Fatal(err)
	}
	report := func(e *Engine, runs map[string]*state.DiscoveryRun) *DiscoveryReport {
		return BuildDiscoveryReport(DiscoveryInputs{Root: e.Root, Runs: runs})
	}
	runs := map[string]*state.DiscoveryRun{"vms": run}
	if v := report(e, runs).View("vms"); v.Freshness != FreshCurrent {
		t.Errorf("a run of the scope that is written: %s", v.Freshness)
	}
	if v := report(e, nil).View("vms"); v.Freshness != FreshNever || len(v.Items) != 0 || v.Counts == nil {
		t.Errorf("a discovery that was not run: %+v", v)
	}
	// The scope changes: the run answers another question.
	changed := h.engine(spec{cue: `vms: {region: "us"}`, discoveries: []string{"vms"}}, Options{})
	v := report(changed, runs).View("vms")
	if v.Freshness != FreshStale || len(v.Items) != 1 || v.Scope["region"] != "us" {
		t.Errorf("a changed scope: %+v", v)
	}
	// The limit is part of the scope.
	if v := report(h.engine(spec{cue: `vms: {region: "eu", $limit: 5}`, discoveries: []string{"vms"}}, Options{}), runs).View("vms"); v.Freshness != FreshStale {
		t.Errorf("a changed limit: %s", v.Freshness)
	}
	// A scope that cannot be listed with.
	broken := h.engine(spec{cue: `vms: {region: string}`, discoveries: []string{"vms"}}, Options{})
	if v := report(broken, runs).View("vms"); v.Freshness != FreshBroken || v.ScopeErr == "" {
		t.Errorf("%+v", v)
	}
	// The discovery is gone from the specification but its run is in the state: with
	// no $ack to speak for its objects they are unmanaged.
	gone := h.engine(spec{cue: `other: "x"`}, Options{})
	v = report(gone, runs).View("vms")
	if v == nil || v.Freshness != FreshUndeclared || v.Declared || len(v.Items) != 1 || v.Items[0].Status != Unmanaged {
		t.Errorf("a run that nothing declares: %+v", v)
	}
}

func rec(typ, attrs string) *state.Resource {
	return &state.Resource{Mode: "managed", Type: typ, Name: typ, Attributes: json.RawMessage(attrs)}
}

func TestMatchingAnIdentityToARecord(t *testing.T) {
	for _, c := range []struct {
		name     string
		identity map[string]any
		record   string
		want     DiscoveryStatus
	}{
		{"the id is the id", map[string]any{"id": "i-1"}, `{"id":"i-1"}`, Managed},
		{"another id", map[string]any{"id": "i-1"}, `{"id":"i-2"}`, Unmanaged},
		{"the id and a region the record has", map[string]any{"id": "i-1", "region": "eu"}, `{"id":"i-1","region":"eu"}`, Managed},
		{"the id of another region", map[string]any{"id": "i-1", "region": "eu"}, `{"id":"i-1","region":"us"}`, Unmanaged},
		{"a region the record lacks is not held against it", map[string]any{"id": "i-1", "region": "eu"}, `{"id":"i-1"}`, Managed},
		{"a region alone says nothing about which object", map[string]any{"id": "i-1", "region": "eu"}, `{"region":"eu"}`, Unmanaged},
		{"an account and a region alone", map[string]any{"account_id": "1", "region": "eu"}, `{"account_id":"1","region":"eu"}`, Unmanaged},
		{"an identity of the name", map[string]any{"bucket": "logs", "region": "eu"}, `{"bucket":"logs","region":"eu","id":"logs"}`, Managed},
		{"a number is a number", map[string]any{"port": int64(80)}, `{"port":80}`, Managed},
		{"a number that differs", map[string]any{"port": int64(80)}, `{"port":8080}`, Unmanaged},
		{"a record that has none of the attributes", map[string]any{"bucket": "logs"}, `{"name":"logs"}`, Unmanaged},
		{"a null attribute of the identity is skipped", map[string]any{"id": "i-1", "zone": nil}, `{"id":"i-1","zone":"a"}`, Managed},
	} {
		run := &state.DiscoveryRun{Name: "d", Type: "t", Digest: "x", Items: []state.Listed{state.ListedOf(c.identity, "")}}
		rep := BuildDiscoveryReport(DiscoveryInputs{Root: &config.Root{}, Runs: map[string]*state.DiscoveryRun{"d": run}, Records: []*state.Resource{rec("t", c.record)}})
		if got := rep.View("d").Items[0].Status; got != c.want {
			t.Errorf("%s: %s, want %s", c.name, got, c.want)
		}
	}
	// A record of another type does not match, and neither does a data source.
	run := &state.DiscoveryRun{Name: "d", Type: "t", Items: []state.Listed{state.ListedOf(map[string]any{"id": "i-1"}, "")}}
	data := rec("t", `{"id":"i-1"}`)
	data.Mode = "data"
	rep := BuildDiscoveryReport(DiscoveryInputs{Root: &config.Root{}, Runs: map[string]*state.DiscoveryRun{"d": run}, Records: []*state.Resource{rec("u", `{"id":"i-1"}`), data}})
	if got := rep.View("d").Items[0].Status; got != Unmanaged {
		t.Errorf("a record of another type or mode: %s", got)
	}
}

func TestReportCountsTheRecordsItCannotRead(t *testing.T) {
	run := &state.DiscoveryRun{Name: "d", Type: "t", Items: []state.Listed{state.ListedOf(map[string]any{"id": "i-1"}, "")}}
	sealed := rec("t", ``)
	rep := BuildDiscoveryReport(DiscoveryInputs{Root: &config.Root{}, Runs: map[string]*state.DiscoveryRun{"d": run}, Records: []*state.Resource{sealed, rec("t", `{"id":"i-2"}`), rec("other", ``)}})
	v := rep.View("d")
	if v.Unreadable != 1 || v.Items[0].Status != Unmanaged {
		t.Errorf("%+v", v)
	}
}

// An $ack covers the objects of its own discovery and no other.
func TestAnAckCoversTheObjectsOfItsOwnDiscoveryOnly(t *testing.T) {
	h := newHarness(t)
	e := h.engine(spec{cue: "a: {}\nb: {}", discoveries: []string{"a", "b"}}, Options{})
	e.Root.DiscoveryByName("a").Acks = []*config.Ack{{Index: 0, IDs: []string{"i-1"}, Reason: "x"}}
	item := []state.Listed{state.ListedOf(map[string]any{"id": "i-1"}, "")}
	runs := map[string]*state.DiscoveryRun{
		"a": {Name: "a", Type: "fake_thing", Digest: e.Root.DiscoveryByName("a").Digest, Items: item},
		"b": {Name: "b", Type: "fake_thing", Digest: e.Root.DiscoveryByName("b").Digest, Items: item},
	}
	rep := BuildDiscoveryReport(DiscoveryInputs{Root: e.Root, Runs: runs})
	if got := rep.View("a").Items[0].Status; got != Acknowledged {
		t.Errorf("a: %s", got)
	}
	if got := rep.View("b").Items[0].Status; got != Unmanaged {
		t.Errorf("b: %s", got)
	}
	// Changing what is acknowledged does not make a run stale.
	if rep.View("a").Freshness != FreshCurrent {
		t.Errorf("%s", rep.View("a").Freshness)
	}
}

func TestAckEntryIsCUE(t *testing.T) {
	got := FormatCUE(AckEntry("vms", []string{"i-1", `we"ird`}, []string{"batch-*"}, "the nightly\njob"))
	for _, want := range []string{`ids:`, `"i-1"`, `"we\"ird"`, `names:`, `"batch-*"`, `reason:`, `"the nightly\njob"`, "$ack: [...]"} {
		if !strings.Contains(got, want) {
			t.Errorf("the entry lacks %q:\n%s", want, got)
		}
	}
	if strings.Count(got, "\n") < 4 || strings.Contains(AckEntry("vms", []string{"a"}, nil, ""), "reason") {
		t.Errorf("%s", got)
	}
	// It is an entry of the list that the definition describes.
	spec := "x: [" + strings.SplitN(got, "\n", 2)[1] + "]"
	if v := cuecontext.New().CompileString(spec); v.Err() != nil {
		t.Errorf("not CUE: %v\n%s", v.Err(), spec)
	}
}

func TestAdoptSnippet(t *testing.T) {
	h := newHarness(t)
	e := h.engine(spec{
		cue:         "vms: {region: \"eu\"}\nweb_1: " + thing("web-1", ""),
		resources:   []string{"web_1"},
		discoveries: []string{"vms"},
		providers:   map[string]string{"fake": "$fake"},
	}, Options{})
	v := &DiscoveryView{Name: "vms", Type: "fake_thing", ProviderKey: "fake"}

	// An object is adopted by its identity, whatever the identity is made of.
	got := FormatCUE(AdoptSnippet(e.Root, v, state.ListedOf(map[string]any{"id": "i-0abc", "region": "eu"}, "Web 1"), ""))
	for _, want := range []string{
		`// Adopts the fake_thing "i-0abc" (Web 1) that the discovery vms found.`,
		"web_1_2: $fake.#Resource.fake_thing & {", // web_1 is taken
		"$import: {", `id:     "i-0abc"`, `region: "eu"`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("the snippet lacks %q:\n%s", want, got)
		}
	}

	// An identity of other attributes needs no id to be told from it: the provider
	// is given the attributes.
	noID := FormatCUE(AdoptSnippet(e.Root, v, state.ListedOf(map[string]any{"account_id": "123", "bucket": "logs", "region": "eu", "versions": []any{int64(1), "b"}, "unset": nil}, "Logs"), ""))
	for _, want := range []string{"logs: ", "$import: {", `account_id: "123"`, `bucket:     "logs"`, `region:     "eu"`, `versions: [1, "b"]`} {
		if !strings.Contains(noID, want) {
			t.Errorf("the snippet lacks %q:\n%s", want, noID)
		}
	}
	if strings.Contains(noID, "<id>") || strings.Contains(noID, "unset") {
		t.Errorf("a placeholder, or an attribute that is null:\n%s", noID)
	}

	withBody := FormatCUE(AdoptSnippet(e.Root, v, state.ListedOf(map[string]any{"id": "i-1"}, ""), "\tcontent: \"x\"\n"))
	if !strings.Contains(withBody, "\t$import: {\n\t\tid: \"i-1\"\n\t}\n\tcontent: \"x\"\n}") {
		t.Errorf("%s", withBody)
	}
	// The snippet is CUE.
	for _, s := range []string{got, noID, withBody} {
		if FormatCUE(s) == s && !strings.HasSuffix(s, "}\n") {
			t.Errorf("not formatted: %s", s)
		}
	}
}

func TestSnippetName(t *testing.T) {
	root := &config.Root{}
	for _, c := range []struct{ name, id, want string }{
		{"Web Server #1", "i-1", "web_server_1"},
		{"", "i-0abc", "i_0abc"},
		{"", "0abc", "r_0abc"},
		{"for", "x", "for_"},
		{"", "", "adopted"},
		{"---", "", "adopted"},
		{"Ünïcode-ñame", "", "n_code_ame"},
	} {
		if got := SnippetName(root, state.Listed{ID: c.id, Name: c.name}); got != c.want {
			t.Errorf("SnippetName(%q, %q) = %q, want %q", c.name, c.id, got, c.want)
		}
	}
	if got := SnippetName(root, state.Listed{Name: strings.Repeat("a", 100)}); len(got) != 60 {
		t.Errorf("a long name: %d", len(got))
	}
}

func TestReadAdoptionRendersWhatTheObjectHas(t *testing.T) {
	h := newHarness(t)
	h.fake.canImport("i-1", "hello")
	e := h.engine(discoverySpec(""), Options{})
	body, err := e.ReadAdoption(context.Background(), "fake", "fake_thing", plugin.ImportTarget{Identity: map[string]any{"id": "i-1", "region": "eu"}})
	if err != nil {
		t.Fatal(err)
	}
	if got := h.fake.importTargets; len(got) != 1 || got[0].Identity["id"] != "i-1" || got[0].ID != "" {
		t.Errorf("the provider was asked for %+v", got)
	}
	// The id is computed and not set by the configuration; the name and the content are.
	if !strings.Contains(body, `name: "obj-i-1"`) || !strings.Contains(body, `content: "hello"`) || strings.Contains(body, "id:") {
		t.Errorf("%s", body)
	}
	if h.state.Len() != 0 {
		t.Error("reading an adoption wrote a record")
	}
	if _, err := e.ReadAdoption(context.Background(), "fake", "fake_thing", plugin.ImportTarget{ID: "i-none"}); err == nil {
		t.Error("an object the provider does not have")
	}
	if _, err := e.ReadAdoption(context.Background(), "fake", "fake_nope", plugin.ImportTarget{ID: "i-1"}); err == nil {
		t.Error("a type the provider does not have")
	}
}

// Text that a provider listed ends up in a snippet that a person pastes into the
// specification, so it cannot end the comment it sits in.
func TestAdoptSnippetKeepsListedTextInItsLine(t *testing.T) {
	h := newHarness(t)
	e := h.engine(spec{cue: "vms: {region: \"eu\"}", discoveries: []string{"vms"}}, Options{})
	v := &DiscoveryView{Name: "vms", Type: "fake_thing", ProviderKey: "fake"}
	evil := "x\n}\nevil: {$import: \"pwn\"}\n// \u202e"
	l := state.ListedOf(map[string]any{"bucket": evil, "region": "eu"}, evil)
	got := AdoptSnippet(e.Root, v, l, "")
	lines := strings.Split(strings.TrimSuffix(got, "\n"), "\n")
	for i, line := range lines {
		trim := strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(trim, "//"):
		case i == 1 && regexp.MustCompile(`^[a-z0-9_]+: \$provider\.#Resource\.fake_thing & \{$`).MatchString(line):
		case trim == "$import: {", trim == "}", line == "}":
		case regexp.MustCompile(`^(bucket|region): "`).MatchString(trim):
		default:
			t.Errorf("line %d lets listed text out of its comment: %q\n%s", i, line, got)
		}
		if strings.ContainsAny(line, "\u202e\r") {
			t.Errorf("line %d has a direction or control character: %q", i, line)
		}
	}
	// A literal carries the character as an escape.
	lit := AdoptSnippet(e.Root, v, state.ListedOf(map[string]any{"id": "a\u202eb"}, ""), "")
	if !strings.Contains(lit, `id: "a\u202eb"`) || strings.ContainsRune(lit, '\u202e') {
		t.Errorf("a direction character in an id is written raw:\n%s", lit)
	}
	if PlainText("a\x1b[31mred\u202e") != "a [31mred " {
		t.Errorf("PlainText = %q", PlainText("a\x1b[31mred\u202e"))
	}
}

func TestNamesIdentity(t *testing.T) {
	listed := map[string]any{"account_id": "1", "bucket": "logs", "region": "eu"}
	for _, c := range []struct {
		name string
		imp  map[string]any
		want bool
	}{
		{"all of it", map[string]any{"account_id": "1", "bucket": "logs", "region": "eu"}, true},
		{"the provider fills in the rest", map[string]any{"bucket": "logs"}, true},
		{"another object", map[string]any{"bucket": "other", "region": "eu"}, false},
		{"another region", map[string]any{"bucket": "logs", "region": "us"}, false},
		{"only where it is", map[string]any{"region": "eu"}, false},
		{"an attribute the object lacks", map[string]any{"bucket": "logs", "arn": "x"}, false},
	} {
		if got := namesIdentity(c.imp, listed); got != c.want {
			t.Errorf("%s: %v", c.name, got)
		}
	}
}
