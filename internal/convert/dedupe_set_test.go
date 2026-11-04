package convert

import (
	"testing"

	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

func TestDedupeSetKeepsFirstOfEqualKnownElements(t *testing.T) {
	str := func(s string) tftypes.Value { return tftypes.NewValue(tftypes.String, s) }
	unk := tftypes.NewValue(tftypes.String, tftypes.UnknownValue)
	got := dedupeSet([]tftypes.Value{str("a"), str("b"), str("a"), unk, unk, str("b")})
	if len(got) != 4 {
		t.Fatalf("want a, b and the two unknowns, got %d elements: %v", len(got), got)
	}
	if !got[0].Equal(str("a")) || !got[1].Equal(str("b")) {
		t.Fatalf("order changed: %v", got)
	}
}
