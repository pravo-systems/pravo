package engine

import (
	"context"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

func TestConsistent(t *testing.T) {
	obj := tftypes.Object{AttributeTypes: map[string]tftypes.Type{
		"id": tftypes.String, "n": tftypes.Number, "tags": tftypes.Map{ElementType: tftypes.String},
	}}
	mk := func(id any, n any, tags map[string]tftypes.Value) tftypes.Value {
		var tv tftypes.Value
		if tags == nil {
			tv = tftypes.NewValue(tftypes.Map{ElementType: tftypes.String}, nil)
		} else {
			tv = tftypes.NewValue(tftypes.Map{ElementType: tftypes.String}, tags)
		}
		return tftypes.NewValue(obj, map[string]tftypes.Value{
			"id": tftypes.NewValue(tftypes.String, id), "n": tftypes.NewValue(tftypes.Number, n), "tags": tv,
		})
	}
	saved := mk(tftypes.UnknownValue, big.NewFloat(3), map[string]tftypes.Value{"a": tftypes.NewValue(tftypes.String, "x")})
	if !consistent(saved, mk("abc", big.NewFloat(3), map[string]tftypes.Value{"a": tftypes.NewValue(tftypes.String, "x")})) {
		t.Error("resolving an unknown must be consistent")
	}
	if consistent(saved, mk("abc", big.NewFloat(4), map[string]tftypes.Value{"a": tftypes.NewValue(tftypes.String, "x")})) {
		t.Error("changing a known value must be inconsistent")
	}
	if consistent(saved, mk("abc", big.NewFloat(3), nil)) {
		t.Error("nulling a known map must be inconsistent")
	}
	if !consistent(tftypes.NewValue(obj, tftypes.UnknownValue), mk("a", big.NewFloat(1), nil)) {
		t.Error("a wholly unknown saved value accepts anything")
	}
}

func TestSubsetClosure(t *testing.T) {
	plan := &Plan{Changes: []*Change{
		{Addr: "a", Action: ActionCreate},
		{Addr: "b", Action: ActionCreate, Deps: []string{"a"}},
		{Addr: "c", Action: ActionNoop, Skipped: true},
		{Addr: "d", Action: ActionUpdate, Deps: []string{"c", "b"}},
		{Addr: "e", Action: ActionCreate, Deps: []string{"a"}, Applied: true},
		{Addr: "f", Action: ActionCreate, Deps: []string{"e"}},
	}}
	if _, err := plan.Subset([]string{"d"}); err == nil {
		t.Fatal("d alone is not closed")
	} else if nc, ok := err.(*ErrNotClosed); !ok || len(nc.Missing) != 2 || nc.Closure[0] != "a" {
		t.Fatalf("unexpected closure error: %v", err)
	}
	sub, err := plan.Subset([]string{"a", "b", "d"})
	if err != nil || len(sub.Changes) != 3 {
		t.Fatalf("closed selection rejected: %v", err)
	}
	if _, err := plan.Subset([]string{"f"}); err != nil {
		t.Errorf("a dependency that was already applied must not be required: %v", err)
	}
	if _, err := plan.Subset([]string{"e"}); err == nil {
		t.Error("an applied change cannot be selected again")
	}
	if _, err := plan.Subset([]string{"zzz"}); err == nil {
		t.Error("unknown address must be rejected")
	}
	del := &Plan{Changes: []*Change{
		{Addr: "x", Action: ActionDelete},
		{Addr: "y", Action: ActionDelete, Deps: []string{"x"}},
	}}
	if _, err := del.Subset([]string{"x"}); err == nil {
		t.Error("deleting x needs its dependent y deleted first")
	}
	if _, err := del.Subset([]string{"y"}); err != nil {
		t.Errorf("deleting the dependent alone is closed: %v", err)
	}
	if rem := plan.Remaining(); len(rem.Changes) != 5 {
		t.Errorf("Remaining = %d changes", len(rem.Changes))
	}

	// A data source whose read returned something other than its record is not a
	// pending change, but the nodes that read it are planned from what it
	// returned: a selection of one of them needs it too.
	data := &Plan{Changes: []*Change{
		{Addr: "src", Mode: "data", Action: ActionRead},
		{Addr: "kept", Mode: "data", Action: ActionRead, Kept: true},
		{Addr: "w", Mode: "managed", Action: ActionUpdate, Deps: []string{"src", "kept"}},
	}}
	if _, err := data.Subset([]string{"w"}); err == nil {
		t.Error("w alone does not hold: it reads src, which returned new data")
	} else if nc, ok := err.(*ErrNotClosed); !ok || len(nc.Missing) != 1 || nc.Missing[0] != "src" {
		t.Errorf("unexpected closure error: %v", err)
	}
	if sub, err := data.Subset([]string{"src", "w"}); err != nil || sub.Partial {
		t.Errorf("src and w: error %v, partial %v", err, sub != nil && sub.Partial)
	}
	if sub, err := data.Subset([]string{"src"}); err != nil || !sub.Partial {
		t.Errorf("src alone leaves w: error %v, partial %v", err, sub != nil && sub.Partial)
	}
}

// planned makes the plan of a specification, saves it, and returns the file.
func planned(t *testing.T, h *harness, s spec) (string, *PlanFile) {
	t.Helper()
	e := h.engine(s, Options{})
	p := plan(t, e)
	file := filepath.Join(t.TempDir(), "p.plan")
	if err := e.SavePlan(file, p, nil, false); err != nil {
		t.Fatal(err)
	}
	pf, err := ReadPlanFile(file)
	if err != nil {
		t.Fatal(err)
	}
	return file, pf
}

// staleReasons loads the plan file for the apply of s and returns why it is stale.
func staleReasons(t *testing.T, h *harness, s spec, pf *PlanFile, only ...string) []string {
	t.Helper()
	e := h.engine(s, Options{Refresh: RefreshNone})
	_, err := e.LoadPlanFor(pf, only)
	if err == nil {
		return nil
	}
	var stale *ErrStalePlan
	if !asStale(err, &stale) {
		t.Fatalf("load: %v", err)
	}
	return stale.Reasons
}

// A saved plan is stale when a node it writes, or a record it reads, was written
// since: not when an apply wrote a node the plan has nothing to do with.
func TestSavedPlanStaleness(t *testing.T) {
	v1 := func(int) string { return "v1" }
	changed := func(which int) func(int) string {
		return func(i int) string {
			if i == which {
				return "v2"
			}
			return "v1"
		}
	}
	two := func(in, w string) spec {
		return spec{
			cue: "in: " + thing("in", fmt.Sprintf("content: %q", in)) + "\n" +
				"w: " + thing("w", fmt.Sprintf(`content: "\(in.name)-%s"`, w)) + "\n",
			resources: []string{"in", "w"},
		}
	}

	t.Run("an unrelated write", func(t *testing.T) {
		h := newHarness(t)
		h.apply(manySpec(3, v1))
		s := manySpec(3, changed(0))
		_, pf := planned(t, h, s)
		h.apply(manySpec(3, changed(2))) // another apply writes r002
		if got := staleReasons(t, h, s, pf); got != nil {
			t.Fatalf("the plan is stale after an apply that wrote a node it does not touch: %v", got)
		}
		e := h.engine(s, Options{Refresh: RefreshNone})
		loaded, err := e.LoadPlan(pf)
		if err != nil {
			t.Fatal(err)
		}
		h.fake.resetCalls()
		if _, err := e.ApplySaved(context.Background(), loaded); err != nil {
			t.Fatal(err)
		}
		if got := recordedContent(t, h, "r000"); got != "v2" {
			t.Errorf("r000 holds %q after the apply", got)
		}
		expectCalls(t, h, map[string]int{"ReadResource": 0, "ApplyResourceChange": 1})
	})

	t.Run("a write to a record the plan reads", func(t *testing.T) {
		h := newHarness(t)
		h.apply(two("a", "1"))
		s := two("a", "2")
		_, pf := planned(t, h, s)
		if len(pf.Changes) != 1 || len(pf.Inputs) != 1 {
			t.Fatalf("the file holds %d changes and inputs %v; want w and in", len(pf.Changes), pf.Inputs)
		}
		h.apply(two("b", "1")) // in is written, w is not
		got := staleReasons(t, h, s, pf)
		if len(got) != 1 || got[0] != "in changed in state (revision 2, plan expected 1); the plan reads it" {
			t.Errorf("reasons = %q", got)
		}
	})

	t.Run("a write to a node the plan writes", func(t *testing.T) {
		h := newHarness(t)
		h.apply(two("a", "1"))
		s := two("a", "2")
		_, pf := planned(t, h, s)
		h.apply(two("a", "3"))
		got := staleReasons(t, h, s, pf)
		if len(got) != 1 || got[0] != "w changed in state (revision 2, plan expected 1)" {
			t.Errorf("reasons = %q", got)
		}
	})

	t.Run("a file from a newer build", func(t *testing.T) {
		h := newHarness(t)
		h.apply(manySpec(1, v1))
		file, pf := planned(t, h, manySpec(1, changed(0)))
		pf.Format = PlanFileFormat + 1
		data, _ := indentJSON(pf)
		if err := os.WriteFile(file, data, 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := ReadPlanFile(file); err == nil || !strings.Contains(err.Error(), "written by a newer pravo") {
			t.Errorf("read: %v", err)
		}
	})
}

// A saved plan whose only change is an output is applied once: it records the
// output, and the file then has nothing left. The same holds for a plan that only
// removes the record of an output.
func TestSavedPlanThatOnlyChangesAnOutputIsApplied(t *testing.T) {
	mk := func(output string, keep bool) spec {
		cue := "a: " + thing("a", "") + "\nout: {value: " + output + "}\n"
		outputs := []string{"out"}
		if keep {
			cue += "kept: {value: \"constant\"}\n"
			outputs = append(outputs, "kept")
		}
		return spec{cue: cue, resources: []string{"a"}, outputs: outputs}
	}
	left := func(t *testing.T, h *harness, s spec, pf *PlanFile) bool {
		t.Helper()
		loaded, err := h.engine(s, Options{Refresh: RefreshNone}).LoadPlan(pf)
		if err != nil {
			t.Fatal(err)
		}
		return pf.LeftToApply(loaded.Remaining())
	}
	applyFile := func(t *testing.T, h *harness, s spec, file string, pf *PlanFile) *ApplyResult {
		t.Helper()
		e := h.engine(s, Options{Refresh: RefreshNone})
		loaded, err := e.LoadPlan(pf)
		if err != nil {
			t.Fatal(err)
		}
		if loaded.HasChanges() {
			t.Fatalf("the plan has changes to a provider: %v", loaded.Changes)
		}
		res, err := e.ApplySaved(context.Background(), loaded)
		if err != nil {
			t.Fatal(err)
		}
		if err := MarkApplied(file, pf, nil, res.Txn); err != nil {
			t.Fatal(err)
		}
		return res
	}

	t.Run("an output changed", func(t *testing.T) {
		h := newHarness(t)
		h.apply(mk(`"x"`, true))
		s := mk(`"y-\(a.id)"`, true)
		file, pf := planned(t, h, s)
		if !left(t, h, s, pf) {
			t.Fatal("the plan changes only the output, and nothing is left to apply")
		}
		h.fake.resetCalls()
		res := applyFile(t, h, s, file, pf)
		expectCalls(t, h, map[string]int{"ReadResource": 0, "ApplyResourceChange": 0})
		if res.Txn == "" || !strings.HasPrefix(string(h.state.Outputs()["out"]), `"y-`) || string(h.state.Outputs()["kept"]) != `"constant"` {
			t.Errorf("transaction %q, outputs %v", res.Txn, h.state.Outputs())
		}
		pf, err := ReadPlanFile(file)
		if err != nil {
			t.Fatal(err)
		}
		if left(t, h, s, pf) {
			t.Error("the output was applied, and the file still has something left to apply")
		}
	})

	t.Run("an output was removed", func(t *testing.T) {
		h := newHarness(t)
		h.apply(mk(`"x"`, true))
		s := mk(`"x"`, false)
		file, pf := planned(t, h, s)
		if !left(t, h, s, pf) {
			t.Fatal("the plan removes an output, and nothing is left to apply")
		}
		applyFile(t, h, s, file, pf)
		if _, ok := h.state.Outputs()["kept"]; ok || len(h.state.Outputs()) != 1 {
			t.Errorf("outputs after the apply: %v", h.state.Outputs())
		}
		pf, err := ReadPlanFile(file)
		if err != nil {
			t.Fatal(err)
		}
		if left(t, h, s, pf) {
			t.Error("the output was removed, and the file still has something left to apply")
		}
	})

	t.Run("a plan with nothing to write", func(t *testing.T) {
		h := newHarness(t)
		h.apply(mk(`"x"`, true))
		s := mk(`"x"`, true)
		_, pf := planned(t, h, s)
		if left(t, h, s, pf) {
			t.Error("the plan changes nothing, and something is left to apply")
		}
	})

	t.Run("a plan with changes that was applied", func(t *testing.T) {
		h := newHarness(t)
		h.apply(mk(`"x"`, true))
		s := spec{cue: "a: " + thing("a", `content: "new"`) + "\nout: {value: a.content}\nkept: {value: \"constant\"}\n", resources: []string{"a"}, outputs: []string{"out", "kept"}}
		file, pf := planned(t, h, s)
		e := h.engine(s, Options{Refresh: RefreshNone})
		loaded, err := e.LoadPlan(pf)
		if err != nil {
			t.Fatal(err)
		}
		if !pf.LeftToApply(loaded.Remaining()) {
			t.Fatal("the plan changes a resource, and nothing is left to apply")
		}
		res, err := e.ApplySaved(context.Background(), loaded)
		if err != nil {
			t.Fatal(err)
		}
		if err := MarkApplied(file, pf, []string{"a"}, res.Txn); err != nil {
			t.Fatal(err)
		}
		pf, err = ReadPlanFile(file)
		if err != nil {
			t.Fatal(err)
		}
		if left(t, h, s, pf) {
			t.Error("the plan was applied whole, and the file still has something left to apply")
		}
	})
}

func TestLeftToApply(t *testing.T) {
	change := &Change{Addr: "a", Mode: "managed", Action: ActionCreate}
	applied := &Change{Addr: "b", Mode: "managed", Action: ActionCreate, Applied: true}
	for _, c := range []struct {
		name string
		pf   *PlanFile
		plan *Plan
		want bool
	}{
		{"a change to a provider", &PlanFile{}, &Plan{Changes: []*Change{change}}, true},
		{"nothing at all", &PlanFile{}, &Plan{}, false},
		{"an output to record", &PlanFile{}, &Plan{EvalOutputs: []string{"o"}}, true},
		{"an output to remove", &PlanFile{}, &Plan{DropOutputs: []string{"o"}}, true},
		{"an output recorded by an apply from the file", &PlanFile{ApplyTxn: "T1"}, &Plan{EvalOutputs: []string{"o"}}, false},
		{"an output after an applied change", &PlanFile{Changes: []savedChange{{Addr: "b", Applied: true}}}, &Plan{Changes: []*Change{applied}, EvalOutputs: []string{"o"}}, false},
		{"an output of a destroy", &PlanFile{}, &Plan{Destroy: true, EvalOutputs: []string{"o"}}, false},
	} {
		if got := c.pf.LeftToApply(c.plan.Remaining()); got != c.want {
			t.Errorf("%s: left to apply = %v, want %v", c.name, got, c.want)
		}
	}
}

// A selection of a plan is judged by the nodes it writes. Another apply may have
// written a node the selection leaves out; the rest of the plan finds it moved.
func TestSavedPlanSelectionStaleness(t *testing.T) {
	v1 := func(int) string { return "v1" }
	v2 := func(int) string { return "v2" }
	h := newHarness(t)
	h.apply(manySpec(2, v1))
	s := manySpec(2, v2)
	x, xf := planned(t, h, s)
	y, yf := planned(t, h, s)
	applyPart := func(file string, pf *PlanFile, only ...string) {
		t.Helper()
		e := h.engine(s, Options{Refresh: RefreshNone})
		loaded, err := e.LoadPlanFor(pf, only)
		if err != nil {
			t.Fatalf("%s %v: %v", file, only, err)
		}
		sub, err := loaded.Subset(only)
		if err != nil {
			t.Fatal(err)
		}
		res, err := e.ApplySaved(context.Background(), sub)
		if err != nil {
			t.Fatal(err)
		}
		if err := MarkApplied(file, pf, only, res.Txn); err != nil {
			t.Fatal(err)
		}
	}

	applyPart(x, xf, "r001")
	if got := staleReasons(t, h, s, yf); len(got) != 1 || !strings.HasPrefix(got[0], "r001 changed in state") {
		t.Errorf("the whole of y after x wrote r001: %q", got)
	}
	// y.plan --only r000 does not mind r001.
	applyPart(y, yf, "r000")
	// The rest of y does.
	yf, err := ReadPlanFile(y)
	if err != nil {
		t.Fatal(err)
	}
	got := staleReasons(t, h, s, yf)
	if len(got) != 1 || got[0] != "r001 changed in state (revision 2, plan expected 1)" {
		t.Errorf("the rest of y: %q", got)
	}
}

// A plan that failed halfway is never resumed: the nodes it wrote moved.
func TestSavedPlanThatFailedIsStale(t *testing.T) {
	h := newHarness(t)
	h.apply(manySpec(2, func(int) string { return "v1" }))
	s := manySpec(2, func(int) string { return "v2" })
	_, pf := planned(t, h, s)
	h.fake.partial = map[string]bool{"r001": true}
	e := h.engine(s, Options{Refresh: RefreshNone})
	loaded, err := e.LoadPlan(pf)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.ApplySaved(context.Background(), loaded); err == nil {
		t.Fatal("the apply of a provider that fails succeeded")
	}
	h.fake.partial = nil
	if got := staleReasons(t, h, s, pf); len(got) == 0 {
		t.Error("a plan whose apply failed halfway can be applied again")
	}
}
