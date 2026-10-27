package state

import (
	"context"
	"fmt"
	"math/rand/v2"
	"reflect"
	"slices"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func mustSet(t testing.TB, s *Store, rs ...*Resource) {
	t.Helper()
	for _, r := range rs {
		if err := s.Set(r, ""); err != nil {
			t.Fatal(err)
		}
	}
}

// refDependents and refReach are the questions answered by a pass over every
// record, as the store did before it kept an index.
func refDependents(s *Store, addr string) []string {
	out := []string{}
	for _, r := range s.List() {
		for _, d := range r.Dependencies {
			if d == addr {
				out = append(out, r.Address())
				break
			}
		}
	}
	return out
}

func refReach(s *Store, addrs ...string) []string {
	seen := map[string]bool{}
	var walk func(a string)
	walk = func(a string) {
		for _, d := range refDependents(s, a) {
			if !seen[d] {
				seen[d] = true
				walk(d)
			}
		}
	}
	for _, a := range addrs {
		walk(a)
	}
	out := make([]string, 0, len(seen))
	for a := range seen {
		out = append(out, a)
	}
	sort.Strings(out)
	return out
}

func TestDependentsAndReachFollowTheEdges(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	// t.a <- t.b <- t.d and t.a <- t.c <- t.d: a diamond below a, and a loner.
	mustSet(t, s, node("a"), node("b", "t.a"), node("c", "t.a", "t.a"), node("d", "t.b", "t.c"), node("z"))
	for addr, want := range map[string][]string{
		"t.a":  {"t.b", "t.c"},
		"t.b":  {"t.d"},
		"t.d":  {},
		"t.z":  {},
		"nope": {},
	} {
		got := s.Dependents(addr)
		if got == nil || !reflect.DeepEqual(got, want) {
			t.Errorf("Dependents(%s) = %#v, want %#v", addr, got, want)
		}
	}
	if got, want := s.Reach("t.a"), []string{"t.b", "t.c", "t.d"}; !reflect.DeepEqual(got, want) {
		t.Errorf("Reach(t.a) = %v, want %v", got, want)
	}
	if got, want := s.ReachOf([]string{"t.b", "t.c"}), []string{"t.d"}; !reflect.DeepEqual(got, want) {
		t.Errorf("ReachOf(b, c) = %v, want %v", got, want)
	}
	if got := s.ReachOf(nil); len(got) != 0 {
		t.Errorf("ReachOf(nothing) = %v", got)
	}
	// The slice a caller gets is its own.
	got := s.Dependents("t.a")
	got[0] = "changed"
	if again := s.Dependents("t.a"); again[0] != "t.b" {
		t.Errorf("a caller changed the answer of the store: %v", again)
	}
	// A cycle leads back to the address it starts from.
	mustSet(t, s, node("x", "t.y"), node("y", "t.x"))
	if got, want := s.Reach("t.x"), []string{"t.x", "t.y"}; !reflect.DeepEqual(got, want) {
		t.Errorf("Reach(t.x) on a cycle = %v, want %v", got, want)
	}
}

// Every way of changing a record or the set of records is seen by the next
// question about the edges.
func TestIndexFollowsEveryChange(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	a, b := node("a"), node("b", "t.a")
	mustSet(t, s, a, b)
	expect := func(step, addr string, want ...string) {
		t.Helper()
		if want == nil {
			want = []string{}
		}
		if got := s.Dependents(addr); !reflect.DeepEqual(got, want) {
			t.Errorf("%s: Dependents(%s) = %v, want %v", step, addr, got, want)
		}
	}
	expect("set", "t.a", "t.b")

	mustSet(t, s, node("c", "t.a"))
	expect("a record that is added", "t.a", "t.b", "t.c")

	mustSet(t, s, node("b", "t.c"))
	expect("a record that changes its dependencies", "t.a", "t.c")
	expect("a record that changes its dependencies", "t.c", "t.b")

	// The record that the store holds is edited where it stands, as a rename
	// does, and written again.
	held := s.Get("t.b")
	held.Dependencies[0] = "t.a"
	mustSet(t, s, held)
	expect("a record edited in place", "t.a", "t.b", "t.c")
	expect("a record edited in place", "t.c")

	// A refresh that changes the edges.
	n := *s.Get("t.c")
	n.Dependencies = []string{"t.b"}
	if err := s.Refresh(&n, ""); err != nil {
		t.Fatal(err)
	}
	expect("a refresh", "t.b", "t.c")
	expect("a refresh", "t.a", "t.b")

	if _, err := s.Remove("t.c"); err != nil {
		t.Fatal(err)
	}
	expect("a remove", "t.b")
	if got, want := s.Addresses(), []string{"t.a", "t.b"}; !reflect.DeepEqual(got, want) {
		t.Errorf("Addresses() after the remove = %v, want %v", got, want)
	}
	var listed []string
	for _, r := range s.List() {
		listed = append(listed, r.Address())
	}
	if want := []string{"t.a", "t.b"}; !reflect.DeepEqual(listed, want) {
		t.Errorf("List() after the remove = %v, want %v", listed, want)
	}

	// A store opened over the same records answers the same.
	again, err := Open(s.Dir())
	if err != nil {
		t.Fatal(err)
	}
	if got, want := again.Dependents("t.a"), []string{"t.b"}; !reflect.DeepEqual(got, want) {
		t.Errorf("a reopened store: Dependents(t.a) = %v, want %v", got, want)
	}
}

// The answers of the index are the answers of a pass over every record, for
// graphs of every shape.
func TestIndexAgreesWithAPassOverTheRecords(t *testing.T) {
	rng := rand.New(rand.NewPCG(1, 2))
	for trial := range 30 {
		s, err := Open(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		n := 2 + rng.IntN(25)
		name := func(i int) string { return fmt.Sprintf("n%02d", i) }
		for i := range n {
			var deps []string
			for k := rng.IntN(4); k > 0; k-- {
				// Edges may point anywhere, so cycles and records that are not there occur.
				deps = append(deps, "t."+name(rng.IntN(n+3)))
			}
			mustSet(t, s, node(name(i), deps...))
		}
		// Some records go, and some are written again with other edges.
		for range 3 {
			if _, err := s.Remove("t." + name(rng.IntN(n))); err != nil {
				t.Fatal(err)
			}
			mustSet(t, s, node(name(rng.IntN(n)), "t."+name(rng.IntN(n))))
		}
		for i := range n + 3 {
			addr := "t." + name(i)
			if got, want := s.Dependents(addr), refDependents(s, addr); !reflect.DeepEqual(got, want) {
				t.Fatalf("trial %d: Dependents(%s) = %v, want %v", trial, addr, got, want)
			}
			if got, want := s.Reach(addr), refReach(s, addr); !reflect.DeepEqual(got, want) {
				t.Fatalf("trial %d: Reach(%s) = %v, want %v", trial, addr, got, want)
			}
		}
		var some []string
		for range 4 {
			some = append(some, "t."+name(rng.IntN(n)))
		}
		if got, want := s.ReachOf(some), refReach(s, some...); !reflect.DeepEqual(got, want) {
			t.Fatalf("trial %d: ReachOf(%v) = %v, want %v", trial, some, got, want)
		}
		if !slices.IsSorted(s.Addresses()) || len(s.Addresses()) != s.Len() {
			t.Fatalf("trial %d: Addresses() = %v for %d records", trial, s.Addresses(), s.Len())
		}
	}
}

// What a change reaches is found by one walk of the edges: a hub with thousands
// of dependents costs the number of its dependents, not that number times the
// size of the state.
func TestReachOfAHubIsLinear(t *testing.T) {
	const n = 3000
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	mustSet(t, s, node("hub"))
	for i := range n {
		mustSet(t, s, node(fmt.Sprintf("d%04d", i), "t.hub"))
	}
	start := time.Now()
	got := s.Reach("t.hub")
	took := time.Since(start)
	if len(got) != n {
		t.Fatalf("Reach(t.hub) holds %d nodes, want %d", len(got), n)
	}
	t.Logf("Reach of a hub with %d dependents, with the build of the index: %s", n, took)
	// The walk, with the index built by this first question, takes a few
	// milliseconds. A pass over every record for each node takes most of a second.
	if took > 50*time.Millisecond {
		t.Errorf("Reach of a hub with %d dependents took %s, want less than 50ms", n, took)
	}
	start = time.Now()
	for i := range n {
		_ = s.Dependents(fmt.Sprintf("t.d%04d", i))
	}
	if took := time.Since(start); took > 50*time.Millisecond {
		t.Errorf("%d questions about direct dependents took %s, want less than 50ms", n, took)
	}
	// A chain is as deep as it is long; the walk does not recurse.
	chain, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	mustSet(t, chain, node("c0000"))
	for i := 1; i < n; i++ {
		mustSet(t, chain, node(fmt.Sprintf("c%04d", i), fmt.Sprintf("t.c%04d", i-1)))
	}
	if got := chain.Reach("t.c0000"); len(got) != n-1 {
		t.Errorf("Reach of the start of a chain holds %d nodes, want %d", len(got), n-1)
	}
}

// Writers and readers of the edges run at the same time.
func TestIndexIsSafeUnderConcurrentWrites(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	mustSet(t, s, node("root"))
	var wg sync.WaitGroup
	for w := range 4 {
		wg.Add(2)
		go func() {
			defer wg.Done()
			for i := range 40 {
				if err := s.Set(node(fmt.Sprintf("w%d_%02d", w, i), "t.root"), ""); err != nil {
					t.Error(err)
					return
				}
			}
		}()
		go func() {
			defer wg.Done()
			for range 40 {
				_ = s.Dependents("t.root")
				_ = s.Reach("t.root")
				_ = s.List()
			}
		}()
	}
	wg.Wait()
	if got := len(s.Dependents("t.root")); got != 160 {
		t.Errorf("t.root has %d dependents, want 160", got)
	}
}

// widthBackend holds up every Get for a moment and reports how many are held up
// together.
type widthBackend struct {
	Backend
	local        bool
	inflight, hi atomic.Int64
}

func (b *widthBackend) Local() bool { return b.local }

func (b *widthBackend) Get(ctx context.Context, key string) ([]byte, error) {
	n := b.inflight.Add(1)
	for {
		hi := b.hi.Load()
		if n <= hi || b.hi.CompareAndSwap(hi, n) {
			break
		}
	}
	defer b.inflight.Add(-1)
	time.Sleep(3 * time.Millisecond)
	return b.Backend.Get(ctx, key)
}

// A store reads its records 8 at a time from a backend on this machine and 64
// at a time from one that is not.
func TestOpenReadsWithTheWidthOfTheBackend(t *testing.T) {
	dir := t.TempDir()
	seed, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	for i := range 200 {
		mustSet(t, seed, node(fmt.Sprintf("r%03d", i)))
	}
	for _, c := range []struct {
		local bool
		want  int64
	}{{true, readWidthLocal}, {false, readWidthRemote}} {
		lb, err := NewLocalBackend(dir)
		if err != nil {
			t.Fatal(err)
		}
		b := &widthBackend{Backend: lb, local: c.local}
		s, err := OpenBackend(context.Background(), b)
		if err != nil {
			t.Fatal(err)
		}
		if s.Len() != 200 {
			t.Errorf("local %v: %d records read, want 200", c.local, s.Len())
		}
		if got := b.hi.Load(); got != c.want {
			t.Errorf("local %v: %d reads at the same time, want %d", c.local, got, c.want)
		}
	}
}

// A record that cannot be read stops the open and is named.
func TestOpenNamesTheRecordItCannotRead(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	mustSet(t, s, node("a"), node("b"))
	if err := s.b.Put(context.Background(), nodeKey("t.b"), []byte("{")); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(dir); err == nil || !strings.Contains(err.Error(), "t.b") {
		t.Errorf("Open = %v, want an error that names t.b", err)
	}
}
