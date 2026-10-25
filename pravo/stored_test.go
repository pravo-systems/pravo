package pravo

import (
	"context"
	"strings"
	"testing"
)

// A plan is stored in the state and applied from it by its id.
func TestAStoredPlanIsAppliedByItsID(t *testing.T) {
	ctx := context.Background()
	ws := open(t, specDir(t, greeting), Env{}, StateWrite)
	planned, err := ws.Plan(ctx, PlanOptions{})
	if err != nil {
		t.Fatal(err)
	}
	sp, err := planned.Store(ctx, StorePlanOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(sp.Meta.ID, "plan-") {
		t.Fatalf("id = %q", sp.Meta.ID)
	}
	read, err := ws.ReadStoredPlan(ctx, sp.Meta.ID, "plan show")
	if err != nil {
		t.Fatal(err)
	}
	if read.Entry.Meta.ID != sp.Meta.ID || !read.Entry.Status.Open() {
		t.Errorf("entry = %+v", read.Entry)
	}
	res, err := ws.ApplyStored(ctx, sp.Meta.ID, ApplyStoredOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if res.ID != sp.Meta.ID || res.Result == nil || res.Result.Txn == "" {
		t.Errorf("result = %+v", res)
	}
	if !strings.Contains(string(ws.State().Outputs()["greeting"]), "hello world") {
		t.Errorf("output = %s", ws.State().Outputs()["greeting"])
	}
	// Applied once, the plan has nothing left.
	res, err = ws.ApplyStored(ctx, sp.Meta.ID, ApplyStoredOptions{})
	if err != nil || !res.NothingLeft {
		t.Errorf("second apply: %+v, %v", res, err)
	}
}

// A stored plan is applied with the tags it was made with.
func TestAStoredPlanNeedsItsTags(t *testing.T) {
	m := &PlanMeta{ID: "plan-12345678", Tags: []string{"env=prod"}}
	if err := CheckStoredTags([]string{"env=prod"}, false, m, "apply"); err != nil {
		t.Errorf("same tags: %v", err)
	}
	err := CheckStoredTags(nil, false, m, "apply")
	if err == nil || !strings.Contains(err.Error(), "-t 'env=prod'") && !strings.Contains(err.Error(), "-t env=prod") {
		t.Errorf("no tags: %v", err)
	}
}

func TestStoredPlansNeedTheState(t *testing.T) {
	ws, err := Open(context.Background(), Config{Dir: specDir(t, greeting)})
	if err != nil {
		t.Fatal(err)
	}
	defer ws.Close()
	if _, err := ws.ApplyStored(context.Background(), "plan-x", ApplyStoredOptions{}); err == nil || !strings.Contains(err.Error(), "live in the state") {
		t.Errorf("err = %v", err)
	}
}
