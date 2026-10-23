package state

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// A rewrap --all writes the new record of a node with the times the node had, and
// replaces the pointer after. A collection that runs between the two sees an old
// record that no pointer names, and marks it. It deletes nothing.
func TestGCWhileRewrapAllIsBetweenARecordAndItsPointer(t *testing.T) {
	skipWhenSealed(t)
	ctx := context.Background()
	lb, _ := NewLocalBackend(t.TempDir())
	g := &gateBackend{Backend: lb}
	ada, fay := testIdentity(t, "ada"), testIdentity(t, "fay")
	admin := sealedStore(t, g, ada, fay)
	other := openIdentity(t, lb, ada)
	if _, err := admin.Rewrap(ctx, RewrapOptions{Readers: readersOf(ada)}); err != nil {
		t.Fatal(err)
	}
	held, release := make(chan struct{}), make(chan struct{})
	var gated atomic.Bool
	g.set(func(g *gateBackend) {
		g.putGate = func(key string) error {
			if strings.HasPrefix(key, "resources/") && gated.CompareAndSwap(false, true) {
				held <- struct{}{}
				<-release
			}
			return nil
		}
	})
	done := make(chan error, 1)
	go func() {
		_, err := admin.Rewrap(ctx, RewrapOptions{Readers: readersOf(ada), All: true})
		done <- err
	}()
	<-held
	res, err := other.GC(ctx, GCOptions{Now: ahead(48 * time.Hour)})
	if err != nil || res.Deleted != 0 || res.Marked == 0 {
		t.Errorf("GC between the record and the pointer of a rewrap = %+v, %v: want records marked and none deleted", res, err)
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatalf("rewrap --all: %v", err)
	}
	fresh, err := OpenBackendWith(ctx, lb, Options{Identity: ada})
	if err != nil {
		t.Fatalf("the state does not open after gc and rewrap --all met: %v", err)
	}
	if ver, err := fresh.VerifyAll(ctx); err != nil || len(ver.Bad) != 0 {
		t.Errorf("VerifyAll = %+v, %v", ver, err)
	}
	// The rewrap is done: a later collection finds the marked records named, drops
	// their marks, and deletes only what is left of the old group.
	later := ahead(48*time.Hour + GCSettle + time.Minute)
	if res, err := other.GC(ctx, GCOptions{Now: later}); err != nil {
		t.Fatalf("GC after the rewrap: %v", err)
	} else if res.Named != fresh.Len() {
		t.Errorf("GC after the rewrap = %+v: want %d named records", res, fresh.Len())
	}
	after, err := OpenBackendWith(ctx, lb, Options{Identity: ada})
	if err != nil {
		t.Fatalf("the state does not open after the second gc: %v", err)
	}
	if ver, err := after.VerifyAll(ctx); err != nil || len(ver.Bad) != 0 {
		t.Errorf("VerifyAll after the second gc = %+v, %v", ver, err)
	}
}

// A push writes meta.json, then every record, then the pointers. A collection of
// the destination in between sees every record unnamed.
func TestGCDuringAPush(t *testing.T) {
	skipWhenSealed(t)
	ctx := context.Background()
	src, _ := NewLocalBackend(t.TempDir())
	ada := testIdentity(t, "ada")
	sealedStore(t, src, ada)
	dlb, _ := NewLocalBackend(t.TempDir())
	g := &gateBackend{Backend: dlb}
	held, release := make(chan struct{}), make(chan struct{})
	var gated atomic.Bool
	g.set(func(g *gateBackend) {
		g.putGate = func(key string) error {
			if strings.HasPrefix(key, "resources/") && gated.CompareAndSwap(false, true) {
				held <- struct{}{}
				<-release
			}
			return nil
		}
	})
	done := make(chan error, 1)
	go func() { _, err := Copy(ctx, src, g); done <- err }()
	<-held
	// The destination holds meta.json and every record, and no pointer yet.
	other, err := OpenBackendWith(ctx, dlb, Options{})
	if err != nil {
		t.Fatal(err)
	}
	res, err := other.GC(ctx, GCOptions{Now: ahead(48 * time.Hour)})
	if err != nil || res.Deleted != 0 || res.Marked == 0 {
		t.Errorf("GC during the push = %+v, %v: want records marked and none deleted", res, err)
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	fresh, err := OpenBackendWith(ctx, dlb, Options{Identity: ada})
	if err != nil {
		t.Fatalf("the destination does not open after gc met the push: %v", err)
	}
	if ver, err := fresh.VerifyAll(ctx); err != nil || len(ver.Bad) != 0 {
		t.Errorf("VerifyAll = %+v, %v", ver, err)
	}
	// The push is done: a later collection spares what the pointers name, and drops
	// the marks.
	if res, err := other.GC(ctx, GCOptions{Now: ahead(48*time.Hour + GCSettle + time.Minute)}); err != nil || res.Deleted != 0 || res.Unnamed != 0 {
		t.Errorf("GC after the push = %+v, %v", res, err)
	}
	if m := marksOf(t, dlb); len(m) != 0 {
		t.Errorf("marks after the push = %v: records that a pointer names lose theirs", m)
	}
	if ver, err := fresh.VerifyAll(ctx); err != nil || len(ver.Bad) != 0 {
		t.Errorf("VerifyAll after the second gc = %+v, %v", ver, err)
	}
}

// A copy of the state does not carry the marks: they are the collections' of the
// bucket they were made in.
func TestCopyLeavesTheMarksBehind(t *testing.T) {
	for _, bk := range testBackends() {
		t.Run(bk.name, func(t *testing.T) {
			b := bk.make(t)
			ctx := context.Background()
			s := openFormat2(t, b)
			seedRecords(t, s)
			putUnnamed(t, b, s, "net.vpc", 90, time.Now().UTC().Add(-48*time.Hour))
			if res, err := s.GC(ctx, GCOptions{}); err != nil || res.Marked != 1 {
				t.Fatalf("GC = %+v, %v", res, err)
			}
			if len(marksOf(t, b)) != 1 {
				t.Fatal("the collection left no mark")
			}
			// The destination has marks of its own, about a record that the copy puts
			// again: they are dropped, so that the record is not deleted on them while
			// no pointer names it.
			dst := bk.make(t)
			if err := dst.Put(ctx, gcMarksKey, encodeMarks(map[string]time.Time{pointerAt(t, b, "net.vpc").Record: time.Now().Add(-time.Hour)})); err != nil {
				t.Fatal(err)
			}
			if _, err := Copy(ctx, b, dst); err != nil {
				t.Fatal(err)
			}
			if exists(t, dst, gcMarksKey) {
				t.Errorf("Copy carried %s, or left the one of the destination", gcMarksKey)
			}
			if !exists(t, b, gcMarksKey) {
				t.Errorf("Copy removed %s from the source", gcMarksKey)
			}
		})
	}
}

// A record that a pointer names between two collections is spared, and loses its
// mark.
func TestGCSparesARecordThatIsNamedBetweenTwoCollections(t *testing.T) {
	skipWhenSealed(t)
	for _, bk := range testBackends() {
		t.Run(bk.name, func(t *testing.T) {
			b := bk.make(t)
			ctx := context.Background()
			s := openFormat2(t, b)
			seedRecords(t, s)
			old := time.Now().UTC().Add(-48 * time.Hour)
			late := putUnnamed(t, b, s, "net.vpc", 90, old)
			gone := putUnnamed(t, b, s, "net.vpc", 91, old)
			at := ahead(48 * time.Hour)
			if res, err := s.GC(ctx, GCOptions{Now: at}); err != nil || res.Marked != 2 || res.Deleted != 0 {
				t.Fatalf("the first GC = %+v, %v", res, err)
			}
			marks := marksOf(t, b)
			if len(marks) != 2 || marks[late].IsZero() || marks[gone].IsZero() {
				t.Fatalf("marks = %v", marks)
			}
			// The writer that put the record points at it.
			ptr := mustIndent(t, pointer{V: pointerVersion, Address: "x.y", Revision: 90, Generation: "g", Record: late})
			if err := b.Put(ctx, "resources/x.y.json", ptr); err != nil {
				t.Fatal(err)
			}
			res, err := s.GC(ctx, GCOptions{Now: ahead(48*time.Hour + GCSettle + time.Minute)})
			if err != nil || res.Deleted != 1 || res.Marked != 0 || !exists(t, b, late) || exists(t, b, gone) {
				t.Fatalf("the second GC = %+v, %v (record there: %v)", res, err, exists(t, b, late))
			}
			if m := marksOf(t, b); len(m) != 0 {
				t.Errorf("marks = %v: the named record and the deleted one lose theirs", m)
			}
			// The pointer is replaced and the record is unnamed again: it is marked
			// anew, and the old mark does not count.
			if err := b.Delete(ctx, "resources/x.y.json"); err != nil {
				t.Fatal(err)
			}
			res, err = s.GC(ctx, GCOptions{Now: ahead(48*time.Hour + 2*GCSettle + 2*time.Minute)})
			if err != nil || res.Marked != 1 || res.Deleted != 0 || !exists(t, b, late) {
				t.Fatalf("GC of the record that is unnamed again = %+v, %v", res, err)
			}
		})
	}
}

// Two collections that run at the same time lose no mark that matters, delete
// nothing on their first look and nothing that a pointer names on their second.
func TestGCRacingCollections(t *testing.T) {
	skipWhenSealed(t)
	for _, bk := range testBackends() {
		t.Run(bk.name, func(t *testing.T) {
			b := bk.make(t)
			ctx := context.Background()
			s := openFormat2(t, b)
			seedRecords(t, s)
			old := time.Now().UTC().Add(-48 * time.Hour)
			var unnamed []string
			for i := 0; i < 12; i++ {
				unnamed = append(unnamed, putUnnamed(t, b, s, "net.vpc", 100+i, old))
			}
			named := s.Len()
			before := listJSON(t, s)
			race := func(now func() time.Time) []GCResult {
				var (
					wg  sync.WaitGroup
					out = make([]GCResult, 3)
					mu  sync.Mutex
				)
				for i := range out {
					wg.Add(1)
					go func() {
						defer wg.Done()
						c := openFormat2(t, b)
						res, err := c.GC(ctx, GCOptions{Now: now})
						mu.Lock()
						defer mu.Unlock()
						if err != nil {
							t.Errorf("GC %d: %v", i, err)
						}
						out[i] = res
					}()
				}
				wg.Wait()
				return out
			}
			for i, res := range race(ahead(48 * time.Hour)) {
				// One that looks after another has marked a record finds it settling.
				if res.Deleted != 0 || res.Marked+res.Settling != len(unnamed) {
					t.Errorf("racing first GC %d = %+v: want %d marked and none deleted", i, res, len(unnamed))
				}
			}
			if got := len(marksOf(t, b)); got != len(unnamed) {
				t.Errorf("%d marks after the first racing collections, want %d", got, len(unnamed))
			}
			deleted := 0
			for _, res := range race(ahead(48*time.Hour + GCSettle + time.Minute)) {
				deleted += res.Deleted
			}
			if deleted == 0 || deleted > 3*len(unnamed) {
				t.Errorf("the racing second collections deleted %d records", deleted)
			}
			for _, k := range unnamed {
				if exists(t, b, k) {
					t.Errorf("%s is still there", k)
				}
			}
			fresh := openFormat2(t, b)
			if fresh.Len() != named || listJSON(t, fresh) != before {
				t.Errorf("the collections changed what the pointers name")
			}
			if ver, err := fresh.VerifyAll(ctx); err != nil || len(ver.Bad) != 0 {
				t.Errorf("VerifyAll = %+v, %v", ver, err)
			}
			if m := marksOf(t, b); len(m) != 0 {
				t.Errorf("marks = %v after every record was deleted", m)
			}
		})
	}
}

// A dry run reads the marks and writes nothing, not even the object that holds
// them, on either backend, whatever the state of the marks.
func TestGCDryRunWritesNoMarks(t *testing.T) {
	skipWhenSealed(t)
	for _, bk := range testBackends() {
		t.Run(bk.name, func(t *testing.T) {
			b := bk.make(t)
			ctx := context.Background()
			s := openFormat2(t, b)
			seedRecords(t, s)
			putUnnamed(t, b, s, "net.vpc", 90, time.Now().UTC().Add(-48*time.Hour))
			counted := &writeCounter{Backend: b}
			cs := openFormat2(t, counted)
			counted.writes.Store(0)
			for _, now := range []func() time.Time{ahead(48 * time.Hour), ahead(48*time.Hour + time.Hour)} {
				res, err := cs.GC(ctx, GCOptions{DryRun: true, Now: now})
				if err != nil || res.Marked != 1 || counted.writes.Load() != 0 || exists(t, b, gcMarksKey) {
					t.Fatalf("dry run = %+v, %v, %d writes, marks there: %v", res, err, counted.writes.Load(), exists(t, b, gcMarksKey))
				}
			}
			// With a mark in place, a dry run of the settled collection changes it not.
			if _, err := s.GC(ctx, GCOptions{Now: ahead(48 * time.Hour)}); err != nil {
				t.Fatal(err)
			}
			raw, err := b.Get(ctx, gcMarksKey)
			if err != nil {
				t.Fatal(err)
			}
			counted.writes.Store(0)
			res, err := cs.GC(ctx, GCOptions{DryRun: true, Now: ahead(48*time.Hour + GCSettle + time.Minute)})
			after, _ := b.Get(ctx, gcMarksKey)
			if err != nil || res.Deleted != 1 || counted.writes.Load() != 0 || string(after) != string(raw) {
				t.Fatalf("dry run after the settle = %+v, %v, %d writes", res, err, counted.writes.Load())
			}
		})
	}
}

// Marks that cannot be read are no marks: the collection marks again.
func TestGCIgnoresMarksItCannotRead(t *testing.T) {
	skipWhenSealed(t)
	ctx := context.Background()
	b, _ := NewLocalBackend(t.TempDir())
	s := openFormat2(t, b)
	seedRecords(t, s)
	key := putUnnamed(t, b, s, "net.vpc", 90, time.Now().UTC().Add(-48*time.Hour))
	if err := b.Put(ctx, gcMarksKey, []byte(`not json`)); err != nil {
		t.Fatal(err)
	}
	if res, err := s.GC(ctx, GCOptions{Now: ahead(48 * time.Hour)}); err != nil || res.Marked != 1 || res.Deleted != 0 || !exists(t, b, key) {
		t.Fatalf("GC = %+v, %v", res, err)
	}
	var m gcMarks
	raw, _ := b.Get(ctx, gcMarksKey)
	if err := json.Unmarshal(raw, &m); err != nil || len(m.Unnamed) != 1 {
		t.Errorf("the marks were not replaced: %s", raw)
	}
}
