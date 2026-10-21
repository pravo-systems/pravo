package engine

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"slices"
	"sort"
	"strings"
	"sync"
	"testing"

	"cuelang.org/go/cue"
	"github.com/hashicorp/terraform-plugin-go/tftypes"

	"github.com/pravo-systems/pravo/internal/config"
)

// walked records the tasks that the apply pass of e starts.
func walked(e *Engine) func() []string {
	var (
		mu  sync.Mutex
		got []string
	)
	e.onTask = func(addr string, apply bool) {
		if apply {
			mu.Lock()
			got = append(got, addr)
			mu.Unlock()
		}
	}
	return func() []string {
		mu.Lock()
		defer mu.Unlock()
		out := append([]string(nil), got...)
		sort.Strings(out)
		return out
	}
}

// converted records the configurations that the apply pass of e converts for
// their providers.
func converted(e *Engine) func() []string {
	var (
		mu  sync.Mutex
		got []string
	)
	e.onConvert = func(addr string, apply bool) {
		if apply {
			mu.Lock()
			got = append(got, addr)
			mu.Unlock()
		}
	}
	return func() []string {
		mu.Lock()
		defer mu.Unlock()
		out := append([]string(nil), got...)
		sort.Strings(out)
		return out
	}
}

func apply(t *testing.T, e *Engine, p *Plan) *ApplyResult {
	t.Helper()
	res, err := e.Apply(context.Background(), p)
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	return res
}

// The apply of a plan uses what the plan read. A node the plan read once is not
// read again, whether it was changed or only read by a node that was.
func TestApplyReusesThePlansReads(t *testing.T) {
	t.Run("a change", func(t *testing.T) {
		const n = 50
		v1 := func(int) string { return "v1" }
		one := func(i int) string {
			if i == 7 {
				return "v2"
			}
			return "v1"
		}
		h := newHarness(t)
		h.apply(manySpec(n, v1))
		h.fake.resetCalls()

		e := h.engine(manySpec(n, one), Options{})
		p := plan(t, e)
		apply(t, e, p)
		expectCalls(t, h, map[string]int{"ReadResource": 1, "UpgradeResourceState": 1, "ApplyResourceChange": 1})
		// The provider validates and plans the node twice: when the plan is made and when it is applied.
		expectCalls(t, h, map[string]int{"ValidateResourceConfig": 2, "PlanResourceChange": 2})
		if got := h.fake.readNames(); !reflect.DeepEqual(got, []string{"r007"}) {
			t.Errorf("read %v, want r007", got)
		}
		if got := recordedContent(t, h, "r007"); got != "v2" {
			t.Errorf("r007 holds %q after the apply", got)
		}
	})

	t.Run("a node that a change reads", func(t *testing.T) {
		h := newHarness(t)
		h.apply(chain(3, "1"))
		h.fake.resetCalls()
		e := h.engine(chain(3, "2"), Options{})
		p := plan(t, e)
		// top reads c2, which reads c1, which reads c0.
		if want := []string{"c0", "c1", "c2"}; !reflect.DeepEqual(keysOf(p.Inputs), want) {
			t.Fatalf("inputs %v, want %v", keysOf(p.Inputs), want)
		}
		apply(t, e, p)
		if got := h.fake.readNames(); !reflect.DeepEqual(got, []string{"c2", "top"}) {
			t.Errorf("read %v, want c2 and top, once each", got)
		}
		expectCalls(t, h, map[string]int{"UpgradeResourceState": 2, "ApplyResourceChange": 1})
	})
}

// A record that the plan found changed in the real world is part of the plan
// that is applied, and it is read once.
func TestApplyOfAPlanThatFoundDrift(t *testing.T) {
	h := newHarness(t)
	h.apply(chain(2, "1"))
	h.fake.drift("c1", "changed")
	h.fake.resetCalls()

	e := h.engine(chain(2, "2"), Options{})
	p := plan(t, e)
	if want := []string{"c1", "top"}; !slices.Equal(p.Write, want) || len(p.Inputs) != 1 || p.Inputs["c0"] != 1 {
		t.Fatalf("write %v, inputs %v: c1 drifted and is planned, c0 is read by c1", p.Write, p.Inputs)
	}
	ran := walked(e)
	apply(t, e, p)
	if got, want := ran(), []string{"c0", "c1", "top"}; !slices.Equal(got, want) {
		t.Errorf("walked %v, want %v", got, want)
	}
	if got := h.fake.readNames(); !slices.Equal(got, []string{"c0", "c1", "top"}) {
		t.Errorf("read %v, want each once", got)
	}
	if got := recordedContent(t, h, "c1"); got != recordedID(t, h, "c0") {
		t.Errorf("c1 holds %q after the apply: the drift is not undone", got)
	}
	if h.state.Get("c1").Marked() {
		t.Error("c1 is marked after the apply wrote it")
	}
}

// A node that is read as its record can have a field that its configuration
// spells as an expression over other nodes, and a node that reads the field reads
// the expression: it is resolved once those nodes are known. The apply walks them
// as inputs too, and a saved plan lists them.
func TestApplyWalksTheInputsOfAnInput(t *testing.T) {
	src := func(tag string) spec {
		return spec{
			cue: "a: " + thing("a", `content: "x"`) + "\n" +
				"b: " + thing("b", `content: "\(a.id)-b"`) + "\n" +
				"c: " + thing("c", `content: "\(b.content)-`+tag+`"`) + "\n" +
				"d: " + thing("d", "") + "\n",
			resources: []string{"a", "b", "c", "d"},
		}
	}
	for _, saved := range []bool{false, true} {
		t.Run(fmt.Sprintf("saved plan %v", saved), func(t *testing.T) {
			h := newHarness(t)
			h.apply(src("1"))
			h.fake.resetCalls()
			e := h.engine(src("2"), Options{})
			p := plan(t, e)
			if !slices.Equal(p.Write, []string{"c"}) || !slices.Equal(keysOf(p.Inputs), []string{"a", "b"}) {
				t.Fatalf("write %v, inputs %v; want c written, b read as a record and a with it", p.Write, keysOf(p.Inputs))
			}
			if saved {
				file := filepath.Join(t.TempDir(), "p.plan")
				if err := e.SavePlan(file, p, nil, false); err != nil {
					t.Fatal(err)
				}
				pf, err := ReadPlanFile(file)
				if err != nil {
					t.Fatal(err)
				}
				e = h.engine(src("2"), Options{Refresh: RefreshNone})
				loaded, err := e.LoadPlan(pf)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := e.ApplySaved(context.Background(), loaded.Remaining()); err != nil {
					t.Fatal(err)
				}
			} else {
				apply(t, e, p)
			}
			if got, want := recordedContent(t, h, "c"), recordedID(t, h, "a")+"-b-2"; got != want {
				t.Errorf("c holds %q, want %q", got, want)
			}
		})
	}
}

// A replacement that was asked for is applied with the nodes that read the
// replaced one, and each is read once.
func TestApplyOfARequestedReplacement(t *testing.T) {
	h := newHarness(t)
	h.apply(chain(2, "1"))
	h.fake.resetCalls()

	e := h.engine(chain(2, "1"), Options{Replace: []string{"c0"}})
	p := plan(t, e)
	if want := []string{"c0", "c1"}; !slices.Equal(p.Write, want) {
		t.Fatalf("write %v, want %v: the replaced node and the one that reads its id", p.Write, want)
	}
	oldID := recordedID(t, h, "c0")
	apply(t, e, p)
	if got := recordedID(t, h, "c0"); got == oldID {
		t.Error("c0 was not replaced")
	}
	if got := recordedContent(t, h, "c1"); got != recordedID(t, h, "c0") {
		t.Errorf("c1 holds %q, want the id of the new c0", got)
	}
	if got := h.fake.readNames(); !slices.Equal(got, []string{"c0", "c1"}) {
		t.Errorf("read %v, want each once", got)
	}
}

func keysOf(m map[string]int) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func envSpec(version string) spec {
	return spec{
		cue: `home: {name: "PRAVO_TEST_UNSET_VARIABLE", default: "dflt", value: string}` + "\n" +
			"w: " + thing("w", `content: "\(home.value)-`+version+`"`) + "\n" +
			"other: " + thing("other", "") + "\n",
		resources: []string{"w", "other"},
		builtins:  []string{"home"},
	}
}

// containerAll is a node that joins the ids of the nodes of a container.
func containerAll(suffix string) spec {
	return spec{
		cue: "import \"strings\"\n" +
			"pets: {a: " + thing("a", "") + ", b: " + thing("b", "") + "}\n" +
			"all: " + thing("all", `content: strings.Join([for k, v in pets {v.id}], ",") + "-`+suffix+`"`) + "\n",
		resources: []string{"pets.a", "pets.b", "all"},
	}
}

func dataSpec(version string) spec {
	return spec{
		cue: "src: " + source("s") + "\n" +
			"w: " + thing("w", `content: "\(src.content)-`+version+`"`) + "\n",
		resources: []string{"w"},
		data:      []string{"src"},
	}
}

// The apply walks the nodes the plan changes, the nodes they read and nothing
// else: a node that is only read as its record is not converted and not sent to
// its provider, and a built-in value runs again.
func TestApplyWalksOnlyReachedNodes(t *testing.T) {
	one := func(i int) string {
		if i == 3 {
			return "v2"
		}
		return "v1"
	}
	cases := []struct {
		name          string
		apply, change spec
		write         []string
		inputs        []string
		support       []string
		age           string
	}{
		{"independent", manySpec(40, func(int) string { return "v1" }), manySpec(40, one), []string{"r003"}, nil, nil, ""},
		{"a node it reads", chain(4, "1"), chain(4, "2"), []string{"top"}, []string{"c0", "c1", "c2", "c3"}, nil, ""},
		{"a built-in value", envSpec("1"), envSpec("2"), []string{"w"}, nil, []string{"home"}, ""},
		{"a data source it reads", dataSpec("1"), dataSpec("2"), []string{"w"}, []string{"src"}, nil, "1h"},
		{"a container it reads", containerAll("1"), containerAll("2"), []string{"all"}, []string{"pets.a", "pets.b"}, nil, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := newHarness(t)
			h.apply(c.apply)
			// With a refresh age, a data source whose record is young is not read again.
			e := h.engine(c.change, Options{DefaultRefresh: c.age})
			p := plan(t, e)
			if !slices.Equal(p.Write, c.write) || !slices.Equal(keysOf(p.Inputs), c.inputs) || !slices.Equal(p.Support, c.support) {
				t.Fatalf("write %v, inputs %v, support %v; want %v, %v, %v", p.Write, keysOf(p.Inputs), p.Support, c.write, c.inputs, c.support)
			}
			ran, conv := walked(e), converted(e)
			h.fake.resetCalls()
			apply(t, e, p)

			want := append(append(append([]string(nil), c.write...), c.inputs...), c.support...)
			sort.Strings(want)
			if got := ran(); !reflect.DeepEqual(got, want) {
				t.Errorf("the apply walked %v, want %v", got, want)
			}
			// A node that is read as its record is not converted.
			if got := conv(); !slices.Equal(got, c.write) {
				t.Errorf("the apply converted %v, want %v: the nodes it writes", got, c.write)
			}
			// Only the nodes it changes reach the provider. A node that the plan read is
			// not upgraded again.
			upgrades := 0
			if c.age != "" {
				upgrades = len(c.write)
			}
			expectCalls(t, h, map[string]int{"PlanResourceChange": len(c.write), "ApplyResourceChange": len(c.write), "UpgradeResourceState": upgrades})
		})
	}
}

// The scope of an apply follows what the nodes read: an ephemeral resource, a
// call or a built-in value runs again together with what it reads, and a node
// that is only read brings what it reads as records, because a field of it can be
// an expression over those nodes.
func TestScopeOfAnApply(t *testing.T) {
	mode := map[string]string{"w": "managed", "in": "managed", "deep": "managed", "far": "managed", "out": "managed",
		"eph": "ephemeral", "call": "call", "env": "builtin", "under": "managed", "d": "data"}
	deps := map[string][]string{
		"w":    {"eph", "in", "d"},
		"eph":  {"call", "under"},
		"call": {"env"},
		"in":   {"deep"},
		"deep": {"far"},
		"out":  {"far"},
	}
	e := &Engine{Root: &config.Root{}, deps: deps, byPath: map[string]*config.Resource{}, graph: map[string]*node{},
		outputDeps: map[string][]string{"o": {"out"}, "q": {"env"}}}
	for addr, m := range mode {
		e.byPath[addr] = &config.Resource{Mode: m, Path: cue.ParsePath(addr)}
	}
	p := &Plan{Changes: []*Change{
		{Addr: "w", Mode: "managed", Action: ActionUpdate},
		{Addr: "skipped", Mode: "managed", Action: ActionNoop, Skipped: true},
		{Addr: "applied", Mode: "managed", Action: ActionUpdate, Applied: true},
		{Addr: "eph", Mode: "ephemeral", Action: ActionOpen},
		{Addr: "orphan", Mode: "managed", Action: ActionDelete},
	}, EvalOutputs: []string{"o"}}
	sc, err := e.scopeFor(p)
	if err != nil {
		t.Fatal(err)
	}
	got := map[role][]string{}
	for _, r := range []role{roleWrite, roleInput, roleSupport} {
		got[r] = sc.addrs(r)
	}
	want := map[role][]string{
		roleWrite:   {"w"},
		roleInput:   {"d", "deep", "far", "in", "out", "under"},
		roleSupport: {"call", "env", "eph"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("scope = %v, want %v", got, want)
	}
	if w := p.writeAddrs(); !reflect.DeepEqual(w, []string{"orphan", "w"}) {
		t.Errorf("write = %v: an orphan is removed, a skipped node and an applied one are not written", w)
	}

	// The graph of the scope: a node waits for what it reads that is in the
	// scope.
	full := map[string]*node{}
	for addr := range mode {
		full[addr] = &node{res: e.byPath[addr], deps: map[string]bool{}}
	}
	for addr, ds := range deps {
		for _, d := range ds {
			full[addr].deps[d] = true
		}
	}
	nodes := sc.nodes(full)
	pending := map[string]int{}
	for addr, n := range nodes {
		pending[addr] = n.pending
	}
	wantPending := map[string]int{"w": 3, "eph": 2, "call": 1, "env": 0, "in": 1, "deep": 1, "far": 0, "d": 0, "out": 1, "under": 0}
	if !reflect.DeepEqual(pending, wantPending) {
		t.Errorf("pending = %v, want %v", pending, wantPending)
	}
	if len(nodes["env"].dependents) != 1 || nodes["env"].dependents[0].res.Address() != "call" {
		t.Errorf("env is waited for by %d nodes", len(nodes["env"].dependents))
	}
}

// A node that is read as its record waits for the nodes of the scope that it
// reads, directly or through nodes that are not in the scope, whatever their
// role: what is filled in from its record has to meet the values they write.
func TestScopeGraphOrdersRecordsAfterWhatTheyRead(t *testing.T) {
	mode := map[string]string{"w": "managed", "s": "ephemeral", "i1": "managed", "i2": "managed", "i3": "managed",
		"i4": "managed", "i5": "managed", "x": "managed", "y": "managed", "z": "managed", "u": "managed"}
	deps := map[string][]string{
		"i1": {"w"},      // reads a node this apply writes
		"i2": {"x"},      // reads w through x, which is not in the scope
		"x":  {"y", "z"}, // y and z are not in the scope either; z reads w
		"z":  {"w"},
		"i3": {"s"},       // reads a node that runs again
		"i4": {"i1", "u"}, // reads another record, and one that the scope does not hold
		"i5": {"y"},       // reads only what is outside the scope
	}
	sc := &applyScope{roles: map[string]role{"w": roleWrite, "s": roleSupport,
		"i1": roleInput, "i2": roleInput, "i3": roleInput, "i4": roleInput, "i5": roleInput}}
	full := map[string]*node{}
	for addr, m := range mode {
		full[addr] = &node{res: &config.Resource{Mode: m, Path: cue.ParsePath(addr)}, deps: map[string]bool{}}
	}
	for addr, ds := range deps {
		for _, d := range ds {
			full[addr].deps[d] = true
		}
	}
	nodes := sc.nodes(full)
	got := map[string][]string{}
	for addr, n := range nodes {
		got[addr] = keysOfSet(n.deps)
		if n.pending != len(n.deps) {
			t.Errorf("%s waits for %d nodes and counts %d", addr, len(n.deps), n.pending)
		}
	}
	want := map[string][]string{"w": {}, "s": {}, "i1": {"w"}, "i2": {"w"}, "i3": {"s"}, "i4": {"i1"}, "i5": {}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("waits for %v, want %v", got, want)
	}
	if n := len(nodes["w"].dependents); n != 2 {
		t.Errorf("w is waited for by %d nodes, want i1 and i2", n)
	}
}

func keysOfSet(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// A part of a plan that holds a node whose dependent is still to be applied,
// when an output reads the dependent. The dependent is read as its record, and
// its configuration reads the node that the part replaces: the record must not
// fill in the old value before the new one is there.
func TestPartialApplyWithAnUnselectedDependent(t *testing.T) {
	for _, viaX := range []bool{false, true} {
		name := "a dependent"
		if viaX {
			name = "a dependent of a dependent"
		}
		t.Run(name, func(t *testing.T) {
			mk := func(version string) spec {
				d := `content: "d-\(b.id)"`
				src := "a: " + thing("a", "") + "\n" +
					"b: " + thing("b-"+version, `content: "\(a.id)"`) + "\n" +
					"c: " + thing("c-"+version, "") + "\n" +
					"e: " + thing("e", "") + "\n"
				resources := []string{"a", "b", "c", "d", "e"}
				if viaX {
					d = `content: "d-\(x.content)"`
					src += "x: " + thing("x", `content: "x-\(b.id)"`) + "\n"
					resources = append(resources, "x")
				}
				src += "d: " + thing("d", d) + "\n" +
					"oa: {value: a.id}\nod: {value: d.content}\noe: {value: e.id}\n"
				return spec{cue: src, resources: resources, outputs: []string{"oa", "od", "oe"}}
			}
			h := newHarness(t)
			h.apply(mk("1"))
			oldB, oldD := recordedID(t, h, "b"), recordedContent(t, h, "d")
			revD := h.revision("d")
			file, pf := planned(t, h, mk("2"))
			if got := changeOf(t, mustLoad(t, h, mk("2"), pf), "d"); got.Action != ActionUpdate {
				t.Fatalf("d is %s in the plan, want update", got.Action)
			}

			// b alone: it is closed, and d and c wait in the file.
			only := []string{"b"}
			e := h.engine(mk("2"), Options{Refresh: RefreshNone})
			loaded, err := e.LoadPlanFor(pf, only)
			if err != nil {
				t.Fatal(err)
			}
			part, err := loaded.Subset(only)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(part.EvalOutputs, []string{"od"}) {
				t.Fatalf("outputs to evaluate: %v, want od, which reads d", part.EvalOutputs)
			}
			ran := walked(e)
			res, err := e.ApplySaved(context.Background(), part)
			if err != nil {
				t.Fatalf("apply of b: %v", err)
			}
			if got := recordedID(t, h, "b"); got == oldB {
				t.Error("b was not replaced")
			}
			if got := h.revision("d"); got != revD || recordedContent(t, h, "d") != oldD {
				t.Errorf("d was written by an apply that did not select it (revision %d, was %d)", got, revD)
			}
			walkedWant := []string{"a", "b", "d"}
			if viaX {
				walkedWant = []string{"a", "b", "d", "x"}
			}
			if got := ran(); !slices.Equal(got, walkedWant) {
				t.Errorf("the apply walked %v, want %v: b, what it reads, the node an output reads and what that reads", got, walkedWant)
			}
			if err := MarkApplied(file, pf, only, res.Txn); err != nil {
				t.Fatal(err)
			}

			// The rest of the plan is current and applies.
			pf, err = ReadPlanFile(file)
			if err != nil {
				t.Fatal(err)
			}
			if got := staleReasons(t, h, mk("2"), pf); got != nil {
				t.Fatalf("the rest of the plan is stale: %q", got)
			}
			e = h.engine(mk("2"), Options{Refresh: RefreshNone})
			loaded, err = e.LoadPlan(pf)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := e.ApplySaved(context.Background(), loaded.Remaining()); err != nil {
				t.Fatalf("apply of the rest: %v", err)
			}
			want := "d-" + recordedID(t, h, "b")
			if viaX {
				want = "d-x-" + recordedID(t, h, "b")
			}
			if got := recordedContent(t, h, "d"); got != want {
				t.Errorf("d holds %q, want %q", got, want)
			}
			if got := string(h.state.Outputs()["od"]); got != `"`+want+`"` {
				t.Errorf("od is %s, want %q", got, want)
			}
			if p := plan(t, h.engine(mk("2"), Options{})); p.HasChanges() || p.NeedsApply() {
				t.Errorf("a plan after both parts has changes %v, needs apply %v", p.HasChanges(), p.NeedsApply())
			}
		})
	}
}

// mustLoad restores the plan of a file without checking it.
func mustLoad(t *testing.T, h *harness, s spec, pf *PlanFile) *Plan {
	t.Helper()
	p, err := h.engine(s, Options{Refresh: RefreshNone}).LoadPlanUnchecked(pf)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// What the apply may do is what the plan said. A node that would be something
// other than the approved change stops the apply before the node is touched, with
// exit code 3 and no change.
func TestApplyExecutesOnlyTheApprovedPlan(t *testing.T) {
	mk := func(inName, suffix string) spec {
		return spec{
			cue: "in: " + thing(inName, "") + "\n" +
				"w: " + thing("w", `content: "\(in.id)-`+suffix+`"`) + "\n" +
				"x: " + thing("x", `content: "x"`) + "\n",
			resources: []string{"in", "w", "x"},
		}
	}
	h := newHarness(t)
	h.apply(mk("in", "1"))

	e := h.engine(mk("in", "2"), Options{})
	p := plan(t, e)
	if want := []string{"w"}; !reflect.DeepEqual(p.Write, want) {
		t.Fatalf("write %v, want %v", p.Write, want)
	}

	// The node that w reads is replaced by another apply while the plan waits for
	// an answer. The other apply writes w as well.
	other := h.engine(mk("in2", "1"), Options{})
	apply(t, other, plan(t, other))
	revision := h.state.Get("w").Revision

	h.fake.resetCalls()
	_, err := e.Apply(context.Background(), p)
	var coded *CodedError
	if !errors.As(err, &coded) || coded.Code != ExitStale || !strings.Contains(err.Error(), "the plan changed while it waited") ||
		!strings.Contains(err.Error(), "w changed in state (revision 2, plan expected 1)") ||
		!strings.Contains(err.Error(), "in changed in state (revision 2, plan expected 1); the plan reads it") {
		t.Fatalf("apply: error = %v, want exit code 3 and the plan changed while it waited, naming in and w", err)
	}
	expectCalls(t, h, map[string]int{"ApplyResourceChange": 0})
	if got := h.state.Get("w").Revision; got != revision {
		t.Errorf("w went from revision %d to %d in an apply that was refused", revision, got)
	}
	if left, err := h.state.Locks(); err != nil || len(left) != 0 {
		t.Errorf("locks left behind: %v %v", left, err)
	}
}

// A scope that lacks a node the apply reads is an error and never a null: the
// conversion in the apply is strict.
func TestApplyRefusesAScopeThatLacksAnInput(t *testing.T) {
	h := newHarness(t)
	h.apply(chain(2, "1"))
	e := h.engine(chain(2, "2"), Options{})
	p := plan(t, e)
	// top reads c1, and c1 reads c0.
	if want := []string{"c0", "c1"}; !reflect.DeepEqual(keysOf(p.Inputs), want) {
		t.Fatalf("inputs %v, want %v", keysOf(p.Inputs), want)
	}
	e.adjustScope = func(sc *applyScope) { delete(sc.roles, "c1") }
	h.fake.resetCalls()
	_, err := e.Apply(context.Background(), p)
	if err == nil || !strings.Contains(err.Error(), "value depends on c1, which is still unknown at apply time") {
		t.Fatalf("apply: error = %v", err)
	}
	expectCalls(t, h, map[string]int{"ApplyResourceChange": 0})
}

// The apply of a plan with nothing to change takes no lock, writes no record and
// leaves no transaction. An output that changed is written, and only that.
func TestNoChangeApplyWritesOnlyChangedOutputs(t *testing.T) {
	mk := func(output string) spec {
		return spec{
			cue: "a: " + thing("a", "") + "\n" +
				"b: " + thing("b", "") + "\n" +
				"out: {value: " + output + "}\n" +
				"fixed: {value: \"constant\"}\n",
			resources: []string{"a", "b"},
			outputs:   []string{"out", "fixed"},
		}
	}
	h := newHarness(t)
	h.apply(mk("a.id"))
	first := h.state.Outputs()
	if len(first) != 2 {
		t.Fatalf("outputs after the first apply: %v", first)
	}

	t.Run("nothing changed", func(t *testing.T) {
		e := h.engine(mk("a.id"), Options{})
		p := plan(t, e)
		if p.HasChanges() || p.NeedsApply() || len(p.EvalOutputs) != 0 {
			t.Fatalf("a plan of the same specification: changes %v, needs apply %v, outputs %v", p.HasChanges(), p.NeedsApply(), p.EvalOutputs)
		}
		h.backend.Reset()
		res := apply(t, e, p)
		if w := h.backend.Writes(); w != 0 || res.Txn != "" {
			t.Errorf("a no-change apply wrote %d objects and the transaction %q", w, res.Txn)
		}
	})

	t.Run("an output changed", func(t *testing.T) {
		e := h.engine(mk(`"x-\(a.id)"`), Options{})
		p := plan(t, e)
		if p.HasChanges() || !p.NeedsApply() || !reflect.DeepEqual(p.EvalOutputs, []string{"out"}) {
			t.Fatalf("changes %v, needs apply %v, outputs %v; want only out", p.HasChanges(), p.NeedsApply(), p.EvalOutputs)
		}
		h.backend.Reset()
		res := apply(t, e, p)
		if got := h.backend.Count("Put", "outputs.json"); got != 1 {
			t.Errorf("outputs.json written %d times, want once", got)
		}
		if got := h.backend.Count("Put", "resources") + h.backend.Count("PutIfAbsent", "locks"); got != 0 {
			t.Errorf("the apply wrote %d records and locks", got)
		}
		if !strings.HasPrefix(string(h.state.Outputs()["out"]), `"x-`) || string(h.state.Outputs()["fixed"]) != `"constant"` {
			t.Errorf("outputs after the apply: %v", h.state.Outputs())
		}
		if len(res.Outputs) != 2 {
			t.Errorf("the apply reports %d outputs, want both", len(res.Outputs))
		}
		// The next plan finds nothing to write.
		p = plan(t, h.engine(mk(`"x-\(a.id)"`), Options{}))
		if p.NeedsApply() {
			t.Errorf("the output is still to be written after the apply")
		}
	})

	t.Run("a change elsewhere", func(t *testing.T) {
		// b changes; the outputs read a and nothing: they keep their records.
		bChanged := spec{
			cue: "a: " + thing("a", "") + "\nb: " + thing("b", `content: "new"`) + "\n" +
				"out: {value: \"x-\\(a.id)\"}\nfixed: {value: \"constant\"}\n",
			resources: []string{"a", "b"},
			outputs:   []string{"out", "fixed"},
		}
		e := h.engine(bChanged, Options{})
		p := plan(t, e)
		if len(p.EvalOutputs) != 0 {
			t.Fatalf("outputs to evaluate: %v", p.EvalOutputs)
		}
		h.backend.Reset()
		res := apply(t, e, p)
		if got := h.backend.Count("Put", "outputs.json"); got != 0 {
			t.Errorf("outputs.json written %d times for outputs that did not change", got)
		}
		if len(res.Outputs) != 2 {
			t.Errorf("the apply reports %d outputs, want the two that are recorded", len(res.Outputs))
		}
	})

	t.Run("an output was removed", func(t *testing.T) {
		// The specification no longer declares fixed: the apply removes the record
		// of it, and writes nothing else.
		removed := spec{
			cue: "a: " + thing("a", "") + "\nb: " + thing("b", `content: "new"`) + "\n" +
				"out: {value: \"x-\\(a.id)\"}\n",
			resources: []string{"a", "b"},
			outputs:   []string{"out"},
		}
		if _, ok := h.state.Outputs()["fixed"]; !ok {
			t.Fatalf("fixed is not recorded before the test: %v", h.state.Outputs())
		}
		e := h.engine(removed, Options{})
		p := plan(t, e)
		if p.HasChanges() || !p.NeedsApply() || len(p.EvalOutputs) != 0 || !reflect.DeepEqual(p.DropOutputs, []string{"fixed"}) {
			t.Fatalf("changes %v, needs apply %v, evaluates %v, removes %v; want only fixed removed", p.HasChanges(), p.NeedsApply(), p.EvalOutputs, p.DropOutputs)
		}
		h.backend.Reset()
		res := apply(t, e, p)
		if got := h.backend.Count("Put", "outputs.json"); got != 1 {
			t.Errorf("outputs.json written %d times, want once", got)
		}
		if got := h.backend.Count("Put", "resources") + h.backend.Count("PutIfAbsent", "locks"); got != 0 {
			t.Errorf("the apply wrote %d records and locks", got)
		}
		if _, ok := h.state.Outputs()["fixed"]; ok || len(h.state.Outputs()) != 1 || len(res.Outputs) != 1 {
			t.Errorf("outputs after the apply: %v, reported %v", h.state.Outputs(), res.Outputs)
		}
		if p := plan(t, h.engine(removed, Options{})); p.NeedsApply() {
			t.Error("the removed output is still to be removed after the apply")
		}
	})
}

// A data source that the plan read is not read again by the apply, and the
// nodes that read it are applied with the values the plan saw.
func TestApplyUsesTheDataSourceThePlanRead(t *testing.T) {
	mk := dataSpec
	h := newHarness(t)
	h.apply(mk("1"))
	if got := recordedContent(t, h, "w"); got != "from s-1" {
		t.Fatalf("w holds %q", got)
	}
	h.fake.resetCalls()

	e := h.engine(mk("2"), Options{})
	p := plan(t, e)
	if want := []string{"w"}; !reflect.DeepEqual(p.Write, want) {
		t.Fatalf("write %v, want %v: a data source whose read returned its record is not written", p.Write, want)
	}
	if want := []string{"src"}; !reflect.DeepEqual(keysOf(p.Inputs), want) {
		t.Fatalf("inputs %v, want %v", keysOf(p.Inputs), want)
	}
	apply(t, e, p)
	expectCalls(t, h, map[string]int{"ReadDataSource": 1, "ApplyResourceChange": 1})
	if got := recordedContent(t, h, "w"); got != "from s-2" {
		t.Errorf("w holds %q after the apply", got)
	}
	if pl := plan(t, h.engine(mk("2"), Options{})); pl.HasChanges() {
		t.Error("a plan after the apply has changes")
	}
}

// The apply of a saved plan uses what the plan read from a data source, as the
// apply of the plan that was just printed does. The resources that read it are
// applied with the value the plan saw, the source is not read a second time, and
// a source that answers otherwise by then changes nothing about what was
// approved.
func TestSavedPlanAppliesTheDataSourceItRead(t *testing.T) {
	ctx := context.Background()

	t.Run("data that changed after the record", func(t *testing.T) {
		h := newHarness(t)
		h.apply(dataSpec("1"))
		h.fake.setData("s", "new")
		s := dataSpec("1")
		file, pf := planned(t, h, s)
		if got := changeOf(t, mustLoad(t, h, s, pf), "src"); got.Action != ActionRead || got.Kept {
			t.Fatalf("src is %s (kept %v) in the plan, want a read that is written", got.Action, got.Kept)
		}
		e := h.engine(s, Options{Refresh: RefreshNone})
		loaded, err := e.LoadPlan(pf)
		if err != nil {
			t.Fatal(err)
		}
		if want := []string{"src", "w"}; !slices.Equal(loaded.Write, want) {
			t.Fatalf("write %v, want %v", loaded.Write, want)
		}
		// The source answers otherwise by the time the plan is applied.
		h.fake.setData("s", "newer")
		h.fake.resetCalls()
		res, err := e.ApplySaved(ctx, loaded.Remaining())
		if err != nil {
			t.Fatalf("apply: %v", err)
		}
		if res.Changed != 1 {
			t.Errorf("the apply changed %d resources, want w", res.Changed)
		}
		expectCalls(t, h, map[string]int{"ReadDataSource": 0, "ApplyResourceChange": 1})
		if got := recordedContent(t, h, "w"); got != "new-1" {
			t.Errorf("w holds %q, want the value the plan read", got)
		}
		if got := recordedContent(t, h, "src"); got != "new" || h.revision("src") != 2 {
			t.Errorf("src holds %q at revision %d, want the value the plan read, written once", got, h.revision("src"))
		}
		if err := MarkApplied(file, pf, loaded.Write, res.Txn); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("a replacement that the data causes", func(t *testing.T) {
		// A change of the data replaces the resource that reads it, as a file does
		// for a resource whose attribute cannot change in place.
		mk := func() spec {
			return spec{
				cue: "src: " + source("s") + "\n" +
					`w: {name: "w-\(src.content)", id: string | null, content: "x"}` + "\n",
				resources: []string{"w"},
				data:      []string{"src"},
			}
		}
		h := newHarness(t)
		h.apply(mk())
		old := recordedID(t, h, "w")
		h.fake.setData("s", "new")
		_, pf := planned(t, h, mk())
		if got := changeOf(t, mustLoad(t, h, mk(), pf), "w"); got.Action != ActionReplace {
			t.Fatalf("w is %s in the plan, want replace", got.Action)
		}
		e := h.engine(mk(), Options{Refresh: RefreshNone})
		loaded, err := e.LoadPlan(pf)
		if err != nil {
			t.Fatal(err)
		}
		h.fake.resetCalls()
		res, err := e.ApplySaved(ctx, loaded.Remaining())
		if err != nil {
			t.Fatalf("apply: %v", err)
		}
		if res.Added != 1 || res.Destroyed != 1 {
			t.Errorf("the apply added %d and destroyed %d, want the replacement of w", res.Added, res.Destroyed)
		}
		if got := recordedID(t, h, "w"); got == old {
			t.Error("w was not replaced")
		}
	})

	t.Run("a part that leaves the data out", func(t *testing.T) {
		h := newHarness(t)
		h.apply(dataSpec("1"))
		h.fake.setData("s", "new")
		s := dataSpec("1")
		_, pf := planned(t, h, s)
		e := h.engine(s, Options{Refresh: RefreshNone})
		loaded, err := e.LoadPlanFor(pf, []string{"w"})
		if err != nil {
			t.Fatal(err)
		}
		var closure *ErrNotClosed
		if _, err := loaded.Subset([]string{"w"}); !errors.As(err, &closure) || !slices.Equal(closure.Missing, []string{"src"}) {
			t.Fatalf("w alone: error = %v, want a selection that needs src", err)
		}
		part, err := loaded.Subset([]string{"src", "w"})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := e.ApplySaved(ctx, part); err != nil {
			t.Fatalf("apply: %v", err)
		}
		if got := recordedContent(t, h, "w"); got != "new-1" {
			t.Errorf("w holds %q", got)
		}
	})

	t.Run("a source that waits for the apply", func(t *testing.T) {
		// The source reads the id of a resource that does not exist yet: the plan
		// cannot read it, and the apply does.
		mk := func() spec {
			return spec{
				cue: "a: " + thing("a", "") + "\n" +
					`src: {name: "x-\(a.id)", id: string | null, content: string | null}` + "\n" +
					"w: " + thing("w", `content: "\(src.content)"`) + "\n",
				resources: []string{"a", "w"},
				data:      []string{"src"},
			}
		}
		h := newHarness(t)
		_, pf := planned(t, h, mk())
		if got := changeOf(t, mustLoad(t, h, mk(), pf), "src"); !got.Deferred {
			t.Fatalf("src is not deferred in the plan: %+v", got)
		}
		e := h.engine(mk(), Options{Refresh: RefreshNone})
		loaded, err := e.LoadPlan(pf)
		if err != nil {
			t.Fatal(err)
		}
		h.fake.resetCalls()
		if _, err := e.ApplySaved(ctx, loaded.Remaining()); err != nil {
			t.Fatalf("apply: %v", err)
		}
		expectCalls(t, h, map[string]int{"ReadDataSource": 1})
		if got := recordedContent(t, h, "w"); got != "from x-"+recordedID(t, h, "a") {
			t.Errorf("w holds %q", got)
		}
	})
}

// An apply does at most what the plan said: a node that has nothing left to do
// is fine when it already is what the plan would have made it, and a node that
// would do more, or that is something else, is not. A difference is exit code 3
// for a saved plan and for the plan that was just printed.
func TestApplyMayDoLessThanThePlanSaid(t *testing.T) {
	obj := tftypes.Object{AttributeTypes: map[string]tftypes.Type{"n": tftypes.String, "id": tftypes.String}}
	val := func(n string) tftypes.Value {
		return tftypes.NewValue(obj, map[string]tftypes.Value{"n": tftypes.NewValue(tftypes.String, n), "id": tftypes.NewValue(tftypes.String, "i")})
	}
	unknownID := func(n string) tftypes.Value {
		return tftypes.NewValue(obj, map[string]tftypes.Value{"n": tftypes.NewValue(tftypes.String, n), "id": tftypes.NewValue(tftypes.String, tftypes.UnknownValue)})
	}
	cases := []struct {
		name          string
		saved, actual Action
		// priorN is what the node holds now, plannedN what the provider plans now.
		priorN, plannedN string
		ok               bool
	}{
		{"same update", ActionUpdate, ActionUpdate, "w", "x", true},
		{"update with nothing left to do", ActionUpdate, ActionNoop, "x", "x", true},
		{"create that already exists", ActionCreate, ActionNoop, "x", "x", true},
		{"replace with nothing left to do", ActionReplace, ActionNoop, "x", "x", true},
		{"update that is not what the plan made", ActionUpdate, ActionNoop, "w", "w", false},
		{"create that is not what the plan made", ActionCreate, ActionNoop, "w", "w", false},
		{"replace that is not what the plan made", ActionReplace, ActionNoop, "w", "w", false},
		{"update read as a data source", ActionUpdate, ActionRead, "w", "w", false},
		{"recorded noop", ActionNoop, ActionNoop, "w", "w", true},
		{"noop that reads", ActionNoop, ActionRead, "x", "x", true},
		{"read of the same data", ActionRead, ActionRead, "x", "x", true},
		{"read of other data", ActionRead, ActionRead, "w", "w", false},
		{"read that found nothing to do", ActionRead, ActionNoop, "w", "w", true},
		{"noop that must update", ActionNoop, ActionUpdate, "w", "x", false},
		{"update that must replace", ActionUpdate, ActionReplace, "w", "x", false},
		{"update that must create", ActionUpdate, ActionCreate, "w", "x", false},
		{"create that must update", ActionCreate, ActionUpdate, "w", "x", false},
		{"read that must update", ActionRead, ActionUpdate, "w", "x", false},
	}
	for _, c := range cases {
		for _, approved := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s, approved %v", c.name, approved), func(t *testing.T) {
				// The plan saw "x" as the result of the change, with an id to come.
				saved := &Change{Addr: "a", Action: c.saved, Planned: unknownID("x"), PlannedCreate: unknownID("x")}
				e := &Engine{approved: approved, saved: map[string]*Change{"a": saved}}
				err := e.checkSaved(&Change{Addr: "a", Action: c.actual, Prior: val(c.priorN), Planned: val(c.plannedN), PlannedCreate: val(c.plannedN)})
				if (err == nil) != c.ok {
					t.Fatalf("error = %v, want ok %v", err, c.ok)
				}
				var coded *CodedError
				if err != nil && (!errors.As(err, &coded) || coded.Code != ExitStale) {
					t.Errorf("error %v has no exit code 3", err)
				}
			})
		}
	}
	e := &Engine{saved: map[string]*Change{"a": {Addr: "a", Action: ActionUpdate, Planned: val("x")}}}
	if err := e.checkSaved(&Change{Addr: "a", Action: ActionUpdate, Planned: val("y")}); err == nil || !strings.Contains(err.Error(), "different plan than the saved one") {
		t.Errorf("a known value that changed: %v", err)
	}
	if err := e.checkSaved(&Change{Addr: "b", Action: ActionUpdate}); err == nil || !strings.Contains(err.Error(), fmt.Sprintf("b: %s", "not part of the saved plan")) {
		t.Errorf("a node that is not in the plan: %v", err)
	}
	// A saved plan that would have changed a node says so when there is nothing to do and the node is not what it would make.
	e = &Engine{saved: map[string]*Change{"a": {Addr: "a", Action: ActionReplace, PlannedCreate: unknownID("x")}}}
	if err := e.checkSaved(&Change{Addr: "a", Action: ActionNoop, Prior: val("w"), Planned: val("w")}); err == nil ||
		!strings.Contains(err.Error(), "the saved plan would replace but the provider now plans to no-op") {
		t.Errorf("a replacement that finds nothing to do: %v", err)
	}
}
