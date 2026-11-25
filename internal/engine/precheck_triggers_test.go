package engine

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

// triggerOf is the value of the key taskDefinition in the triggers of a planned service.
func triggerOf(t *testing.T, planned tftypes.Value) tftypes.Value {
	t.Helper()
	var obj, triggers map[string]tftypes.Value
	if err := planned.As(&obj); err != nil {
		t.Fatal(err)
	}
	if err := obj["triggers"].As(&triggers); err != nil {
		t.Fatal(err)
	}
	return triggers["taskDefinition"]
}

// An ECS service redeploys when the ARN of its task definition changes: its
// triggers hold that ARN. The task definition is replaced in the same apply, so the
// ARN is not known when the plan is made, and the service is planned with an unknown
// value inside a map. The plan of the apply, with the new ARN, is the saved one.
func TestSavedApplyAcceptsAnUnknownInsideATriggersMap(t *testing.T) {
	mk := func(td string) spec {
		return spec{
			cue: "#S: {name: string, id: string | null, triggers?: {[string]: string}, ...}\n" +
				"td: #S & {name: \"td-" + td + "\"}\n" +
				"svc: #S & {name: \"svc\", triggers: {taskDefinition: td.id}}\n",
			resources: []string{"td", "svc"},
		}
	}
	h := newHarness(t)
	h.apply(mk("1"))
	s := mk("2")
	_, pf := planned(t, h, s)
	e := h.engine(s, Options{Refresh: RefreshNone})
	loaded, err := e.LoadPlan(pf)
	if err != nil {
		t.Fatal(err)
	}
	// The task definition is replaced, so the ARN in the triggers of the service is not
	// known when the plan is made, and the saved plan holds it unknown.
	for addr, want := range map[string]Action{"td": ActionReplace, "svc": ActionUpdate} {
		if got := changeOf(t, loaded, addr).Action; got != want {
			t.Fatalf("%s: the plan would %s, want %s", addr, got, want)
		}
	}
	if arn := triggerOf(t, changeOf(t, loaded, "svc").Planned); arn.IsKnown() {
		t.Fatalf("the ARN in the triggers is known in the saved plan: %v", arn)
	}
	if _, err := e.ApplySaved(context.Background(), loaded.Remaining()); err != nil {
		t.Fatalf("apply: %v", err)
	}
	// The service holds the id of the new task definition.
	attrs := func(addr string) (m struct {
		ID       string            `json:"id"`
		Triggers map[string]string `json:"triggers"`
	}) {
		rec := h.state.Get(addr)
		if rec == nil {
			t.Fatalf("%s has no record", addr)
		}
		if err := json.Unmarshal(rec.Attributes, &m); err != nil {
			t.Fatal(err)
		}
		return m
	}
	if got, want := attrs("svc").Triggers["taskDefinition"], attrs("td").ID; got == "" || got != want {
		t.Errorf("the triggers of svc hold %q, want the id of td, %q", got, want)
	}
}

// An unknown value, inside a map or as the whole map, agrees with whatever the apply
// knows of it; a known value does not, and the paths of the differences come back.
func TestConsistentUnknownInsideAMap(t *testing.T) {
	m := tftypes.Map{ElementType: tftypes.String}
	str := func(v any) tftypes.Value { return tftypes.NewValue(tftypes.String, v) }
	mp := func(kv map[string]tftypes.Value) tftypes.Value { return tftypes.NewValue(m, kv) }
	unknown := tftypes.UnknownValue
	for _, c := range []struct {
		name          string
		saved, actual tftypes.Value
		want          []string
	}{
		{"an unknown element", mp(map[string]tftypes.Value{"a": str(unknown), "b": str("x")}), mp(map[string]tftypes.Value{"a": str("arn"), "b": str("x")}), nil},
		{"the whole map unknown", tftypes.NewValue(m, unknown), mp(map[string]tftypes.Value{"a": str("arn")}), nil},
		{"a known element that changed", mp(map[string]tftypes.Value{"a": str("old"), "b": str("x")}), mp(map[string]tftypes.Value{"a": str("arn"), "b": str("x")}), []string{"a"}},
		{"a key that went", mp(map[string]tftypes.Value{"a": str(unknown), "b": str("x")}), mp(map[string]tftypes.Value{"a": str("arn"), "c": str("x")}), []string{"b"}},
		{"unknown now, known then", mp(map[string]tftypes.Value{"a": str("old")}), mp(map[string]tftypes.Value{"a": str(unknown)}), []string{"a"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			got := inconsistentPaths(c.saved, c.actual)
			if strings.Join(got, ",") != strings.Join(c.want, ",") {
				t.Errorf("paths = %q, want %q", got, c.want)
			}
			if consistent(c.saved, c.actual) != (len(c.want) == 0) {
				t.Errorf("consistent = %v, want %v", consistent(c.saved, c.actual), len(c.want) == 0)
			}
		})
	}
}
