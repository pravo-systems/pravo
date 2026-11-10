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
	mk := func(name, opt, zone string, tags []string, optUnknown bool) tftypes.Value {
		ov := tftypes.NewValue(tftypes.String, opt)
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

// A provider lists the paths that force a replacement in the order of its map,
// which differs between runs. The plan lists them in one order.
func TestChangedPathsAreSorted(t *testing.T) {
	typ := tftypes.Object{AttributeTypes: map[string]tftypes.Type{"a": tftypes.String, "b": tftypes.String, "c": tftypes.String}}
	mk := func(s string) tftypes.Value {
		return tftypes.NewValue(typ, map[string]tftypes.Value{"a": tftypes.NewValue(tftypes.String, s), "b": tftypes.NewValue(tftypes.String, s), "c": tftypes.NewValue(tftypes.String, s)})
	}
	attr := func(n string) *tftypes.AttributePath { return tftypes.NewAttributePath().WithAttributeName(n) }
	in := []*tftypes.AttributePath{attr("c"), attr("a"), attr("b")}
	for _, prior := range []tftypes.Value{mk("x"), tftypes.NewValue(typ, nil)} {
		got := changedPaths(prior, mk("y"), in)
		if len(got) != 3 || got[0].String() != `AttributeName("a")` || got[1].String() != `AttributeName("b")` || got[2].String() != `AttributeName("c")` {
			t.Errorf("paths out of order: %v", got)
		}
	}
	if in[0].String() != `AttributeName("c")` {
		t.Error("the provider's list was reordered in place")
	}
}
