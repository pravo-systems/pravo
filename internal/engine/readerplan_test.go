package engine

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/pravo-systems/pravo/internal/state"
)

// A reader refreshes what a plan reads and cannot write it: the state of a reader is
// an overlay. The plan that it stores is applied by a writer on the state itself.
func TestStoredPlanOfAReaderAppliesOnTheState(t *testing.T) {
	ctx := context.Background()
	mk := func(tag string) spec {
		return spec{
			cue: "import \"encoding/json\"\n\n" +
				"dns: " + thing("dns", "") + "\n" +
				"db: " + thing("db", `content: "fixed"`) + "\n" +
				"top: " + thing("top", `content: json.Marshal([dns.id, "`+tag+`"])`) + "\n",
			resources: []string{"dns", "db", "top"},
		}
	}
	h := newHarness(t)
	h.apply(mk("1"))
	dnsRev, dbRev := h.revision("dns"), h.revision("db")
	// The database changes behind pravo's back, as a managed database does.
	h.fake.drift("db", "changed")

	writer := h.state
	reader, err := state.OpenBackend(ctx, state.OverlayBackend(h.backend, "plans"))
	if err != nil {
		t.Fatal(err)
	}
	h.state = reader
	pe := h.engine(mk("2"), Options{Refresh: RefreshAll})
	file := filepath.Join(t.TempDir(), "p.plan")
	if err := pe.SavePlan(file, plan(t, pe), nil, false); err != nil {
		t.Fatal(err)
	}
	pf, err := ReadPlanFile(file)
	if err != nil {
		t.Fatal(err)
	}
	h.state = writer
	if got := changeOf(t, mustLoad(t, h, mk("2"), pf), "db"); !got.Refreshed || !got.Drifted {
		t.Fatalf("db in the plan: refreshed %v, drifted %v, want both", got.Refreshed, got.Drifted)
	}
	if h.revision("dns") != dnsRev || h.revision("db") != dbRev {
		t.Fatalf("the plan of a reader wrote to the state: dns %d, db %d", h.revision("dns"), h.revision("db"))
	}

	e := h.engine(mk("2"), Options{Refresh: RefreshNone})
	loaded, err := e.LoadPlan(pf)
	if err != nil {
		t.Fatalf("the plan of a reader is stale on the state it was made against: %v", err)
	}
	if _, err := e.ApplySaved(ctx, loaded.Remaining()); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if got := recordedContent(t, h, "db"); got != "fixed" {
		t.Errorf("db holds %q: the drift is not undone", got)
	}
	if got := recordedContent(t, h, "top"); got == "" {
		t.Error("top was not applied")
	}
}
