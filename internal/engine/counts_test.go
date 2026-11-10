package engine

import (
	"testing"

	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

// One way of counting a plan: the plan, the plan file and the summary of a stored plan
// say the same numbers, a deposed record whose prior is a null counting for nothing.
func TestCountsAgreeAcrossSurfaces(t *testing.T) {
	obj := tftypes.Object{AttributeTypes: map[string]tftypes.Type{"id": tftypes.String}}
	null := tftypes.NewValue(obj, nil)
	known := tftypes.NewValue(obj, map[string]tftypes.Value{"id": tftypes.NewValue(tftypes.String, "x")})
	nullBytes, err := packValue(null)
	if err != nil {
		t.Fatal(err)
	}
	knownBytes, err := packValue(known)
	if err != nil {
		t.Fatal(err)
	}
	plan := &Plan{Changes: []*Change{
		{Addr: "a", Action: ActionCreate},
		{Addr: "b", Action: ActionReplace, Prior: known},
		{Addr: `c."deposed id-1"`, Action: ActionDelete, Prior: null},
		{Addr: `d."deposed id-2"`, Action: ActionDelete, Prior: known},
		{Addr: "e", Action: ActionRead},
		{Addr: "f", Action: ActionRead, Skipped: true},
	}}
	pf := &PlanFile{Changes: []savedChange{
		{Addr: "a", Action: ActionCreate},
		{Addr: "b", Action: ActionReplace, Prior: knownBytes},
		{Addr: `c."deposed id-1"`, Action: ActionDelete, Prior: nullBytes},
		{Addr: `d."deposed id-2"`, Action: ActionDelete, Prior: knownBytes},
		{Addr: "e", Action: ActionRead},
		{Addr: "f", Action: ActionRead, Skipped: true},
	}}
	add, change, destroy, read := plan.Counts()
	want := PlanCounts{Add: 2, Change: 0, Destroy: 2, Read: 1}
	if got := (PlanCounts{add, change, destroy, read}); got != want {
		t.Errorf("plan counts %+v, want %+v", got, want)
	}
	if got := pf.Counts(); got != want {
		t.Errorf("plan file counts %+v, want %+v", got, want)
	}
	if got := newPlanMeta(pf, "me", 0, 0).Summary; got != want {
		t.Errorf("stored plan summary %+v, want %+v", got, want)
	}
}

func TestActsOnProvider(t *testing.T) {
	for _, a := range []Action{ActionNoop, ActionRead, ActionOpen, ActionCall, ActionMove, ActionDefer} {
		if a.ActsOnProvider() {
			t.Errorf("%s must not act on a provider", a)
		}
	}
	for _, a := range []Action{ActionCreate, ActionUpdate, ActionReplace, ActionDelete} {
		if !a.ActsOnProvider() {
			t.Errorf("%s must act on a provider", a)
		}
	}
}
