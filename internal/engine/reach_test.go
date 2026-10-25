package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"cuelang.org/go/cue"

	"github.com/pravo-systems/pravo/internal/convert"
)

// manySpec is a specification of n independent resources named r000, r001 and
// so on, each with the content that content gives it.
func manySpec(n int, content func(i int) string) spec {
	var src strings.Builder
	var names []string
	for i := 0; i < n; i++ {
		name := fmt.Sprintf("r%03d", i)
		names = append(names, name)
		fmt.Fprintf(&src, "%s: %s\n", name, thing(name, fmt.Sprintf("content: %q", content(i))))
	}
	return spec{cue: src.String(), resources: names}
}

func plan(t *testing.T, e *Engine) *Plan {
	t.Helper()
	p, err := e.Plan(context.Background())
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	return p
}

func changeOf(t *testing.T, p *Plan, addr string) *Change {
	t.Helper()
	for _, ch := range p.Changes {
		if ch.Addr == addr {
			return ch
		}
	}
	t.Fatalf("%s is not in the plan", addr)
	return nil
}

func expectCalls(t *testing.T, h *harness, want map[string]int) {
	t.Helper()
	for name, n := range want {
		if got := h.fake.called(name); got != n {
			t.Errorf("%s was called %d times, want %d", name, got, n)
		}
	}
}

// A plan reads the records it reaches. A record it does not reach is neither
// upgraded nor read, no provider process starts for it, and nothing is locked
// or written.
func TestUnreachedRecordsAreNeverRead(t *testing.T) {
	const n = 500
	v1 := func(int) string { return "v1" }
	h := newHarness(t)
	h.apply(manySpec(n, v1))
	h.fake.resetCalls()
	h.backend.Reset()

	p := plan(t, h.engine(manySpec(n, v1), Options{}))
	if p.HasChanges() {
		t.Error("a plan of an unchanged specification has changes")
	}
	if want := (Stats{Total: n, Skipped: n, NotRead: n}); p.Stats != want {
		t.Errorf("stats = %+v, want %+v", p.Stats, want)
	}
	expectCalls(t, h, map[string]int{"start": 0, "UpgradeResourceState": 0, "ReadResource": 0, "ValidateResourceConfig": 0, "PlanResourceChange": 0})
	if w := h.backend.Writes(); w != 0 {
		t.Errorf("a plan that reads nothing wrote %d objects", w)
	}

	one := func(i int) string {
		if i == 123 {
			return "v2"
		}
		return "v1"
	}
	p = plan(t, h.engine(manySpec(n, one), Options{}))
	if want := (Stats{Total: n, Reached: 1, Skipped: n - 1, Refreshed: 1, NotRead: n - 1}); p.Stats != want {
		t.Errorf("stats = %+v, want %+v", p.Stats, want)
	}
	expectCalls(t, h, map[string]int{"start": 1, "UpgradeResourceState": 1, "ReadResource": 1, "ValidateResourceConfig": 1, "PlanResourceChange": 1})
	if got := h.fake.readNames(); !reflect.DeepEqual(got, []string{"r123"}) {
		t.Errorf("read %v, want r123", got)
	}
	if got := h.backend.Count("Put", "resources"); got != 1 {
		t.Errorf("%d records written, want the one that was read", got)
	}
	if got := h.backend.Count("PutIfAbsent", "locks"); got != 1 {
		t.Errorf("%d locks taken, want the one for the record that was written", got)
	}
	unread := 0
	for _, ch := range p.Changes {
		if ch.Unread {
			unread++
			if !ch.Skipped || ch.Action != ActionNoop || ch.Refreshed {
				t.Errorf("%s: an unread node is %s, skipped %v, refreshed %v", ch.Addr, ch.Action, ch.Skipped, ch.Refreshed)
			}
		}
	}
	if unread != n-1 {
		t.Errorf("%d nodes unread, want %d", unread, n-1)
	}
	why := reasonsOf(p, "r007")
	if len(why) != 2 || why[0] != "skipped: nothing the provider saw has changed" ||
		!strings.HasPrefix(why[1], "not read: the plan did not reach it; the record is ") || !strings.HasSuffix(why[1], " old, its refresh age is always") {
		t.Errorf("reasons of an unread node: %q", why)
	}
}

// The refresh age decides whether a record that the plan looks at is read; the
// reach decides what the plan looks at.
func TestRefreshModes(t *testing.T) {
	src := func(version string) spec {
		return spec{
			cue: "input: " + thing("input", "") + "\n" +
				"reached: " + thing("reached", `content: "\(input.id)-`+version+`"`) + "\n" +
				"unreached: " + thing("unreached", "") + "\n" +
				"pinned: " + thing("pinned", `$lifecycle: refresh: "1ms"`) + "\n" +
				"never: " + thing("never", `$lifecycle: refresh: "never"`) + "\n",
			resources: []string{"input", "reached", "unreached", "pinned", "never"},
		}
	}
	cases := []struct {
		mode  RefreshMode
		age   string
		reads []string
	}{
		{RefreshAge, "", []string{"input", "pinned", "reached"}},
		{RefreshAge, "1ms", []string{"input", "pinned", "reached"}},
		{RefreshAge, "1h", []string{"pinned"}},
		{RefreshExpired, "", []string{"input", "pinned", "reached", "unreached"}},
		{RefreshExpired, "1ms", []string{"input", "pinned", "reached", "unreached"}},
		{RefreshExpired, "1h", []string{"pinned"}},
		{RefreshAll, "", []string{"input", "never", "pinned", "reached", "unreached"}},
		{RefreshAll, "1h", []string{"input", "never", "pinned", "reached", "unreached"}},
		{RefreshNone, "", nil},
		{RefreshNone, "1ms", nil},
	}
	for _, c := range cases {
		t.Run(fmt.Sprintf("mode %d, age %q", c.mode, c.age), func(t *testing.T) {
			h := newHarness(t)
			h.apply(src("1"))
			time.Sleep(20 * time.Millisecond)
			h.fake.resetCalls()
			p := plan(t, h.engine(src("2"), Options{Refresh: c.mode, DefaultRefresh: c.age}))
			if got := h.fake.readNames(); !reflect.DeepEqual(got, c.reads) {
				t.Errorf("read %v, want %v", got, c.reads)
			}
			if ch := changeOf(t, p, "reached"); ch.Action != ActionUpdate {
				t.Errorf("reached is %s, want update", ch.Action)
			}
			if c.mode != RefreshNone {
				if st := p.Stats; st.Refreshed+st.Fresh+st.NotRead != st.Total {
					t.Errorf("%d refreshed, %d within refresh age and %d not read do not add up to %d", st.Refreshed, st.Fresh, st.NotRead, st.Total)
				}
				if st := p.Stats; st.Refreshed != len(c.reads) {
					t.Errorf("%d refreshed, want %d", st.Refreshed, len(c.reads))
				}
			}
			if c.mode == RefreshNone && (p.Stats.Fresh != 0 || p.Stats.NotRead != 0 || !p.NoRefresh) {
				t.Errorf("stats of -refresh=false: %+v, no refresh %v", p.Stats, p.NoRefresh)
			}
			// Only the default mode leaves a record unread because the plan does not
			// reach it.
			if c.mode != RefreshAge && p.Stats.NotRead != 0 && c.age != "" && c.age != "1h" {
				t.Errorf("%d not read in a mode that reads by age", p.Stats.NotRead)
			}
		})
	}
}

// chain is a specification of resources c0, c1, ... where each reads the id of
// the one before, and top reads the last. The version is part of the content
// of top.
func chain(depth int, version string) spec {
	var src strings.Builder
	names := []string{}
	for i := 0; i < depth; i++ {
		extra := ""
		if i > 0 {
			extra = fmt.Sprintf(`content: "\(c%d.id)"`, i-1)
		}
		fmt.Fprintf(&src, "c%d: %s\n", i, thing(fmt.Sprintf("c%d", i), extra))
		names = append(names, fmt.Sprintf("c%d", i))
	}
	fmt.Fprintf(&src, "top: %s\n", thing("top", fmt.Sprintf(`content: "\(c%d.id)-%s"`, depth-1, version)))
	fmt.Fprintf(&src, "other: %s\n", thing("other", ""))
	return spec{cue: src.String(), resources: append(names, "top", "other")}
}

// The records that a reached node reads are read as well, once. When one of
// them differs from the state the plan walks again from the records as they
// are, and no read is made twice.
func TestInputsOfReachedNodesAreReadOnce(t *testing.T) {
	t.Run("no drift", func(t *testing.T) {
		h := newHarness(t)
		h.apply(chain(1, "1"))
		h.fake.resetCalls()
		p := plan(t, h.engine(chain(1, "2"), Options{}))
		if got := h.fake.readNames(); !reflect.DeepEqual(got, []string{"c0", "top"}) {
			t.Errorf("read %v, want c0 and top", got)
		}
		// One walk: top is planned once.
		expectCalls(t, h, map[string]int{"PlanResourceChange": 1, "UpgradeResourceState": 2, "ReadResource": 2})
		if want := (Stats{Total: 3, Reached: 1, Skipped: 2, Refreshed: 2, NotRead: 1}); p.Stats != want {
			t.Errorf("stats = %+v, want %+v", p.Stats, want)
		}
		if ch := changeOf(t, p, "c0"); !ch.Refreshed || ch.Unread || !ch.Skipped {
			t.Errorf("c0: refreshed %v, unread %v, skipped %v", ch.Refreshed, ch.Unread, ch.Skipped)
		}
		if h.state.Get("c0").Marked() {
			t.Error("a read that found nothing left a mark")
		}
	})

	t.Run("drift", func(t *testing.T) {
		h := newHarness(t)
		h.apply(chain(1, "1"))
		h.fake.drift("c0", "changed")
		h.fake.resetCalls()
		p := plan(t, h.engine(chain(1, "2"), Options{}))
		// One more walk. The read of c0 is used again, not made again; c0 is
		// planned once and top twice.
		expectCalls(t, h, map[string]int{"UpgradeResourceState": 2, "ReadResource": 2, "PlanResourceChange": 3})
		if ch := changeOf(t, p, "c0"); ch.Action != ActionUpdate || !ch.Drifted || !ch.Refreshed {
			t.Errorf("c0: %s, drifted %v, refreshed %v; want an update that undoes the drift", ch.Action, ch.Drifted, ch.Refreshed)
		}
		if ch := changeOf(t, p, "top"); ch.Action != ActionUpdate {
			t.Errorf("top is %s, want update", ch.Action)
		}
		if want := (Stats{Total: 3, Reached: 2, Skipped: 1, Refreshed: 2, Drifted: 1, NotRead: 1}); p.Stats != want {
			t.Errorf("stats = %+v, want %+v", p.Stats, want)
		}
		if m := h.state.Get("c0"); m.DriftedAt == nil || m.Gone || !strings.Contains(string(m.Attributes), "changed") {
			t.Errorf("the record of c0 after the read: %+v", m)
		}
		// The transaction of the plan says how the object differed from the record.
		var found bool
		txns, _ := h.state.Transactions()
		for _, tx := range txns {
			for _, c := range tx.Changes {
				if c.Addr == "c0" && c.Action == "drift" {
					found = true
					if c.Diff == nil || len(c.Diff.Before)+len(c.Diff.After)+len(c.Diff.Withheld) == 0 || !strings.Contains(string(c.Diff.After["content"]), "changed") && !strings.Contains(fmt.Sprint(c.Diff.After), "changed") {
						t.Errorf("the drift of c0 has the diff %+v", c.Diff)
					}
				}
			}
		}
		if !found {
			t.Errorf("no transaction records the drift of c0: %+v", txns)
		}
	})

	t.Run("gone", func(t *testing.T) {
		h := newHarness(t)
		h.apply(chain(1, "1"))
		h.fake.vanish("c0")
		h.fake.resetCalls()
		p := plan(t, h.engine(chain(1, "2"), Options{}))
		expectCalls(t, h, map[string]int{"UpgradeResourceState": 2, "ReadResource": 2})
		if ch := changeOf(t, p, "c0"); ch.Action != ActionCreate {
			t.Errorf("c0 is %s, want create", ch.Action)
		}
		if ch := changeOf(t, p, "top"); ch.Action != ActionUpdate {
			t.Errorf("top is %s, want update", ch.Action)
		}
		if m := h.state.Get("c0"); !m.Gone || m.DriftedAt == nil {
			t.Errorf("the record of c0 after the read: %+v", m)
		}
	})

	// Every record of a chain is read once, one level further up the chain in each
	// round, however long the chain is: nothing flaps, the records are as they
	// are.
	t.Run("a chain of changed records", func(t *testing.T) {
		for _, w := range []struct {
			name   string
			change func(f *fakeProvider, name string)
			action Action
		}{
			{"drifted", func(f *fakeProvider, name string) { f.drift(name, "changed") }, ActionUpdate},
			{"gone", func(f *fakeProvider, name string) { f.vanish(name) }, ActionCreate},
		} {
			for depth := 1; depth <= 6; depth++ {
				t.Run(fmt.Sprintf("%s %d", w.name, depth), func(t *testing.T) {
					h := newHarness(t)
					h.apply(chain(6, "1"))
					var want []string
					for i := 6 - depth; i < 6; i++ {
						w.change(h.fake, fmt.Sprintf("c%d", i))
						want = append(want, fmt.Sprintf("c%d", i))
					}
					h.fake.resetCalls()
					p, err := h.engine(chain(6, "2"), Options{}).Plan(context.Background())
					if err != nil {
						t.Fatalf("plan: %v", err)
					}
					// top reaches the last record of the chain, which reaches the one before
					// it, and so on to the first record that is as it was.
					wantRead := append([]string{}, want...)
					if depth < 6 {
						wantRead = append([]string{fmt.Sprintf("c%d", 5-depth)}, wantRead...)
					}
					wantRead = append(wantRead, "top")
					if got := h.fake.readNames(); !reflect.DeepEqual(got, wantRead) {
						t.Errorf("read %v, want %v: each record once", got, wantRead)
					}
					for _, a := range want {
						if ch := changeOf(t, p, a); ch.Action != w.action {
							t.Errorf("%s is %s, want %s", a, ch.Action, w.action)
						}
						if !h.state.Get(a).Marked() {
							t.Errorf("%s was read and has no mark", a)
						}
					}
					if ch := changeOf(t, p, "top"); ch.Action != ActionUpdate {
						t.Errorf("top is %s, want update", ch.Action)
					}
					// The plan the next run makes is the plan this one made.
					h.fake.resetCalls()
					again := plan(t, h.engine(chain(6, "2"), Options{}))
					for _, ch := range p.Changes {
						if got := changeOf(t, again, ch.Addr).Action; got != ch.Action {
							t.Errorf("%s: the next plan has %s, this one had %s", ch.Addr, got, ch.Action)
						}
					}
				})
			}
		}
	})

	// A record that is found changed in one round and again in a later one keeps
	// changing: the plan fails and names it. A different record in each round is a
	// chain, and the walk goes on.
	t.Run("a record that keeps changing", func(t *testing.T) {
		h := newHarness(t)
		h.apply(chain(5, "1"))
		rounds := func(found ...[]string) (*Plan, error, int) {
			e := h.engine(chain(5, "2"), Options{})
			n := 0
			e.readOnce = func(context.Context) ([]string, error) {
				n++
				if n > len(found) {
					return nil, nil
				}
				return found[n-1], nil
			}
			p, err := e.Plan(context.Background())
			return p, err, n
		}
		if p, err, n := rounds([]string{"c4"}, []string{"c3"}, []string{"c2"}, []string{"c1"}, []string{"c0"}); err != nil || p == nil || n != 6 {
			t.Errorf("five rounds that find a record each: error %v, %d rounds; want a plan after 6", err, n)
		}
		p, err, n := rounds([]string{"c4"}, []string{"c3", "c4"})
		if err == nil || !strings.Contains(err.Error(), "c4 changed while the plan read it; run pravo plan again") || strings.Contains(err.Error(), "c3") {
			t.Fatalf("plan: error = %v", err)
		}
		if p != nil || n != 2 {
			t.Errorf("plan %v after %d rounds; want none, after 2", p, n)
		}
	})
}

// A mark that a read leaves on a node lasts until an apply writes the node. A
// later plan, a saved plan and an apply act on it without reading the node
// again.
func TestDriftSurvivesUntilApplied(t *testing.T) {
	two := spec{
		cue:       "a: " + thing("a", `content: "x"`) + "\nb: " + thing("b", `content: "y"`) + "\n",
		resources: []string{"a", "b"},
	}
	worlds := []struct {
		name   string
		change func(f *fakeProvider)
		action Action
	}{
		{"drift", func(f *fakeProvider) { f.drift("a", "changed") }, ActionUpdate},
		{"gone", func(f *fakeProvider) { f.vanish("a") }, ActionCreate},
	}
	for _, w := range worlds {
		setup := func(t *testing.T) *harness {
			h := newHarness(t)
			h.apply(two)
			w.change(h.fake)
			return h
		}
		// finish checks that the node was written by the apply and carries no
		// mark any more, and that the next plan has nothing to do.
		finish := func(t *testing.T, h *harness, revision int) {
			t.Helper()
			h.fake.restore("a")
			h.reopen()
			a := h.state.Get("a")
			if a.Marked() || a.Revision <= revision {
				t.Errorf("after the apply a is marked %v, revision %d (it was %d)", a.Marked(), a.Revision, revision)
			}
			if p := plan(t, h.engine(two, Options{})); p.HasChanges() {
				t.Errorf("a plan after the apply has changes")
			}
		}
		t.Run(w.name+": plan, then apply", func(t *testing.T) {
			h := setup(t)
			e := h.engine(two, Options{Refresh: RefreshAll})
			p := plan(t, e)
			if ch := changeOf(t, p, "a"); ch.Action != w.action || !ch.Drifted {
				t.Errorf("a is %s, drifted %v; want %s", ch.Action, ch.Drifted, w.action)
			}
			if ch := changeOf(t, p, "b"); ch.Action != ActionNoop {
				t.Errorf("b is %s, want no-op", ch.Action)
			}
			rev := h.revision("a")
			if _, err := e.Apply(context.Background(), p); err != nil {
				t.Fatal(err)
			}
			finish(t, h, rev)
		})
		t.Run(w.name+": plan -out, then apply the file", func(t *testing.T) {
			h := setup(t)
			e := h.engine(two, Options{Refresh: RefreshAll})
			p := plan(t, e)
			file := t.TempDir() + "/a.plan"
			if err := e.SavePlan(file, p, nil, false); err != nil {
				t.Fatal(err)
			}
			rev := h.revision("a")
			pf, err := ReadPlanFile(file)
			if err != nil {
				t.Fatal(err)
			}
			// A command that applies a file does not read the nodes before it writes them.
			e = h.engine(two, Options{Refresh: RefreshNone, NoReadBack: true})
			loaded, err := e.LoadPlan(pf)
			if err != nil {
				t.Fatal(err)
			}
			h.fake.resetCalls()
			if _, err := e.ApplySaved(context.Background(), loaded); err != nil {
				t.Fatal(err)
			}
			expectCalls(t, h, map[string]int{"ReadResource": 0, "ApplyResourceChange": 1})
			finish(t, h, rev)
		})
		t.Run(w.name+": drift, then plan", func(t *testing.T) {
			h := setup(t)
			p := plan(t, h.engine(two, Options{Refresh: RefreshAll}))
			if ch := changeOf(t, p, "a"); !ch.Drifted {
				t.Fatal("-refresh=all did not find the drift")
			}
			// The world is as the state records it now, or the nodes are not read:
			// the mark alone keeps a in the plan.
			h.fake.resetCalls()
			p = plan(t, h.engine(two, Options{Refresh: RefreshNone}))
			if ch := changeOf(t, p, "a"); ch.Action != w.action || !ch.Drifted {
				t.Errorf("a is %s, drifted %v; want %s with the drift", ch.Action, ch.Drifted, w.action)
			}
			if ch := changeOf(t, p, "b"); ch.Action != ActionNoop {
				t.Errorf("b is %s, want no-op", ch.Action)
			}
			expectCalls(t, h, map[string]int{"ReadResource": 0})
		})
		t.Run(w.name+": drift, then apply", func(t *testing.T) {
			h := setup(t)
			plan(t, h.engine(two, Options{Refresh: RefreshAll}))
			rev := h.revision("a")
			e := h.engine(two, Options{Refresh: RefreshNone})
			p := plan(t, e)
			if _, err := e.Apply(context.Background(), p); err != nil {
				t.Fatal(err)
			}
			finish(t, h, rev)
		})
	}
}

// An apply that stops halfway leaves the state the provider reached, and the
// record does not say that the configuration was applied.
func TestPartialApplyIsPlannedAgain(t *testing.T) {
	s := spec{cue: "a: " + thing("a", `content: "x"`) + "\n", resources: []string{"a"}}
	h := newHarness(t)
	h.fake.partial = map[string]bool{"a": true}
	e := h.engine(s, Options{})
	p := plan(t, e)
	if _, err := e.Apply(context.Background(), p); err == nil {
		t.Fatal("the apply of a provider that fails succeeded")
	}
	a := h.state.Get("a")
	if a == nil || a.ConfigHash != "" || a.AppliedAt == nil {
		t.Fatalf("the record after the failed apply: %+v", a)
	}

	h.fake.partial = nil
	for _, mode := range []RefreshMode{RefreshAge, RefreshNone} {
		p = plan(t, h.engine(s, Options{Refresh: mode}))
		ch := changeOf(t, p, "a")
		if ch.Action != ActionUpdate || ch.Skipped {
			t.Errorf("mode %d: a is %s, skipped %v; want an update", mode, ch.Action, ch.Skipped)
		}
		if !hasReason(ch.Reasons, "the last apply of it did not finish") {
			t.Errorf("mode %d: reasons %q lack the unfinished apply", mode, ch.Reasons)
		}
	}
	e = h.engine(s, Options{})
	if _, err := e.Apply(context.Background(), plan(t, e)); err != nil {
		t.Fatal(err)
	}
	if a := h.state.Get("a"); a.ConfigHash == "" {
		t.Error("the record is still unfinished after an apply that finished")
	}
	if p := plan(t, h.engine(s, Options{})); p.HasChanges() {
		t.Error("a plan after the finished apply has changes")
	}
}

// containerSpec is a container of two resources, a and b, and a resource that
// joins their ids. The extra text is added to a.
func containerSpec(extra string) spec {
	return spec{
		cue: "import \"strings\"\n" +
			"pets: {a: " + thing("a", extra) + ", b: " + thing("b", "") + "}\n" +
			"all: " + thing("all", `content: strings.Join([for k, v in pets {v.id}], ",")`) + "\n",
		resources: []string{"pets.a", "pets.b", "all"},
	}
}

// What a default plan decides about the nodes it reaches is what a plan that
// reads every record past its age decides, when nothing drifted. A record that
// drifted where the default plan does not look is found only by the second.
func TestDefaultPlanAgreesWithExpiredPlan(t *testing.T) {
	cases := []struct {
		name          string
		apply, change spec
		unreached     string
	}{
		{"chain", chain(4, "1"), chain(4, "2"), "other"},
		{"container", containerSpec(""), containerSpec(`content: "changed"`), "pets.b"},
		{"independent", manySpec(6, func(int) string { return "v1" }), manySpec(6, func(i int) string {
			if i == 2 {
				return "v2"
			}
			return "v1"
		}), "r005"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := newHarness(t)
			h.apply(c.apply)
			age := plan(t, h.engine(c.change, Options{}))
			expired := plan(t, h.engine(c.change, Options{Refresh: RefreshExpired}))
			if !age.HasChanges() {
				t.Fatal("the fixture changes nothing")
			}
			var got, want []string
			for _, ch := range age.Changes {
				if !ch.Skipped {
					got = append(got, describe(ch))
				}
			}
			for _, ch := range expired.Changes {
				if !ch.Skipped {
					want = append(want, describe(ch))
				}
			}
			if !reflect.DeepEqual(got, want) {
				t.Errorf("the default plan and the expired plan differ:\n%s\n---\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
			}

			// Drift where the default plan does not look.
			sr := h.state.Get(c.unreached)
			if sr == nil {
				t.Fatalf("%s is not in the state", c.unreached)
			}
			name := nameOf(t, sr.Attributes)
			h.fake.drift(name, "elsewhere")
			h.fake.resetCalls()
			again := plan(t, h.engine(c.change, Options{}))
			if ch := changeOf(t, again, c.unreached); ch.Action != ActionNoop || ch.Drifted {
				t.Errorf("the default plan reports %s, drifted %v for %s", ch.Action, ch.Drifted, c.unreached)
			}
			for _, r := range h.fake.readNames() {
				if r == name {
					t.Errorf("the default plan read %s", name)
				}
			}
			found := plan(t, h.engine(c.change, Options{Refresh: RefreshExpired}))
			if ch := changeOf(t, found, c.unreached); ch.Action != ActionUpdate || !ch.Drifted {
				t.Errorf("the expired plan reports %s, drifted %v for %s", ch.Action, ch.Drifted, c.unreached)
			}
		})
	}
}

// describe writes the decision of a plan about a node: the action, the planned
// values and the reasons.
func describe(ch *Change) string {
	return fmt.Sprintf("%s %s %v %q", ch.Addr, ch.Action, ToData(ch.Planned), ch.Reasons)
}

func nameOf(t *testing.T, attrs []byte) string {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(attrs, &m); err != nil {
		t.Fatal(err)
	}
	name, _ := m["name"].(string)
	return name
}

// The values of a node that the plan does not look at are unified into the
// configuration only when something reads them: another node, or an output.
func TestUnreadNodesAreFilledOnlyWhenRead(t *testing.T) {
	src := func(version string) spec {
		return spec{
			cue: "a: " + thing("a", "") + "\nb: " + thing("b", "") + "\n" +
				"c: " + thing("c", `content: "\(b.id)-`+version+`"`) + "\n" +
				"d: " + thing("d", "") + "\n" +
				"out: {value: d.id}\n",
			resources: []string{"a", "b", "c", "d"},
			outputs:   []string{"out"},
		}
	}
	h := newHarness(t)
	h.apply(src("1"))
	e := h.engine(src("2"), Options{})
	p := plan(t, e)
	if want := (Stats{Total: 4, Reached: 1, Skipped: 3, Refreshed: 2, NotRead: 2}); p.Stats != want {
		t.Errorf("stats = %+v, want %+v", p.Stats, want)
	}
	// c reads b: its configuration is known, and it is an update.
	c := changeOf(t, p, "c")
	if c.Action != ActionUpdate || c.ConfigHash == "" {
		t.Errorf("c is %s with configuration hash %q; it reads b, which is recorded", c.Action, c.ConfigHash)
	}
	if want := recordedID(t, h, "b") + "-2"; !strings.Contains(describe(c), want) {
		t.Errorf("the plan of c lacks %q: %s", want, describe(c))
	}
	// The output reads d.
	if got, err := outputJSON(p.Outputs["out"]); err != nil || string(got) != `"`+recordedID(t, h, "d")+`"` {
		t.Errorf("out = %s (%v), want the id of d", got, err)
	}
	// Nothing reads a.
	concrete := func(addr string) bool {
		e.cueMu.Lock()
		defer e.cueMu.Unlock()
		return convert.IsConcrete(e.current.LookupPath(cue.ParsePath(addr + ".id")))
	}
	for addr, want := range map[string]bool{"a": false, "b": true, "d": true} {
		if got := concrete(addr); got != want {
			t.Errorf("the id of %s is unified into the configuration: %v, want %v", addr, got, want)
		}
	}
}
