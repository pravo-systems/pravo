package engine

import (
	"context"
	"encoding/json"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/spf13/pflag"
)

// withDeps is a change of a test plan that depends on the changes at deps.
func withDeps(sc savedChange, deps ...string) savedChange {
	sc.Deps = deps
	return sc
}

// dataSource is a data source that the plan read and that returned something
// other than its record, so that the changes that read it need it.
func dataSource(addr string, rev int) savedChange {
	sc := change(addr, ActionRead, rev)
	sc.Mode = "data"
	return sc
}

func metaOf(changes ...savedChange) *PlanMeta {
	return newPlanMeta(gcPlan(t0, changes...), "alice", 0, 1)
}

// entryOf works out where the plan stands when the records are at the revisions,
// 0 when one is not listed, and the changes at applied were applied.
func entryOf(m *PlanMeta, revisions map[string]int, applied ...string) *PlanEntry {
	p := &PlanProgress{Format: PlanMetaFormat, ID: m.ID}
	if len(applied) > 0 {
		p.Applied = map[string]string{}
		for _, a := range applied {
			p.Applied[a] = "txn"
		}
	}
	return newPlanEntry(m, p, stateAt(revisions), t0.Add(time.Minute))
}

func conflictsOf(e *PlanEntry) string {
	var out []string
	for a := range e.Conflicts {
		out = append(out, a)
	}
	sort.Strings(out)
	return strings.Join(out, ",")
}

func TestAConflictOfOneChangeLeavesTheOthersToApply(t *testing.T) {
	m := metaOf(change("a", ActionUpdate, 1), change("b", ActionCreate, 0), change("c", ActionDelete, 4))
	e := entryOf(m, map[string]int{"a": 2, "c": 4})
	if e.Status != PlanPartlyStale || !e.Status.Open() {
		t.Fatalf("status %s, open %v", e.Status, e.Status.Open())
	}
	if got := conflictsOf(e); got != "a" {
		t.Errorf("conflicts %v", e.Conflicts)
	}
	if want := "a changed in state (revision 2, plan expected 1)"; !reflect.DeepEqual(e.Conflicts["a"], []string{want}) || !reflect.DeepEqual(e.Reasons, []string{want}) {
		t.Errorf("conflicts %v, reasons %v", e.Conflicts, e.Reasons)
	}
	if !reflect.DeepEqual(e.Live, []string{"b", "c"}) {
		t.Errorf("live %v", e.Live)
	}
	if e.OnlyFlag() != "--only b,c" || e.ApplyLiveCommand() != "pravo apply "+m.ID+" --only b,c" {
		t.Errorf("flag %q, command %q", e.OnlyFlag(), e.ApplyLiveCommand())
	}
	if e.FirstConflict() != "a changed in state (revision 2, plan expected 1)" || e.Unapplied() != 3 {
		t.Errorf("first conflict %q, unapplied %d", e.FirstConflict(), e.Unapplied())
	}
	// Nothing moved: the whole plan is there.
	if e := entryOf(m, map[string]int{"a": 1, "c": 4}); e.Status != PlanPending || e.Conflicts != nil || e.Live != nil || e.OnlyFlag() != "" || e.ApplyLiveCommand() != "" {
		t.Errorf("nothing moved: %+v", e)
	}
}

func TestTheCommandOfTheLivePartCarriesTheTagsOfThePlan(t *testing.T) {
	pf := gcPlan(t0, change("a", ActionUpdate, 1), change("b", ActionCreate, 0))
	pf.Tags, pf.SysTags = []string{"env=prod", "msg=hello world"}, true
	m := newPlanMeta(pf, "alice", 0, 1)
	m.ID = "plan-12345678"
	e := entryOf(m, map[string]int{"a": 2})
	if want := "pravo apply plan-12345678 -t env=prod -t 'msg=hello world' -T --only b"; e.ApplyLiveCommand() != want {
		t.Errorf("%s\nwant %s", e.ApplyLiveCommand(), want)
	}
}

func TestAChangeThatNeedsAConflictedChangeIsBlockedToo(t *testing.T) {
	m := metaOf(
		change("a", ActionUpdate, 1),
		withDeps(change("b", ActionCreate, 0), "a"),
		withDeps(change("c", ActionCreate, 0), "b"),
		change("d", ActionCreate, 0),
		withDeps(change("e", ActionUpdate, 2), "d"),
	)
	e := entryOf(m, map[string]int{"a": 2, "e": 2})
	if e.Status != PlanPartlyStale || conflictsOf(e) != "a,b,c" || !reflect.DeepEqual(e.Live, []string{"d", "e"}) {
		t.Fatalf("%s: conflicts %v, live %v", e.Status, e.Conflicts, e.Live)
	}
	// A dependent says what it waits for, and the nearest change that blocks it.
	if got := e.Conflicts["b"]; len(got) != 1 || got[0] != "b must be applied with a, which cannot be applied" {
		t.Errorf("b: %v", got)
	}
	if got := e.Conflicts["c"]; len(got) != 1 || got[0] != "c must be applied with b, which cannot be applied" {
		t.Errorf("c: %v", got)
	}
	// The first conflict is one whose own record moved, not one that waits.
	if e.FirstConflict() != "a changed in state (revision 2, plan expected 1)" {
		t.Errorf("first conflict %q", e.FirstConflict())
	}
	wantReasons := []string{
		"a changed in state (revision 2, plan expected 1)",
		"b must be applied with a, which cannot be applied",
		"c must be applied with b, which cannot be applied",
	}
	if !reflect.DeepEqual(e.Reasons, wantReasons) {
		t.Errorf("reasons %v", e.Reasons)
	}
	// What a change depends on can conflict without blocking it, if it is not needed: b is
	// blocked by a, and a is not blocked by b.
	if e2 := entryOf(m, map[string]int{"a": 1, "c": 7, "e": 2}); e2.Status != PlanPartlyStale || conflictsOf(e2) != "c" || !reflect.DeepEqual(e2.Live, []string{"a", "b", "d", "e"}) {
		t.Errorf("c moved: %s, conflicts %v, live %v", e2.Status, e2.Conflicts, e2.Live)
	}
}

func TestALiveChangeBringsTheChangesItNeeds(t *testing.T) {
	m := metaOf(
		change("a", ActionUpdate, 1),
		withDeps(change("b", ActionCreate, 0), "c", "z"),
		change("c", ActionCreate, 0),
		change("z", ActionUpdate, 3),
		dataSource("rd", 5),
		withDeps(change("u", ActionUpdate, 2), "rd"),
		change("n", ActionNoop, 9),
		withDeps(change("v", ActionUpdate, 2), "n"),
	)
	// A noop that a change depends on is not needed by it: Subset does not ask for it.
	if got := m.Needs["v"]; got != nil {
		t.Errorf("v needs %v", got)
	}
	if got := m.Needs["b"]; !reflect.DeepEqual(got, []string{"c", "z"}) {
		t.Errorf("b needs %v", got)
	}
	e := entryOf(m, map[string]int{"a": 2, "z": 3, "rd": 5, "n": 9, "u": 2, "v": 2})
	if e.Status != PlanPartlyStale || conflictsOf(e) != "a" {
		t.Fatalf("%s: conflicts %v", e.Status, e.Conflicts)
	}
	// The data source that u needs is part of the selection that applies u.
	if want := []string{"b", "c", "rd", "u", "v", "z"}; !reflect.DeepEqual(e.Live, want) {
		t.Errorf("live %v, want %v", e.Live, want)
	}
	// The data source moved: u cannot be applied, and the data source is a conflict although the
	// summary does not list it.
	e = entryOf(m, map[string]int{"a": 1, "rd": 6, "z": 3, "n": 9, "u": 2, "v": 2})
	if e.Status != PlanPartlyStale || conflictsOf(e) != "rd,u" || e.Unapplied() != 7 {
		t.Errorf("rd moved: %s, conflicts %v, unapplied %d", e.Status, e.Conflicts, e.Unapplied())
	}
	if want := []string{"a", "b", "c", "v", "z"}; !reflect.DeepEqual(e.Live, want) {
		t.Errorf("live %v, want %v", e.Live, want)
	}
}

func TestADeleteIsBlockedByTheDeletesThatDependOnIt(t *testing.T) {
	// y depends on x, so y is deleted first; x is deleted when y has been.
	m := metaOf(change("x", ActionDelete, 1), withDeps(change("y", ActionDelete, 2), "x"), change("w", ActionCreate, 0))
	if got := m.Needs["x"]; !reflect.DeepEqual(got, []string{"y"}) || m.Needs["y"] != nil {
		t.Errorf("needs %v", m.Needs)
	}
	e := entryOf(m, map[string]int{"x": 1, "y": 3})
	if e.Status != PlanPartlyStale || conflictsOf(e) != "x,y" || !reflect.DeepEqual(e.Live, []string{"w"}) {
		t.Errorf("y moved: %s, conflicts %v, live %v", e.Status, e.Conflicts, e.Live)
	}
	if got := e.Conflicts["x"]; len(got) != 1 || !strings.Contains(got[0], "x must be applied with y") {
		t.Errorf("x: %v", got)
	}
	// The delete that nothing depends on is free when the one it depends on moved.
	e = entryOf(m, map[string]int{"x": 2, "y": 2})
	if e.Status != PlanPartlyStale || conflictsOf(e) != "x" || !reflect.DeepEqual(e.Live, []string{"w", "y"}) {
		t.Errorf("x moved: %s, conflicts %v, live %v", e.Status, e.Conflicts, e.Live)
	}
}

func TestNeedsAreTheRulesOfSubset(t *testing.T) {
	saved := []savedChange{
		change("a", ActionCreate, 0),
		withDeps(change("b", ActionUpdate, 1), "a", "n", "rd", "skd", "ap", "missing"),
		change("n", ActionNoop, 1),
		dataSource("rd", 2),
		dataSource("skd", 3),
		withDeps(change("c", ActionReplace, 4), "b"),
		change("del1", ActionDelete, 5),
		withDeps(change("del2", ActionDelete, 6), "del1"),
		withDeps(change("del3", ActionDelete, 7), "del2", "del1"),
		withDeps(change("mix", ActionCreate, 0), "del3", "c"),
		withDeps(change("def", ActionRead, 0), "a"),
		withDeps(change("fromdef", ActionUpdate, 8), "def"),
		withDeps(change("loop", ActionUpdate, 9), "loop"),
		change("ap", ActionUpdate, 10),
	}
	for i := range saved {
		switch saved[i].Addr {
		case "skd":
			saved[i].Skipped = true
		case "def":
			saved[i].Deferred = true
		case "ap":
			saved[i].Applied = true
		}
	}
	m := newPlanMeta(gcPlan(t0, saved...), "alice", 0, 1)
	plan := &Plan{}
	for _, sc := range saved {
		plan.Changes = append(plan.Changes, &Change{Addr: sc.Addr, Mode: sc.Mode, Action: sc.Action, Deps: sc.Deps, Deferred: sc.Deferred, Skipped: sc.Skipped, Kept: sc.Kept, Applied: sc.Applied})
	}
	// The closure that the summary gives is the closure that Subset asks for.
	reach := func(from string) []string {
		seen := map[string]bool{from: true}
		for stack := []string{from}; len(stack) > 0; {
			a := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			for _, n := range m.Needs[a] {
				if !seen[n] {
					seen[n] = true
					stack = append(stack, n)
				}
			}
		}
		out := make([]string, 0, len(seen))
		for a := range seen {
			out = append(out, a)
		}
		sort.Strings(out)
		return out
	}
	for _, sc := range saved {
		if sc.Applied {
			continue
		}
		want := []string{sc.Addr}
		if _, err := plan.Subset([]string{sc.Addr}); err != nil {
			nc, ok := err.(*ErrNotClosed)
			if !ok {
				t.Fatalf("%s: %v", sc.Addr, err)
			}
			want = nc.Closure
		}
		if got := reach(sc.Addr); !reflect.DeepEqual(got, want) {
			t.Errorf("%s: the summary needs %v, Subset needs %v", sc.Addr, got, want)
		}
	}
	// Only a change that can be selected has an entry, and nobody needs itself or what was applied.
	if _, ok := m.Needs["n"]; ok {
		t.Errorf("a noop that nothing needs has needs: %v", m.Needs["n"])
	}
	if _, ok := m.Needs["ap"]; ok || len(m.Needs["loop"]) != 0 {
		t.Errorf("needs of an applied change or of itself: %v", m.Needs)
	}
	if got := m.Needs["b"]; !reflect.DeepEqual(got, []string{"a", "rd"}) {
		t.Errorf("b needs %v", got)
	}
}

func TestAMovedRecordThatThePlanReadsMakesItStaleAsAWhole(t *testing.T) {
	pf := gcPlan(t0, change("a", ActionUpdate, 1), change("b", ActionCreate, 0))
	pf.Inputs = map[string]int{"in": 9}
	m := newPlanMeta(pf, "alice", 0, 1)
	if e := entryOf(m, map[string]int{"a": 1, "in": 9}); e.Status != PlanPending {
		t.Fatalf("as expected: %s", e.Status)
	}
	e := entryOf(m, map[string]int{"a": 1, "in": 10})
	if e.Status != PlanStale || len(e.Reasons) != 1 || !strings.Contains(e.Reasons[0], "the plan reads it") || e.Conflicts != nil || e.Live != nil {
		t.Errorf("an input moved: %s %v", e.Status, e.Reasons)
	}
	// A change that conflicts as well does not make a part of the plan live: every reason is given.
	e = entryOf(m, map[string]int{"a": 2, "in": 10})
	if e.Status != PlanStale || len(e.Reasons) != 2 || e.Conflicts != nil {
		t.Errorf("an input and a change moved: %s %v", e.Status, e.Reasons)
	}
	// The same record, but not one that the plan reads, leaves the rest to apply.
	if e := entryOf(m, map[string]int{"a": 2, "in": 9}); e.Status != PlanPartlyStale {
		t.Errorf("a change moved: %s", e.Status)
	}
}

func TestADifferentStateMakesAPlanStaleAsAWhole(t *testing.T) {
	m := metaOf(change("a", ActionUpdate, 1), change("b", ActionCreate, 0))
	check := stateAt(map[string]int{"a": 2})
	check.Lineage = "lineage-2"
	e := newPlanEntry(m, &PlanProgress{}, check, t0)
	if e.Status != PlanStale || len(e.Reasons) != 1 || !strings.Contains(e.Reasons[0], "lineage") || e.Live != nil {
		t.Errorf("%s %v live %v", e.Status, e.Reasons, e.Live)
	}
}

func TestAPlanWithNothingLiveIsStale(t *testing.T) {
	// Every change moved.
	m := metaOf(change("a", ActionUpdate, 1), change("b", ActionCreate, 0))
	if e := entryOf(m, map[string]int{"a": 2, "b": 1}); e.Status != PlanStale || len(e.Reasons) != 2 || e.Conflicts != nil || e.Live != nil || e.Status.Open() {
		t.Errorf("both moved: %s %v", e.Status, e.Reasons)
	}
	// The change that did not move needs the one that did. The reasons are those of the records that moved.
	m = metaOf(change("a", ActionUpdate, 1), withDeps(change("b", ActionCreate, 0), "a"))
	e := entryOf(m, map[string]int{"a": 2})
	if e.Status != PlanStale || len(e.Reasons) != 1 || !strings.HasPrefix(e.Reasons[0], "a changed") || e.Live != nil {
		t.Errorf("b waits for a: %s %v", e.Status, e.Reasons)
	}
	// A moved record that no change of the summary lists leaves no live change when that is all there is.
	m = metaOf(change("n", ActionNoop, 3))
	if e := entryOf(m, map[string]int{"n": 4}); e.Status != PlanStale {
		t.Errorf("only a noop: %s", e.Status)
	}
}

// A record that the plan has only as a no-op may be what the other changes were planned
// from: when it moves, the plan is stale as a whole, as it was before the changes were
// judged one by one.
func TestAMovedNoopMakesThePlanStaleAsAWhole(t *testing.T) {
	m := metaOf(change("a", ActionUpdate, 1), change("b", ActionCreate, 0), change("n", ActionNoop, 3))
	e := entryOf(m, map[string]int{"a": 1, "n": 4})
	if e.Status != PlanStale || e.Live != nil || len(e.Reasons) != 1 {
		t.Errorf("%s: conflicts %v, live %v, reasons %v", e.Status, e.Conflicts, e.Live, e.Reasons)
	}
}

func TestASummaryWithoutNeedsIsJudgedAsAWhole(t *testing.T) {
	m := metaOf(change("a", ActionUpdate, 1), change("b", ActionCreate, 0))
	enc, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	// A summary that has the field, even when no change needs another, says that the plan
	// was summarised with the dependencies.
	if !strings.Contains(string(enc), `"needs":{}`) {
		t.Fatalf("a summary without dependencies: %s", enc)
	}
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(enc, &doc); err != nil {
		t.Fatal(err)
	}
	delete(doc, "needs")
	old, _ := json.Marshal(doc)
	var om PlanMeta
	if err := json.Unmarshal(old, &om); err != nil {
		t.Fatal(err)
	}
	if om.Needs != nil {
		t.Fatalf("needs: %v", om.Needs)
	}
	e := entryOf(&om, map[string]int{"a": 2})
	if e.Status != PlanStale || len(e.Reasons) != 1 || !strings.Contains(e.Reasons[0], "a changed") || e.Live != nil || e.Conflicts != nil {
		t.Errorf("old summary: %s %v", e.Status, e.Reasons)
	}
	if e := entryOf(&om, map[string]int{"a": 1}); e.Status != PlanPending {
		t.Errorf("old summary, nothing moved: %s", e.Status)
	}
	if e := entryOf(m, map[string]int{"a": 2}); e.Status != PlanPartlyStale {
		t.Errorf("new summary: %s", e.Status)
	}
}

func TestChangesThatWereAppliedAreNotJudged(t *testing.T) {
	m := metaOf(change("a", ActionUpdate, 1), change("b", ActionCreate, 0), change("c", ActionCreate, 0))
	// a was applied from the plan, and its record moved because of it.
	if e := entryOf(m, map[string]int{"a": 2}, "a"); e.Status != PlanPartial || e.Conflicts != nil {
		t.Errorf("a applied: %s", e.Status)
	}
	// b moved too: c is left, and the count does not include what was applied.
	e := entryOf(m, map[string]int{"a": 2, "b": 1}, "a")
	if e.Status != PlanPartlyStale || conflictsOf(e) != "b" || !reflect.DeepEqual(e.Live, []string{"c"}) || e.Unapplied() != 2 {
		t.Errorf("a applied, b moved: %s, conflicts %v, live %v, unapplied %d", e.Status, e.Conflicts, e.Live, e.Unapplied())
	}
	// What is left is only what conflicts: stale.
	if e := entryOf(m, map[string]int{"b": 1, "c": 1}, "a"); e.Status != PlanStale {
		t.Errorf("only conflicts left: %s", e.Status)
	}
	// A change waits for what it needs, not for what was applied: a chain that goes through
	// an applied change is cut there.
	m = metaOf(withDeps(change("x", ActionUpdate, 1), "y"), withDeps(change("y", ActionUpdate, 1), "z"), change("z", ActionUpdate, 1), change("w", ActionCreate, 0))
	e = entryOf(m, map[string]int{"x": 1, "y": 1, "z": 2}, "y")
	if e.Status != PlanPartlyStale || conflictsOf(e) != "z" || !reflect.DeepEqual(e.Live, []string{"w", "x"}) {
		t.Errorf("y applied, z moved: %s, conflicts %v, live %v", e.Status, e.Conflicts, e.Live)
	}
	// Everything applied is applied, whatever moved.
	done := time.Now()
	p := &PlanProgress{Applied: map[string]string{"a": "t", "b": "t", "c": "t"}, DoneAt: &done}
	if st, _ := planStatus(m, p, stateAt(map[string]int{"z": 9}), t0); st != PlanApplied {
		t.Errorf("done: %s", st)
	}
}

func TestAPartlyStalePlanExpiresLikeAnyOther(t *testing.T) {
	m := metaOf(change("a", ActionUpdate, 1), change("b", ActionCreate, 0))
	p := &PlanProgress{}
	if st, _ := planStatus(m, p, stateAt(map[string]int{"a": 2}), t0.Add(DefaultPlanTTL+time.Second)); st != PlanExpired {
		t.Errorf("past the ttl: %s", st)
	}
	for st, want := range map[PlanStatus]bool{PlanPending: true, PlanPartial: true, PlanPartlyStale: true, PlanStale: false, PlanApplied: false, PlanExpired: false} {
		if st.Open() != want {
			t.Errorf("%s: open %v", st, st.Open())
		}
	}
}

func TestTheOnlyFlagIsReadBackByTheFlag(t *testing.T) {
	live := []string{`a["x,y"]`, `b`, `c "quoted"`, `d`}
	e := &PlanEntry{Meta: &PlanMeta{ID: "plan-12345678"}, Live: live}
	flag := e.OnlyFlag()
	value, ok := strings.CutPrefix(flag, "--only ")
	if !ok || !strings.HasPrefix(value, "'") || !strings.HasSuffix(value, "'") {
		t.Fatalf("flag %q", flag)
	}
	fs := pflag.NewFlagSet("apply", pflag.ContinueOnError)
	only := fs.StringSlice("only", nil, "")
	// The shell takes the quotes off; the flag reads what is left.
	if err := fs.Parse([]string{"--only", strings.Trim(value, "'")}); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(*only, live) {
		t.Errorf("the flag reads %q from %s", *only, flag)
	}
}

func TestOverlapsLeaveOutTheChangesThatConflict(t *testing.T) {
	eachBackend(t, func(t *testing.T, ps *PlanStore, _ *time.Time) {
		ctx := context.Background()
		mine := putPlan(t, ps, t0, "alice", 0, change("a", ActionUpdate, 1), change("b", ActionUpdate, 1))
		theirs := putPlan(t, ps, t0.Add(time.Minute), "bob", 0, change("a", ActionUpdate, 1), change("b", ActionUpdate, 1), change("c", ActionCreate, 0))
		es, err := ps.Entries(ctx, stateAt(map[string]int{"a": 2, "b": 1}))
		if err != nil {
			t.Fatal(err)
		}
		over := Overlaps(es, mine.ID, mine.Changes)
		if len(over) != 1 || over[0].Plan.ID != theirs.ID || strings.Join(over[0].Addrs, ",") != "b" {
			t.Errorf("overlaps %+v", over)
		}
		// A listing by resource still finds the plan for the resource it cannot change.
		if got := Touching(es, "a"); len(got) != 2 {
			t.Errorf("touching a: %d", len(got))
		}
	})
}

func TestListingJudgesEachPlanChangeByChange(t *testing.T) {
	eachBackend(t, func(t *testing.T, ps *PlanStore, _ *time.Time) {
		ctx := context.Background()
		partly := putPlan(t, ps, t0, "alice", 0, change("a", ActionUpdate, 1), withDeps(change("b", ActionCreate, 0), "a"), change("c", ActionCreate, 0))
		whole := putPlan(t, ps, t0.Add(time.Second), "bob", 0, change("a", ActionUpdate, 1))
		fine := putPlan(t, ps, t0.Add(2*time.Second), "carol", 0, change("z", ActionCreate, 0))
		es, err := ps.Entries(ctx, stateAt(map[string]int{"a": 2}))
		if err != nil {
			t.Fatal(err)
		}
		got := map[string]*PlanEntry{}
		for _, e := range es {
			got[e.Meta.ID] = e
		}
		if e := got[partly.ID]; e.Status != PlanPartlyStale || conflictsOf(e) != "a,b" || !reflect.DeepEqual(e.Live, []string{"c"}) {
			t.Errorf("partly: %s %v %v", e.Status, e.Conflicts, e.Live)
		}
		if e := got[whole.ID]; e.Status != PlanStale || len(e.Reasons) != 1 {
			t.Errorf("whole: %s %v", e.Status, e.Reasons)
		}
		if e := got[fine.ID]; e.Status != PlanPending {
			t.Errorf("fine: %s", e.Status)
		}
		// The entry of one plan is the one the listing gives.
		one, err := ps.Entry(ctx, partly.ID, stateAt(map[string]int{"a": 2}))
		if err != nil {
			t.Fatal(err)
		}
		if one.Status != PlanPartlyStale || !reflect.DeepEqual(one.Live, got[partly.ID].Live) || !reflect.DeepEqual(one.Conflicts, got[partly.ID].Conflicts) {
			t.Errorf("entry: %+v", one)
		}
	})
}

func TestAPartlyStalePlanIsNeitherMarkedNorCollectedAsStale(t *testing.T) {
	eachBackend(t, func(t *testing.T, ps *PlanStore, clock *time.Time) {
		ctx := context.Background()
		partly := putPlan(t, ps, t0, "alice", 0, change("a", ActionUpdate, 1), change("b", ActionCreate, 0))
		whole := putPlan(t, ps, t0.Add(time.Second), "bob", 0, change("a", ActionUpdate, 1))
		moved := stateAt(map[string]int{"a": 2})

		*clock = t0.Add(time.Hour)
		res := collect(t, ps, GCOptions{Check: moved})
		if ids(res.Marked) != whole.ID || len(res.Removed) != 0 || len(res.Kept) != 0 {
			t.Fatalf("first sight: %+v", res)
		}
		if pr, _ := ps.Progress(ctx, partly.ID); pr.StaleSince != nil {
			t.Errorf("the partly stale plan was marked: %v", pr.StaleSince)
		}
		// A dry run says the same.
		if res := collect(t, ps, GCOptions{Check: moved, DryRun: true}); len(res.Marked) != 0 || len(res.Removed) != 0 {
			t.Errorf("dry run: %+v", res)
		}
		// Past the grace the stale plan goes and the partly stale one stays, until it expires.
		*clock = t0.Add(3 * time.Hour)
		res = collect(t, ps, GCOptions{Check: moved})
		if ids(res.Removed) != whole.ID || len(res.Marked) != 0 {
			t.Errorf("after the grace: %+v", res)
		}
		if !exists(ps, partly.ID) || exists(ps, whole.ID) {
			t.Errorf("objects: partly %v, whole %v", exists(ps, partly.ID), exists(ps, whole.ID))
		}
		*clock = t0.Add(DefaultPlanTTL - time.Hour)
		if res := collect(t, ps, GCOptions{Check: moved}); len(res.Removed) != 0 || len(res.Marked) != 0 {
			t.Errorf("a day before it expires: %+v", res)
		}
		*clock = t0.Add(DefaultPlanTTL + time.Hour)
		if res := collect(t, ps, GCOptions{Check: moved}); ids(res.Removed) != partly.ID || !strings.HasPrefix(res.Removed[0].Reason, "expired 1 hour ago") {
			t.Errorf("past the ttl: %+v", res)
		}
	})
}

func TestAPlanThatIsOnlyPartlyStaleLosesItsMark(t *testing.T) {
	eachBackend(t, func(t *testing.T, ps *PlanStore, clock *time.Time) {
		ctx := context.Background()
		m := putPlan(t, ps, t0, "alice", 0, change("a", ActionUpdate, 1), change("b", ActionUpdate, 1))
		*clock = t0.Add(time.Minute)
		both := stateAt(map[string]int{"a": 2, "b": 2})
		if res := collect(t, ps, GCOptions{Check: both}); ids(res.Marked) != m.ID {
			t.Fatalf("stale as a whole: %+v", res)
		}
		// Someone put b back: one change can be applied again, and the mark goes.
		*clock = t0.Add(time.Hour)
		onlyA := stateAt(map[string]int{"a": 2, "b": 1})
		if res := collect(t, ps, GCOptions{Check: onlyA, DryRun: true}); len(res.Removed) != 0 || len(res.Marked) != 0 {
			t.Errorf("dry run: %+v", res)
		}
		if pr, _ := ps.Progress(ctx, m.ID); pr.StaleSince == nil {
			t.Fatal("a dry run took the mark away")
		}
		if res := collect(t, ps, GCOptions{Check: onlyA}); len(res.Removed) != 0 || len(res.Marked) != 0 || len(res.Kept) != 0 {
			t.Errorf("partly stale: %+v", res)
		}
		if pr, _ := ps.Progress(ctx, m.ID); pr.StaleSince != nil {
			t.Errorf("the mark stayed: %v", pr.StaleSince)
		}
		// Long after the grace that the mark would have run out: the plan is still there.
		*clock = t0.Add(5 * time.Hour)
		if res := collect(t, ps, GCOptions{Check: onlyA}); len(res.Removed) != 0 || !exists(ps, m.ID) {
			t.Errorf("long after: %+v", res)
		}
		// It is stale again, and is marked as a plan is when it first is.
		if res := collect(t, ps, GCOptions{Check: both}); ids(res.Marked) != m.ID {
			t.Errorf("stale again: %+v", res)
		}
	})
}

func TestAMarkedPlanThatIsPartlyStaleAfterTheGraceIsKept(t *testing.T) {
	// A plan that was stale as a whole and marked, and is partly stale when the collection
	// comes after the grace, is not removed: it can be applied in part.
	eachBackend(t, func(t *testing.T, ps *PlanStore, clock *time.Time) {
		m := putPlan(t, ps, t0, "alice", 0, change("a", ActionUpdate, 1), change("b", ActionUpdate, 1))
		*clock = t0.Add(time.Minute)
		collect(t, ps, GCOptions{Check: stateAt(map[string]int{"a": 2, "b": 2})})
		*clock = t0.Add(3 * time.Hour)
		res := collect(t, ps, GCOptions{Check: stateAt(map[string]int{"a": 2, "b": 1})})
		if len(res.Removed) != 0 || !exists(ps, m.ID) {
			t.Errorf("partly stale after the grace: %+v", res)
		}
	})
}

func TestAPartlyStalePlanThatIsBeingAppliedIsKept(t *testing.T) {
	eachBackend(t, func(t *testing.T, ps *PlanStore, clock *time.Time) {
		ctx := context.Background()
		m := putPlan(t, ps, t0, "alice", 0, change("a", ActionUpdate, 1), change("b", ActionCreate, 0))
		*clock = t0.Add(DefaultPlanTTL - time.Hour)
		if err := ps.BeginApply(ctx, m.ID, "alice#1"); err != nil {
			t.Fatal(err)
		}
		*clock = t0.Add(DefaultPlanTTL + time.Hour)
		res := collect(t, ps, GCOptions{Check: stateAt(map[string]int{"a": 2})})
		if len(res.Removed) != 0 || ids(res.Kept) != m.ID {
			t.Errorf("an apply is running: %+v", res)
		}
	})
}
