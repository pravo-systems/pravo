package pravo

import (
	"context"
	"strings"
	"testing"
)

func TestParseRefresh(t *testing.T) {
	for in, want := range map[string]RefreshMode{"": RefreshAge, "age": RefreshAge, "false": RefreshNone, "all": RefreshAll, "expired": RefreshExpired} {
		if got, err := ParseRefresh(in); err != nil || got != want {
			t.Errorf("ParseRefresh(%q) = %v, %v", in, got, err)
		}
	}
	if _, err := ParseRefresh("sometimes"); err == nil {
		t.Error("an unknown mode is accepted")
	}
}

func TestWhySaysThatAnUnknownAddressIsNotDeclared(t *testing.T) {
	ws := open(t, specDir(t, greeting), Env{}, StateWrite)
	got, err := ws.Why(context.Background(), RefreshNone, "nothing.here")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Found || !strings.Contains(got[0].Reasons[0], "not declared") {
		t.Errorf("explanations = %+v", got)
	}
}

func TestDriftOfAnEstateWithoutResourcesFindsNone(t *testing.T) {
	ctx := context.Background()
	ws := open(t, specDir(t, greeting), Env{}, StateWrite)
	rep, err := ws.Drift(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Drifted) != 0 || rep.Root == "" {
		t.Errorf("report = %+v", rep)
	}
	due, err := ws.DriftDue(ctx, 0)
	if err != nil || len(due.Drifted) != 0 {
		t.Errorf("due = %+v, %v", due, err)
	}
}

func TestQueryStateSeesTheOutputs(t *testing.T) {
	ctx := context.Background()
	ws := open(t, specDir(t, greeting), Env{}, StateWrite)
	planned, err := ws.Plan(ctx, PlanOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := planned.Apply(ctx); err != nil {
		t.Fatal(err)
	}
	res, err := ws.QueryState(`outputs.greeting`)
	if err != nil {
		t.Fatal(err)
	}
	j, err := res.JSON()
	if err != nil || !strings.Contains(string(j), "hello world") {
		t.Errorf("json = %s, %v", j, err)
	}
	if _, err := ws.QueryState(`[`); err == nil {
		t.Error("a bad expression is accepted")
	}
	if _, err := ws.Dependents("nothing.here"); err == nil || !strings.Contains(err.Error(), "not in state") {
		t.Errorf("dependents err = %v", err)
	}
}
