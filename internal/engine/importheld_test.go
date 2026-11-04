package engine

import (
	"context"
	"strings"
	"testing"

	"github.com/pravo-systems/pravo/internal/config"
)

// An $import of an object that another record holds is refused with the holder's address, and
// so are two $import of one id in one plan; a distinct id imports.
func TestImportOfAnObjectAnotherRecordHolds(t *testing.T) {
	setup := func() (*harness, spec) {
		h := newHarness(t)
		h.fake.fixedID = "X"
		h.apply(spec{cue: "a: " + thing("obj-X", `content: "c"`) + "\n", resources: []string{"a"}})
		h.fake.fixedID = ""
		h.fake.canImport("X", "c")
		h.fake.canImport("Y", "c")
		return h, spec{}
	}
	planOf := func(h *harness, imports map[string]string) error {
		var cue strings.Builder
		cue.WriteString("a: " + thing("obj-X", `content: "c"`) + "\n")
		names := []string{"a"}
		for n, id := range imports {
			cue.WriteString(n + ": " + thing("obj-"+id, `content: "c"`) + "\n")
			names = append(names, n)
		}
		e := h.engine(spec{cue: cue.String(), resources: names}, Options{})
		for n, id := range imports {
			e.Root.Imports[n] = &config.Import{Addr: n, ID: id}
		}
		_, err := e.Plan(context.Background())
		return err
	}
	t.Run("held by a record", func(t *testing.T) {
		h, _ := setup()
		err := planOf(h, map[string]string{"b": "X"})
		if err == nil || !strings.Contains(err.Error(), "already managed as a") {
			t.Fatalf("plan: %v", err)
		}
	})
	t.Run("two imports of one id", func(t *testing.T) {
		h, _ := setup()
		err := planOf(h, map[string]string{"x": "Y", "y": "Y"})
		if err == nil || !strings.Contains(err.Error(), "already managed as") {
			t.Fatalf("plan: %v", err)
		}
	})
	t.Run("distinct id", func(t *testing.T) {
		h, _ := setup()
		if err := planOf(h, map[string]string{"b": "Y"}); err != nil {
			t.Fatal(err)
		}
	})
}

// Two objects with one id (a content hash) and different attributes are two objects.
func TestHeldByTellsTwoObjectsOfOneIDApart(t *testing.T) {
	h := newHarness(t)
	h.fake.fixedID = "H"
	h.apply(spec{cue: "one: " + thing("one", `content: "same"`) + "\ntwo: " + thing("two", `content: "same"`) + "\n", resources: []string{"one", "two"}})
	if got := HeldBy(h.state, h.state.Get("one")); got != "" {
		t.Errorf("one is held by %q although two differs in name", got)
	}
}

// A holder with an argument the read does not return (here "pin" is set on the record) still
// holds the object; so does a deposed record of a replacement whose destroy failed.
func TestImportHeldByRecordWithConfigOnlyAttributeOrDeposed(t *testing.T) {
	importB := func(h *harness, id, name string) error {
		h.fake.mu.Lock()
		h.fake.importNames = map[string]string{id: name}
		h.fake.mu.Unlock()
		h.fake.canImport(id, "c")
		h.reopen()
		e := h.engine(spec{cue: "b: " + thing(name, `content: "c"`) + "\n", resources: []string{"b"}}, Options{})
		e.Root.Imports["b"] = &config.Import{Addr: "b", ID: id}
		_, err := e.Plan(context.Background())
		return err
	}
	t.Run("config only attribute", func(t *testing.T) {
		h := newHarness(t)
		h.apply(spec{cue: "a: " + thing("a", `content: "c", pin: "p"`) + "\n", resources: []string{"a"}})
		id := recordID(t, thingRecord(t, h, "a"))
		if err := importB(h, id, "a"); err == nil || !strings.Contains(err.Error(), "already managed as a") {
			t.Fatalf("plan: %v", err)
		}
	})
	t.Run("deposed holder", func(t *testing.T) {
		h := newHarness(t)
		mk := func(name string) spec {
			return spec{cue: "a: " + thing(name, `content: "c", $lifecycle: create_before_destroy: true`) + "\n", resources: []string{"a"}}
		}
		h.apply(mk("one"))
		oldID := recordID(t, thingRecord(t, h, "a"))
		h.fake.refuseDestroy("one")
		e := h.engine(mk("two"), Options{})
		if _, err := e.Apply(context.Background(), mustPlan(t, e)); err == nil {
			t.Fatal("apply succeeded")
		}
		err := importB(h, oldID, "one")
		if err == nil || !strings.Contains(err.Error(), "already managed as") {
			t.Fatalf("plan: %v", err)
		}
	})
}
