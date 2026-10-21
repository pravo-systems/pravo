package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"cuelang.org/go/cue"
	"cuelang.org/go/cue/cuecontext"

	"pravo.dev/pravo/internal/config"
)

// within fails the test when f has not returned after a while: a plan that
// waits for a task that never becomes ready would otherwise hang the suite.
func within(t *testing.T, f func()) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		defer close(done)
		f()
	}()
	select {
	case <-done:
	case <-time.After(20 * time.Second):
		t.Fatal("did not return: the run is waiting for a task that cannot become ready")
	}
}

func cycleSpec() spec {
	return spec{
		cue: "a: " + thing("a", "content: \"x\\(b.id)\"") + "\n" +
			"b: " + thing("b", "content: \"y\\(c.id)\"") + "\n" +
			"c: " + thing("c", "content: \"z\\(a.id)\"") + "\n" +
			"behind: " + thing("behind", "content: a.id") + "\n" +
			"free: " + thing("free", ""),
		resources: []string{"a", "b", "c", "behind", "free"},
	}
}

func TestCycleIsReportedByPlanApplyAndDestroy(t *testing.T) {
	const want = "dependency cycle between a, b, c"
	h := newHarness(t)
	within(t, func() {
		if _, err := h.engine(cycleSpec(), Options{}).Plan(context.Background()); err == nil || err.Error() != want {
			t.Errorf("plan: error = %v, want %q", err, want)
		} else if coded := new(*CodedError); !errors.As(err, coded) || (*coded).Code != ExitInvalid {
			t.Errorf("plan: error %v has no exit code %d", err, ExitInvalid)
		}
		e := h.engine(cycleSpec(), Options{})
		if _, err := e.Apply(context.Background(), &Plan{}); err == nil || err.Error() != want {
			t.Errorf("apply: error = %v, want %q", err, want)
		}
		if _, err := h.engine(cycleSpec(), Options{Destroy: true}).Plan(context.Background()); err == nil || err.Error() != want {
			t.Errorf("destroy: error = %v, want %q", err, want)
		}
	})
	if h.state.Len() != 0 {
		t.Errorf("a refused run left %d records in the state", h.state.Len())
	}
}

func TestCheckGraphNeedsNoStateOrProviders(t *testing.T) {
	s := cycleSpec()
	ctx := cuecontext.New()
	root := &config.Root{Ctx: ctx}
	for _, addr := range s.resources {
		root.Resources = append(root.Resources, &config.Resource{Mode: "managed", Type: "fake_thing", Name: addr, Path: cue.ParsePath(addr), ProviderKey: "fake"})
	}
	v := ctx.CompileString(s.cue)
	within(t, func() {
		if err := CheckGraph(root, v); err == nil || err.Error() != "dependency cycle between a, b, c" {
			t.Errorf("CheckGraph = %v", err)
		}
	})
	ok := ctx.CompileString("a: " + thing("a", "") + "\nb: " + thing("b", "content: a.id"))
	root.Resources = root.Resources[:2]
	if err := CheckGraph(root, ok); err != nil {
		t.Errorf("CheckGraph of an acyclic specification = %v", err)
	}
}

func TestCheckCyclesNamesOnlyTheTasksOnCycles(t *testing.T) {
	mk := func(edges map[string][]string) map[string]*node {
		nodes := map[string]*node{}
		for a := range edges {
			nodes[a] = &node{res: &config.Resource{Path: cue.ParsePath(a)}, deps: map[string]bool{}}
		}
		for a, ds := range edges {
			for _, d := range ds {
				nodes[a].deps[d] = true
				nodes[d].dependents = append(nodes[d].dependents, nodes[a])
			}
		}
		return nodes
	}
	cases := []struct {
		name  string
		edges map[string][]string
		want  string
	}{
		{"none", map[string][]string{"a": nil, "b": {"a"}, "c": {"a", "b"}}, ""},
		{"two", map[string][]string{"a": {"b"}, "b": {"a"}}, "dependency cycle between a, b"},
		{"behind", map[string][]string{"a": {"c"}, "b": {"a"}, "c": {"b"}, "d": {"c"}, "e": nil}, "dependency cycle between a, b, c"},
		{"two cycles", map[string][]string{"a": {"b"}, "b": {"a"}, "x": {"y"}, "y": {"z"}, "z": {"x"}, "m": {"a", "x"}},
			"dependency cycle between a, b; dependency cycle between x, y, z"},
	}
	for _, c := range cases {
		err := checkCycles(mk(c.edges))
		switch {
		case c.want == "" && err != nil:
			t.Errorf("%s: unexpected error %v", c.name, err)
		case c.want != "" && (err == nil || err.Error() != c.want):
			t.Errorf("%s: error = %v, want %q", c.name, err, c.want)
		}
	}
}

// Tasks that run in parallel record their changes in maps that the others
// read. The race detector finds a read without the lock.
func TestParallelTasksShareTheirMapsSafely(t *testing.T) {
	var cueSrc strings.Builder
	var names []string
	for i := 0; i < 40; i++ {
		name := fmt.Sprintf("r%02d", i)
		names = append(names, name)
		extra := ""
		if i > 0 {
			extra = fmt.Sprintf("content: \"after \\(r%02d.id)\"", i/3)
		}
		fmt.Fprintf(&cueSrc, "%s: %s\n", name, thing(name, extra))
	}
	s := spec{cue: cueSrc.String(), resources: names}
	h := newHarness(t)
	res := h.apply(s)
	if res.Added != 40 {
		t.Errorf("added %d, want 40", res.Added)
	}
	e := h.engine(s, Options{})
	plan, err := e.Plan(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if plan.HasChanges() {
		t.Errorf("a second plan found changes")
	}
}

func TestProviderIsConfiguredOnceUnderConcurrentUse(t *testing.T) {
	h := newHarness(t)
	e := h.engine(spec{cue: "a: " + thing("a", ""), resources: []string{"a"}}, Options{})
	m := e.Providers
	for i := 0; i < 8; i++ {
		key := fmt.Sprintf("fake.k%d", i)
		m.instances[key] = &instance{p: h.fake, key: key}
	}
	var wg sync.WaitGroup
	for rep := 0; rep < 4; rep++ {
		for i := 0; i < 8; i++ {
			wg.Add(1)
			go func(key string) {
				defer wg.Done()
				if _, err := m.Provider(context.Background(), key); err != nil {
					t.Error(err)
				}
			}(fmt.Sprintf("fake.k%d", i))
		}
	}
	wg.Wait()
}

func partialSpec() spec {
	return spec{
		cue: "a: " + thing("a", "") + "\n" +
			"b: " + thing("b", "") + "\n" +
			"mine: {value: a.id}\n" +
			"url: {value: \"https://\\(b.id)\"}\n" +
			"both: {value: {first: a.id, second: b.id}}\n",
		resources: []string{"a", "b"},
		outputs:   []string{"both", "mine", "url"},
	}
}

func TestPartialApplySkipsOutputsThatWaitForTheRestOfThePlan(t *testing.T) {
	h := newHarness(t)
	s := partialSpec()
	e := h.engine(s, Options{})
	plan, err := e.Plan(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(t.TempDir(), "p.plan")
	if err := e.SavePlan(file, plan, nil, false); err != nil {
		t.Fatal(err)
	}
	pf, err := ReadPlanFile(file)
	if err != nil {
		t.Fatal(err)
	}

	// Only a: the outputs that read b wait.
	e = h.engine(s, Options{Refresh: RefreshNone})
	loaded, err := e.LoadPlan(pf)
	if err != nil {
		t.Fatal(err)
	}
	sel, err := loaded.Subset([]string{"a"})
	if err != nil {
		t.Fatal(err)
	}
	if !sel.Partial {
		t.Fatal("a selection that leaves b out is not marked partial")
	}
	res, err := e.ApplySaved(context.Background(), sel)
	if err != nil {
		t.Fatalf("partial apply: %v", err)
	}
	if got := keys(res.Outputs); got != "mine" {
		t.Errorf("outputs of the partial apply = %s, want mine", got)
	}
	if got := keys(h.state.Outputs()); got != "mine" {
		t.Errorf("outputs in the state = %s, want mine", got)
	}
	if err := MarkApplied(file, pf, []string{"a"}, res.Txn); err != nil {
		t.Fatal(err)
	}

	// The rest of the plan: every output is concrete and recorded.
	pf, err = ReadPlanFile(file)
	if err != nil {
		t.Fatal(err)
	}
	e = h.engine(s, Options{Refresh: RefreshNone})
	loaded, err = e.LoadPlan(pf)
	if err != nil {
		t.Fatalf("the plan is stale after a partial apply: %v", err)
	}
	rest := loaded.Remaining()
	res, err = e.ApplySaved(context.Background(), rest)
	if err != nil {
		t.Fatalf("apply of the rest: %v", err)
	}
	if got := keys(res.Outputs); got != "both mine url" {
		t.Errorf("outputs after the rest = %s, want both mine url", got)
	}
	if got := keys(h.state.Outputs()); got != "both mine url" {
		t.Errorf("outputs in the state = %s", got)
	}
}

// A whole apply that leaves an output without a value is an error.
func TestFullApplyStillRequiresConcreteOutputs(t *testing.T) {
	h := newHarness(t)
	s := spec{
		cue:       "a: " + thing("a", "") + "\nloop: {value: {a: a.id}}\n",
		resources: []string{"a"},
		outputs:   []string{"loop"},
	}
	e := h.engine(s, Options{})
	plan, err := e.Plan(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.Apply(context.Background(), plan); err == nil || !strings.Contains(err.Error(), "output loop is not concrete after apply") {
		t.Errorf("apply: error = %v", err)
	}
}

func TestSubsetIsPartialOnlyWhenChangesAreLeftOut(t *testing.T) {
	plan := &Plan{Changes: []*Change{
		{Addr: "a", Action: ActionCreate},
		{Addr: "b", Action: ActionCreate},
		{Addr: "c", Action: ActionNoop, Skipped: true},
		{Addr: "d", Action: ActionCreate, Applied: true},
	}}
	if sub, err := plan.Subset([]string{"a"}); err != nil || !sub.Partial {
		t.Errorf("a alone: partial = %v, err = %v", sub != nil && sub.Partial, err)
	}
	if sub, err := plan.Subset([]string{"a", "b"}); err != nil || sub.Partial {
		t.Errorf("a and b leave nothing pending out: partial = %v, err = %v", sub != nil && sub.Partial, err)
	}
	if plan.Partial {
		t.Error("the plan itself is not partial")
	}
}

func keys(m map[string]json.RawMessage) string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	for i := range out {
		for j := i + 1; j < len(out); j++ {
			if out[j] < out[i] {
				out[i], out[j] = out[j], out[i]
			}
		}
	}
	return strings.Join(out, " ")
}

func TestOutputStructKeysAreSorted(t *testing.T) {
	ctx := cuecontext.New()
	v := ctx.CompileString(`{zulu: "z", alpha: {y: 1.0, b: [{k: 2, a: 1}], x: "<&>"}, mike: 3}`)
	b, err := outputJSON(v)
	if err != nil {
		t.Fatal(err)
	}
	const want = `{"alpha":{"b":[{"a":1,"k":2}],"x":"<&>","y":1.0},"mike":3,"zulu":"z"}`
	if string(b) != want {
		t.Errorf("outputJSON = %s\nwant       %s", b, want)
	}
	c := canonicalOutput(v)
	got, err := c.MarshalJSON()
	if err != nil || string(got) != `{"alpha":{"b":[{"a":1,"k":2}],"x":"<&>","y":1.0},"mike":3,"zulu":"z"}` {
		t.Errorf("canonicalOutput = %s, %v", got, err)
	}
	var fields []string
	it, _ := c.Fields()
	for it.Next() {
		fields = append(fields, it.Selector().String())
	}
	if strings.Join(fields, ",") != "alpha,mike,zulu" {
		t.Errorf("fields of canonicalOutput = %v", fields)
	}
	// A value that is not concrete yet stays as it is.
	open := ctx.CompileString(`{b: string, a: 1}`)
	if got := canonicalOutput(open); got.Kind() != cue.StructKind {
		t.Errorf("an open value changed kind: %v", got)
	}
}

// Fields that arrive from parallel tasks in any order are recorded in one.
func TestAppliedStructOutputIsRecordedInKeyOrder(t *testing.T) {
	var cueSrc strings.Builder
	var names []string
	for _, n := range []string{"zulu", "alpha", "mike", "bravo", "yankee", "charlie", "x", "y", "w", "v"} {
		names = append(names, "t_"+n)
		fmt.Fprintf(&cueSrc, "t_%s: %s\n", n, thing(n, ""))
	}
	cueSrc.WriteString("ids: {value: {for _, n in [\"zulu\", \"alpha\", \"mike\", \"bravo\", \"yankee\", \"charlie\", \"x\", \"y\", \"w\", \"v\"] {(n): t_zulu.id}}}\n")
	_ = names
	s := spec{cue: cueSrc.String(), resources: names, outputs: []string{"ids"}}
	h := newHarness(t)
	res := h.apply(s)
	raw, _ := UnwrapOutput(res.Outputs["ids"])
	var order []string
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	if _, err := dec.Token(); err != nil {
		t.Fatal(err)
	}
	for dec.More() {
		k, _ := dec.Token()
		order = append(order, k.(string))
		var skip string
		_ = dec.Decode(&skip)
	}
	if got, want := strings.Join(order, ","), "alpha,bravo,charlie,mike,v,w,x,y,yankee,zulu"; got != want {
		t.Errorf("recorded key order = %s, want %s", got, want)
	}
}

func preventSpec(withKeep bool) spec {
	src := "other: " + thing("other", "") + "\n"
	res := []string{"other"}
	if withKeep {
		src += "keep: " + thing("keep", "$lifecycle: prevent_destroy: true") + "\n"
		res = append(res, "keep")
	}
	return spec{cue: src, resources: res}
}

func TestDestroyRefusesAResourceThatPreventsIt(t *testing.T) {
	h := newHarness(t)
	h.apply(preventSpec(true))
	if h.state.Len() != 2 {
		t.Fatalf("state holds %d records", h.state.Len())
	}

	// The plan of a destroy and the destroy itself are refused, and nothing changes.
	e := h.engine(preventSpec(true), Options{Destroy: true})
	_, err := e.Plan(context.Background())
	if err == nil || !strings.Contains(err.Error(), "$lifecycle.prevent_destroy") || !strings.Contains(err.Error(), "keep") || strings.Contains(err.Error(), "other") {
		t.Fatalf("destroy plan: error = %v", err)
	}
	e = h.engine(preventSpec(true), Options{Destroy: true})
	_, err = e.Apply(context.Background(), &Plan{Destroy: true})
	if err == nil || !strings.Contains(err.Error(), "keep") {
		t.Fatalf("destroy apply: error = %v", err)
	}
	if h.state.Len() != 2 || h.state.Get("keep") == nil || h.state.Get("other") == nil {
		t.Errorf("a refused destroy changed the state: %v", h.state.Addresses())
	}

	// A resource that the specification no longer declares has no lifecycle to read.
	e = h.engine(preventSpec(false), Options{Destroy: true})
	plan, err := e.Plan(context.Background())
	if err != nil {
		t.Fatalf("destroy of a specification without keep: %v", err)
	}
	res, err := e.Apply(context.Background(), plan)
	if err != nil {
		t.Fatal(err)
	}
	if res.Destroyed != 2 || h.state.Len() != 0 {
		t.Errorf("destroyed %d, %d records left", res.Destroyed, h.state.Len())
	}
}

func TestPreventDestroyOnlyBindsWhatIsRecorded(t *testing.T) {
	h := newHarness(t)
	h.apply(preventSpec(false))
	// keep is declared with prevent_destroy but has no record: there is nothing to refuse.
	e := h.engine(preventSpec(true), Options{Destroy: true})
	plan, err := e.Plan(context.Background())
	if err != nil {
		t.Fatalf("destroy: %v", err)
	}
	if _, err := e.Apply(context.Background(), plan); err != nil {
		t.Fatal(err)
	}
	if h.state.Len() != 0 {
		t.Errorf("%d records left", h.state.Len())
	}
}

func TestDeletedResourcesSayWhy(t *testing.T) {
	h := newHarness(t)
	h.apply(preventSpec(true))

	plan, err := h.engine(preventSpec(false), Options{}).Plan(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, ch := range plan.Changes {
		if ch.Addr == "keep" {
			found = true
			if ch.Action != ActionDelete || len(ch.Reasons) != 1 || !strings.HasPrefix(ch.Reasons[0], "removed from the specification") {
				t.Errorf("keep: %s %q", ch.Action, ch.Reasons)
			}
		}
	}
	if !found {
		t.Fatal("keep is not in the plan")
	}

	// A destroy plan of a specification that is not protected gives its own reason.
	h2 := newHarness(t)
	h2.apply(preventSpec(false))
	plan, err = h2.engine(preventSpec(false), Options{Destroy: true}).Plan(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, ch := range plan.Changes {
		if ch.Action != ActionDelete || len(ch.Reasons) != 1 || !strings.HasPrefix(ch.Reasons[0], "destroy requested") {
			t.Errorf("%s: %s %q", ch.Addr, ch.Action, ch.Reasons)
		}
	}
	if len(plan.Changes) != 1 {
		t.Errorf("%d changes in the destroy plan", len(plan.Changes))
	}
}

func TestReplaceContinuesTheRevision(t *testing.T) {
	for _, cbd := range []bool{false, true} {
		t.Run(fmt.Sprintf("create_before_destroy=%v", cbd), func(t *testing.T) {
			extra := ""
			if cbd {
				extra = "$lifecycle: create_before_destroy: true"
			}
			// tail reads the id of pet: a plan that changes tail reads the record of pet.
			mk := func(name, suffix string) spec {
				return spec{
					cue:       "pet: " + thing(name, extra) + "\ntail: " + thing("tail", `content: "\(pet.id)-`+suffix+`"`) + "\n",
					resources: []string{"pet", "tail"},
				}
			}
			h := newHarness(t)
			h.apply(mk("one", "a"))
			if got := h.revision("pet"); got != 1 {
				t.Fatalf("revision after create = %d", got)
			}

			// A plan made now expects revision 1 of pet.
			e := h.engine(mk("one", "b"), Options{})
			plan, err := e.Plan(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if got := plan.Inputs["pet"]; got != 1 {
				t.Fatalf("the plan expects pet at revision %d, want 1 (inputs %v)", got, plan.Inputs)
			}
			file := filepath.Join(t.TempDir(), "p.plan")
			if err := e.SavePlan(file, plan, nil, false); err != nil {
				t.Fatal(err)
			}

			// Another apply replaces the node: the revision goes on.
			h.apply(mk("two", "a"))
			if got := h.revision("pet"); got != 2 {
				t.Errorf("revision after replace = %d, want 2", got)
			}
			pf, err := ReadPlanFile(file)
			if err != nil {
				t.Fatal(err)
			}
			e = h.engine(mk("two", "a"), Options{})
			pf.ConfigFingerprint, _ = e.Root.Fingerprint() // leave only the revision to judge
			_, err = e.LoadPlan(pf)
			var stale *ErrStalePlan
			if !asStale(err, &stale) || !strings.Contains(strings.Join(stale.Reasons, ";"), "pet changed in state (revision 2, plan expected 1); the plan reads it") {
				t.Errorf("the plan made against revision 1 is not stale after a replace: %v", err)
			}

			// The log of the transaction reads r1 -> r2.
			txns, err := h.state.Transactions()
			if err != nil {
				t.Fatal(err)
			}
			var line string
			for _, tx := range txns {
				for _, c := range tx.Changes {
					if c.Action == "replace" {
						line = fmt.Sprintf("%s r%d -> r%d", c.Addr, c.Before, c.After)
					}
				}
			}
			if line != "pet r1 -> r2" {
				t.Errorf("replace logged as %q, want pet r1 -> r2", line)
			}
		})
	}
}

func asStale(err error, target **ErrStalePlan) bool {
	s, ok := err.(*ErrStalePlan)
	if ok {
		*target = s
	}
	return ok
}

func TestStalePlanHasSnakeCaseKeys(t *testing.T) {
	b, err := json.Marshal(map[string]any{"stale": &ErrStalePlan{Reasons: []string{"x changed"}}})
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != `{"stale":{"reasons":["x changed"]}}` {
		t.Errorf("stale plan marshals as %s", b)
	}
}
