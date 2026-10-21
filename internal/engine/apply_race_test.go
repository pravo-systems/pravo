package engine

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/pravo-systems/pravo/internal/state"
)

// engineOn returns an engine for s that works on the store st, as a command of
// another process does.
func (h *harness) engineOn(st *state.Store, s spec, opts Options) *Engine {
	h.t.Helper()
	was := h.state
	h.state = st
	defer func() { h.state = was }()
	return h.engine(s, opts)
}

// process opens the state again and returns the store: a store that another
// command holds, with the records as they are now.
func (h *harness) process() *state.Store {
	h.t.Helper()
	st, err := state.OpenBackend(context.Background(), h.backend)
	if err != nil {
		h.t.Fatal(err)
	}
	return st
}

// dataEstate is a data source, a resource that reads it and two resources that
// nothing reads. The suffixes are part of the content of w, u and z.
func dataEstate(w, u, z string) spec {
	return spec{
		cue: "src: " + source("s") + "\n" +
			"w: " + thing("w", `content: "\(src.content)-`+w+`"`) + "\n" +
			"u: " + thing("u", fmt.Sprintf("content: %q", u)) + "\n" +
			"z: " + thing("z", fmt.Sprintf("content: %q", z)) + "\n",
		resources: []string{"w", "u", "z"},
		data:      []string{"src"},
	}
}

// A data source that was read and returned what its record holds is not a
// write. An apply that changes nothing else takes no lock, writes no record and
// leaves no transaction, however often it runs, and the revision of the record
// stays, so a saved plan that reads the data source stays current.
func TestUnchangedDataReadIsNotAWrite(t *testing.T) {
	t.Run("an apply that changes nothing", func(t *testing.T) {
		h := newHarness(t)
		h.apply(dataEstate("1", "1", "1"))
		if got := h.revision("src"); got != 1 {
			t.Fatalf("src is at revision %d after the first apply", got)
		}
		txns, _ := h.state.Transactions()
		h.backend.Reset()
		h.fake.resetCalls()

		for i := 0; i < 3; i++ {
			e := h.engine(dataEstate("1", "1", "1"), Options{})
			p := plan(t, e)
			if p.NeedsApply() || len(p.Write) != 0 {
				t.Fatalf("round %d: the plan writes %v", i, p.Write)
			}
			res := apply(t, e, p)
			if res.Txn != "" {
				t.Errorf("round %d: the apply recorded the transaction %s", i, res.Txn)
			}
		}
		if got := h.fake.called("ReadDataSource"); got != 3 {
			t.Errorf("the data source was read %d times, want once for each plan", got)
		}
		if w := h.backend.Writes(); w != 0 {
			t.Errorf("three no-change applies wrote %d objects", w)
		}
		if got := h.revision("src"); got != 1 {
			t.Errorf("src went to revision %d", got)
		}
		after, _ := h.state.Transactions()
		if len(after) != len(txns) {
			t.Errorf("%d transactions after the applies, %d before", len(after), len(txns))
		}
	})

	t.Run("an apply of a node that does not read it", func(t *testing.T) {
		h := newHarness(t)
		h.apply(dataEstate("1", "1", "1"))

		e := h.engine(dataEstate("1", "2", "1"), Options{})
		p := plan(t, e)
		if want := []string{"u"}; !reflect.DeepEqual(p.Write, want) {
			t.Fatalf("write %v, want %v", p.Write, want)
		}
		h.backend.Reset()
		apply(t, e, p)
		if got := h.backend.Count("PutIfAbsent", "locks"); got != 1 {
			t.Errorf("%d locks were taken, want the one of u", got)
		}
		if got := h.backend.Count("Put", "resources"); got != 1 {
			t.Errorf("%d records were written, want u", got)
		}
		if got := h.revision("src"); got != 1 {
			t.Errorf("src went to revision %d", got)
		}
	})

	t.Run("a saved plan that reads it survives other applies", func(t *testing.T) {
		h := newHarness(t)
		h.apply(dataEstate("1", "1", "1"))
		s := dataEstate("2", "1", "1")
		_, pf := planned(t, h, s)
		if want := map[string]int{"src": 1}; !reflect.DeepEqual(pf.Inputs, want) {
			t.Fatalf("inputs %v, want %v", pf.Inputs, want)
		}

		h.apply(dataEstate("1", "1", "1")) // nothing to change
		h.apply(dataEstate("1", "2", "1")) // u
		if got := staleReasons(t, h, s, pf); got != nil {
			t.Fatalf("the plan is stale after applies that did not touch what it reads: %v", got)
		}
		e := h.engine(s, Options{Refresh: RefreshNone})
		loaded, err := e.LoadPlan(pf)
		if err != nil {
			t.Fatal(err)
		}
		h.fake.resetCalls()
		if _, err := e.ApplySaved(context.Background(), loaded); err != nil {
			t.Fatal(err)
		}
		if got := recordedContent(t, h, "w"); got != "from s-2" {
			t.Errorf("w holds %q", got)
		}
		expectCalls(t, h, map[string]int{"ReadDataSource": 0, "ApplyResourceChange": 1})
	})

	t.Run("a lock on it does not reject an apply of another node", func(t *testing.T) {
		h := newHarness(t)
		h.apply(dataEstate("1", "1", "1"))
		other := h.process()
		if err := other.Lock("src", "pravo apply", ""); err != nil {
			t.Fatal(err)
		}
		defer other.Unlock("src")

		e := h.engine(dataEstate("1", "2", "1"), Options{})
		apply(t, e, plan(t, e))
		if got := recordedContent(t, h, "u"); got != "2" {
			t.Errorf("u holds %q", got)
		}
	})

	t.Run("a data source that returned something else is written", func(t *testing.T) {
		h := newHarness(t)
		h.apply(dataEstate("1", "1", "1"))
		changed := dataEstate("1", "1", "1")
		changed.cue = strings.Replace(changed.cue, `name: "s"`, `name: "s2"`, 1)
		e := h.engine(changed, Options{})
		p := plan(t, e)
		if want := []string{"src", "w"}; !reflect.DeepEqual(p.Write, want) {
			t.Fatalf("write %v, want %v", p.Write, want)
		}
		apply(t, e, p)
		if got := h.revision("src"); got != 2 {
			t.Errorf("src is at revision %d, want 2", got)
		}
		if got := recordedContent(t, h, "w"); got != "from s2-1" {
			t.Errorf("w holds %q", got)
		}
	})

	t.Run("a refresh age that is a duration renews the time of the read", func(t *testing.T) {
		h := newHarness(t)
		h.apply(dataEstate("1", "1", "1"))
		old := *h.state.Get("src")
		aged := time.Now().UTC().Add(-2 * time.Hour)
		old.RefreshedAt = &aged
		if err := h.state.Refresh(&old, ""); err != nil {
			t.Fatal(err)
		}
		h.fake.resetCalls()

		opts := Options{DefaultRefresh: "1h"}
		p := plan(t, h.engine(dataEstate("1", "1", "1"), opts))
		if p.NeedsApply() {
			t.Fatalf("the plan writes %v", p.Write)
		}
		expectCalls(t, h, map[string]int{"ReadDataSource": 1})
		if got := h.revision("src"); got != 1 {
			t.Errorf("src went to revision %d", got)
		}
		if at := h.state.Get("src").RefreshedAt; at == nil || time.Since(*at) > time.Minute {
			t.Fatalf("the time of the read is %v", at)
		}
		// Within the age, the next plan does not read it.
		plan(t, h.engine(dataEstate("1", "1", "1"), opts))
		expectCalls(t, h, map[string]int{"ReadDataSource": 1})
	})
}

// A plain apply and an apply of a saved plan stop when another process wrote a
// node that the plan writes or reads after the store of this one was read, and
// they stop before anything is touched.
func TestApplyStopsWhenAnotherProcessWroteFirst(t *testing.T) {
	mk := func(inName, wSuffix, uContent string) spec {
		return spec{
			cue: "in: " + thing(inName, "") + "\n" +
				"w: " + thing("w", `content: "\(in.id)-`+wSuffix+`"`) + "\n" +
				"u: " + thing("u", fmt.Sprintf("content: %q", uContent)) + "\n",
			resources: []string{"in", "w", "u"},
		}
	}
	// intercede is another command that replaces in, and with it writes w.
	intercede := func(t *testing.T, h *harness) {
		t.Helper()
		other := h.engineOn(h.process(), mk("in2", "1", "1"), Options{})
		apply(t, other, plan(t, other))
	}
	check := func(t *testing.T, h *harness, err error, saved bool) {
		t.Helper()
		var coded *CodedError
		if !errors.As(err, &coded) || coded.Code != ExitStale {
			t.Fatalf("apply: error = %v, want exit code 3", err)
		}
		for _, want := range []string{"in changed in state (revision 2, plan expected 1); the plan reads it", "w changed in state (revision 2, plan expected 1)"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("the error does not say %q: %v", want, err)
			}
		}
		if saved != strings.Contains(err.Error(), "saved plan is stale") {
			t.Errorf("a saved plan is stale: %v, error = %v", saved, err)
		}
		// Nothing was applied, not even the node that nothing else touches.
		expectCalls(t, h, map[string]int{"ApplyResourceChange": 0})
		reread := h.process()
		if sr := reread.Get("u"); sr == nil || sr.Revision != 1 {
			t.Errorf("u is %+v after a refused apply", sr)
		}
		if locks, lerr := reread.Locks(); lerr != nil || len(locks) != 0 {
			t.Errorf("locks left behind: %v %v", locks, lerr)
		}
	}

	t.Run("a plain apply", func(t *testing.T) {
		h := newHarness(t)
		h.apply(mk("in", "1", "1"))
		e := h.engineOn(h.process(), mk("in", "2", "2"), Options{})
		p := plan(t, e)
		if want := []string{"u", "w"}; !reflect.DeepEqual(p.Write, want) {
			t.Fatalf("write %v, want %v", p.Write, want)
		}
		intercede(t, h)
		h.fake.resetCalls()
		_, err := e.Apply(context.Background(), p)
		check(t, h, err, false)
	})

	t.Run("a saved plan", func(t *testing.T) {
		h := newHarness(t)
		h.apply(mk("in", "1", "1"))
		s := mk("in", "2", "2")
		_, pf := planned(t, h, s)
		// The file is current when this command loads it. The command applies what
		// is left of it, as pravo apply does.
		e := h.engineOn(h.process(), s, Options{Refresh: RefreshNone})
		loaded, err := e.LoadPlan(pf)
		if err != nil {
			t.Fatal(err)
		}
		intercede(t, h)
		h.fake.resetCalls()
		_, err = e.ApplySaved(context.Background(), loaded.Remaining())
		check(t, h, err, true)
	})

	t.Run("a saved plan, a write to a record it only reads", func(t *testing.T) {
		// in is written and w, which reads it, is not. The part of the plan that is
		// applied lists no input of its own: the file does, and the apply checks
		// the ones it reads once it holds its locks.
		mkIn := func(content, wSuffix string) spec {
			return spec{
				cue: "in: " + thing("in", fmt.Sprintf("content: %q", content)) + "\n" +
					"w: " + thing("w", `content: "\(in.name)-`+wSuffix+`"`) + "\n" +
					"u: " + thing("u", `content: "u"`) + "\n",
				resources: []string{"in", "w", "u"},
			}
		}
		for _, only := range [][]string{nil, {"w"}} {
			t.Run(fmt.Sprintf("only %v", only), func(t *testing.T) {
				h := newHarness(t)
				h.apply(mkIn("a", "1"))
				s := mkIn("a", "2")
				_, pf := planned(t, h, s)
				if want := map[string]int{"in": 1}; !reflect.DeepEqual(pf.Inputs, want) {
					t.Fatalf("inputs %v, want %v", pf.Inputs, want)
				}
				e := h.engineOn(h.process(), s, Options{Refresh: RefreshNone})
				loaded, err := e.LoadPlanFor(pf, only)
				if err != nil {
					t.Fatal(err)
				}
				part := loaded.Remaining()
				if len(only) > 0 {
					if part, err = loaded.Subset(only); err != nil {
						t.Fatal(err)
					}
				}
				// Another command writes in, and not w, while this one waits.
				other := h.engineOn(h.process(), mkIn("b", "1"), Options{})
				apply(t, other, plan(t, other))
				h.fake.resetCalls()
				_, err = e.ApplySaved(context.Background(), part)
				var coded *CodedError
				if !errors.As(err, &coded) || coded.Code != ExitStale || !strings.Contains(err.Error(), "saved plan is stale") ||
					!strings.Contains(err.Error(), "in changed in state (revision 2, plan expected 1); the plan reads it") {
					t.Fatalf("apply: error = %v, want exit code 3 and in, which the plan reads", err)
				}
				expectCalls(t, h, map[string]int{"ApplyResourceChange": 0})
				h.reopen()
				if got := recordedContent(t, h, "w"); got != "in-1" {
					t.Errorf("w holds %q after a refused apply", got)
				}
				if locks, lerr := h.process().Locks(); lerr != nil || len(locks) != 0 {
					t.Errorf("locks left behind: %v %v", locks, lerr)
				}
			})
		}
	})

	t.Run("a saved plan, a write to a record that only the rest of the plan reads", func(t *testing.T) {
		// w reads in and v reads in2. The apply of w alone reads in, so a write to in2
		// does not stop it.
		mk2 := func(in2Content, suffix string) spec {
			return spec{
				cue: "in: " + thing("in", "") + "\n" +
					"in2: " + thing("in2", fmt.Sprintf("content: %q", in2Content)) + "\n" +
					"w: " + thing("w", `content: "\(in.name)-`+suffix+`"`) + "\n" +
					"v: " + thing("v", `content: "\(in2.name)-`+suffix+`"`) + "\n",
				resources: []string{"in", "in2", "w", "v"},
			}
		}
		h := newHarness(t)
		h.apply(mk2("c", "1"))
		s := mk2("c", "2")
		_, pf := planned(t, h, s)
		if len(pf.Inputs) != 2 {
			t.Fatalf("inputs %v, want in and in2", pf.Inputs)
		}
		e := h.engineOn(h.process(), s, Options{Refresh: RefreshNone})
		loaded, err := e.LoadPlanFor(pf, []string{"w"})
		if err != nil {
			t.Fatal(err)
		}
		part, err := loaded.Subset([]string{"w"})
		if err != nil {
			t.Fatal(err)
		}
		// Another command writes in2 only, then the apply of w runs.
		h.apply(mk2("other", "1"))
		if _, err := e.ApplySaved(context.Background(), part); err != nil {
			t.Fatalf("the apply of w stopped for a record it does not read: %v", err)
		}
		h.reopen()
		if got := recordedContent(t, h, "w"); got != "in-2" {
			t.Errorf("w holds %q", got)
		}
	})

	t.Run("a write that is not part of the plan", func(t *testing.T) {
		h := newHarness(t)
		h.apply(mk("in", "1", "1"))
		e := h.engineOn(h.process(), mk("in", "2", "1"), Options{})
		p := plan(t, e)
		if want := []string{"w"}; !reflect.DeepEqual(p.Write, want) {
			t.Fatalf("write %v, want %v", p.Write, want)
		}
		other := h.engineOn(h.process(), mk("in", "1", "2"), Options{})
		apply(t, other, plan(t, other)) // u
		h.fake.resetCalls()
		if _, err := e.Apply(context.Background(), p); err != nil {
			t.Fatalf("an apply that wrote a node this plan has nothing to do with stopped this one: %v", err)
		}
		expectCalls(t, h, map[string]int{"ApplyResourceChange": 1})
	})
}

// A provider that answers differently when the plan is applied stops the apply
// before the node is touched, with exit code 3, even when no record moved.
func TestApplyStopsWhenTheProviderChangedItsAnswer(t *testing.T) {
	mk := func(content string) spec {
		return spec{cue: "w: " + thing("w", fmt.Sprintf("content: %q", content)) + "\n", resources: []string{"w"}}
	}
	h := newHarness(t)
	h.apply(mk("1"))
	e := h.engine(mk("2"), Options{})
	p := plan(t, e)
	h.fake.replan("w", "other")
	h.fake.resetCalls()
	_, err := e.Apply(context.Background(), p)
	var coded *CodedError
	if !errors.As(err, &coded) || coded.Code != ExitStale || !strings.Contains(err.Error(), "w: the plan changed while it waited") {
		t.Fatalf("apply: error = %v, want exit code 3 and the plan changed while it waited", err)
	}
	expectCalls(t, h, map[string]int{"ApplyResourceChange": 0})
	if got := recordedContent(t, h, "w"); got != "1" {
		t.Errorf("w holds %q", got)
	}
}

// A provider that answers differently when a saved plan is applied stops the
// apply before the node is touched, with exit code 3, as it does for the plan
// that was just printed.
func TestSavedApplyStopsWhenTheProviderChangedItsAnswer(t *testing.T) {
	mk := func(content string) spec {
		return spec{cue: "w: " + thing("w", fmt.Sprintf("content: %q", content)) + "\n", resources: []string{"w"}}
	}
	h := newHarness(t)
	h.apply(mk("1"))
	s := mk("2")
	_, pf := planned(t, h, s)
	e := h.engine(s, Options{Refresh: RefreshNone})
	loaded, err := e.LoadPlan(pf)
	if err != nil {
		t.Fatal(err)
	}
	h.fake.replan("w", "other")
	h.fake.resetCalls()
	_, err = e.ApplySaved(context.Background(), loaded.Remaining())
	var coded *CodedError
	if !errors.As(err, &coded) || coded.Code != ExitStale || !strings.Contains(err.Error(), "w: the provider produced a different plan than the saved one") {
		t.Fatalf("apply: error = %v, want exit code 3 and a different plan than the saved one", err)
	}
	expectCalls(t, h, map[string]int{"ApplyResourceChange": 0})
	if got := recordedContent(t, h, "w"); got != "1" {
		t.Errorf("w holds %q", got)
	}
}

// Two applies of different nodes, in two processes, both succeed in a root that
// has a data source and no outputs, and neither rewrites the object of outputs.
func TestConcurrentAppliesOfDifferentNodes(t *testing.T) {
	h := newHarness(t)
	h.apply(dataEstate("1", "0", "0"))
	if got := h.state.Outputs(); len(got) != 0 {
		t.Fatalf("outputs: %v", got)
	}
	h.backend.Reset()

	const rounds = 25
	u, z := "0", "0"
	for i := 1; i <= rounds; i++ {
		nu, nz := fmt.Sprintf("u%d", i), fmt.Sprintf("z%d", i)
		// Each command sees one difference between the specification and the
		// records: the content of u for one, the content of z for the other.
		engines := []*Engine{
			h.engineOn(h.process(), dataEstate("1", nu, z), Options{}),
			h.engineOn(h.process(), dataEstate("1", u, nz), Options{}),
		}
		var (
			wg   sync.WaitGroup
			errs [2]error
		)
		for k, e := range engines {
			wg.Add(1)
			go func() {
				defer wg.Done()
				p, err := e.Plan(context.Background())
				if err == nil {
					_, err = e.Apply(context.Background(), p)
				}
				errs[k] = err
			}()
		}
		wg.Wait()
		for k, err := range errs {
			if err != nil {
				t.Fatalf("round %d, apply %d: %v", i, k, err)
			}
		}
		u, z = nu, nz
	}
	if got, want := recordedContent(t, &harness{state: h.process()}, "u"), u; got != want {
		t.Errorf("u holds %q, want %q", got, want)
	}
	if got := h.backend.Count("Put", "outputs.json"); got != 0 {
		t.Errorf("outputs.json was written %d times by applies that changed no output", got)
	}
}

// The state of a root without outputs gets the object of outputs once, so that
// a root that reads them finds it.
func TestFirstApplyWritesTheOutputsObjectOnce(t *testing.T) {
	h := newHarness(t)
	if _, err := h.backend.Get(context.Background(), "outputs.json"); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("outputs.json before any apply: %v", err)
	}
	h.apply(dataEstate("1", "1", "1"))
	if _, err := h.backend.Get(context.Background(), "outputs.json"); err != nil {
		t.Fatalf("outputs.json after the first apply: %v", err)
	}
	h.backend.Reset()
	h.apply(dataEstate("1", "2", "1"))
	if got := h.backend.Count("Put", "outputs.json"); got != 0 {
		t.Errorf("the second apply wrote outputs.json %d times", got)
	}
}
