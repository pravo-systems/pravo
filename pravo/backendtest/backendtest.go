// Package backendtest checks an implementation of pravo.Backend against the
// contract that a state rests on. A program that keeps the state of pravo in a store
// of its own (Config.Backend) runs Run from a test:
//
//	func TestMyBackend(t *testing.T) {
//		backendtest.Run(t, func(t *testing.T) pravo.Backend { return newMyBackend(t) })
//	}
//
// The checks that matter most are the concurrent ones. A lock is taken with
// PutIfAbsent and renewed and released with ReplaceIf and DeleteIf; two commands
// that race for one lock are told apart only if exactly one of the calls wins.
package backendtest

import (
	"bytes"
	"context"
	"errors"
	"slices"
	"strconv"
	"sync"
	"testing"

	"github.com/pravo-systems/pravo/pravo"
)

// racers is the number of goroutines in the concurrent checks.
const racers = 16

// Run runs the checks as subtests. fresh returns an empty backend for each of them;
// it is called once per subtest, with that subtest's *testing.T, so that it can use
// t.TempDir and t.Cleanup.
func Run(t *testing.T, fresh func(t *testing.T) pravo.Backend) {
	t.Helper()
	for _, c := range []struct {
		name string
		run  func(t *testing.T, b pravo.Backend)
	}{
		{"Name", name},
		{"GetMissing", getMissing},
		{"PutThenGet", putThenGet},
		{"ReturnedBytesAreACopy", returnedBytesAreACopy},
		{"PutIfAbsent", putIfAbsent},
		{"PutIfAbsentRace", putIfAbsentRace},
		{"ReplaceIf", replaceIf},
		{"ReplaceIfIsAtomic", replaceIfIsAtomic},
		{"DeleteIf", deleteIf},
		{"DeleteIfRace", deleteIfRace},
		{"Delete", deleteKey},
		{"List", list},
	} {
		t.Run(c.name, func(t *testing.T) { c.run(t, fresh(t)) })
	}
}

func name(t *testing.T, b pravo.Backend) {
	if b.Name() == "" {
		t.Error("Name is empty: messages name the store with it")
	}
}

func getMissing(t *testing.T, b pravo.Backend) {
	_, err := b.Get(context.Background(), "resources/missing.json")
	if !errors.Is(err, pravo.ErrNotFound) {
		t.Errorf("Get of a missing key = %v, want an error that wraps pravo.ErrNotFound", err)
	}
}

func putThenGet(t *testing.T, b pravo.Backend) {
	ctx := context.Background()
	if err := b.Put(ctx, "resources/a.json", []byte("one")); err != nil {
		t.Fatal(err)
	}
	if err := b.Put(ctx, "resources/a.json", []byte("two")); err != nil {
		t.Fatal(err)
	}
	got, err := b.Get(ctx, "resources/a.json")
	if err != nil || string(got) != "two" {
		t.Errorf("Get after two Puts = %q, %v, want %q", got, err, "two")
	}
	if err := b.Put(ctx, "resources/empty.json", nil); err != nil {
		t.Fatal(err)
	}
	if got, err := b.Get(ctx, "resources/empty.json"); err != nil || len(got) != 0 {
		t.Errorf("Get of an empty object = %q, %v", got, err)
	}
}

func returnedBytesAreACopy(t *testing.T, b pravo.Backend) {
	ctx := context.Background()
	in := []byte("kept")
	if err := b.Put(ctx, "locks/k", in); err != nil {
		t.Fatal(err)
	}
	in[0] = 'X'
	got, _ := b.Get(ctx, "locks/k")
	if string(got) != "kept" {
		t.Errorf("a change to the slice given to Put reached the object: %q", got)
	}
	got[0] = 'Y'
	again, _ := b.Get(ctx, "locks/k")
	if string(again) != "kept" {
		t.Errorf("a change to the slice returned by Get reached the object: %q", again)
	}
}

func putIfAbsent(t *testing.T, b pravo.Backend) {
	ctx := context.Background()
	ok, err := b.PutIfAbsent(ctx, "locks/x", []byte("first"))
	if err != nil || !ok {
		t.Fatalf("PutIfAbsent of a new key = %v, %v, want true", ok, err)
	}
	ok, err = b.PutIfAbsent(ctx, "locks/x", []byte("second"))
	if err != nil || ok {
		t.Errorf("PutIfAbsent of an existing key = %v, %v, want false and no error", ok, err)
	}
	if got, _ := b.Get(ctx, "locks/x"); string(got) != "first" {
		t.Errorf("a refused PutIfAbsent changed the object: %q", got)
	}
}

func putIfAbsentRace(t *testing.T, b pravo.Backend) {
	ctx := context.Background()
	var wins int
	var mu sync.Mutex
	race(t, func(i int) {
		ok, err := b.PutIfAbsent(ctx, "locks/race", []byte(strconv.Itoa(i)))
		if err != nil {
			t.Errorf("PutIfAbsent: %v", err)
			return
		}
		if ok {
			mu.Lock()
			wins++
			mu.Unlock()
		}
	})
	if wins != 1 {
		t.Errorf("%d of %d concurrent PutIfAbsent calls on one key reported success, want exactly 1", wins, racers)
	}
}

func replaceIf(t *testing.T, b pravo.Backend) {
	ctx := context.Background()
	ok, err := b.ReplaceIf(ctx, "locks/r", func([]byte) ([]byte, bool) { return []byte("x"), true })
	if ok || !errors.Is(err, pravo.ErrNotFound) {
		t.Errorf("ReplaceIf of a missing key = %v, %v, want false and pravo.ErrNotFound", ok, err)
	}
	if err := b.Put(ctx, "locks/r", []byte("old")); err != nil {
		t.Fatal(err)
	}
	var seen string
	ok, err = b.ReplaceIf(ctx, "locks/r", func(cur []byte) ([]byte, bool) { seen = string(cur); return nil, false })
	if ok || err != nil || seen != "old" {
		t.Errorf("ReplaceIf declined = %v, %v (update saw %q), want false, no error, %q", ok, err, seen, "old")
	}
	if got, _ := b.Get(ctx, "locks/r"); string(got) != "old" {
		t.Errorf("a declined ReplaceIf changed the object: %q", got)
	}
	ok, err = b.ReplaceIf(ctx, "locks/r", func(cur []byte) ([]byte, bool) { return append(bytes.Clone(cur), "+new"...), true })
	if !ok || err != nil {
		t.Fatalf("ReplaceIf = %v, %v, want true", ok, err)
	}
	if got, _ := b.Get(ctx, "locks/r"); string(got) != "old+new" {
		t.Errorf("object after ReplaceIf = %q, want %q", got, "old+new")
	}
}

// replaceIfIsAtomic counts with ReplaceIf from several goroutines: a lost update
// means two callers saw the same current contents.
func replaceIfIsAtomic(t *testing.T, b pravo.Backend) {
	ctx := context.Background()
	if err := b.Put(ctx, "locks/counter", []byte("0")); err != nil {
		t.Fatal(err)
	}
	race(t, func(int) {
		_, err := b.ReplaceIf(ctx, "locks/counter", func(cur []byte) ([]byte, bool) {
			n, _ := strconv.Atoi(string(cur))
			return []byte(strconv.Itoa(n + 1)), true
		})
		if err != nil {
			t.Errorf("ReplaceIf: %v", err)
		}
	})
	if got, _ := b.Get(ctx, "locks/counter"); string(got) != strconv.Itoa(racers) {
		t.Errorf("counter = %q after %d concurrent increments: ReplaceIf is not atomic", got, racers)
	}
}

func deleteIf(t *testing.T, b pravo.Backend) {
	ctx := context.Background()
	ok, err := b.DeleteIf(ctx, "locks/d", func([]byte) bool { return true })
	if ok || !errors.Is(err, pravo.ErrNotFound) {
		t.Errorf("DeleteIf of a missing key = %v, %v, want false and pravo.ErrNotFound", ok, err)
	}
	if err := b.Put(ctx, "locks/d", []byte("mine")); err != nil {
		t.Fatal(err)
	}
	ok, err = b.DeleteIf(ctx, "locks/d", func(cur []byte) bool { return string(cur) == "theirs" })
	if ok || err != nil {
		t.Errorf("DeleteIf declined = %v, %v, want false and no error", ok, err)
	}
	if _, err := b.Get(ctx, "locks/d"); err != nil {
		t.Errorf("a declined DeleteIf removed the object: %v", err)
	}
	ok, err = b.DeleteIf(ctx, "locks/d", func(cur []byte) bool { return string(cur) == "mine" })
	if !ok || err != nil {
		t.Errorf("DeleteIf = %v, %v, want true", ok, err)
	}
	if _, err := b.Get(ctx, "locks/d"); !errors.Is(err, pravo.ErrNotFound) {
		t.Errorf("Get after DeleteIf = %v, want pravo.ErrNotFound", err)
	}
}

func deleteIfRace(t *testing.T, b pravo.Backend) {
	ctx := context.Background()
	if err := b.Put(ctx, "locks/once", []byte("x")); err != nil {
		t.Fatal(err)
	}
	var wins int
	var mu sync.Mutex
	race(t, func(int) {
		ok, err := b.DeleteIf(ctx, "locks/once", func([]byte) bool { return true })
		if err != nil && !errors.Is(err, pravo.ErrNotFound) {
			t.Errorf("DeleteIf: %v", err)
		}
		if ok {
			mu.Lock()
			wins++
			mu.Unlock()
		}
	})
	if wins != 1 {
		t.Errorf("%d of %d concurrent DeleteIf calls on one object reported success, want exactly 1", wins, racers)
	}
}

func deleteKey(t *testing.T, b pravo.Backend) {
	ctx := context.Background()
	if err := b.Delete(ctx, "txn/none"); err != nil {
		t.Errorf("Delete of a missing key = %v, want nil", err)
	}
	_ = b.Put(ctx, "txn/some", []byte("x"))
	if err := b.Delete(ctx, "txn/some"); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Get(ctx, "txn/some"); !errors.Is(err, pravo.ErrNotFound) {
		t.Errorf("Get after Delete = %v, want pravo.ErrNotFound", err)
	}
}

func list(t *testing.T, b pravo.Backend) {
	ctx := context.Background()
	for _, k := range []string{"resources/b.json", "resources/a.json", "resources/c.json", "txn/t1", "meta.json"} {
		if err := b.Put(ctx, k, []byte("x")); err != nil {
			t.Fatal(err)
		}
	}
	want := []string{"resources/a.json", "resources/b.json", "resources/c.json"}
	for _, prefix := range []string{"resources", "resources/"} {
		got, err := b.List(ctx, prefix)
		if err != nil || !slices.Equal(got, want) {
			t.Errorf("List(%q) = %v, %v, want %v (full keys, sorted)", prefix, got, err, want)
		}
	}
	got, err := b.List(ctx, "locks")
	if err != nil || len(got) != 0 {
		t.Errorf("List of a prefix with no objects = %v, %v, want none and no error", got, err)
	}
}

// race calls f from racers goroutines that start together.
func race(t *testing.T, f func(i int)) {
	t.Helper()
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := range racers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			f(i)
		}()
	}
	close(start)
	wg.Wait()
}
