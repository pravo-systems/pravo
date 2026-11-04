package engine

import (
	"testing"

	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

func TestChangedPaths(t *testing.T) {
	inner := tftypes.Object{AttributeTypes: map[string]tftypes.Type{"zone": tftypes.String}}
	typ := tftypes.Object{AttributeTypes: map[string]tftypes.Type{
		"name": tftypes.String,
		"opt":  tftypes.String,
		"net":  tftypes.List{ElementType: inner},
		"tags": tftypes.Set{ElementType: tftypes.String},
	}}
	str := tftypes.NewValue(tftypes.String, nil)
	_ = str
	mk := func(name, opt, zone string, tags []string, optUnknown bool) tftypes.Value {
		var ov tftypes.Value = tftypes.NewValue(tftypes.String, opt)
		if optUnknown {
			ov = tftypes.NewValue(tftypes.String, tftypes.UnknownValue)
		}
		var tv []tftypes.Value
		for _, s := range tags {
			tv = append(tv, tftypes.NewValue(tftypes.String, s))
		}
		return tftypes.NewValue(typ, map[string]tftypes.Value{
			"name": tftypes.NewValue(tftypes.String, name),
			"opt":  ov,
			"net": tftypes.NewValue(tftypes.List{ElementType: inner}, []tftypes.Value{
				tftypes.NewValue(inner, map[string]tftypes.Value{"zone": tftypes.NewValue(tftypes.String, zone)}),
			}),
			"tags": tftypes.NewValue(tftypes.Set{ElementType: tftypes.String}, tv),
		})
	}
	attr := func(n string) *tftypes.AttributePath { return tftypes.NewAttributePath().WithAttributeName(n) }
	zone := attr("net").WithElementKeyInt(0).WithAttributeName("zone")
	all := []*tftypes.AttributePath{attr("name"), attr("opt"), zone, attr("tags")}
	prior := mk("a", "x", "z1", []string{"p", "q"}, false)

	cases := []struct {
		name    string
		planned tftypes.Value
		want    int
	}{
		{"nothing changed", mk("a", "x", "z1", []string{"p", "q"}, false), 0},
		{"top-level changed", mk("b", "x", "z1", []string{"p", "q"}, false), 1},
		{"nested block path changed", mk("a", "x", "z2", []string{"p", "q"}, false), 1},
		{"set path changed", mk("a", "x", "z1", []string{"p"}, false), 1},
		{"unknown counts as different", mk("a", "x", "z1", []string{"p", "q"}, true), 1},
		{"mix of changed and unchanged", mk("b", "x", "z2", []string{"p", "q"}, false), 2},
	}
	for _, c := range cases {
		got := changedPaths(prior, c.planned, all)
		if len(got) != c.want {
			t.Errorf("%s: %d paths kept, want %d: %v", c.name, len(got), c.want, got)
		}
	}
	if got := changedPaths(tftypes.NewValue(typ, nil), prior, all); len(got) != len(all) {
		t.Errorf("null prior: paths dropped: %v", got)
	}
}
