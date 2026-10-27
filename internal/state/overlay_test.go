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
