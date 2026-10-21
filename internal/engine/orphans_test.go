package engine

import (
	"context"
	"errors"
	"fmt"
	"os"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
)

// destroyChanges returns delete changes for the nodes of a graph: deps maps each
// node to the nodes it depends on.
func destroyChanges(deps map[string][]string) []*Change {
	var addrs []string
	for a := range deps {
		addrs = append(addrs, a)
	}
	sort.Strings(addrs)
	var out []*Change
	for _, a := range addrs {
		out = append(out, &Change{Addr: a, Action: ActionDelete, Deps: deps[a]})
	}
	return orderForDestroy(out)
}

// turns records the order in which the calls of a walk started and ended.
type turns struct {
	mu          sync.Mutex
	tick        int
	start, end  map[string]int
	running, hi int
	hold        time.Duration
}

func newTurns(hold time.Duration) *turns {
	return &turns{start: map[string]int{}, end: map[string]int{}, hold: hold}
}

func (t *turns) do(c *Change) error {
	t.mu.Lock()
	t.tick++
	t.start[c.Addr] = t.tick
	if t.running++; t.running > t.hi {
		t.hi = t.running
	}
	t.mu.Unlock()
	time.Sleep(t.hold)
	t.mu.Lock()
	defer t.mu.Unlock()
	t.running--
	t.tick++
	t.end[c.Addr] = t.tick
	return nil
}

// dependentsFirst checks that every change started after the changes that
// depend on it ended.
func (t *turns) dependentsFirst(tb testing.TB, changes []*Change) {
	tb.Helper()
	for _, c := range changes {
		if _, ok := t.start[c.Addr]; !ok {
			tb.Errorf("%s did not run", c.Addr)
		}
	}
	for _, c := range changes {
		for _, d := range c.Deps {
			if _, ok := t.start[d]; !ok || d == c.Addr {
				continue
			}
			if t.start[d] < t.end[c.Addr] {
				tb.Errorf("%s depends on %s, which started (%d) before %s ended (%d)", c.Addr, d, t.start[d], c.Addr, t.end[c.Addr])
			}
		}
	}
}

func TestInReverseRunsDependentsFirst(t *testing.T) {
	chain := map[string][]string{}
	for i := 0; i < 12; i++ {
		var deps []string
		if i > 0 {
			deps = []string{fmt.Sprintf("n%02d", i-1)}
		}
		chain[fmt.Sprintf("n%02d", i)] = deps
	}
	for name, deps := range map[string]map[string][]string{
		"a chain":       chain,
		"independent":   {"a": nil, "b": nil, "c": nil, "d": nil},
		"a diamond":     {"top": nil, "left": {"top"}, "right": {"top"}, "bottom": {"left", "right", "left"}},
		"a hub":         {"hub": nil, "a": {"hub"}, "b": {"hub"}, "c": {"hub"}, "d": {"hub", "a"}},
		"two trees":     {"x": nil, "x1": {"x"}, "x2": {"x"}, "y": nil, "y1": {"y"}},
		"records gone":  {"a": {"not-deleted", "b"}, "b": {"also-not-deleted"}},
		"one":           {"only": nil},
		"nothing":       {},
		"a self edge":   {"s": {"s"}, "t": {"s"}},
		"a long layers": {"l0a": nil, "l0b": nil, "l1a": {"l0a", "l0b"}, "l1b": {"l0a"}, "l2": {"l1a", "l1b"}},
	} {
		t.Run(name, func(t *testing.T) {
			changes := destroyChanges(deps)
			tr := newTurns(time.Millisecond)
			if err := inReverse(changes, 4, tr.do); err != nil {
				t.Fatal(err)
			}
			if len(tr.start) != len(changes) {
				t.Errorf("%d of %d changes ran", len(tr.start), len(changes))
			}
			tr.dependentsFirst(t, changes)
			if tr.hi > 4 {
				t.Errorf("%d changes ran at the same time, want at most 4", tr.hi)
			}
		})
	}
}

// Changes that do not wait for each other run at the same time, up to the width.
func TestInReverseRunsIndependentChangesTogether(t *testing.T) {
	deps := map[string][]string{}
	for i := 0; i < 40; i++ {
		deps[fmt.Sprintf("i%02d", i)] = nil
	}
	tr := newTurns(20 * time.Millisecond)
	if err := inReverse(destroyChanges(deps), 10, tr.do); err != nil {
		t.Fatal(err)
	}
	if tr.hi != 10 {
		t.Errorf("%d changes ran at the same time, want 10", tr.hi)
	}
}

// Changes in a cycle have no order to follow. They run once the rest is done, one
// at a time, and nothing is left out.
func TestInReverseRunsACycleLast(t *testing.T) {
	changes := destroyChanges(map[string][]string{
		"free":  nil,
		"above": {"c1"},
		"c1":    {"c2"},
		"c2":    {"c1"},
		"below": nil,
	})
	// above depends on c1, c1 and c2 on each other.
	tr := newTurns(time.Millisecond)
	if err := inReverse(changes, 4, tr.do); err != nil {
		t.Fatal(err)
	}
	for _, a := range []string{"free", "above", "c1", "c2", "below"} {
		if _, ok := tr.start[a]; !ok {
			t.Errorf("%s did not run", a)
		}
	}
	if tr.start["above"] > tr.start["c1"] && tr.start["above"] > tr.start["c2"] {
		t.Errorf("above (%d) depends on c1 (%d) and ran after it", tr.start["above"], tr.start["c1"])
	}
	for _, a := range []string{"c1", "c2"} {
		for _, b := range []string{"free", "below"} {
			if tr.end[b] > tr.start[a] {
				t.Errorf("%s ended (%d) after the cycle member %s started (%d)", b, tr.end[b], a, tr.start[a])
			}
		}
	}
}

// After an error nothing new starts, what runs finishes, and what the failed
// change depends on is left alone.
func TestInReverseStopsAfterAnError(t *testing.T) {
	deps := map[string][]string{"top": nil, "mid": {"top"}, "leaf": {"mid"}}
	for i := 0; i < 30; i++ {
		deps[fmt.Sprintf("x%02d", i)] = nil
	}
	changes := destroyChanges(deps)
	boom := errors.New("boom")
	var (
		mu       sync.Mutex
		started  = map[string]bool{}
		finished = map[string]bool{}
	)
	err := inReverse(changes, 3, func(c *Change) error {
		mu.Lock()
		started[c.Addr] = true
		mu.Unlock()
		time.Sleep(5 * time.Millisecond)
		defer func() {
			mu.Lock()
			finished[c.Addr] = true
			mu.Unlock()
		}()
		if c.Addr == "mid" {
			return boom
		}
		return nil
	})
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v, want boom", err)
	}
	if started["top"] {
		t.Error("top, which mid depends on, started after mid failed")
	}
	for a := range started {
		if !finished[a] {
			t.Errorf("%s started and did not finish", a)
		}
	}
}

// spec of independent resources o000 ... and a chain r000 ... in which every
// resource reads the one before it.
func orphanSpec(independent, chain int) spec {
	var src strings.Builder
	var names []string
	for i := 0; i < independent; i++ {
		name := fmt.Sprintf("o%03d", i)
		names = append(names, name)
		fmt.Fprintf(&src, "%s: %s\n", name, thing(name, ""))
	}
	c := chainSpec(chain, true)
	return spec{cue: src.String() + c.cue, resources: append(names, c.resources...)}
}

// Orphans are read and deleted at the same time, and a record goes after the
// records that depend on it.
func TestOrphansDeleteDependentsFirst(t *testing.T) {
	const independent, chain = 200, 20
	lat := 25 * time.Millisecond
	if env := os.Getenv("PRAVO_ORPHAN_LATENCY"); env != "" {
		d, err := time.ParseDuration(env)
		if err != nil {
			t.Fatal(err)
		}
		lat = d
	}
	s := orphanSpec(independent, chain)
	h := newHarness(t)
	h.apply(s)
	deps := map[string][]string{}
	for _, sr := range h.state.List() {
		deps[sr.Address()] = sr.Dependencies
	}
	if len(deps) != independent+chain {
		t.Fatalf("%d records, want %d", len(deps), independent+chain)
	}

	h.fake.resetCalls()
	h.fake.setLatency(lat)
	start := time.Now()
	e := h.engine(s, Options{Destroy: true})
	p := plan(t, e)
	planned := time.Since(start)
	if _, err := e.Apply(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	took := time.Since(start)
	if h.state.Len() != 0 {
		t.Errorf("%d records remain", h.state.Len())
	}
	destroys, peak := h.fake.destroyed()
	if len(destroys) != independent+chain {
		t.Fatalf("%d destroys, want %d", len(destroys), independent+chain)
	}
	if peak < Parallelism-1 {
		t.Errorf("%d destroys ran at the same time, want at least %d", peak, Parallelism-1)
	}
	byName := map[string]destroyRecord{}
	for _, d := range destroys {
		byName[d.name] = d
	}
	for addr, ds := range deps {
		for _, d := range ds {
			if byName[d].start < byName[addr].end {
				t.Errorf("%s depends on %s, which was destroyed (from %d) before %s was done (%d)", addr, d, byName[d].start, addr, byName[addr].end)
			}
		}
	}
	// Reading and deleting one record at a time takes (independent+chain) times
	// the latency of both; the time of the two phases together is far below that.
	serial := time.Duration(independent+chain) * 2 * lat
	if took > serial/3 {
		t.Errorf("plan and destroy took %s (plan %s), serial would take %s", took, planned, serial)
	}
	t.Logf("latency %s: plan %s, plan and destroy %s, serial %s, peak %d", lat, planned.Round(time.Millisecond), took.Round(time.Millisecond), serial, peak)
	if got := h.fake.called("ReadResource"); got != independent+chain {
		t.Errorf("%d reads, want one per record (%d)", got, independent+chain)
	}
}

// Orphans of an apply that is not a destroy are deleted in the same order, and
// the records of data sources are forgotten.
func TestRemovedResourcesAreDeletedDependentsFirst(t *testing.T) {
	full := spec{
		cue: "keep: " + thing("keep", "") + "\n" +
			"a: " + thing("a", "") + "\n" +
			"b: " + thing("b", `content: "\(a.id)"`) + "\n" +
			"c: " + thing("c", `content: "\(b.id)"`) + "\n" +
			"src: " + source("s") + "\n",
		resources: []string{"keep", "a", "b", "c"},
		data:      []string{"src"},
	}
	h := newHarness(t)
	h.apply(full)
	if h.state.Get("src") == nil {
		t.Fatal("the data source is not recorded")
	}
	h.fake.setLatency(5 * time.Millisecond)
	kept := spec{cue: "keep: " + thing("keep", "") + "\n", resources: []string{"keep"}}
	e := h.engine(kept, Options{})
	p := plan(t, e)
	res, err := e.Apply(context.Background(), p)
	if err != nil {
		t.Fatal(err)
	}
	if res.Destroyed != 3 {
		t.Errorf("destroyed %d, want 3", res.Destroyed)
	}
	if got := h.state.Addresses(); !reflect.DeepEqual(got, []string{"keep"}) {
		t.Errorf("records after the apply: %v, want keep", got)
	}
	destroys, _ := h.fake.destroyed()
	var order []string
	sort.Slice(destroys, func(i, j int) bool { return destroys[i].start < destroys[j].start })
	for _, d := range destroys {
		order = append(order, d.name)
	}
	if want := []string{"c", "b", "a"}; !reflect.DeepEqual(order, want) {
		t.Errorf("destroyed in the order %v, want %v", order, want)
	}
	forgotten := 0
	for _, c := range p.Changes {
		if c.Addr == "src" && c.Action == ActionDelete {
			forgotten++
		}
	}
	if forgotten != 1 {
		t.Errorf("the plan deletes the record of the data source %d times, want once", forgotten)
	}
}

// A destroy that fails stops the ones that have not started. The ones that ran
// are recorded, and what depended on the failed record is not touched.
func TestDestroyThatFailsLeavesWhatItDidNotReach(t *testing.T) {
	s := orphanSpec(30, 4)
	h := newHarness(t)
	h.apply(s)
	// r001 reads r000, r002 reads r001: r002 and r003 go first, then r001 fails.
	h.fake.refuseDestroy("r001")
	h.fake.setLatency(2 * time.Millisecond)
	e := h.engine(s, Options{Destroy: true})
	p := plan(t, e)
	_, err := e.Apply(context.Background(), p)
	if err == nil || !strings.Contains(err.Error(), "r001") {
		t.Fatalf("apply = %v, want an error that names r001", err)
	}
	for _, addr := range []string{"r000", "r001"} {
		if h.state.Get(addr) == nil {
			t.Errorf("%s is not in the state; it waits for r001", addr)
		}
	}
	for _, addr := range []string{"r002", "r003"} {
		if h.state.Get(addr) != nil {
			t.Errorf("%s is in the state; it was destroyed before r001", addr)
		}
	}
	// Every destroy that ran was recorded: the objects that were destroyed have no record.
	destroys, _ := h.fake.destroyed()
	for _, d := range destroys {
		if d.name != "r001" && h.state.Get(d.name) != nil {
			t.Errorf("%s was destroyed and is still recorded", d.name)
		}
	}
}

// The records of a plan are read at the same time, and a failing read is the
// error of the plan.
func TestOrphansArePlannedAtTheSameTime(t *testing.T) {
	const n = 60
	s := orphanSpec(n, 0)
	h := newHarness(t)
	h.apply(s)
	lat := 30 * time.Millisecond
	h.fake.setLatency(lat)
	start := time.Now()
	p := plan(t, h.engine(s, Options{Destroy: true}))
	took := time.Since(start)
	if got := len(p.Changes); got != n {
		t.Errorf("%d changes, want %d", got, n)
	}
	if serial := n * lat; took > serial/2 {
		t.Errorf("the plan took %s, reading one record at a time takes %s", took, serial)
	}
	// Changes come dependents first and by address otherwise.
	var addrs []string
	for _, c := range p.Changes {
		addrs = append(addrs, c.Addr)
	}
	if !sort.StringsAreSorted(addrs) {
		t.Errorf("independent changes are not in address order: %v", addrs[:5])
	}
}

// The records of a destroy plan are read at the same time, and the lines that
// announce the reads come in the order of the records, as they did when the
// records were read one after the other.
func TestOrphanReadsAreAnnouncedInOrder(t *testing.T) {
	const n = 40
	s := orphanSpec(n, 0)
	h := newHarness(t)
	h.apply(s)
	h.fake.jitterUpgrades(8 * time.Millisecond)
	h.fake.setLatency(5 * time.Millisecond)
	var (
		mu    sync.Mutex
		lines []string
	)
	opts := Options{Destroy: true, Progress: func(format string, args ...any) {
		line := fmt.Sprintf(format, args...)
		if strings.Contains(line, "Refreshing state") {
			mu.Lock()
			lines = append(lines, strings.SplitN(line, ":", 2)[0])
			mu.Unlock()
		}
	}}
	plan(t, h.engine(s, opts))
	if len(lines) != n {
		t.Fatalf("%d reads announced, want %d", len(lines), n)
	}
	if !sort.StringsAreSorted(lines) {
		t.Errorf("the reads were announced in the order %v", lines)
	}
	if got := h.fake.called("ReadResource"); got != n {
		t.Errorf("%d reads, want %d", got, n)
	}
}

// A record that is not read does not hold up the lines of the records after it.
func TestOrphanAnnouncementsDoNotWaitForARecordThatPrintsNothing(t *testing.T) {
	const n = 12
	s := orphanSpec(n, 0)
	h := newHarness(t)
	h.apply(s)
	// Every other record is old enough to be read; the others are within the age.
	old := time.Now().Add(-2 * time.Hour)
	for i := 1; i < n; i += 2 {
		rec := *h.state.Get(fmt.Sprintf("o%03d", i))
		rec.RefreshedAt = &old
		if err := h.state.Refresh(&rec, ""); err != nil {
			t.Fatal(err)
		}
	}
	h.fake.resetCalls()
	h.fake.jitterUpgrades(4 * time.Millisecond)
	var (
		mu    sync.Mutex
		lines []string
	)
	opts := Options{Destroy: true, DefaultRefresh: "1h", Progress: func(format string, args ...any) {
		if line := fmt.Sprintf(format, args...); strings.Contains(line, "Refreshing state") {
			mu.Lock()
			lines = append(lines, strings.SplitN(line, ":", 2)[0])
			mu.Unlock()
		}
	}}
	done := make(chan struct{})
	go func() {
		defer close(done)
		plan(t, h.engine(s, opts))
	}()
	select {
	case <-done:
	case <-time.After(20 * time.Second):
		t.Fatal("the plan does not finish: a record that prints no line holds up the others")
	}
	if want := []string{"o001", "o003", "o005", "o007", "o009", "o011"}; !reflect.DeepEqual(lines, want) {
		t.Errorf("announced the reads of %v, want %v", lines, want)
	}
}
