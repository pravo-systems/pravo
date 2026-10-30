package serve

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hashicorp/terraform-plugin-go/tftypes"

	"github.com/pravo-systems/pravo/internal/config"
	"github.com/pravo-systems/pravo/internal/state"
)

const unknownValue = "\x00unknown"

// mk builds a value of a type from plain data. A missing field is null.
func mk(t tftypes.Type, v any) tftypes.Value {
	if v == nil {
		return tftypes.NewValue(t, nil)
	}
	if v == unknownValue {
		return tftypes.NewValue(t, tftypes.UnknownValue)
	}
	switch {
	case t.Is(tftypes.Object{}):
		m := v.(map[string]any)
		out := map[string]tftypes.Value{}
		for k, at := range t.(tftypes.Object).AttributeTypes {
			out[k] = mk(at, m[k])
		}
		return tftypes.NewValue(t, out)
	case t.Is(tftypes.List{}):
		var out []tftypes.Value
		for _, e := range v.([]any) {
			out = append(out, mk(t.(tftypes.List).ElementType, e))
		}
		return tftypes.NewValue(t, out)
	case t.Is(tftypes.Map{}):
		out := map[string]tftypes.Value{}
		for k, e := range v.(map[string]any) {
			out[k] = mk(t.(tftypes.Map).ElementType, e)
		}
		return tftypes.NewValue(t, out)
	}
	return tftypes.NewValue(t, v)
}

type testChange struct {
	Addr            string   `json:"addr"`
	Mode            string   `json:"mode"`
	Type            string   `json:"type"`
	Name            string   `json:"name"`
	Action          string   `json:"action"`
	ProviderKey     string   `json:"provider_key"`
	Config          []byte   `json:"config,omitempty"`
	Prior           []byte   `json:"prior,omitempty"`
	Planned         []byte   `json:"planned,omitempty"`
	Reasons         []string `json:"reasons,omitempty"`
	RequiresReplace []string `json:"requires_replace,omitempty"`
	PriorRevision   int      `json:"prior_revision"`
	PriorGeneration string   `json:"prior_generation,omitempty"`
	ConfigHash      string   `json:"config_hash,omitempty"`
}

func pack(t *testing.T, typ tftypes.Type, v any) []byte {
	t.Helper()
	if v == nil {
		return nil
	}
	b, err := mk(typ, v).MarshalMsgPack(typ)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func priorDB(name string) map[string]any {
	return map[string]any{"name": name, "blob": secretBlob, "api_token": secretToken, "port": 5432,
		"tags": map[string]any{"owner": "team", "db_password": secretMapKey}, "zones": []any{"eu-1", "eu-2"},
		"settings": map[string]any{"region": "eu", "key": secretNested},
		"rule":     []any{map[string]any{"cidr": "10.0.0.0/8", "pin": secretPin}}}
}

// writePlan writes a saved plan against the project as it is.
func writePlan(t *testing.T, p *project, path string) {
	t.Helper()
	root, err := config.Load(p.dir, config.LoadOptions{})
	if err != nil {
		t.Fatal(err)
	}
	fp, err := root.Fingerprint()
	if err != nil {
		t.Fatal(err)
	}
	dbType := fakeSchemas().Type("managed", "fake_db").Block.Type()
	fileType := fakeSchemas().Type("managed", "fake_file").Block.Type()
	rev := func(addr string) (int, string) {
		r := p.store.Get(addr)
		return r.Revision, r.Generation
	}
	nextDB := priorDB("a")
	nextDB["blob"] = "new-blob-value-9999"
	nextDB["api_token"] = unknownValue
	nextDB["port"] = 6543
	nextDB["tags"] = map[string]any{"owner": "team-b", "db_password": "new-map-secret-5555"}
	nextDB["zones"] = []any{"eu-1"}
	nextDB["settings"] = map[string]any{"region": "us", "key": "new-nested-key-6666"}
	nextDB["rule"] = []any{map[string]any{"cidr": "192.168.0.0/16", "pin": "new-pin-value-7777"}}
	aRev, aGen := rev("services.a.db")
	oRev, oGen := rev("old.thing")
	pf := map[string]any{
		"format": 1, "created": time.Now().UTC(), "command": "pravo plan -out plan.pravo", "lineage": p.store.Lineage(),
		"config_fingerprint": fp, "providers": map[string]string{"fake": "1.0.0"}, "actor": "ci@example", "stats": map[string]any{"total": 8},
		"changes": []testChange{
			{Addr: "services.a.db", Mode: "managed", Type: "fake_db", Name: "db", Action: "update", ProviderKey: "fake",
				Prior: pack(t, dbType, priorDB("a")), Planned: pack(t, dbType, nextDB), Config: pack(t, dbType, nextDB),
				Reasons: []string{"the specification changed since the last apply"}, PriorRevision: aRev, PriorGeneration: aGen},
			{Addr: "services.b.file", Mode: "managed", Type: "fake_file", Name: "file", Action: "create", ProviderKey: "fake",
				Planned: pack(t, fileType, map[string]any{"path": "/tmp/b", "content": "id=b"}), Reasons: []string{"not in the state yet"}},
			{Addr: "hosts.x", Mode: "managed", Type: "fake_db", Name: "x", Action: "create", ProviderKey: "fake", ConfigHash: configHash,
				Planned: pack(t, dbType, map[string]any{"name": "x"}), Reasons: []string{"not in the state yet"}},
			{Addr: "old.thing", Mode: "managed", Type: "fake_db", Name: "thing", Action: "delete", ProviderKey: "fake",
				Prior: pack(t, dbType, priorDB("old")), Reasons: []string{"the specification does not declare it"}, PriorRevision: oRev, PriorGeneration: oGen},
		},
	}
	data, err := json.MarshalIndent(pf, "", " ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func newPlanProject(t *testing.T) (*project, string) {
	t.Helper()
	dir := t.TempDir()
	writeSpec(t, dir, specCUE)
	st, err := state.Open(filepath.Join(dir, "pravo.state"))
	if err != nil {
		t.Fatal(err)
	}
	seed(t, st)
	path := filepath.Join(dir, "plan.pravo")
	holder := &project{t: t, dir: dir, store: st}
	writePlan(t, holder, path)
	return start(t, dir, st, Options{PlanFile: path}), path
}

func TestPlanOverlay(t *testing.T) {
	p, planPath := newPlanProject(t)
	g := p.graph()
	if g.Plan == nil {
		t.Fatal("no plan in the graph")
	}
	pl := g.Plan
	if pl.File != "plan.pravo" || pl.Actor != "ci@example" || pl.Stale || len(pl.Reasons) != 0 {
		t.Errorf("plan %+v", pl)
	}
	if pl.Summary.Add != 2 || pl.Summary.Change != 1 || pl.Summary.Destroy != 1 || pl.Planned != 4 || pl.Total != 8 {
		t.Errorf("plan summary %+v planned %d of %d", pl.Summary, pl.Planned, pl.Total)
	}
	// The pending nodes and what depends on them: services.a.db, its file and shared; services.b.file; old.thing.
	if pl.Summary.BlastRadius != 6 {
		t.Errorf("blast radius %d, want 6", pl.Summary.BlastRadius)
	}
	if len(pl.Hints) != 1 || !strings.Contains(pl.Hints[0], `hosts.x looks like old.thing under a new address`) {
		t.Errorf("hints %v", pl.Hints)
	}
	for addr, want := range map[string]string{"services.a.db": "update", "services.b.file": "create", "hosts.x": "create", "old.thing": "delete", "shared": "", "services.b.db": ""} {
		if n := nodeOf(g, addr); n.Action != want {
			t.Errorf("%s: action %q, want %q", addr, n.Action, want)
		}
	}
	// A plan lists the value of every output and does not say which it changes: the outputs stay without an action.
	for _, n := range g.Nodes {
		if n.Mode == "output" && n.Action != "" {
			t.Errorf("output %s has the action %q", n.Addr, n.Action)
		}
	}
	raw := p.get("/api/detail?addr=services.a.db")
	noSecrets(t, "detail", raw, append(allSecrets, "new-blob-value-9999", "new-map-secret-5555", "new-nested-key-6666", "new-pin-value-7777")...)
	var d Detail
	if err := json.Unmarshal(raw, &d); err != nil {
		t.Fatal(err)
	}
	if d.Change == nil || d.Change.Action != "update" || len(d.Change.Reasons) != 1 {
		t.Fatalf("change %+v", d.Change)
	}
	changes := map[string]AttrChange{}
	for _, a := range d.Change.Attrs {
		changes[a.Path] = a
	}
	for _, path := range []string{"blob", "tags.db_password", "settings.key", "rule[0].pin"} {
		a, ok := changes[path]
		if !ok || !a.BeforeRedacted || !a.AfterRedacted || a.Before != nil || a.After != nil {
			t.Errorf("%s: %+v, want a change with both sides withheld", path, a)
		}
	}
	if a := changes["api_token"]; !a.BeforeRedacted || !a.AfterUnknown || a.AfterRedacted {
		t.Errorf("api_token: %+v", a)
	}
	if a := changes["port"]; a.Before != float64(5432) || a.After != float64(6543) {
		t.Errorf("port: %+v", a)
	}
	if a := changes["tags.owner"]; a.Before != "team" || a.After != "team-b" {
		t.Errorf("tags.owner: %+v", a)
	}
	if a := changes["settings.region"]; a.Before != "eu" || a.After != "us" {
		t.Errorf("settings.region: %+v", a)
	}
	if a := changes["rule[0].cidr"]; a.Before != "10.0.0.0/8" || a.After != "192.168.0.0/16" {
		t.Errorf("rule[0].cidr: %+v", a)
	}
	if a, ok := changes["zones"]; !ok || a.Before == nil || a.After == nil {
		t.Errorf("zones: %+v", a)
	}
	if _, ok := changes["name"]; ok {
		t.Errorf("name did not change and is listed")
	}
	// A create lists what is planned and nothing before.
	c := p.detail("services.b.file").Change
	if c == nil || c.Action != "create" || len(c.Attrs) != 2 {
		t.Fatalf("create change %+v", c)
	}
	for _, a := range c.Attrs {
		// content reads the sensitive blob of the database, so the plan does not show it.
		if a.Path == "content" && a.AfterRedacted && a.Before == nil && a.After == nil {
			continue
		}
		if a.Before != nil || a.After == nil {
			t.Errorf("create: %+v", a)
		}
	}
	// A delete lists what goes.
	c = p.detail("old.thing").Change
	if c == nil || c.Action != "delete" || len(c.Attrs) == 0 {
		t.Fatalf("delete change %+v", c)
	}
	for _, a := range c.Attrs {
		if a.After != nil || a.AfterRedacted || a.AfterUnknown {
			t.Errorf("delete: %+v", a)
		}
	}
	noSecrets(t, "delete detail", p.get("/api/detail?addr=old.thing"))
	// The lines the plan changes are marked in the source, and a literal stays masked.
	s := p.source("services.a.db")
	marked := map[string]string{}
	for _, l := range s.Blocks[0].Lines {
		marked[strings.TrimSpace(strings.SplitN(strings.TrimSpace(l.Text), ":", 2)[0])] = l.Mark
	}
	if marked["port"] != "changed" || marked["blob"] != "sensitive" || marked["name"] != "focus" && marked["\"name\""] != "focus" {
		t.Errorf("marks %v", marked)
	}
	noSecrets(t, "source", p.get("/api/source?addr=services.a.db"))

	// A change in the state makes the plan stale, with the reason the engine gives.
	r := *p.store.Get("services.a.db")
	if err := p.store.Set(&r, ""); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "a stale plan", func() bool { pl := p.graph().Plan; return pl != nil && pl.Stale })
	g = p.graph()
	if len(g.Plan.Reasons) == 0 || !strings.Contains(strings.Join(g.Plan.Reasons, ";"), "services.a.db") {
		t.Errorf("reasons %v", g.Plan.Reasons)
	}
	found := false
	for _, w := range g.Warnings {
		found = found || strings.Contains(w, "stale")
	}
	if !found {
		t.Errorf("no warning about a stale plan: %v", g.Warnings)
	}
	// A plan file that is replaced is seen.
	writePlan(t, p, planPath)
	waitFor(t, "the plan to be fresh again", func() bool { pl := p.graph().Plan; return pl != nil && !pl.Stale })
}

func TestPlanWithoutSchemasShowsActions(t *testing.T) {
	dir := t.TempDir()
	writeSpec(t, dir, specCUE)
	st, _ := state.Open(filepath.Join(dir, "pravo.state"))
	seed(t, st)
	path := filepath.Join(dir, "plan.pravo")
	writePlan(t, &project{t: t, dir: dir, store: st}, path)
	p := start(t, dir, st, Options{PlanFile: path, Schemas: fakeLookupNone})
	g := p.graph()
	if g.Plan == nil || g.Plan.Summary.Add != 2 || g.Plan.Summary.Destroy != 1 {
		t.Fatalf("plan %+v", g.Plan)
	}
	if n := nodeOf(g, "services.a.db"); n.Action != "update" {
		t.Errorf("action %q", n.Action)
	}
	found := false
	for _, w := range g.Warnings {
		found = found || strings.Contains(w, "values of the plan are not shown")
	}
	if !found {
		t.Errorf("warnings %v", g.Warnings)
	}
	d := p.detail("services.a.db")
	if d.Change == nil || d.Change.Action != "update" || len(d.Change.Attrs) != 0 {
		t.Errorf("change %+v", d.Change)
	}
	noSecrets(t, "detail", p.get("/api/detail?addr=services.a.db"))
}

func TestUnreadablePlan(t *testing.T) {
	dir := t.TempDir()
	writeSpec(t, dir, specCUE)
	st, _ := state.Open(filepath.Join(dir, "pravo.state"))
	seed(t, st)
	path := filepath.Join(dir, "plan.pravo")
	os.WriteFile(path, []byte("not a plan"), 0o600)
	p := start(t, dir, st, Options{PlanFile: path})
	g := p.graph()
	if g.Plan != nil || len(g.Warnings) == 0 || !strings.Contains(g.Warnings[0], "plan file") {
		t.Errorf("plan %+v warnings %v", g.Plan, g.Warnings)
	}
	if len(resourceNodes(g)) != 8 {
		t.Errorf("%d resources", len(resourceNodes(g)))
	}
}

// The M of "planned N of M" is the resources and data sources, as the plan of
// the engine counts it: a variable of the environment is a node of the graph and
// is not planned.
func TestPlanTotalLeavesTheEnvironmentVariablesOut(t *testing.T) {
	dir := t.TempDir()
	writeSpec(t, dir, specCUE+"\nregion: {$kind: \"env\", name: \"SERVE_TEST_REGION\", default: \"eu\", value: \"eu\"}\n")
	st, err := state.Open(filepath.Join(dir, "pravo.state"))
	if err != nil {
		t.Fatal(err)
	}
	seed(t, st)
	path := filepath.Join(dir, "plan.pravo")
	writePlan(t, &project{t: t, dir: dir, store: st}, path)
	g := start(t, dir, st, Options{PlanFile: path}).graph()
	if g.Plan == nil {
		t.Fatal("no plan in the graph")
	}
	var env bool
	for _, n := range g.Nodes {
		env = env || n.Mode == "builtin"
	}
	if !env {
		t.Skip("the specification of the test has no node of an environment variable")
	}
	if g.Plan.Total != 8 {
		t.Errorf("planned %d of %d: the variable of the environment is counted", g.Plan.Planned, g.Plan.Total)
	}
}
