package engine

import (
	"math/big"
	"strings"
	"testing"

	"github.com/pravo-systems/pravo/internal/state"

	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

// TestOneEquivalenceForEveryComparison runs one table through every comparison of values that
// came from a provider: the JSON of two records (drift, refresh, the read of a data source, the
// read-back), and the typed values of a plan (a saved plan against an apply). They must agree.
func TestOneEquivalenceForEveryComparison(t *testing.T) {
	str, num := tftypes.String, tftypes.Number
	tags := tftypes.Map{ElementType: str}
	rule := tftypes.Object{AttributeTypes: map[string]tftypes.Type{"port": num, "tags": tags}}
	rules := tftypes.Set{ElementType: rule}
	ruleList := tftypes.List{ElementType: rule}
	holder := func(ty tftypes.Type, v any) tftypes.Value {
		return tftypes.NewValue(tftypes.Object{AttributeTypes: map[string]tftypes.Type{"rules": ty}},
			map[string]tftypes.Value{"rules": tftypes.NewValue(ty, v)})
	}
	rl := func(port int64, tg any) tftypes.Value {
		return tftypes.NewValue(rule, map[string]tftypes.Value{
			"port": tftypes.NewValue(num, big.NewFloat(float64(port))), "tags": tftypes.NewValue(tags, tg)})
	}
	big53a := func(s string) tftypes.Value {
		f, _, _ := big.ParseFloat(s, 10, 200, big.ToNearestEven)
		return tftypes.NewValue(tftypes.Object{AttributeTypes: map[string]tftypes.Type{"n": num}},
			map[string]tftypes.Value{"n": tftypes.NewValue(num, f)})
	}
	for _, c := range []struct {
		name   string
		a, b   string
		va, vb tftypes.Value
		want   bool
	}{
		{"set of objects, null map and empty map",
			`{"rules":[{"port":1,"tags":null}]}`, `{"rules":[{"port":1,"tags":{}}]}`,
			holder(rules, []tftypes.Value{rl(1, nil)}), holder(rules, []tftypes.Value{rl(1, map[string]tftypes.Value{})}), true},
		{"list of objects, null map and empty map",
			`{"rules":[{"port":1,"tags":null}]}`, `{"rules":[{"port":1,"tags":{}}]}`,
			holder(ruleList, []tftypes.Value{rl(1, nil)}), holder(ruleList, []tftypes.Value{rl(1, map[string]tftypes.Value{})}), true},
		{"set of objects, one element differs",
			`{"rules":[{"port":1,"tags":null}]}`, `{"rules":[{"port":2,"tags":{}}]}`,
			holder(rules, []tftypes.Value{rl(1, nil)}), holder(rules, []tftypes.Value{rl(2, map[string]tftypes.Value{})}), false},
		{"set of objects in another order",
			`{"rules":[{"port":1,"tags":null},{"port":2,"tags":{}}]}`, `{"rules":[{"port":2,"tags":null},{"port":1,"tags":{}}]}`,
			holder(rules, []tftypes.Value{rl(1, nil), rl(2, map[string]tftypes.Value{})}), holder(rules, []tftypes.Value{rl(2, nil), rl(1, map[string]tftypes.Value{})}), true},
		{"an empty set and a set with an element",
			`{"rules":[]}`, `{"rules":[{"port":1,"tags":null}]}`,
			holder(rules, []tftypes.Value{}), holder(rules, []tftypes.Value{rl(1, nil)}), false},
		{"numbers above 2^53 that differ",
			`{"n":9007199254740993}`, `{"n":9007199254740992}`,
			big53a("9007199254740993"), big53a("9007199254740992"), false},
		{"the same number written two ways",
			`{"n":1.0}`, `{"n":1}`, big53a("1.0"), big53a("1"), true},
	} {
		if got := sameAttributes([]byte(c.a), []byte(c.b)); got != c.want && !strings.Contains(c.name, "another order") {
			// JSON has no sets: the order of one cannot be judged from a record.
			t.Errorf("%s: sameAttributes = %v, want %v", c.name, got, c.want)
		}
		if got := consistent(c.va, c.vb) && consistent(c.vb, c.va); got != c.want {
			t.Errorf("%s: consistent = %v, want %v", c.name, got, c.want)
		}
	}
}

// TestDataSourceReadHoldsNullAndEmpty: a data source whose read comes back with an empty list
// where its record holds null says nothing new, as it does for drift; the plan must not take it
// for a change and rewrite the record at every run. Numbers above 2^53 still tell apart.
func TestDataSourceReadHoldsNullAndEmpty(t *testing.T) {
	e := &Engine{Providers: &Providers{}}
	read := func(ty tftypes.Type, v any) tftypes.Value {
		o := tftypes.Object{AttributeTypes: map[string]tftypes.Type{"ids": tftypes.List{ElementType: tftypes.String}, "n": tftypes.Number}}
		_ = ty
		vals := map[string]tftypes.Value{"ids": tftypes.NewValue(tftypes.List{ElementType: tftypes.String}, v), "n": tftypes.NewValue(tftypes.Number, big.NewFloat(1))}
		return tftypes.NewValue(o, vals)
	}
	sr := &state.Resource{Attributes: []byte(`{"ids":null,"n":1}`)}
	if !e.recordHolds(sr, &Change{}, read(nil, []tftypes.Value{})) {
		t.Error("an empty list read where the record holds null is a change")
	}
}
