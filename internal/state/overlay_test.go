package state

import (
	"context"
	"errors"
	"testing"
)

func TestOverlayKeepsWritesInMemory(t *testing.T) {
	ctx := context.Background()
	base, err := NewLocalBackend(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := base.Put(ctx, "resources/a.json", []byte("old")); err != nil {
		t.Fatal(err)
	}
	o := OverlayBackend(base, "plans")

	if err := o.Put(ctx, "resources/a.json", []byte("new")); err != nil {
		t.Fatal(err)
	}
	if ok, err := o.PutIfAbsent(ctx, "resources/b.json", []byte("b")); err != nil || !ok {
		t.Fatalf("PutIfAbsent = %v, %v", ok, err)
	}
	if ok, _ := o.PutIfAbsent(ctx, "resources/b.json", []byte("c")); ok {
		t.Error("PutIfAbsent replaced an object")
	}
	if got, _ := o.Get(ctx, "resources/a.json"); string(got) != "new" {
		t.Errorf("overlay reads %q", got)
	}
	if got, _ := base.Get(ctx, "resources/a.json"); string(got) != "old" {
		t.Errorf("the backend holds %q", got)
	}
	if _, err := base.Get(ctx, "resources/b.json"); !errors.Is(err, ErrNotFound) {
		t.Errorf("an object reached the backend: %v", err)
	}
	keys, _ := o.List(ctx, "resources")
	if len(keys) != 2 {
		t.Errorf("list = %v", keys)
	}
	if err := o.Delete(ctx, "resources/a.json"); err != nil {
		t.Fatal(err)
	}
	if _, err := o.Get(ctx, "resources/a.json"); !errors.Is(err, ErrNotFound) {
		t.Errorf("a deleted object reads: %v", err)
	}
	if keys, _ := o.List(ctx, "resources"); len(keys) != 1 {
		t.Errorf("list after delete = %v", keys)
	}
	if got, err := base.Get(ctx, "resources/a.json"); err != nil || string(got) != "old" {
		t.Errorf("the delete reached the backend: %q, %v", got, err)
	}
}

func TestOverlayPassesStoredPlans(t *testing.T) {
	ctx := context.Background()
	base, _ := NewLocalBackend(t.TempDir())
	o := OverlayBackend(base, "plans")
	if err := o.Put(ctx, "plans/p1.plan.json", []byte("plan")); err != nil {
		t.Fatal(err)
	}
	if got, err := base.Get(ctx, "plans/p1.plan.json"); err != nil || string(got) != "plan" {
		t.Errorf("a plan did not reach the backend: %q, %v", got, err)
	}
	if ok, err := o.ReplaceIf(ctx, "plans/p1.plan.json", func(cur []byte) ([]byte, bool) { return []byte("plan2"), true }); err != nil || !ok {
		t.Fatal(ok, err)
	}
	if got, _ := base.Get(ctx, "plans/p1.plan.json"); string(got) != "plan2" {
		t.Errorf("ReplaceIf of a plan: %q", got)
	}
}

// What a store writes through an overlay never reaches the backend, so the version of the
// record stays the one that the backend holds. A stored plan that expected the revision
// the write would have given could not be applied by anyone.
func TestOverlayStoreKeepsTheVersionTheBackendHolds(t *testing.T) {
	ctx := context.Background()
	base, err := NewLocalBackend(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	st, err := OpenBackend(ctx, base)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Set(&Resource{Addr: "db", Mode: "managed", Type: "t", Name: "db", Attributes: []byte(`{"v":"1"}`)}, "t1"); err != nil {
		t.Fatal(err)
	}
	st.Close()
	for name, b := range map[string]Backend{"overlay": OverlayBackend(base, "plans"), "traced overlay": traced(OverlayBackend(base, "plans"))} {
		st, err := OpenBackend(ctx, b)
		if err != nil {
			t.Fatal(err)
		}
		n := *st.Get("db")
		n.Attributes = []byte(`{"v":"2"}`)
		if err := st.Refresh(&n, "t2"); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if got := st.Get("db"); !jsonSame(got.Attributes, []byte(`{"v":"2"}`)) || got.Revision != 1 {
			t.Errorf("%s: the store holds %s at revision %d, want the refreshed record at revision 1", name, got.Attributes, got.Revision)
		}
		if err := st.Set(&Resource{Addr: "db", Mode: "managed", Type: "t", Name: "db", Attributes: []byte(`{"v":"3"}`)}, "t3"); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if got := st.Get("db"); got.Revision != 1 {
			t.Errorf("%s: a set moved the revision to %d", name, got.Revision)
		}
		st.Close()
	}
	plain, err := OpenBackend(ctx, base)
	if err != nil {
		t.Fatal(err)
	}
	defer plain.Close()
	if got := plain.Get("db"); got.Revision != 1 || !jsonSame(got.Attributes, []byte(`{"v":"1"}`)) {
		t.Errorf("the backend holds %s at revision %d: an overlay wrote to it", got.Attributes, got.Revision)
	}
	n := *plain.Get("db")
	n.Attributes = []byte(`{"v":"2"}`)
	if err := plain.Refresh(&n, "t4"); err != nil || plain.Get("db").Revision != 2 {
		t.Errorf("a store without an overlay: revision %d, %v, want 2", plain.Get("db").Revision, err)
	}
}
