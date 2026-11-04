package engine

import (
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

func TestInconsistencies(t *testing.T) {
	str := tftypes.String
	obj := tftypes.Object{AttributeTypes: map[string]tftypes.Type{"a": str, "b": tftypes.Bool, "set": tftypes.Set{ElementType: str}, "l": tftypes.List{ElementType: str}}}
	set := func(v ...string) tftypes.Value {
		var e []tftypes.Value
		for _, s := range v {
			e = append(e, tftypes.NewValue(str, s))
		}
		return tftypes.NewValue(tftypes.Set{ElementType: str}, e)
	}
	list := func(v ...string) tftypes.Value {
		var e []tftypes.Value
		for _, s := range v {
			e = append(e, tftypes.NewValue(str, s))
		}
		return tftypes.NewValue(tftypes.List{ElementType: str}, e)
	}
	mk := func(a any, b any, s, l tftypes.Value) tftypes.Value {
		return tftypes.NewValue(obj, map[string]tftypes.Value{"a": tftypes.NewValue(str, a), "b": tftypes.NewValue(tftypes.Bool, b), "set": s, "l": l})
	}
	planned := mk("x", false, set("p", "q"), list("1"))
	cases := []struct {
		name   string
		actual tftypes.Value
		want   string
	}{
		{"same", mk("x", false, set("q", "p"), list("1")), ""},
		{"computed filled", mk("x", false, set("p", "q"), list("1")), ""},
		{"string differs", mk("y", false, set("p", "q"), list("1")), ".a:"},
		{"false became null", mk("x", nil, set("p", "q"), list("1")), ".b:"},
		{"set differs", mk("x", false, set("p", "z"), list("1")), ".set:"},
		{"list length", mk("x", false, set("p", "q"), list("1", "2")), ".l:"},
	}
	for _, c := range cases {
		got := strings.Join(inconsistencies(planned, c.actual), "\n")
		if c.want == "" && got != "" || c.want != "" && !strings.Contains(got, c.want) {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
		if strings.Contains(got, `"y"`) {
			t.Errorf("%s: the message shows a value: %q", c.name, got)
		}
	}
	// An unknown planned value may come back as anything known.
	unk := tftypes.NewValue(obj, map[string]tftypes.Value{"a": tftypes.NewValue(str, tftypes.UnknownValue), "b": tftypes.NewValue(tftypes.Bool, nil), "set": set(), "l": list()})
	if got := inconsistencies(unk, mk("anything", nil, set(), list())); len(got) != 0 {
		t.Errorf("unknown planned value: %v", got)
	}
}
