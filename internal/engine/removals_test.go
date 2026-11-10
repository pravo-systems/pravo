package engine

import (
	"context"
	"strings"
	"testing"

	"github.com/pravo-systems/pravo/internal/state"
)

// removalCases are applies that delete pointers: a destroy, a replacement (the pointer is
// gone while the new object is created), a replacement that creates first (the old object
// is kept under an address of its own and then removed), a resource that left the
// specification, and a rename.
func removalCases(b *boundHarness) map[string]func() *Engine {
	const start = "a: {name: \"a\", id: string | null, content: \"one\"}\n" +
		"b: {name: \"b\", id: string | null, content: a.content}\n" +
		"c: {name: \"c\", id: string | null, $lifecycle: create_before_destroy: true}\n"
	b.apply(spec{cue: start, resources: []string{"a", "b", "c"}})
	return map[string]func() *Engine{
		"destroy": func() *Engine {
			return b.engine(spec{cue: start, resources: []string{"a", "b", "c"}}, Options{Destroy: true, Command: "pravo destroy"})
		},
		"replace": func() *Engine {
			return b.engine(spec{cue: strings.Replace(start, `name: "a"`, `name: "a2"`, 1), resources: []string{"a", "b", "c"}}, Options{Command: "pravo apply"})
		},
		"replace, create first": func() *Engine {
			return b.engine(spec{cue: strings.Replace(start, `name: "c"`, `name: "c2"`, 1), resources: []string{"a", "b", "c"}}, Options{Command: "pravo apply"})
		},
		"orphan": func() *Engine {
			return b.engine(spec{cue: strings.SplitAfter(start, "\n")[0], resources: []string{"a"}}, Options{Command: "pravo apply"})
		},
		"rename": func() *Engine {
			return b.renamed(spec{cue: strings.Replace(strings.ReplaceAll(start, "a.content", "d.content"), "a: {", "d: {", 1), resources: []string{"d", "b", "c"}}, "a", "d")
		},
	}
}

// An apply that deletes pointers says so in the transaction it starts with, so the command
// of someone else that runs in the middle of it, or after it was killed at any of its
// writes, finds no pointer missing: the state of a team does not stop for a destroy that is
// under way, and not for one that died.
func TestAnApplyKilledAtAnyWriteLeavesNoPointerMissing(t *testing.T) {
	for _, name := range []string{"destroy", "replace", "replace, create first", "orphan", "rename"} {
		t.Run(name, func(t *testing.T) {
			probe := newBoundHarness(t)
			mk := removalCases(probe)[name]
			start := probe.crash.seen.Load()
			e := mk()
			if _, err := e.Apply(context.Background(), plan(t, e)); err != nil {
				t.Fatal(err)
			}
			writes := probe.crash.seen.Load() - start
			if writes < 4 {
				t.Fatalf("the apply made %d writes", writes)
			}
			for k := int64(0); k <= writes; k++ {
				b := newBoundHarness(t)
				e := removalCases(b)[name]()
				p := plan(t, e)
				b.crash.left.Store(k)
				_, _ = e.Apply(context.Background(), p)
				b.crash.left.Store(1 << 40)
				b.open() // the next command, of anyone
				if missing, err := b.state.MissingNodes(context.Background(), b.readers); err != nil || len(missing) > 0 {
					t.Fatalf("killed after write %d of %d: missing %v, %v", k, writes, missing, err)
				}
				if err := b.state.CheckLoadedNodes(context.Background(), b.readers); err != nil {
					t.Fatalf("killed after write %d of %d: %v", k, writes, err)
				}
			}
		})
	}
}

// txnWrites counts the times each transaction was written.
type txnWrites struct {
	state.Backend
	n map[string]int
}

func (b *txnWrites) Put(ctx context.Context, key string, data []byte) error {
	if strings.HasPrefix(key, "txn/") {
		b.n[key]++
	}
	return b.Backend.Put(ctx, key, data)
}

// The engine names the removals before the transaction starts: the transaction is written
// when it starts and when it ends, and never again for a pointer that nobody announced.
func TestAnApplyAnnouncesItsRemovalsWithItsStart(t *testing.T) {
	for _, name := range []string{"destroy", "replace", "replace, create first", "orphan"} {
		t.Run(name, func(t *testing.T) {
			b := newBoundHarness(t)
			e := removalCases(b)[name]()
			p := plan(t, e)
			count := &txnWrites{Backend: b.crash.Backend, n: map[string]int{}}
			b.crash.Backend = count
			if _, err := e.Apply(context.Background(), p); err != nil {
				t.Fatal(err)
			}
			if len(count.n) != 1 {
				t.Fatalf("the apply wrote the transactions %v", count.n)
			}
			for key, n := range count.n {
				if n != 2 {
					t.Errorf("%s was written %d times, want 2: at its start and at its end", key, n)
				}
			}
		})
	}
}
