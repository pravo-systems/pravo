package engine

import (
	"context"
	"fmt"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"
)

// fills records the addresses of the tasks whose values an engine queues for the
// configuration.
type fills struct {
	mu   sync.Mutex
	seen map[string]int
}

func watchFills(e *Engine) *fills {
	f := &fills{seen: map[string]int{}}
	e.onFill = func(addr string) {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.seen[addr]++
	}
	return f
}

func (f *fills) addrs() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, 0, len(f.seen))
	for a := range f.seen {
		out = append(out, a)
	}
	sort.Strings(out)
	return out
}

// floorSpec has ten resources that nothing reads, a chain b <- c <- top that
// reads the id of the one before, a resource d that an output reads, and a
// version in the content of top.
func floorSpec(version string) spec {
	var src strings.Builder
	names := []string{}
	for i := 0; i < 10; i++ {
		name := fmt.Sprintf("a%d", i)
		names = append(names, name)
		fmt.Fprintf(&src, "%s: %s\n", name, thing(name, ""))
	}
	fmt.Fprintf(&src, "b: %s\n", thing("b", ""))
	fmt.Fprintf(&src, "c: %s\n", thing("c", `content: "\(b.id)"`))
	fmt.Fprintf(&src, "top: %s\n", thing("top", `content: "\(c.id)-`+version+`"`))
	fmt.Fprintf(&src, "d: %s\n", thing("d", ""))
	src.WriteString("out: {value: d.id}\n")
	return spec{cue: src.String(), resources: append(names, "b", "c", "top", "d"), outputs: []string{"out"}}
}

// The values of a node that the plan leaves alone are queued for the
// configuration only when something reads them: a node, the configuration of a
// provider or an output. A node that the plan reaches always queues its values,
// because they are checked against the specification. What the readers see is
// the record.
func TestSkippedNodesAreFilledOnlyWhenRead(t *testing.T) {
	for _, mode := range []struct {
		name string
		opts Options
	}{{"age", Options{}}, {"no refresh", Options{Refresh: RefreshNone}}} {
		t.Run(mode.name, func(t *testing.T) {
			h := newHarness(t)
			h.apply(floorSpec("1"))
			e := h.engine(floorSpec("2"), mode.opts)
			f := watchFills(e)
			p := plan(t, e)

			var reached, skipped, readSkipped []string
			for _, ch := range p.Changes {
				switch {
				case !ch.Skipped:
					reached = append(reached, ch.Addr)
				default:
					skipped = append(skipped, ch.Addr)
					if e.readers[ch.Addr] {
						readSkipped = append(readSkipped, ch.Addr)
					}
				}
			}
			sort.Strings(reached)
			sort.Strings(readSkipped)
			if want := []string{"top"}; !reflect.DeepEqual(reached, want) {
				t.Fatalf("the plan reaches %v, want %v", reached, want)
			}
			if len(skipped) != 13 {
				t.Errorf("%d nodes are skipped, want 13", len(skipped))
			}
			// b and c are read by the chain, d by the output, and nothing reads a0 to a9.
			if want := []string{"b", "c", "d"}; !reflect.DeepEqual(readSkipped, want) {
				t.Errorf("nodes that something reads among the skipped: %v, want %v", readSkipped, want)
			}
			want := append(append([]string{}, reached...), readSkipped...)
			sort.Strings(want)
			if got := f.addrs(); !reflect.DeepEqual(got, want) {
				t.Errorf("queued the values of %v, want the reached nodes and the skipped nodes that are read: %v", got, want)
			}
			for addr, n := range f.seen {
				if n != 1 {
					t.Errorf("%s queued its values %d times, want once", addr, n)
				}
			}
			// The reader of a skipped node sees its record.
			top := changeOf(t, p, "top")
			if want := recordedID(t, h, "c") + "-2"; top.Action != ActionUpdate || !strings.Contains(describe(top), want) {
				t.Errorf("the plan of top lacks %q: %s", want, describe(top))
			}
			if got, err := outputJSON(p.Outputs["out"]); err != nil || string(got) != `"`+recordedID(t, h, "d")+`"` {
				t.Errorf("out = %s (%v), want the id of d", got, err)
			}
		})
	}
}

// A plan that reads nothing starts no provider for a node that did not change,
// whatever the mode, and decodes the record of a node only when something reads
// it.
func TestPlanWithoutRefreshAsksNoProviderForUnchangedNodes(t *testing.T) {
	const n = 120
	v1 := func(int) string { return "v1" }
	h := newHarness(t)
	h.apply(manySpec(n, v1))
	h.fake.resetCalls()
	h.backend.Reset()

	p := plan(t, h.engine(manySpec(n, v1), Options{Refresh: RefreshNone}))
	if p.HasChanges() {
		t.Error("a plan of an unchanged specification has changes")
	}
	if want := (Stats{Total: n, Skipped: n}); p.Stats != want {
		t.Errorf("stats = %+v, want %+v: nothing was read, so no record is early or late", p.Stats, want)
	}
	expectCalls(t, h, map[string]int{"start": 0, "UpgradeResourceState": 0, "ReadResource": 0, "PlanResourceChange": 0})
	if w := h.backend.Writes(); w != 0 {
		t.Errorf("a plan that reads nothing wrote %d objects", w)
	}
	if got, want := reasonsOf(p, "r007"), []string{"refresh skipped: -refresh=false was given", "skipped: nothing the provider saw has changed"}; !reflect.DeepEqual(got, want) {
		t.Errorf("reasons of an unchanged node: %q, want %q", got, want)
	}

	one := func(i int) string {
		if i == 55 {
			return "v2"
		}
		return "v1"
	}
	p = plan(t, h.engine(manySpec(n, one), Options{Refresh: RefreshNone}))
	if want := (Stats{Total: n, Reached: 1, Skipped: n - 1}); p.Stats != want {
		t.Errorf("stats = %+v, want %+v", p.Stats, want)
	}
	expectCalls(t, h, map[string]int{"start": 1, "UpgradeResourceState": 1, "ReadResource": 0, "PlanResourceChange": 1})
	if ch := changeOf(t, p, "r055"); ch.Action != ActionUpdate {
		t.Errorf("r055 is %s, want update", ch.Action)
	}
}

// The graph is worked out once for an engine, whatever the engine does with it:
// plan, apply, walk again, apply a saved plan or destroy.
func TestGraphIsBuiltOncePerProcess(t *testing.T) {
	t.Run("plan and apply", func(t *testing.T) {
		h := newHarness(t)
		e := h.engine(floorSpec("1"), Options{})
		p := plan(t, e)
		if _, err := e.Apply(context.Background(), p); err != nil {
			t.Fatal(err)
		}
		if e.graphBuilds != 1 {
			t.Errorf("the graph was built %d times by a plan and an apply, want once", e.graphBuilds)
		}
	})

	t.Run("a plan that walks again", func(t *testing.T) {
		h := newHarness(t)
		h.apply(chain(2, "1"))
		h.fake.drift("c1", "changed")
		e := h.engine(chain(2, "2"), Options{})
		plan(t, e)
		if e.graphBuilds != 1 {
			t.Errorf("the graph was built %d times by a plan that walked twice, want once", e.graphBuilds)
		}
		if e.settles < 2 {
			t.Errorf("the plan unified fills %d times; it was meant to walk again", e.settles)
		}
	})

	t.Run("a saved plan", func(t *testing.T) {
		h := newHarness(t)
		h.apply(floorSpec("1"))
		e := h.engine(floorSpec("2"), Options{})
		p := plan(t, e)
		file := filepath.Join(t.TempDir(), "p.plan")
		if err := e.SavePlan(file, p, nil, false); err != nil {
			t.Fatal(err)
		}
		pf, err := ReadPlanFile(file)
		if err != nil {
			t.Fatal(err)
		}
		next := h.engine(floorSpec("2"), Options{Refresh: RefreshNone})
		loaded, err := next.LoadPlan(pf)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := next.ApplySaved(context.Background(), loaded); err != nil {
			t.Fatal(err)
		}
		if next.graphBuilds != 1 {
			t.Errorf("the graph was built %d times to load and apply a saved plan, want once", next.graphBuilds)
		}
	})

	t.Run("destroy", func(t *testing.T) {
		h := newHarness(t)
		h.apply(floorSpec("1"))
		e := h.engine(floorSpec("1"), Options{Destroy: true})
		p := plan(t, e)
		if _, err := e.Apply(context.Background(), p); err != nil {
			t.Fatal(err)
		}
		if e.graphBuilds != 1 {
			t.Errorf("the graph was built %d times by a destroy, want once", e.graphBuilds)
		}
	})

	// The reads of the outputs are worked out once too: the check of the outputs
	// before the plan, the graph and the destroy of a part of the state share them.
	t.Run("outputs", func(t *testing.T) {
		h := newHarness(t)
		e := h.engine(floorSpec("1"), Options{})
		if err := e.checkOutputs(); err != nil {
			t.Fatal(err)
		}
		first := reflect.ValueOf(e.outputReads).Pointer()
		if _, err := e.buildGraph(); err != nil {
			t.Fatal(err)
		}
		if reflect.ValueOf(e.outputDeps).Pointer() != first {
			t.Error("the graph worked out the reads of the outputs again")
		}
		if got, want := e.outputDeps["out"], []string{"d"}; !reflect.DeepEqual(got, want) {
			t.Errorf("out reads %v, want %v", got, want)
		}
		if !e.readers["d"] || e.readers["a0"] {
			t.Errorf("readers: d %v, a0 %v; want d read by the output and a0 by nothing", e.readers["d"], e.readers["a0"])
		}
	})
}

// The blast radius of a plan is the number of distinct nodes its changes touch:
// the changed nodes and everything that depends on them, once each.
func TestBlastRadiusCountsEveryNodeTheChangesReach(t *testing.T) {
	src := func(a, e, c string) spec {
		return spec{
			cue: "a: " + thing("a", `content: "`+a+`"`) + "\n" +
				"b: " + thing("b", `content: "\(a.id)"`) + "\n" +
				"c: " + thing("c", `content: "\(b.id)`+c+`"`) + "\n" +
				"d: " + thing("d", `content: "\(a.id)"`) + "\n" +
				"e: " + thing("e", `content: "`+e+`"`) + "\n" +
				"f: " + thing("f", `content: "\(e.id)"`) + "\n" +
				"g: " + thing("g", "") + "\n",
			resources: []string{"a", "b", "c", "d", "e", "f", "g"},
		}
	}
	h := newHarness(t)
	h.apply(src("1", "1", ""))
	for _, c := range []struct {
		name string
		spec spec
		want int
	}{
		{"nothing", src("1", "1", ""), 0},
		{"a hub", src("2", "1", ""), 4},       // a, b, c, d
		{"two trees", src("2", "2", ""), 6},   // and e, f
		{"a leaf", src("1", "1", "-late"), 1}, // c
	} {
		e := h.engine(c.spec, Options{})
		p := plan(t, e)
		if got := e.NewView(p, ViewOptions{}).Summary.BlastRadius; got != c.want {
			t.Errorf("%s: blast radius %d, want %d", c.name, got, c.want)
		}
	}
}
