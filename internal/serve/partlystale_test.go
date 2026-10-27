package serve

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/pravo-systems/pravo/internal/engine"
)

// partlyStale stores a plan that the state moved on from under two of its four
// changes: services.a.db, whose record is bumped, and services.b.file, which has to
// be applied with it. hosts.x and old.thing are the part that can be applied. The
// summary says what its changes need (Needs), so the plan is judged change by
// change and not as a whole.
func partlyStale(t *testing.T, p *project, author string, created time.Time) string {
	t.Helper()
	id := storePlan(t, p, stored{Author: author, Reason: "raise the port", Created: created,
		Addrs: []string{"services.a.db", "services.b.file", "hosts.x", "old.thing"},
		Needs: map[string][]string{"services.b.file": {"services.a.db"}}})
	r := *p.store.Get("services.a.db")
	if err := p.store.Set(&r, ""); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the plan to be partly stale", func() bool { sp := planOf(p.plans(), id); return sp != nil && sp.Status == "partly-stale" })
	return id
}

// A plan that the state moved on from under some of its changes is open: it lists
// what conflicts and why, the part that can be applied by name and the command
// that applies it, and it keeps its badge on the nodes of the changes that
// conflict, without an overlap that it cannot make.
func TestPartlyStalePlanIsListed(t *testing.T) {
	p := newProject(t)
	id := partlyStale(t, p, "ana@acme", time.Now().Add(-3*time.Hour))
	l := p.plans()
	sp := planOf(l, id)
	if !sp.Open || sp.Status != "partly-stale" || len(sp.Reasons) != 0 || sp.Apply != "pravo apply "+id {
		t.Errorf("status %q open %v reasons %v apply %q", sp.Status, sp.Open, sp.Reasons, sp.Apply)
	}
	if sp.Unapplied != 4 || sp.ConflictsOmitted != 0 || len(sp.Conflicts) != 2 {
		t.Fatalf("unapplied %d, conflicts %+v (%d omitted)", sp.Unapplied, sp.Conflicts, sp.ConflictsOmitted)
	}
	// by address, each with its reason as the engine words it
	if c := sp.Conflicts[0]; c.Addr != "services.a.db" || len(c.Reasons) != 1 || !strings.Contains(c.Reasons[0], "services.a.db changed in state") {
		t.Errorf("the first conflict: %+v", c)
	}
	if c := sp.Conflicts[1]; c.Addr != "services.b.file" || len(c.Reasons) != 1 || !strings.Contains(c.Reasons[0], "services.b.file must be applied with services.a.db") {
		t.Errorf("the second conflict: %+v", c)
	}
	if !same(sp.ApplyOnly, []string{"hosts.x", "old.thing"}) || sp.ApplyOnlyOmitted != 0 || sp.ApplyLive != "pravo apply "+id+" --only hosts.x,old.thing" {
		t.Errorf("the live part: %v, command %q", sp.ApplyOnly, sp.ApplyLive)
	}
	for _, c := range sp.Touches {
		if want := c.Addr == "services.a.db" || c.Addr == "services.b.file"; c.Conflict != want || c.Applied {
			t.Errorf("touch %+v: conflict should be %v", c, want)
		}
	}
	// The command carries nothing that the author wrote: not the reason, not tags.
	raw := string(p.get("/api/plans"))
	if strings.Contains(raw, "raise the port") {
		t.Errorf("the listing carries the reason: %s", raw)
	}
	noSecrets(t, "the list of plans", []byte(raw))

	// Its badge stays on every node that it has a change left on.
	g := p.graph()
	for _, addr := range []string{"services.a.db", "services.b.file", "hosts.x", "old.thing"} {
		if n := nodeOf(g, addr); !same(n.Plans, []string{id}) || n.Overlap {
			t.Errorf("%s: plans %v overlap %v", addr, n.Plans, n.Overlap)
		}
	}
	// A plan that changes the resource that conflicts is no overlap: the first cannot apply the change.
	ben := storePlan(t, p, stored{Author: "ben@acme", Created: time.Now().Add(-2 * time.Hour), Addrs: []string{"services.a.db", "hosts.x"}})
	waitFor(t, "the second plan", func() bool { sp := planOf(p.plans(), ben); return sp != nil && sp.Status == "pending" })
	l = p.plans()
	a, b := planOf(l, id), planOf(l, ben)
	if len(a.Overlaps) != 1 || a.Overlaps[0].ID != ben || !same(a.Overlaps[0].Addrs, []string{"hosts.x"}) || len(b.Overlaps) != 1 || b.Overlaps[0].ID != id || !same(b.Overlaps[0].Addrs, []string{"hosts.x"}) {
		t.Errorf("overlaps: %+v / %+v, want the two on hosts.x only", a.Overlaps, b.Overlaps)
	}
	g = p.graph()
	for addr, want := range map[string]struct {
		plans   []string
		overlap bool
	}{
		"services.a.db":   {[]string{ben, id}, false},
		"hosts.x":         {[]string{ben, id}, true},
		"services.b.file": {[]string{id}, false},
	} {
		if n := nodeOf(g, addr); !same(n.Plans, want.plans) || n.Overlap != want.overlap {
			t.Errorf("%s: plans %v overlap %v, want %v %v", addr, n.Plans, n.Overlap, want.plans, want.overlap)
		}
	}
	if sp := planOf(l, ben); sp.Conflicts != nil || sp.ApplyLive != "" || sp.ApplyOnly != nil || sp.Unapplied != 0 {
		t.Errorf("a pending plan carries what a partly stale one does: %+v", sp)
	}
}

// The overlay of a partly stale plan is not stale: the page lays it over the graph
// without the banner of a plan that cannot be applied, and the node of a change that
// conflicts says why.
func TestPartlyStalePlanOverlay(t *testing.T) {
	p := newProject(t)
	id := partlyStale(t, p, "ana@acme", time.Now().Add(-time.Hour))
	o := p.overlay(id)
	if o.Plan.Status != "partly-stale" || o.Plan.Stale || len(o.Plan.Reasons) != 0 || len(o.Warnings) != 0 {
		t.Errorf("the overlay is %q, stale %v, reasons %v, warnings %v", o.Plan.Status, o.Plan.Stale, o.Plan.Reasons, o.Warnings)
	}
	if o.Plan.Planned != 4 || len(o.Nodes) != 4 {
		t.Errorf("planned %d, %d nodes", o.Plan.Planned, len(o.Nodes))
	}
	note := func(addr string) string {
		d := p.detailFor(addr, id)
		for _, n := range d.Notes {
			if strings.Contains(n, "conflicts with the state") {
				return n
			}
		}
		return ""
	}
	if n := note("services.a.db"); !strings.Contains(n, "services.a.db changed in state") {
		t.Errorf("the note of the record that moved: %q", n)
	}
	if n := note("services.b.file"); !strings.Contains(n, "must be applied with services.a.db") {
		t.Errorf("the note of the change that needs it: %q", n)
	}
	for _, addr := range []string{"hosts.x", "old.thing"} {
		if n := note(addr); n != "" {
			t.Errorf("%s can be applied, and says %q", addr, n)
		}
	}
	// A server started with the plan lays it over its own graph: no banner, no warning of a stale plan.
	q := start(t, p.dir, p.store, Options{PlanID: id})
	g := q.graph()
	if g.Plan == nil || g.Plan.Status != "partly-stale" || g.Plan.Stale || len(g.Plan.Reasons) != 0 {
		t.Errorf("the plan of the graph: %+v", g.Plan)
	}
	for _, w := range g.Warnings {
		if strings.Contains(w, "stale") {
			t.Errorf("the graph warns of %q", w)
		}
	}
}

// detailFor asks for the detail of a node as a stored plan sees it.
func (p *project) detailFor(addr, plan string) Detail {
	p.t.Helper()
	var d Detail
	if err := json.Unmarshal(p.get("/api/detail?addr="+queryEscape(addr)+"&plan="+queryEscape(plan)), &d); err != nil {
		p.t.Fatal(err)
	}
	return d
}

// A partly stale plan that is not applied expires without anything being written,
// and the watcher counts the moment as a change of the plans, as it does for a
// pending one.
func TestAnExpiringPartlyStalePlanIsNoticed(t *testing.T) {
	p := newProject(t)
	id := storePlan(t, p, stored{Author: "ana@acme", Created: time.Now().Add(-time.Hour), TTL: time.Hour + 4*time.Second,
		Addrs: []string{"services.a.db", "services.b.file", "hosts.x"}, Needs: map[string][]string{"services.b.file": {"services.a.db"}}})
	r := *p.store.Get("services.a.db")
	if err := p.store.Set(&r, ""); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the plan", func() bool { sp := planOf(p.plans(), id); return sp != nil && sp.Status == "partly-stale" })
	m, err := p.srv.current()
	if err != nil {
		t.Fatal(err)
	}
	if got, want := m.stored.nextExpiry(), planOf(p.plans(), id).Expires; !got.Equal(want) {
		t.Errorf("the next expiry is %s, the plan's is %s", got, want)
	}
	next := eventsOf(t, p)
	for deadline := time.Now().Add(12 * time.Second); ; {
		ev, ok := next(time.Until(deadline))
		if !ok {
			t.Fatal("no event when the plan expired")
		}
		if same(ev.What, []string{"plans"}) && planOf(p.plans(), id).Status == "expired" {
			break
		}
	}
	if n := nodeOf(p.graph(), "services.a.db"); len(n.Plans) != 0 {
		t.Errorf("the node after the expiry: %v", n.Plans)
	}
}

// What is sent of a partly stale plan is bounded and masked: the conflicts are the
// first 20, each reason is clipped and masked as the reasons of a stale plan are,
// the part to apply is bounded, and a command that would be too long is left out.
func TestPartlyStaleListIsBoundedAndMasked(t *testing.T) {
	p := newProject(t)
	m, err := p.srv.current()
	if err != nil {
		t.Fatal(err)
	}
	meta := &engine.PlanMeta{ID: "plan-00aa11bb", Created: time.Now(), Expires: time.Now().Add(time.Hour), Author: "ana@acme"}
	conflicts := map[string][]string{}
	for i := range maxReasons + 7 {
		addr := fmt.Sprintf("c.r%03d", i)
		meta.Changes = append(meta.Changes, engine.PlanTouch{Addr: addr, Action: engine.ActionUpdate})
		conflicts[addr] = []string{addr + " changed in state " + secretToken + " " + strings.Repeat("x", 2*maxReason)}
	}
	var live []string
	for i := range maxTouches + 5 {
		addr := fmt.Sprintf("live.resource.name%04d", i)
		meta.Changes = append(meta.Changes, engine.PlanTouch{Addr: addr, Action: engine.ActionCreate})
		live = append(live, addr)
	}
	e := &engine.PlanEntry{Meta: meta, Progress: &engine.PlanProgress{Applied: map[string]string{}}, Status: engine.PlanPartlyStale, Conflicts: conflicts, Live: live}
	sp := m.storedPlan(e)
	if sp.Status != "partly-stale" || !sp.Open || len(sp.Reasons) != 0 {
		t.Errorf("status %q open %v reasons %v", sp.Status, sp.Open, sp.Reasons)
	}
	if len(sp.Conflicts) != maxReasons || sp.ConflictsOmitted != 7 || sp.Unapplied != len(meta.Changes) || sp.Conflicts[0].Addr != "c.r000" {
		t.Errorf("%d conflicts, %d omitted, %d unapplied, first %q", len(sp.Conflicts), sp.ConflictsOmitted, sp.Unapplied, sp.Conflicts[0].Addr)
	}
	data, _ := json.Marshal(sp)
	noSecrets(t, "a partly stale plan", data, secretToken)
	for _, c := range sp.Conflicts {
		if len(c.Reasons) != 1 || len([]rune(c.Reasons[0])) > maxReason+1 {
			t.Errorf("the reasons of %s: %d characters", c.Addr, len([]rune(strings.Join(c.Reasons, ""))))
		}
	}
	if len(sp.ApplyOnly) != maxTouches || sp.ApplyOnlyOmitted != 5 || len(sp.Touches) != maxTouches {
		t.Errorf("apply_only %d (%d omitted), touches %d", len(sp.ApplyOnly), sp.ApplyOnlyOmitted, len(sp.Touches))
	}
	// 2005 addresses of twenty characters make a command of about 40 KiB: it is not sent in part.
	if sp.ApplyLive != "" {
		t.Errorf("a command of %d bytes", len(sp.ApplyLive))
	}
	// A part that fits is given whole, with the quoting that the flag reads.
	e.Live = []string{"a b", "c,d", "e"}
	sp = m.storedPlan(e)
	if want := `pravo apply plan-00aa11bb --only 'a b,"c,d",e'`; sp.ApplyLive != want {
		t.Errorf("the command is %q, want %q", sp.ApplyLive, want)
	}
}

// A plan that is stale as a whole (its summary does not say what its changes need,
// or nothing of it can be applied) says so with reasons, as before, and none of
// what a partly stale plan says.
func TestStalePlanIsNotPartlyStale(t *testing.T) {
	p := newProject(t)
	f := storeFive(t, p)
	waitFor(t, "the plans", func() bool { sp := planOf(p.plans(), f.ci); return sp != nil && sp.Status == "stale" })
	sp := planOf(p.plans(), f.ci)
	if sp.Open || len(sp.Reasons) == 0 || sp.Conflicts != nil || sp.ApplyLive != "" || sp.ApplyOnly != nil || sp.Unapplied != 0 {
		t.Errorf("a stale plan: %+v", sp)
	}
	if o := p.overlay(f.ci); !o.Plan.Stale || len(o.Warnings) == 0 || !strings.Contains(o.Warnings[0], "stale") {
		t.Errorf("its overlay: %+v %v", o.Plan, o.Warnings)
	}
}
