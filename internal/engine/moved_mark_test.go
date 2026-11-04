package engine

import (
	"context"
	"testing"

	"github.com/pravo-systems/pravo/internal/config"
)

// A read of a record that $from moves finds the object gone, or changed. The plan stages
// the record at its new address, but the apply moves the record of the backend, so the
// finding must have been written there: the apply that follows the plan, in a new engine
// that reads nothing, creates the object again or corrects it.
func TestARecordThatMovesKeepsWhatThePlanReadOfIt(t *testing.T) {
	for _, tc := range []struct {
		name   string
		break_ func(f *fakeProvider)
		want   Action
	}{
		{"gone", func(f *fakeProvider) { f.vanish("x") }, ActionCreate},
		{"drifted", func(f *fakeProvider) { f.drift("x", "by hand") }, ActionUpdate},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			old := spec{cue: "a: " + thing("x", `content: "one"`) + "\n", resources: []string{"a"}}
			neu := spec{cue: "c: " + thing("x", `content: "one"`) + "\n", resources: []string{"c"}}
			h.apply(old)
			tc.break_(h.fake)
			moving := func(opts Options) *Engine {
				e := h.engine(neu, opts)
				e.Root.Moves = []config.Move{{From: "a", To: "c"}}
				return e
			}
			p := plan(t, moving(Options{Refresh: RefreshAll}))
			if ch := objectChange(t, p, "c"); ch.Action != tc.want {
				t.Fatalf("the reading plan says %s for c, want %s", ch.Action, tc.want)
			}
			// The plain apply that follows is a new process: it has read nothing.
			h.reopen()
			e := moving(Options{})
			p2 := plan(t, e)
			if ch := objectChange(t, p2, "c"); ch.Action != tc.want {
				t.Fatalf("the plain plan says %s for c, want %s", ch.Action, tc.want)
			}
			if _, err := e.Apply(context.Background(), p2); err != nil {
				t.Fatal(err)
			}
			if h.state.Get("a") != nil {
				t.Errorf("a record is left at a")
			}
			if n := h.state.Get("c"); n == nil || n.Gone || n.Marked() {
				t.Errorf("the record of c after the apply: %+v", n)
			}
			h.reopen()
			if got := objectChange(t, plan(t, moving(Options{Refresh: RefreshAll})), "c").Action; got != ActionNoop {
				t.Errorf("a reading plan after the apply says %s for c", got)
			}
		})
	}
}

// objectChange is the change of addr that is not its move.
func objectChange(t *testing.T, p *Plan, addr string) *Change {
	t.Helper()
	for _, ch := range p.Changes {
		if ch.Addr == addr && ch.Action != ActionMove {
			return ch
		}
	}
	return &Change{Addr: addr, Action: ActionNoop}
}
