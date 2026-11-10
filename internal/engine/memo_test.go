package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/pravo-systems/pravo/internal/env"
)

// The tests of the plan memo (memo.go). Next to them, every test of the package
// plans with a memo and has each node it leaves alone converted as well
// (memoaudit_test.go), and the tests of equivalence_test.go compare the plans
// of random specifications that change, which have a memo, with plans that
// have none.

// memoPlan plans s as a command does and returns the plan with the addresses
// whose configuration the plan converted, sorted.
func memoPlan(t *testing.T, h *harness, s spec, opts Options) (*Plan, []string) {
	t.Helper()
	e := h.engine(s, opts)
	var (
		mu  sync.Mutex
		got []string
	)
	e.onConvert = func(addr string, apply bool) {
		if !apply {
			mu.Lock()
			got = append(got, addr)
			mu.Unlock()
		}
	}
	plan, err := e.Plan(context.Background())
	if err != nil {
		t.Fatalf("plan: %v\nspecification:\n%s", err, s.cue)
	}
	sort.Strings(got)
	return plan, slices.Compact(got)
}

// sameAsWithoutMemo plans s on h, and on a copy of its state that has no memo,
// and fails when the two plans differ. It returns what the plan with the memo
// converted.
func sameAsWithoutMemo(t *testing.T, h *harness, s spec, opts Options) []string {
	t.Helper()
	bare := h.fork()
	bare.noMemo = true
	want, all := memoPlan(t, bare, s, opts)
	got, converted := memoPlan(t, h, s, opts)
	if a, b := eqDump(got), eqDump(want); a != b {
		t.Fatalf("the plan with a memo differs from the plan without one:\nwith:\n%s\nwithout:\n%s\nspecification:\n%s", a, b, s.cue)
	}
	if len(converted) > len(all) {
		t.Fatalf("the plan with a memo converted %v, the plan without one %v", converted, all)
	}
	return converted
}

func memoEntries(t *testing.T, h *harness) map[string]memoEntry {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(h.dir, "plan.memo"))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	var f memoFile
	if err := json.Unmarshal(data, &f); err != nil {
		t.Fatalf("the memo file: %v", err)
	}
	return f.Entries
}

// memoT is the type of the resources of these tests: a reference in a body
// that has no type is not converted as a specification of a provider is.
const memoT = "#T: {name: string, id: string | null, content?: string, secret_wo?: string, secret_wo_version?: string, ...}\n"

func mt(name, extra string) string {
	return fmt.Sprintf("#T & {name: %q\n%s}", name, extra)
}

func memoChain(a, shared string) spec {
	return spec{
		cue: memoT + "_shared: \"" + shared + "\"\n" +
			"a: " + mt("a", "content: \""+a+"\"") + "\n" +
			"b: " + mt("b", "content: a.id") + "\n" +
			"c: " + mt("c", "content: _shared") + "\n" +
			"d: " + mt("d", "content: b.id") + "\n",
		resources: []string{"a", "b", "c", "d"},
	}
}

// A plan converts the configurations that the memo does not answer for, and
// no other: after an apply, the ones whose values were not known when they were
// planned; after a change, the resource that changed and the ones that read it.
func TestAPlanConvertsOnlyWhatChanged(t *testing.T) {
	h := newHarness(t)
	h.apply(memoChain("x", "s0"))

	// b and d were planned before a and b had an id.
	if got := sameAsWithoutMemo(t, h, memoChain("x", "s0"), Options{}); !slices.Equal(got, []string{"b", "d"}) {
		t.Fatalf("the plan after the apply converted %v, want b and d", got)
	}
	if got := sameAsWithoutMemo(t, h, memoChain("x", "s0"), Options{}); len(got) != 0 {
		t.Fatalf("a plan of what did not change converted %v", got)
	}
	// A change to a: a is planned, and b, which reads it. d reads b, which the
	// plan found as it was.
	if got := sameAsWithoutMemo(t, h, memoChain("y", "s0"), Options{}); !slices.Equal(got, []string{"a", "b"}) {
		t.Fatalf("the plan of a change to a converted %v, want a and b", got)
	}
	// The memo is of the last plan: the specification as it was is converted
	// where the plan of the change converted, and then remembered again.
	if got := sameAsWithoutMemo(t, h, memoChain("x", "s0"), Options{}); !slices.Equal(got, []string{"a", "b"}) {
		t.Fatalf("the plan of the specification as it was converted %v, want a and b", got)
	}
	if got := sameAsWithoutMemo(t, h, memoChain("x", "s0"), Options{}); len(got) != 0 {
		t.Fatalf("the plan after it converted %v", got)
	}
	// A plain field that c reads.
	if got := sameAsWithoutMemo(t, h, memoChain("x", "s1"), Options{}); !slices.Equal(got, []string{"c"}) {
		t.Fatalf("the plan of a change to the plain field converted %v, want c", got)
	}
	// -full plans every resource.
	if got := sameAsWithoutMemo(t, h, memoChain("x", "s0"), Options{Full: true}); len(got) != 4 {
		t.Fatalf("-full converted %v, want all four", got)
	}
}

// What a plan remembers, it remembers from the plan that converted: an apply
// that another command made, with no memo or with another one, changes the
// records, and the plan after it converts what reads them.
func TestTheMemoIsBehindTheState(t *testing.T) {
	h := newHarness(t)
	h.apply(memoChain("x", "s0"))
	sameAsWithoutMemo(t, h, memoChain("x", "s0"), Options{})

	h.noMemo = true
	h.apply(memoChain("y", "s1"))
	h.noMemo = false
	if got := sameAsWithoutMemo(t, h, memoChain("y", "s1"), Options{}); !slices.Equal(got, []string{"a", "b", "c"}) {
		t.Fatalf("the plan after an apply without the memo converted %v, want a, b and c", got)
	}
	if got := sameAsWithoutMemo(t, h, memoChain("y", "s1"), Options{}); len(got) != 0 {
		t.Fatalf("the plan after it converted %v", got)
	}
}

// memoForms are bodies of the resource r next to the resources a and b, with
// whether the memo may answer for r.
var memoForms = []struct {
	name, cue  string
	remembered bool
	// extra lists more resources that the form declares.
	extra []string
}{
	{"literals", `r: #T & {name: "r", content: "x"}`, true, nil},
	{"a reference", `r: #T & {name: "r", content: a.id}`, true, nil},
	{"two references", `r: #T & {name: a.name, content: b.id}`, true, nil},
	{"a plain field", "_p: \"x\"\nr: #T & {name: \"r\", content: _p}", true, nil},
	{"an interpolation of a plain field", "_p: \"x\"\nr: #T & {name: \"r\", content: \"c-\\(_p)\"}", true, nil},
	{"a hidden field", `r: #T & {name: "r", _c: "x", content: _c}`, true, nil},
	{"a definition", "#U: #T & {content: \"t\"}\nr: #U & {name: \"r\"}", true, nil},
	{"labels and lifecycle", `r: #T & {name: "r", content: a.id, $labels: {team: "x"}, $lifecycle: {prevent_destroy: true, refresh: "1h"}}`, true, nil},
	{"a loop read by element", "pets: {p: #T & {name: \"p\"}, q: #T & {name: \"q\"}}\nfor k, v in pets {\"r-\\(k)\": #T & {name: \"r-\\(k)\", content: v.id}}", true, []string{"pets.p", "pets.q", `"r-p"`, `"r-q"`}},

	// A body that a definition gives a reference to a resource: the struct
	// itself is then made from the resource, and such a struct is not read
	// leaf by leaf.
	{"a definition with a reference", "#U: #T & {content: a.id}\nr: #U & {name: \"r\"}", false, nil},
	{"an interpolation", `r: #T & {name: "r", content: "c-\(a.id)"}`, false, nil},
	{"a plain field built from a resource", "_p: a.id\nr: #T & {name: \"r\", content: _p}", false, nil},
	{"a hidden field built from a resource", `r: #T & {name: "r", _c: {x: "c-\(a.name)", y: a.id}, content: _c.x}`, false, nil},
	{"a default", `r: #T & {name: "r", content: *a.id | "z"}`, false, nil},
	{"a call", `r: #T & {name: "r", content: "\(len(a.id))"}`, false, nil},
	{"a comprehension", `r: #T & {name: "r", content: "\(len([for k, v in {x: a.id} {v}]))"}`, false, nil},
	{"a condition", "r: #T & {name: \"r\"\nif a.name == \"a\" {content: \"x\"}}", false, nil},
	{"a let", "r: #T & {name: \"r\"\nlet L = a.id\ncontent: L}", false, nil},
	{"a loop over a collection", "pets: {p: #T & {name: \"p\"}, q: #T & {name: \"q\"}}\nr: #T & {name: \"r\", content: \"\\(len([for k, v in pets {v.id}]))\"}", false, []string{"pets.p", "pets.q", "r"}},
}

func memoFormSpec(form int, a string) spec {
	f := memoForms[form]
	s := spec{
		cue:       memoT + "a: " + mt("a", "content: \""+a+"\"") + "\nb: " + mt("b", "") + "\n" + f.cue + "\n",
		resources: []string{"a", "b"},
	}
	if f.extra == nil {
		s.resources = append(s.resources, "r")
	}
	s.resources = append(s.resources, f.extra...)
	return s
}

// The memo answers for a body in which other resources are read in plain
// references and in no other way, and for every such body: the plan after the
// plan that converted it converts nothing. A body of any other shape is
// converted by every plan. With or without the memo the plans are the same,
// before and after a resource that the body reads changes.
func TestTheBodiesTheMemoAnswersFor(t *testing.T) {
	for i, f := range memoForms {
		t.Run(f.name, func(t *testing.T) {
			t.Parallel()
			h := newHarness(t)
			s := memoFormSpec(i, "x")
			h.apply(s)
			sameAsWithoutMemo(t, h, s, Options{})
			got := sameAsWithoutMemo(t, h, s, Options{})
			if f.remembered && len(got) != 0 {
				t.Errorf("the second plan after the apply converted %v, want nothing", got)
			}
			if !f.remembered && len(got) == 0 {
				t.Errorf("the second plan after the apply converted nothing: the memo answers for a body it cannot know")
			}
			for _, a := range got {
				if a == "a" || a == "b" {
					t.Errorf("the plan converted %s, whose body is literals", a)
				}
			}
			// a changes, and with it what its readers are given.
			s = memoFormSpec(i, "y")
			sameAsWithoutMemo(t, h, s, Options{})
			h.apply(s)
			sameAsWithoutMemo(t, h, s, Options{})
			sameAsWithoutMemo(t, h, s, Options{})
		})
	}
}

// The variable of a loop is written the same whatever it stands for. What a
// body is remembered by names the element that each leaf reads: two loops that
// change places change what the resources they make read, and the plan finds
// it.
func TestTheMemoKnowsWhichElementALoopReads(t *testing.T) {
	form := func(first, second string) spec {
		return spec{
			cue: memoT +
				"one: {p: #T & {name: \"one-p\"}, q: #T & {name: \"one-q\"}}\n" +
				"two: {p: #T & {name: \"two-p\"}, q: #T & {name: \"two-q\"}}\n" +
				"for k, v in " + first + " for l, w in " + second + " {\"r-\\(k)-\\(l)\": #T & {name: \"r-\\(k)-\\(l)\", content: v.id, secret_wo_version: w.name}}\n",
			resources: []string{"one.p", "one.q", "two.p", "two.q", `"r-p-p"`, `"r-p-q"`, `"r-q-p"`, `"r-q-q"`},
		}
	}
	h := newHarness(t)
	s := form("one", "two")
	h.apply(s)
	sameAsWithoutMemo(t, h, s, Options{})
	if got := sameAsWithoutMemo(t, h, s, Options{}); len(got) != 0 {
		t.Fatalf("the plan of what did not change converted %v", got)
	}
	swapped := form("two", "one")
	plan, got := memoPlan(t, h, swapped, Options{})
	if len(got) != 4 {
		t.Errorf("the plan of the loops in the other order converted %v, want the four that the loops make", got)
	}
	if n := strings.Count(eqDump(plan), " update planned="); n != 4 {
		t.Errorf("the plan of the loops in the other order has %d updates, want 4:\n%s", n, eqDump(plan))
	}
	sameAsWithoutMemo(t, h, swapped, Options{})
	h.apply(swapped)
	sameAsWithoutMemo(t, h, swapped, Options{})
	if got := sameAsWithoutMemo(t, h, swapped, Options{}); len(got) != 0 {
		t.Errorf("the plan after the apply converted %v", got)
	}
}

// The nodes that the memo left alone make their fills when something reads
// them. A task whose body needs the configuration evaluated with those values
// in it takes all of them at once: a thousand resources that a loop makes, each
// reading one node that was left alone, evaluate the configuration once and not
// a thousand times.
func TestNodesLeftAloneAreUnifiedTogether(t *testing.T) {
	const n = 40
	s := spec{cue: memoT + fmt.Sprintf("a: {for j in list.Range(0, %d, 1) {\"k\\(j)\": #T & {name: \"a-\\(j)\"}}}\n", n) +
		"b: {for k, v in a {\"\\(k)\": #T & {name: \"b-\\(k)\", content: v.id}}}\n"}
	s.cue = "import \"list\"\n\n" + s.cue
	for j := range n {
		s.resources = append(s.resources, fmt.Sprintf("a.k%d", j), fmt.Sprintf("b.k%d", j))
	}
	h := newHarness(t)
	h.apply(s)
	// The plan after the apply converts every b, whose a was left alone.
	e := h.engine(s, Options{})
	var converted atomic.Int64
	e.onConvert = func(string, bool) { converted.Add(1) }
	if _, err := e.Plan(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := converted.Load(); got != n {
		t.Errorf("the plan after the apply converted %d configurations, want the %d of b", got, n)
	}
	if e.settles > 2 {
		t.Errorf("the plan after the apply evaluated the configuration %d times for %d resources", e.settles, n)
	}
	// The plan after it converts nothing and evaluates nothing.
	e = h.engine(s, Options{})
	converted.Store(0)
	e.onConvert = func(string, bool) { converted.Add(1) }
	if _, err := e.Plan(context.Background()); err != nil {
		t.Fatal(err)
	}
	if converted.Load() != 0 || e.settles != 0 {
		t.Errorf("the second plan converted %d configurations and evaluated the configuration %d times, want neither", converted.Load(), e.settles)
	}
	// A plan that checks its memo converts every one and keeps what it evaluated.
	e = h.engine(s, Options{Memo: &PlanMemo{Path: filepath.Join(h.dir, "plan.memo"), Key: []byte("the key of the memo of the tests"), Check: true}})
	if _, err := e.Plan(context.Background()); err != nil {
		t.Fatal(err)
	}
	if e.settles > 3 {
		t.Errorf("the plan that checks its memo evaluated the configuration %d times for %d resources", e.settles, n)
	}
}

// What a record holds besides the digest of the configuration is compared as
// a conversion compares it: a new label, prevent_destroy or refresh age is a
// change that the plan with a memo finds as the plan without one does.
func TestTheMemoFindsWhatIsNotInTheConfiguration(t *testing.T) {
	form := func(extra string) spec {
		return spec{cue: memoT + "a: " + mt("a", "content: \"x\"\n"+extra) + "\n", resources: []string{"a"}}
	}
	h := newHarness(t)
	h.apply(form(""))
	sameAsWithoutMemo(t, h, form(""), Options{})
	for _, extra := range []string{
		`$labels: {team: "x"}`,
		`$labels: {team: "y"}`,
		`$lifecycle: {prevent_destroy: true}`,
		`$lifecycle: {refresh: "1h"}`,
		`$lifecycle: {refresh: "never"}`,
		`$lifecycle: {create_before_destroy: true}`,
		``,
	} {
		s := form(extra)
		if got := sameAsWithoutMemo(t, h, s, Options{}); len(got) != 1 {
			t.Errorf("%s: the plan converted %v, want a", extra, got)
		}
		h.apply(s)
		sameAsWithoutMemo(t, h, s, Options{})
		if got := sameAsWithoutMemo(t, h, s, Options{}); len(got) != 0 {
			t.Errorf("%s: the plan after the apply converted %v", extra, got)
		}
	}
}

// An environment variable is read by every plan, and what reads it is
// converted when its value is another. The value is not in the file.
func TestTheMemoFollowsAnEnvironmentVariable(t *testing.T) {
	s := spec{
		cue:       memoT + "e: {name: \"STAGE_NAME\", value: string}\na: " + mt("a", "content: e.value") + "\n",
		resources: []string{"a"}, builtins: []string{"e"},
	}
	h := newHarness(t)
	one := Options{Env: env.Map(map[string]string{"STAGE_NAME": "the-first-stage"})}
	two := Options{Env: env.Map(map[string]string{"STAGE_NAME": "the-second-stage"})}
	e := h.engine(s, one)
	plan, err := e.Plan(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	apply(t, e, plan)
	sameAsWithoutMemo(t, h, s, one)
	if got := sameAsWithoutMemo(t, h, s, one); len(got) != 0 {
		t.Errorf("the plan with the same value converted %v", got)
	}
	if got := sameAsWithoutMemo(t, h, s, two); !slices.Equal(got, []string{"a"}) {
		t.Errorf("the plan with another value converted %v, want a", got)
	}
	raw, err := os.ReadFile(filepath.Join(h.dir, "plan.memo"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "stage") {
		t.Errorf("the memo file holds the value of the environment variable:\n%s", raw)
	}
}

// The memo holds nothing for a resource that reads a secret, nor for what
// reads a value that the digest of a configuration leaves out: no digest in the
// file is made from the value of a secret, with or without a key.
func TestTheMemoHoldsNothingOfASecret(t *testing.T) {
	s := spec{
		cue: memoT + "sec: {name: \"prod/token\", value: string}\n" +
			"a: " + mt("a", "secret_wo: sec.value\nsecret_wo_version: \"1\"") + "\n" +
			"b: " + mt("b", "content: a.id") + "\n" +
			"c: " + mt("c", "content: \"x\"") + "\n",
		resources: []string{"a", "b", "c"}, secrets: []string{"sec"},
	}
	h, opts := secretHarness(t)
	h.setSecret("prod/token", secretOne)
	h.apply2(t, s, opts)
	for range 2 {
		if _, got := memoPlan(t, h, s, opts); !slices.Contains(got, "a") || !slices.Contains(got, "b") {
			t.Errorf("the plan converted %v: a reads a secret and b reads a, so every plan converts both", got)
		}
	}
	entries := memoEntries(t, h)
	if _, ok := entries["c"]; !ok || len(entries) != 1 {
		t.Errorf("the memo holds %v, want c alone", entries)
	}
	if e := h.engine(s, opts); len(h.leaks(t, e, nil, secretOne)) > 0 {
		t.Errorf("the secret is in %v", h.leaks(t, e, nil, secretOne))
	}
}

// A memo file that is damaged, or that another key made, is a memo that
// remembers nothing, and the plan writes a new one.
func TestAMemoFileThatCannotBeUsedIsNotUsed(t *testing.T) {
	h := newHarness(t)
	s := memoChain("x", "s0")
	h.apply(s)
	sameAsWithoutMemo(t, h, s, Options{})
	path := filepath.Join(h.dir, "plan.memo")
	good, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := sameAsWithoutMemo(t, h, s, Options{}); len(got) != 4 {
		t.Errorf("the plan with a damaged memo converted %v, want all four", got)
	}
	if got := sameAsWithoutMemo(t, h, s, Options{}); len(got) != 0 {
		t.Errorf("the plan after it converted %v", got)
	}

	other := Options{Memo: &PlanMemo{Path: path, Key: []byte("another key")}}
	if _, got := memoPlan(t, h, s, other); len(got) != 4 {
		t.Errorf("the plan with another key converted %v, want all four", got)
	}
	// An entry that was changed by hand: the refresh age is part of the digest.
	var f memoFile
	if err := json.Unmarshal(good, &f); err != nil {
		t.Fatal(err)
	}
	ent := f.Entries["a"]
	ent.Age = "never"
	f.Entries["a"] = ent
	data, _ := json.Marshal(f)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if got := sameAsWithoutMemo(t, h, s, Options{}); !slices.Equal(got, []string{"a"}) {
		t.Errorf("the plan with a changed entry converted %v, want a", got)
	}
}

// With Check, a plan converts every configuration that the memo answers for
// and fails when the two differ.
func TestCheckComparesTheMemoWithTheConversion(t *testing.T) {
	h := newHarness(t)
	s := memoChain("x", "s0")
	h.apply(s)
	sameAsWithoutMemo(t, h, s, Options{})
	check := Options{Memo: &PlanMemo{Path: filepath.Join(h.dir, "plan.memo"), Key: []byte("the key of the memo of the tests"), Check: true}}
	before := memoChecks.Load()
	if _, got := memoPlan(t, h, s, check); len(got) != 0 {
		t.Errorf("the plan converted %v for its providers", got)
	}
	if n := memoChecks.Load() - before; n < 4 {
		t.Errorf("%d nodes were checked, want the four", n)
	}

	// The check itself: a node whose facts are not those of its configuration.
	e := h.engine(s, check)
	if _, err := e.buildGraph(); err != nil {
		t.Fatal(err)
	}
	r := e.addrRes("a")
	sc, err := e.resourceSchema(r)
	if err != nil {
		t.Fatal(err)
	}
	sr := h.state.Get("a")
	right := planFacts{hash: sr.ConfigHash, labels: sr.Labels, prevent: sr.PreventDestroy}
	if problem := e.shadowCheck(r, e.planOf(r, sc, right, false), false); problem != "" {
		t.Errorf("the facts of the record: %s", problem)
	}
	for name, wrong := range map[string]planFacts{
		"hash":            {hash: "0000000000000000"},
		"prevent_destroy": {hash: sr.ConfigHash, prevent: true},
		"refresh":         {hash: sr.ConfigHash, age: "1h"},
		"labels":          {hash: sr.ConfigHash, labels: map[string]string{"team": "x"}},
	} {
		if problem := e.shadowCheck(r, e.planOf(r, sc, wrong, false), false); problem == "" {
			t.Errorf("%s: the check found nothing wrong", name)
		}
	}
}
