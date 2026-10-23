package state

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

// Two stores read a node at the same revision and write it: the pointer is
// replaced if it still holds the revision and generation they read. A refresh that
// only moved the time of the record leaves the version as it is, so that an apply
// that read the same version goes through; the refresh's record is replaced by the
// apply's, and no pointer is left without its record.
func TestImmutableInterleavingsOfARefreshAndAnApply(t *testing.T) {
	for _, bk := range testBackends() {
		t.Run(bk.name, func(t *testing.T) {
			ctx := context.Background()
			b := bk.make(t)
			first := openFormat2(t, b)
			mustSet(t, first, node("a"))
			x, y := openFormat2(t, b), openFormat2(t, b)
			check := func(label string, wantRev int, wantAttr string) {
				t.Helper()
				fresh := openFormat2(t, b)
				got := fresh.Get("t.a")
				if got == nil || got.Revision != wantRev || !strings.Contains(string(got.Attributes), wantAttr) {
					t.Errorf("%s: t.a = %+v, want revision %d with %q", label, got, wantRev, wantAttr)
				}
				if v, err := fresh.VerifyAll(ctx); err != nil || len(v.Bad) != 0 {
					t.Errorf("%s: VerifyAll = %+v, %v", label, v, err)
				}
			}
			// X refreshes without a change; Y, from the same view, applies.
			rx := *x.Get("t.a")
			if err := x.Refresh(&rx, ""); err != nil {
				t.Fatalf("refresh: %v", err)
			}
			ry := *y.Get("t.a")
			ry.Attributes = json.RawMessage(`{"id":"applied"}`)
			if err := y.Set(&ry, ""); err != nil {
				t.Fatalf("an apply from the version that a refresh kept = %v", err)
			}
			check("refresh then apply", 2, "applied")
			// Now X, which still holds revision 1, applies: refused, nothing changes.
			rx2 := *x.Get("t.a")
			rx2.Attributes = json.RawMessage(`{"id":"late"}`)
			if err := x.Set(&rx2, ""); !errors.Is(err, ErrChanged) {
				t.Errorf("apply from an old view = %v, want ErrChanged", err)
			}
			if err := x.Refresh(&rx2, ""); !errors.Is(err, ErrChanged) {
				t.Errorf("refresh from an old view = %v, want ErrChanged", err)
			}
			check("late apply", 2, "applied")
			// Y refreshes without a change while Z, from the same view, applies first.
			z := openFormat2(t, b)
			rz := *z.Get("t.a")
			rz.Attributes = json.RawMessage(`{"id":"z"}`)
			if err := z.Set(&rz, ""); err != nil {
				t.Fatal(err)
			}
			ry2 := *y.Get("t.a")
			if err := y.Refresh(&ry2, ""); !errors.Is(err, ErrChanged) {
				t.Errorf("refresh after another command applied = %v, want ErrChanged", err)
			}
			check("apply then refresh", 3, `"z"`)
			// The same view applies twice: the second is refused.
			p, q := openFormat2(t, b), openFormat2(t, b)
			rp, rq := *p.Get("t.a"), *q.Get("t.a")
			rp.Attributes, rq.Attributes = json.RawMessage(`{"id":"p"}`), json.RawMessage(`{"id":"q"}`)
			if err := p.Set(&rp, ""); err != nil {
				t.Fatal(err)
			}
			if err := q.Set(&rq, ""); !errors.Is(err, ErrChanged) {
				t.Errorf("second apply from one view = %v, want ErrChanged", err)
			}
			check("two applies", 4, `"p"`)
		})
	}
}
