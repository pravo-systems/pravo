package engine

import (
	"context"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-go/tftypes"

	"github.com/pravo-systems/pravo/internal/plugin"
	"github.com/pravo-systems/pravo/internal/schema"
)

// relatedFirst answers an import with a related object before the one asked for, as a
// provider that imports an object together with the objects it holds may.
type relatedFirst struct {
	plugin.Provider
	read []string
}

func (p *relatedFirst) ImportResourceState(_ context.Context, typ string, _ plugin.ImportTarget, _ map[string]tftypes.Type) ([]plugin.ImportedResource, plugin.Diagnostics, error) {
	t := tftypes.Object{AttributeTypes: map[string]tftypes.Type{"id": tftypes.String}}
	mk := func(id string) tftypes.Value {
		return tftypes.NewValue(t, map[string]tftypes.Value{"id": tftypes.NewValue(tftypes.String, id)})
	}
	return []plugin.ImportedResource{{TypeName: "other_thing", State: mk("related")}, {TypeName: typ, State: mk("wanted")}}, nil, nil
}

func (p *relatedFirst) ReadResource(_ context.Context, typ string, st tftypes.Value, _ []byte) (tftypes.Value, []byte, plugin.Diagnostics, error) {
	var m map[string]tftypes.Value
	_ = st.As(&m)
	var id string
	_ = m["id"].As(&id)
	p.read = append(p.read, typ+":"+id)
	return st, nil, nil, nil
}

// An import takes the entry of the type it was asked for, in a plan, in `pravo import` and in
// `discover adopt`: they all call ImportObject.
func TestImportObjectPicksTheEntryOfItsType(t *testing.T) {
	p := &relatedFirst{}
	e := &Engine{}
	st, _, err := e.ImportObject(context.Background(), p, &schema.ProviderSchema{}, "my_thing", plugin.ImportTarget{ID: "x"})
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(p.read, ","); got != "my_thing:wanted" {
		t.Errorf("the provider read %q, want only the entry of the type asked for", got)
	}
	if st.IsNull() {
		t.Error("the state read is null")
	}
}
