package state

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pravo-systems/pravo/internal/protect"
)

// ahead is a clock that runs d ahead of the real one, so that the records a test
// has just written are old.
func ahead(d time.Duration) func() time.Time {
	return func() time.Time { return time.Now().Add(d) }
}

// putUnnamed puts a record of addr that no pointer names, as a writer that was
// stopped between the two writes does, with the times it is given. It copies the
// shape of the record that the pointer of addr names, so that it is sealed in the
// same group.
func putUnnamed(t *testing.T, b Backend, s *Store, addr string, revision int, updated time.Time) string {
	t.Helper()
	r := *s.Get(addr)
	r.Revision, r.UpdatedAt, r.RefreshedAt, r.AppliedAt = revision, updated, nil, nil
	data, _, err := s.encodeRecord(&r)
	if err != nil {
		t.Fatal(err)
	}
	key := recKey(data)
	if _, err := b.PutIfAbsent(context.Background(), key, data); err != nil {
		t.Fatal(err)
	}
	return key
}

func exists(t *testing.T, b Backend, key string) bool {
	t.Helper()
	_, err := b.Get(context.Background(), key)
	if err != nil && !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	return err == nil
}

// A collection deletes the superseded records and the record of a destroyed
// resource, once they are old, and nothing a pointer names.
func TestGCDeletesTheRecordsNothingNames(t *testing.T) {
	for _, bk := range testBackends() {
		t.Run(bk.name, func(t *testing.T) {
			b := bk.make(t)
			ctx := context.Background()
			s := openFormat2(t, b)
			seedRecords(t, s)
			for i := 2; i <= 4; i++ {
				again := *s.Get("net.vpc")
				again.Attributes = json.RawMessage(fmt.Sprintf(`{"id":"vpc-1","rev":%d}`, i))
				mustSet(t, s, &again)
			}
			destroyed := pointerAt(t, b, "db.old").Record
			if ok, err := s.Remove("db.old"); err != nil || !ok {
				t.Fatalf("Remove = %v, %v", ok, err)
			}
			total, named := len(listKeys(t, b, "rec")), s.Len()
			if total != named+4 {
				t.Fatalf("%d records for %d nodes: want the 3 superseded ones of net.vpc and the destroyed one", total, named)
			}
			before := listJSON(t, s)

			// Everything is young: nothing is deleted.
			res, err := s.GC(ctx, GCOptions{})
			if err != nil || res.Deleted != 0 || res.Young != 4 || res.Unnamed != 4 || res.Listed != total || res.Named != named || res.Grace != DefaultGCGrace {
				t.Fatalf("GC of a young state = %+v, %v", res, err)
			}

			// A dry run says what it would delete and writes nothing.
			counted := &writeCounter{Backend: b}
			cs := openFormat2(t, counted)
			counted.writes.Store(0)
			dry, err := cs.GC(ctx, GCOptions{DryRun: true, Now: ahead(48 * time.Hour)})
			if err != nil || dry.Deleted != 4 || dry.Bytes == 0 || !dry.DryRun || counted.writes.Load() != 0 || len(listKeys(t, b, "rec")) != total {
				t.Fatalf("dry run = %+v, %v, %d writes, %d records", dry, err, counted.writes.Load(), len(listKeys(t, b, "rec")))
			}

			real, err := s.GC(ctx, GCOptions{Now: ahead(48 * time.Hour)})
			if err != nil || real.Deleted != dry.Deleted || real.Bytes != dry.Bytes {
				t.Fatalf("GC = %+v, %v; the dry run said %+v", real, err, dry)
			}
			if got := len(listKeys(t, b, "rec")); got != named {
				t.Errorf("%d records after the collection, want %d", got, named)
			}
			if exists(t, b, destroyed) {
				t.Error("the record of the destroyed resource is still there")
			}
			fresh := openFormat2(t, b)
			if listJSON(t, fresh) != before {
				t.Error("the collection changed the records")
			}
			if ver, err := fresh.VerifyAll(ctx); err != nil || len(ver.Bad) != 0 {
				t.Errorf("VerifyAll after the collection = %+v, %v", ver, err)
			}
			if again, err := s.GC(ctx, GCOptions{Now: ahead(48 * time.Hour)}); err != nil || again.Deleted != 0 || again.Unnamed != 0 {
				t.Errorf("a second collection = %+v, %v", again, err)
			}
		})
	}
}

// A collection leaves alone the record that a writer has put and not yet pointed
// at, the one whose age cannot be told, and an object that is not what its name
// says; it deletes the old one.
func TestGCSparesTheRecordOfAStaleWriter(t *testing.T) {
	skipWhenSealed(t)
	for _, format := range []string{"2", "3"} {
		t.Run("format "+format, func(t *testing.T) {
			b, _ := NewLocalBackend(t.TempDir())
			ctx := context.Background()
			var s *Store
			if format == "2" {
				s = openFormat2(t, b)
				seedRecords(t, s)
			} else {
				s = sealedStore(t, b, testIdentity(t, "ada"))
			}
			addr := "net.vpc"
			now := time.Now().UTC()
			young := putUnnamed(t, b, s, addr, 90, now)
			old := putUnnamed(t, b, s, addr, 91, now.Add(-48*time.Hour))
			unknown := putUnnamed(t, b, s, addr, 92, time.Time{})
			bytesOfForeign := []byte(`{"not":"a record"}`)
			sum := sha256.Sum256([]byte("another"))
			foreign := recPrefix + hex.EncodeToString(sum[:])
			if err := b.Put(ctx, foreign, bytesOfForeign); err != nil {
				t.Fatal(err)
			}
			// A record that was refreshed a moment ago is young whatever its updated_at says.
			refreshed := *s.Get(addr)
			recent := now
			refreshed.Revision, refreshed.UpdatedAt, refreshed.RefreshedAt = 93, now.Add(-72*time.Hour), &recent
			data, _, err := s.encodeRecord(&refreshed)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := b.PutIfAbsent(ctx, recKey(data), data); err != nil {
				t.Fatal(err)
			}

			res, err := s.GC(ctx, GCOptions{})
			if err != nil || res.Deleted != 1 || res.Young != 2 || res.Unknown != 1 || len(res.Foreign) != 1 || res.Foreign[0] != foreign {
				t.Fatalf("GC = %+v, %v", res, err)
			}
			for name, key := range map[string]string{"the young record": young, "the record of unknown age": unknown, "the refreshed record": recKey(data), "the foreign object": foreign} {
				if !exists(t, b, key) {
					t.Errorf("%s was deleted", name)
				}
			}
			if exists(t, b, old) {
				t.Error("the old record is still there")
			}
			if !exists(t, b, pointerAt(t, b, addr).Record) {
				t.Error("the record that the pointer names was deleted")
			}

			// The writer finishes: its pointer names the young record, and the collection,
			// however late, keeps it.
			ptr := mustIndent(t, pointer{V: pointerVersion, Address: addr, Revision: 90, Generation: s.Get(addr).Generation, Record: young})
			if err := b.Put(ctx, nodeKey(addr), ptr); err != nil {
				t.Fatal(err)
			}
			if _, err := s.GC(ctx, GCOptions{Now: ahead(1000 * time.Hour)}); err != nil || !exists(t, b, young) {
				t.Errorf("the record that a pointer names was deleted: %v", err)
			}
		})
	}
}

// relistBackend runs a hook before the second listing of the pointers.
type relistBackend struct {
	Backend
	lists atomic.Int64
	hook  func()
}

func (b *relistBackend) List(ctx context.Context, prefix string) ([]string, error) {
	if prefix == "resources" && b.lists.Add(1) == 2 && b.hook != nil {
		b.hook()
	}
	return b.Backend.List(ctx, prefix)
}

// changeBackend changes the object about to be deleted, once.
type changeBackend struct {
	Backend
	change func(key string)
}

func (b *changeBackend) DeleteIf(ctx context.Context, key string, match func([]byte) bool) (bool, error) {
	if b.change != nil {
		b.change(key)
	}
	return b.Backend.DeleteIf(ctx, key, match)
}

// A record that a pointer names by the time of the delete is not deleted, and one
// that changed since it was judged is not either.
func TestGCReadsThePointersAgainAndComparesTheBytes(t *testing.T) {
	skipWhenSealed(t)
	ctx := context.Background()
	b, _ := NewLocalBackend(t.TempDir())
	s := openFormat2(t, b)
	seedRecords(t, s)
	old := time.Now().UTC().Add(-48 * time.Hour)
	named := putUnnamed(t, b, s, "net.vpc", 90, old)
	other := putUnnamed(t, b, s, "net.vpc", 91, old)

	rb := &relistBackend{Backend: b}
	rb.hook = func() {
		ptr := mustIndent(t, pointer{V: pointerVersion, Address: "x.y", Revision: 90, Generation: "g", Record: named})
		if err := b.Put(ctx, "resources/x.y.json", ptr); err != nil {
			t.Fatal(err)
		}
	}
	rs, err := OpenBackend(ctx, rb)
	if err != nil {
		t.Fatal(err)
	}
	rb.lists.Store(0) // the open listed the pointers once
	res, err := rs.GC(ctx, GCOptions{})
	if err != nil || res.Deleted != 1 || !exists(t, b, named) || exists(t, b, other) {
		t.Fatalf("GC with a pointer that appeared = %+v, %v (named record there: %v)", res, err, exists(t, b, named))
	}

	if err := b.Delete(ctx, "resources/x.y.json"); err != nil {
		t.Fatal(err)
	}
	if err := b.Delete(ctx, named); err != nil {
		t.Fatal(err)
	}
	changed := putUnnamed(t, b, s, "net.vpc", 92, old)
	cb := &changeBackend{Backend: b}
	cb.change = func(key string) {
		if key == changed {
			if err := b.Put(ctx, key, []byte(`{"replaced":true}`)); err != nil {
				t.Fatal(err)
			}
		}
	}
	cs, err := OpenBackend(ctx, cb)
	if err != nil {
		t.Fatal(err)
	}
	res, err = cs.GC(ctx, GCOptions{})
	if err != nil || res.Deleted != 0 || !exists(t, b, changed) {
		t.Fatalf("GC of a record that changed = %+v, %v", res, err)
	}
}

func TestGCOfAStateOfFormatOne(t *testing.T) {
	skipWhenSealed(t)
	b, _ := NewLocalBackend(t.TempDir())
	s, err := OpenBackendWith(context.Background(), b, Options{NewFormat: Format})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.GC(context.Background(), GCOptions{}); !errors.Is(err, ErrNoRecords) {
		t.Errorf("GC of format 1 = %v, want ErrNoRecords", err)
	}
}

func TestGCGrace(t *testing.T) {
	least := sweepGrace()
	for _, c := range []struct {
		asked, grace time.Duration
		raised       bool
	}{
		{0, DefaultGCGrace, false},
		{time.Second, least, true},
		{least, least, false},
		{72 * time.Hour, 72 * time.Hour, false},
	} {
		if grace, raised := GCGrace(c.asked); grace != c.grace || raised != c.raised {
			t.Errorf("GCGrace(%v) = %v, %v; want %v, %v", c.asked, grace, raised, c.grace, c.raised)
		}
	}
}

// groupsOf lists the groups of the meta.json in b.
func groupsOf(t *testing.T, b Backend) []string {
	t.Helper()
	m := formatMeta(t, b)
	var ids []string
	for _, g := range m.Sealed.Groups {
		ids = append(ids, g.ID)
	}
	return ids
}

// A reader is removed and the state is sealed again; the collection then drops the
// old group, and the old key opens nothing that is left while the readers that
// remain open all of it.
func TestGCDropsTheGroupOfARemovedReader(t *testing.T) {
	skipWhenSealed(t)
	for _, bk := range testBackends() {
		t.Run(bk.name, func(t *testing.T) {
			b := bk.make(t)
			ctx := context.Background()
			ada, fay, dan := testIdentity(t, "ada"), testIdentity(t, "fay"), testIdentity(t, "dan")
			s := sealedStore(t, b, ada, fay, dan)
			oldMeta, _ := b.Get(ctx, "meta.json") // what fay keeps
			stale := putUnnamed(t, b, s, "net.vpc", 90, time.Now().UTC())
			if _, err := s.Rewrap(ctx, RewrapOptions{Readers: readersOf(ada, dan)}); err != nil {
				t.Fatal(err)
			}
			plans := map[string]int{}
			planCount := func(context.Context) (map[string]int, error) { return plans, nil }
			everything := ahead(1000 * time.Hour)

			// Before the records are sealed again, everything is in g1. The record of
			// the writer is young, and spared.
			res, err := s.GC(ctx, GCOptions{Groups: true, Plans: planCount})
			if err != nil || len(res.Groups) != 1 || res.Groups[0].ID != "g1" || res.Groups[0].Dropped || res.Groups[0].Records != s.Len() || res.Groups[0].Spare != 1 || !res.Groups[0].Outputs {
				t.Fatalf("GC before rewrap --all = %+v, %v", res, err)
			}
			if got := groupsOf(t, b); fmt.Sprint(got) != "[g1 g2]" {
				t.Fatalf("groups = %v", got)
			}

			// After it, the records and the outputs are in g2. A sealed stored plan keeps
			// the group, and so does the record that a writer put and did not point at.
			if res, err := s.Rewrap(ctx, RewrapOptions{Readers: readersOf(ada, dan), All: true, Plans: func(context.Context) (int, int, error) { return 0, 0, nil }}); err != nil || res.Resealed != s.Len() {
				t.Fatalf("rewrap --all = %+v, %v", res, err)
			}
			if !exists(t, b, stale) {
				t.Fatal("the sweep of rewrap --all deleted the young record")
			}
			plans["g1"] = 2
			res, err = s.GC(ctx, GCOptions{Groups: true, Plans: planCount})
			if err != nil || len(res.Groups) != 1 || res.Groups[0].Dropped || res.Groups[0].Plans != 2 || res.Groups[0].Records != 0 || res.Groups[0].Outputs || res.Groups[0].Spare != 1 {
				t.Fatalf("GC with stored plans in g1 = %+v, %v", res, err)
			}
			delete(plans, "g1")
			res, err = s.GC(ctx, GCOptions{Groups: true, Plans: planCount})
			if err != nil || len(res.Groups) != 1 || res.Groups[0].Dropped || res.Groups[0].Spare != 1 || !exists(t, b, stale) {
				t.Fatalf("GC with a stale writer's record in g1 = %+v, %v", res, err)
			}
			if fmt.Sprint(groupsOf(t, b)) != "[g1 g2]" {
				t.Fatalf("a group with a reference was dropped: %v", groupsOf(t, b))
			}

			// A plan store that cannot be read keeps every group.
			res, err = s.GC(ctx, GCOptions{Groups: true, Now: everything, DryRun: true, Plans: func(context.Context) (map[string]int, error) { return nil, errors.New("plan-1 cannot be read") }})
			if err != nil || res.GroupsRefused == "" || len(res.Groups) != 0 || fmt.Sprint(groupsOf(t, b)) != "[g1 g2]" {
				t.Fatalf("GC with unreadable plans = %+v, %v", res, err)
			}

			// A dry run reports the drop and writes nothing.
			counted := &writeCounter{Backend: b}
			cs := openIdentity(t, counted, ada)
			counted.writes.Store(0)
			dry, err := cs.GC(ctx, GCOptions{Groups: true, DryRun: true, Plans: planCount, Now: everything})
			if err != nil || len(dry.Groups) != 1 || !dry.Groups[0].Dropped || dry.Deleted != 1 || counted.writes.Load() != 0 || fmt.Sprint(groupsOf(t, b)) != "[g1 g2]" || !exists(t, b, stale) {
				t.Fatalf("dry run = %+v, %v, %d writes", dry, err, counted.writes.Load())
			}

			// Now nothing refers to g1.
			res, err = s.GC(ctx, GCOptions{Groups: true, Plans: planCount, Now: everything})
			if err != nil || len(res.Groups) != 1 || !res.Groups[0].Dropped || res.Deleted != 1 {
				t.Fatalf("GC = %+v, %v", res, err)
			}
			if got := groupsOf(t, b); fmt.Sprint(got) != "[g2]" {
				t.Fatalf("groups after the collection = %v", got)
			}
			m := formatMeta(t, b)
			if h := m.History[len(m.History)-1]; h.What != "gc" || h.By != "ada" {
				t.Errorf("history = %+v", m.History)
			}
			want := listJSON(t, s)
			for _, id := range []*protect.Identity{ada, dan} {
				got := openIdentity(t, b, id)
				if got.Locked() || listJSON(t, got) != want {
					t.Errorf("%s does not read what is left", id.Name)
				}
				if out, _, err := got.LoadOutputs(); err != nil || !strings.Contains(string(out["password"]), secretValue) {
					t.Errorf("%s does not read the outputs: %v", id.Name, err)
				}
			}
			if ver, err := openIdentity(t, b, ada).VerifyAll(ctx); err != nil || len(ver.Bad) != 0 {
				t.Errorf("VerifyAll = %+v, %v", ver, err)
			}

			// Fay holds the lock for g1 in the meta.json she kept, and so the key of g1.
			// Nothing that is left is sealed in g1.
			var old meta
			if err := json.Unmarshal(oldMeta, &old); err != nil {
				t.Fatal(err)
			}
			keys, err := old.Sealed.Unlock(old.Lineage, fay.Reader)
			if err != nil {
				t.Fatalf("fay cannot unlock the group she was in: %v", err)
			}
			defer keys.Close()
			sealedObjects := 0
			for k, data := range allObjects(t, b) {
				if k == "meta.json" {
					continue
				}
				var env struct {
					Sealed *protect.Sealed `json:"sealed"`
				}
				if json.Unmarshal(data, &env) != nil || env.Sealed == nil {
					continue
				}
				sealedObjects++
				if env.Sealed.Group == "g1" {
					t.Errorf("%s is still sealed in g1, which fay's key opens", k)
				}
				if _, err := keys.Open(protect.Ctx{Lineage: old.Lineage}, *env.Sealed); err == nil {
					t.Errorf("fay's key opened %s", k)
				}
			}
			if sealedObjects < s.Len()+1 {
				t.Errorf("only %d sealed objects were looked at", sealedObjects)
			}
			assertNoSecret(t, b, secretValue)
		})
	}
}

// A reader who is not on the list does not drop a group, and a state that is not at
// rest does not either; neither stops the records from being collected.
func TestGCOfGroupsNeedsAReaderAndAStateAtRest(t *testing.T) {
	skipWhenSealed(t)
	ctx := context.Background()
	b, _ := NewLocalBackend(t.TempDir())
	ada, fay := testIdentity(t, "ada"), testIdentity(t, "fay")
	s := sealedStore(t, b, ada, fay)
	if _, err := s.Rewrap(ctx, RewrapOptions{Readers: readersOf(ada)}); err != nil {
		t.Fatal(err)
	}
	defer func(was func() time.Duration) { sweepGrace = was }(sweepGrace)
	sweepGrace = func() time.Duration { return 0 }
	if _, err := s.Rewrap(ctx, RewrapOptions{Readers: readersOf(ada), All: true}); err != nil {
		t.Fatal(err)
	}
	old := putUnnamed(t, b, s, "net.vpc", 90, time.Now().UTC().Add(-48*time.Hour))
	none := func(context.Context) (map[string]int, error) { return nil, nil }

	for name, id := range map[string]*protect.Identity{"fay": fay, "nobody": nil} {
		locked := openIdentity(t, b, id)
		if !locked.Locked() {
			t.Fatalf("%s opens the store", name)
		}
		if _, err := locked.GC(ctx, GCOptions{Groups: true, Plans: none, DryRun: true}); !errors.Is(err, ErrNotReader) {
			t.Errorf("%s: GC of groups = %v, want ErrNotReader", name, err)
		}
	}
	// A store without a key collects the records: it reads only the pointers and
	// the times in the clear.
	locked := openIdentity(t, b, nil)
	res, err := locked.GC(ctx, GCOptions{})
	if err != nil || res.Deleted != 1 || exists(t, b, old) {
		t.Fatalf("GC without a key = %+v, %v", res, err)
	}

	if err := s.Lock("net.vpc", "pravo apply", "txn-1"); err != nil {
		t.Fatal(err)
	}
	var locks *ErrLocked
	if _, err := s.GC(ctx, GCOptions{Groups: true, Plans: none}); !errors.As(err, &locks) {
		t.Errorf("GC of groups while a lock is held = %v, want ErrLocked", err)
	}
	if got := groupsOf(t, b); fmt.Sprint(got) != "[g1 g2]" {
		t.Errorf("groups = %v", got)
	}
	if err := s.Unlock("net.vpc"); err != nil {
		t.Fatal(err)
	}
	res, err = s.GC(ctx, GCOptions{Groups: true, Plans: none})
	if err != nil || len(res.Groups) != 1 || !res.Groups[0].Dropped {
		t.Fatalf("GC at rest = %+v, %v", res, err)
	}
	if got := groupsOf(t, b); fmt.Sprint(got) != "[g2]" {
		t.Errorf("groups = %v", got)
	}
	// The current group is never dropped: with nothing in it either.
	if res, err = s.GC(ctx, GCOptions{Groups: true, Plans: none}); err != nil || len(res.Groups) != 0 {
		t.Errorf("GC of a single group = %+v, %v", res, err)
	}
	// Bytes unchanged: a state with nothing to drop writes no meta.json.
	before, _ := b.Get(ctx, "meta.json")
	if _, err := s.GC(ctx, GCOptions{Groups: true, Plans: none}); err != nil {
		t.Fatal(err)
	}
	if after, _ := b.Get(ctx, "meta.json"); !bytes.Equal(before, after) {
		t.Error("meta.json was written though there was nothing to drop")
	}
}

// Of several groups, the ones with nothing in them go and the ones with records in
// them stay, and the current one is never a candidate.
func TestGCDropsOnlyTheGroupsNothingIsSealedIn(t *testing.T) {
	skipWhenSealed(t)
	ctx := context.Background()
	b, _ := NewLocalBackend(t.TempDir())
	ada, fay, dan := testIdentity(t, "ada"), testIdentity(t, "fay"), testIdentity(t, "dan")
	s := sealedStore(t, b, ada, fay, dan)
	defer func(was func() time.Duration) { sweepGrace = was }(sweepGrace)
	sweepGrace = func() time.Duration { return 0 }
	if _, err := s.Rewrap(ctx, RewrapOptions{Readers: readersOf(ada, dan), All: true}); err != nil {
		t.Fatal(err)
	}
	// Dan leaves after the records were sealed again: they are in g2 and stay there.
	if _, err := s.Rewrap(ctx, RewrapOptions{Readers: readersOf(ada)}); err != nil {
		t.Fatal(err)
	}
	if got := groupsOf(t, b); fmt.Sprint(got) != "[g1 g2 g3]" {
		t.Fatalf("groups = %v", got)
	}
	none := func(context.Context) (map[string]int, error) { return nil, nil }
	res, err := s.GC(ctx, GCOptions{Groups: true, Plans: none})
	if err != nil || len(res.Groups) != 2 || !res.Groups[0].Dropped || res.Groups[0].ID != "g1" || res.Groups[1].Dropped || res.Groups[1].Records != s.Len() {
		t.Fatalf("GC = %+v, %v", res, err)
	}
	if got := groupsOf(t, b); fmt.Sprint(got) != "[g2 g3]" {
		t.Fatalf("groups after the collection = %v", got)
	}
	// Ada still opens everything, including what is in g2.
	if got := openIdentity(t, b, ada); got.Locked() || listJSON(t, got) != listJSON(t, s) {
		t.Error("ada does not read what is left")
	}
	// The store went on with the keys it has: it writes in g3.
	changed := *s.Get("net.vpc")
	changed.Attributes = json.RawMessage(`{"id":"vpc-2"}`)
	mustSet(t, s, &changed)
	if g := recGroup(t, b, pointerAt(t, b, "net.vpc").Record); g != "g3" {
		t.Errorf("a record written after the collection is in %s", g)
	}
}

// A writer that has put its record and is held up before it creates or replaces
// the pointer does not lose the record to a collection that runs in between, in
// format 2 and in a sealed state. The record is not named, and it is young.
func TestGCWhileAWriterIsBetweenItsRecordAndItsPointer(t *testing.T) {
	skipWhenSealed(t)
	for _, format := range []string{"2", "3"} {
		t.Run("format "+format, func(t *testing.T) {
			ctx := context.Background()
			lb, _ := NewLocalBackend(t.TempDir())
			g := &gateBackend{Backend: lb}
			ada := testIdentity(t, "ada")
			var writer, other *Store
			if format == "2" {
				writer = openFormat2(t, g)
				other = openFormat2(t, g)
			} else {
				writer = sealedStore(t, g, ada)
				other = openIdentity(t, g, ada)
			}
			mustSet(t, writer, node("a"))
			held, release := make(chan struct{}), make(chan struct{})
			g.set(func(g *gateBackend) {
				g.putGate = func(key string) error {
					if key == nodeKey("t.b") {
						held <- struct{}{}
						<-release
					}
					return nil
				}
			})
			done := make(chan error, 1)
			go func() { done <- writer.Set(node("b", "t.a"), "") }()
			<-held
			before := len(listKeys(t, lb, "rec"))
			res, err := other.GC(ctx, GCOptions{})
			if err != nil || res.Deleted != 0 || res.Young == 0 || len(listKeys(t, lb, "rec")) != before {
				t.Errorf("GC between the two writes = %+v, %v (%d records, %d before)", res, err, len(listKeys(t, lb, "rec")), before)
			}
			close(release)
			if err := <-done; err != nil {
				t.Fatalf("the writer: %v", err)
			}
			var fresh *Store
			if format == "2" {
				fresh = openFormat2(t, lb)
			} else {
				fresh = openIdentity(t, lb, ada)
			}
			if fresh.Get("t.b") == nil {
				t.Error("the node of the writer is not in the state")
			}
			if ver, err := fresh.VerifyAll(ctx); err != nil || len(ver.Bad) != 0 {
				t.Errorf("VerifyAll = %+v, %v", ver, err)
			}
			if res, err := other.GC(ctx, GCOptions{Now: ahead(48 * time.Hour)}); err != nil || res.Deleted != 0 {
				t.Errorf("GC after the writer finished = %+v, %v", res, err)
			}
		})
	}
}

// A pointer that names a record that is not there stops the drop: the record may
// have been sealed in the group that would go.
func TestGCKeepsEveryGroupWhenAPointerNamesNothing(t *testing.T) {
	skipWhenSealed(t)
	ctx := context.Background()
	b, _ := NewLocalBackend(t.TempDir())
	ada, fay := testIdentity(t, "ada"), testIdentity(t, "fay")
	s := sealedStore(t, b, ada, fay)
	if _, err := s.Rewrap(ctx, RewrapOptions{Readers: readersOf(ada), All: true}); err != nil {
		t.Fatal(err)
	}
	none := func(context.Context) (map[string]int, error) { return nil, nil }
	if err := b.Delete(ctx, pointerAt(t, b, "net.vpc").Record); err != nil {
		t.Fatal(err)
	}
	res, err := s.GC(ctx, GCOptions{Groups: true, Plans: none})
	if err != nil || res.GroupsRefused == "" || len(res.Groups) != 0 || fmt.Sprint(groupsOf(t, b)) != "[g1 g2]" {
		t.Fatalf("GC with a pointer that names nothing = %+v, %v", res, err)
	}
}
