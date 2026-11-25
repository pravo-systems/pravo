package engine

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// threeThings is a specification of three resources: b waits for a, and c is on its own, so
// that an apply that walks them in order has changed a before it reaches the others.
func threeThings(content string) spec {
	return spec{
		cue: "a: " + thing("a", `content: "a-`+content+`"`) + "\n" +
			"b: " + thing("b", `content: "b-\(a.id)-`+content+`"`) + "\n" +
			"c: " + thing("c", `content: "c-`+content+`"`) + "\n",
		resources: []string{"a", "b", "c"},
	}
}

// savedChain applies the chain, plans the change of its content, saves the plan and
// loads it for an apply.
func savedChain(t *testing.T, h *harness) (*Engine, *Plan) {
	t.Helper()
	h.apply(threeThings("1"))
	s := threeThings("2")
	_, pf := planned(t, h, s)
	e := h.engine(s, Options{Refresh: RefreshNone})
	loaded, err := e.LoadPlan(pf)
	if err != nil {
		t.Fatal(err)
	}
	return e, loaded.Remaining()
}

func wantStale(t *testing.T, err error) *ErrStalePlan {
	t.Helper()
	var coded *CodedError
	var stale *ErrStalePlan
	if !errors.As(err, &coded) || coded.Code != ExitStale || !errors.As(err, &stale) {
		t.Fatalf("error = %v, want exit code 3 and a stale plan", err)
	}
	return stale
}

// A saved plan in which one resource would be planned differently by now applies
// nothing: not the resource that is first in the walk either. Before it, the apply
// changed a and b and stopped at c.
func TestSavedApplyOfAPlanWithOneDriftedResourceChangesNothing(t *testing.T) {
	h := newHarness(t)
	e, p := savedChain(t, h)
	h.fake.replan("c", "other")
	h.fake.resetCalls()
	res, err := e.ApplySaved(context.Background(), p)
	stale := wantStale(t, err)
	if len(stale.Reasons) != 1 || !strings.HasPrefix(stale.Reasons[0], "c: the provider produced a different plan than the saved one") || !strings.Contains(stale.Reasons[0], "content") {
		t.Errorf("reasons = %q, want c, the saved plan and its content", stale.Reasons)
	}
	if !strings.Contains(err.Error(), "Nothing was changed") {
		t.Errorf("the error does not say that nothing was changed: %v", err)
	}
	expectCalls(t, h, map[string]int{"ApplyResourceChange": 0})
	for addr, want := range map[string]string{"a": "a-1", "c": "c-1"} {
		if got := recordedContent(t, h, addr); got != want {
			t.Errorf("%s holds %q, want %q", addr, got, want)
		}
	}
	if res != nil && len(res.Applied) > 0 {
		t.Errorf("the apply says it applied %v", res.Applied)
	}
}

// Every resource that differs is listed, and a resource that is planned as saved is not.
func TestSavedApplyListsEveryResourceThatDiffers(t *testing.T) {
	h := newHarness(t)
	e, p := savedChain(t, h)
	h.fake.replan("a", "other-a")
	h.fake.replan("c", "other-c")
	h.fake.resetCalls()
	_, err := e.ApplySaved(context.Background(), p)
	stale := wantStale(t, err)
	var named []string
	for _, r := range stale.Reasons {
		named = append(named, strings.SplitN(r, ":", 2)[0])
	}
	if strings.Join(named, ",") != "a,c" {
		t.Errorf("reasons name %v, want a and c: %q", named, stale.Reasons)
	}
	expectCalls(t, h, map[string]int{"ApplyResourceChange": 0})
}

// A plan that is as saved applies as before, whole.
func TestSavedApplyOfAnUnchangedPlanStillApplies(t *testing.T) {
	h := newHarness(t)
	e, p := savedChain(t, h)
	h.fake.resetCalls()
	res, err := e.ApplySaved(context.Background(), p)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Applied) != 3 {
		t.Errorf("applied %v, want all three", res.Applied)
	}
	// One plan of each resource for the check, one for the apply.
	expectCalls(t, h, map[string]int{"ApplyResourceChange": 3})
}

// What changes after the check is still caught by the check of each resource: the
// apply stops there, and says so as it did.
func TestSavedApplyStillChecksEachResourceDuringTheApply(t *testing.T) {
	h := newHarness(t)
	e, p := savedChain(t, h)
	e.onTask = func(addr string, apply bool) {
		if apply && addr == "a" {
			h.fake.replan("c", "other")
		}
	}
	res, err := e.ApplySaved(context.Background(), p)
	var coded *CodedError
	if !errors.As(err, &coded) || coded.Code != ExitStale || !strings.Contains(err.Error(), "c: the provider produced a different plan than the saved one") {
		t.Fatalf("error = %v, want exit code 3 and a different plan at c", err)
	}
	if res == nil || len(res.Applied) == 0 {
		t.Errorf("the apply says it applied %v; a was changed before it reached c", res)
	}
}
