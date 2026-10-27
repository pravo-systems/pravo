package state

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// openFormat opens a store over b in format 2 when it is created.
func openFormat2(t *testing.T, b Backend) *Store {
	t.Helper()
	s, err := OpenBackendWith(context.Background(), b, Options{NewFormat: FormatImmutable})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// seedRecords writes a graph with the shapes a store holds: dependencies, a mark,
// a node that is gone, private data, a data source, outputs and a transaction.
func seedRecords(t *testing.T, s *Store) {
	t.Helper()
	when := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	mustSet(t, s,
		&Resource{Mode: "managed", Type: "net", Name: "vpc", Provider: "p", ProviderKey: "p/aws", Attributes: json.RawMessage(`{"id":"vpc-1","cidr":"10.0.0.0/16"}`), Private: []byte("opaque"), ConfigHash: "c1", RefreshedAt: &when},
		&Resource{Mode: "managed", Type: "net", Name: "subnet", Attributes: json.RawMessage(`{"id":"s-1"}`), Dependencies: []string{"net.vpc"}},
		&Resource{Mode: "managed", Type: "db", Name: "main", Attributes: json.RawMessage(`{"password":"hunter2"}`), Dependencies: []string{"net.subnet"}, DriftedAt: &when},
		&Resource{Mode: "managed", Type: "db", Name: "old", Attributes: json.RawMessage(`{}`), Gone: true},
		&Resource{Mode: "data", Type: "dns", Name: "zone", Attributes: json.RawMessage(`{"name":"example.com"}`)},
		&Resource{Addr: "mod.a b/c", Mode: "managed", Type: "x", Name: "odd", Attributes: json.RawMessage(`{"é":"<&>"}`)},
	)
	if err := s.SetOutputs(map[string]json.RawMessage{"url": json.RawMessage(`"https://example.com"`)}); err != nil {
		t.Fatal(err)
	}
	txn := s.Begin("apply", "pravo apply")
	if err := txn.Start(); err != nil {
		t.Fatal(err)
	}
	txn.Record("db.main", "create", 0, 1)
	if err := txn.Finish("succeeded", "Apply complete", true); err != nil {
		t.Fatal(err)
	}
}

// listJSON is the records of the store in a form that two stores can be compared
// by.
func listJSON(t *testing.T, s *Store) string {
	t.Helper()
	data, err := json.Marshal(s.List())
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// pointerAt reads the object at the node key of addr, which must be a pointer.
func pointerAt(t *testing.T, b Backend, addr string) pointer {
	t.Helper()
	data, err := b.Get(context.Background(), nodeKey(addr))
	if err != nil {
		t.Fatal(err)
	}
	p, isPtr, err := parsePointer(data)
	if err != nil || !isPtr {
		t.Fatalf("%s is not a pointer (%v): %s", nodeKey(addr), err, data)
	}
	return p
}

func listKeys(t *testing.T, b Backend, prefix string) []string {
	t.Helper()
	keys, err := b.List(context.Background(), prefix)
	if err != nil {
		t.Fatal(err)
	}
	return keys
}

// A store created in format 2 keeps every record as an object named by its hash,
// and a pointer at the key that format 1 used for the record itself.
func TestImmutableLayout(t *testing.T) {
	for _, bk := range testBackends() {
		t.Run(bk.name, func(t *testing.T) {
			b := bk.make(t)
			ctx := context.Background()
			s := openFormat2(t, b)
			if got := formatOf(t, b); got != FormatImmutable || s.Format() != FormatImmutable {
				t.Fatalf("a store created in format 2 is format %d (in memory %d)", got, s.Format())
			}
			seedRecords(t, s)
			for _, r := range s.List() {
				p := pointerAt(t, b, r.Address())
				if p.V != 1 || p.Address != r.Address() || p.Revision != r.Revision || p.Generation != r.Generation || p.Revision != 1 {
					t.Errorf("pointer of %s = %+v for the record %+v", r.Address(), p, r)
				}
				data, err := b.Get(ctx, p.Record)
				if err != nil {
					t.Fatalf("the record %s of %s: %v", p.Record, r.Address(), err)
				}
				if p.Record != recKey(data) {
					t.Errorf("%s is not named by the hash of its bytes", p.Record)
				}
				var onDisk Resource
				if err := json.Unmarshal(data, &onDisk); err != nil || onDisk.Address() != r.Address() || onDisk.Revision != r.Revision {
					t.Errorf("record of %s holds %+v, %v", r.Address(), onDisk, err)
				}
			}
			if n := len(listKeys(t, b, "rec")); n != s.Len() {
				t.Errorf("%d records for %d nodes", n, s.Len())
			}
			again, err := OpenBackend(ctx, b)
			if err != nil {
				t.Fatal(err)
			}
			if listJSON(t, again) != listJSON(t, s) {
				t.Errorf("a reopened store lists other records:\n%s\n%s", listJSON(t, again), listJSON(t, s))
			}
			// The store that exists keeps its format whatever the options say.
			if kept, err := OpenBackendWith(ctx, b, Options{NewFormat: Format}); err != nil || kept.Format() != FormatImmutable {
				t.Errorf("open of a format 2 store with NewFormat 1: format %v, %v", kept.Format(), err)
			}
			if _, err := OpenBackendWith(ctx, bk.make(t), Options{NewFormat: FormatSealed}); err == nil && NewStoreIdentity == nil {
				t.Error("a new store was created in format 3 with no identity to seal it to")
			}
		})
	}
}

// Writes make a record each time; a revision that moves keeps the record it
// replaced, and a refresh that leaves the revision does not leave one behind.
func TestImmutableRecordsAreKeptOrSwept(t *testing.T) {
	for _, bk := range testBackends() {
		t.Run(bk.name, func(t *testing.T) {
			b := bk.make(t)
			s := openFormat2(t, b)
			a := node("a")
			mustSet(t, s, a)
			first := pointerAt(t, b, "t.a")
			if n := len(listKeys(t, b, "rec")); n != 1 {
				t.Fatalf("%d records after one write", n)
			}

			fresh := *s.Get("t.a")
			fresh.RefreshedAt = Now()
			if err := s.Refresh(&fresh, "t2"); err != nil {
				t.Fatal(err)
			}
			second := pointerAt(t, b, "t.a")
			if second.Revision != 1 || second.Generation != first.Generation {
				t.Errorf("a refresh moved the version: %+v after %+v", second, first)
			}
			if second.Record == first.Record {
				t.Error("a refresh left the pointer at the old record")
			}
			if keys := listKeys(t, b, "rec"); len(keys) != 1 || keys[0] != second.Record {
				t.Errorf("records after a refresh = %v, want only %s", keys, second.Record)
			}

			changed := *s.Get("t.a")
			changed.Attributes = json.RawMessage(`{"x":1}`)
			mustSet(t, s, &changed)
			third := pointerAt(t, b, "t.a")
			if third.Revision != 2 {
				t.Errorf("revision after a change = %d", third.Revision)
			}
			if keys := listKeys(t, b, "rec"); len(keys) != 2 {
				t.Errorf("records after a change = %v, want the two revisions", keys)
			}
		})
	}
}

// Moving a store from format 1 to format 2 changes the layout and nothing a
// caller can see.
func TestUpgradeToImmutable(t *testing.T) {
	for _, bk := range testBackends() {
		t.Run(bk.name, func(t *testing.T) {
			b := bk.make(t)
			ctx := context.Background()
			s, err := OpenBackendWith(ctx, b, Options{NewFormat: Format})
			if err != nil {
				t.Fatal(err)
			}
			seedRecords(t, s)
			before := listJSON(t, s)
			beforeOut := fmt.Sprint(s.Outputs())
			legacy := map[string][]byte{}
			for _, r := range s.List() {
				legacy[r.Address()], _ = b.Get(ctx, nodeKey(r.Address()))
			}
			txnsBefore, _ := s.Transactions()

			var progress atomic.Int64
			res, err := s.Upgrade(ctx, UpgradeOptions{To: FormatImmutable, Progress: func(done, total int) { progress.Add(1) }})
			if err != nil {
				t.Fatal(err)
			}
			if res.From != 1 || res.To != 2 || res.Records != s.Len() || res.Resumed || progress.Load() != int64(s.Len()) {
				t.Errorf("result %+v, %d progress calls for %d records", res, progress.Load(), s.Len())
			}
			if formatOf(t, b) != FormatImmutable || s.Format() != FormatImmutable {
				t.Errorf("format is %d (in memory %d)", formatOf(t, b), s.Format())
			}
			if listJSON(t, s) != before {
				t.Errorf("the store that was upgraded lists other records")
			}
			for addr, raw := range legacy {
				p := pointerAt(t, b, addr)
				rec, err := b.Get(ctx, p.Record)
				if err != nil || !bytes.Equal(rec, raw) {
					t.Errorf("the record of %s is not the bytes that format 1 held: %v", addr, err)
				}
			}
			again, err := OpenBackend(ctx, b)
			if err != nil {
				t.Fatal(err)
			}
			if listJSON(t, again) != before || fmt.Sprint(again.Outputs()) != beforeOut {
				t.Errorf("a store opened after the upgrade lists other records or outputs")
			}
			if txns, _ := again.Transactions(); len(txns) != len(txnsBefore) {
				t.Errorf("%d transactions after the upgrade, %d before", len(txns), len(txnsBefore))
			}
			if v, err := again.VerifyAll(ctx); err != nil || len(v.Bad) != 0 || v.Checked != s.Len() {
				t.Errorf("VerifyAll = %+v, %v", v, err)
			}
			// The store that was upgraded can be written, and a second upgrade writes
			// nothing.
			mustSet(t, s, node("after"))
			if got, err := OpenBackend(ctx, b); err != nil || got.Get("t.after") == nil {
				t.Errorf("a record written after the upgrade is not read back: %v", err)
			}
			if res, err := s.Upgrade(ctx, UpgradeOptions{To: FormatImmutable}); err != nil || res.Records != 0 || res.Resumed {
				t.Errorf("second upgrade = %+v, %v", res, err)
			}
		})
	}
}

// The upgrade refuses a store that is in use, and a format it cannot reach.
func TestUpgradeRefusals(t *testing.T) {
	ctx := context.Background()
	b, _ := NewLocalBackend(t.TempDir())
	s, err := OpenBackendWith(ctx, b, Options{NewFormat: Format})
	if err != nil {
		t.Fatal(err)
	}
	mustSet(t, s, node("a"))
	other, err := OpenBackend(ctx, b)
	if err != nil {
		t.Fatal(err)
	}
	if err := other.Lock("t.a", "pravo apply", "txn"); err != nil {
		t.Fatal(err)
	}
	var locked *ErrLocked
	if _, err := s.Upgrade(ctx, UpgradeOptions{To: FormatImmutable}); !errors.As(err, &locked) {
		t.Fatalf("upgrade with a lock held: %v, want ErrLocked", err)
	}
	if formatOf(t, b) != Format {
		t.Error("a refused upgrade changed the format")
	}
	if err := other.Unlock("t.a"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Upgrade(ctx, UpgradeOptions{To: 7}); err == nil {
		t.Error("upgrade to format 7 was accepted")
	}
	if _, err := s.Upgrade(ctx, UpgradeOptions{To: FormatImmutable}); err != nil {
		t.Fatal(err)
	}
}

// Two commands that write one address from the view they both read: one wins and
// the other is told the record changed.
func TestImmutableSetIsCompareAndSwap(t *testing.T) {
	for _, bk := range testBackends() {
		t.Run(bk.name, func(t *testing.T) {
			b := bk.make(t)
			seed := openFormat2(t, b)
			mustSet(t, seed, node("a"))

			const writers = 8
			stores := openMany(t, b, writers)
			var (
				wins, changed atomic.Int64
				wg            sync.WaitGroup
				start         = make(chan struct{})
			)
			for i, s := range stores {
				wg.Add(1)
				go func() {
					defer wg.Done()
					r := *s.Get("t.a")
					r.Attributes = json.RawMessage(fmt.Sprintf(`{"writer":%d}`, i))
					<-start
					switch err := s.Set(&r, ""); {
					case err == nil:
						wins.Add(1)
					case errors.Is(err, ErrChanged):
						changed.Add(1)
					default:
						t.Errorf("writer %d: %v", i, err)
					}
				}()
			}
			close(start)
			wg.Wait()
			if wins.Load() != 1 || changed.Load() != writers-1 {
				t.Fatalf("%d writers won and %d were told the record changed, want 1 and %d", wins.Load(), changed.Load(), writers-1)
			}
			final, err := OpenBackend(context.Background(), b)
			if err != nil {
				t.Fatal(err)
			}
			if got := final.Get("t.a"); got == nil || got.Revision != 2 {
				t.Errorf("the record after the race = %+v, want revision 2", got)
			}

			// A node that two commands create: one wins.
			racers := openMany(t, b, 2)
			first := racers[0].Set(node("new"), "")
			second := racers[1].Set(node("new"), "")
			if first != nil || !errors.Is(second, ErrChanged) {
				t.Errorf("two creations of one node: %v and %v, want nil and ErrChanged", first, second)
			}
			if racers[1].Get("t.new") != nil {
				t.Error("the store that lost holds the node it did not write")
			}
		})
	}
}

// A bucket that answers 412 to a replace whose compare the store accepted is a
// lost race, not a changed record: the store reads the pointer again.
type lostRace struct {
	Backend
	prefix string
	lose   atomic.Int64 // how many accepted replaces to refuse
}

func (b *lostRace) ReplaceIf(ctx context.Context, key string, update func([]byte) ([]byte, bool)) (bool, error) {
	if strings.HasPrefix(key, b.prefix) && b.lose.Load() > 0 {
		cur, err := b.Get(ctx, key)
		if err != nil {
			return false, err
		}
		if _, ok := update(cur); ok {
			b.lose.Add(-1)
			return false, nil
		}
	}
	return b.Backend.ReplaceIf(ctx, key, update)
}

func TestImmutableTellsALostRaceFromAChangedRecord(t *testing.T) {
	lb, _ := NewLocalBackend(t.TempDir())
	b := &lostRace{Backend: lb, prefix: "resources/"}
	s := openFormat2(t, b)
	mustSet(t, s, node("a"))

	b.lose.Store(2)
	r := *s.Get("t.a")
	r.Attributes = json.RawMessage(`{"x":1}`)
	if err := s.Set(&r, ""); err != nil {
		t.Fatalf("Set after two lost races: %v", err)
	}
	if got := pointerAt(t, lb, "t.a"); got.Revision != 2 {
		t.Errorf("revision after the retried write = %d", got.Revision)
	}

	// A store that never wins is told, in the end, that the record changed.
	b.lose.Store(1000)
	r2 := *s.Get("t.a")
	r2.Attributes = json.RawMessage(`{"x":2}`)
	if err := s.Set(&r2, ""); !errors.Is(err, ErrChanged) {
		t.Errorf("Set that lost every race: %v, want ErrChanged", err)
	}
	if got := s.Get("t.a"); got.Revision != 2 {
		t.Errorf("a write that failed changed the node in memory: %+v", got)
	}
	b.lose.Store(0)

	// A refused removal is a changed record as well, and a removal of a pointer that
	// is gone is a removal.
	stale := openMany(t, lb, 1)[0]
	r3 := *s.Get("t.a")
	r3.Attributes = json.RawMessage(`{"x":3}`)
	mustSet(t, s, &r3)
	if ok, err := stale.Remove("t.a"); !ok || !errors.Is(err, ErrChanged) {
		t.Errorf("Remove from a view that is out of date = %v, %v, want ErrChanged", ok, err)
	}
	if stale.Get("t.a") == nil {
		t.Error("a refused removal removed the node from memory")
	}
	if _, err := OpenBackend(context.Background(), lb); err != nil {
		t.Fatal(err)
	}
	if ok, err := s.Remove("t.a"); !ok || err != nil {
		t.Fatalf("Remove = %v, %v", ok, err)
	}
	if keys := listKeys(t, lb, "resources"); len(keys) != 0 {
		t.Errorf("pointers after a removal: %v", keys)
	}
	if len(listKeys(t, lb, "rec")) == 0 {
		t.Error("a removal deleted the immutable records")
	}
	// The store that was out of date holds a node whose pointer is gone.
	if ok, err := stale.Remove("t.a"); !ok || err != nil {
		t.Errorf("Remove of a pointer that is already gone = %v, %v", ok, err)
	}
}

// failAfter lets n writes through and fails the ones after.
type failAfter struct {
	Backend
	left atomic.Int64
	seen atomic.Int64
}

var errCrash = errors.New("the machine went down")

func (b *failAfter) allow() error {
	b.seen.Add(1)
	if b.left.Add(-1) < 0 {
		return errCrash
	}
	return nil
}

func (b *failAfter) Put(ctx context.Context, key string, data []byte) error {
	if err := b.allow(); err != nil {
		return err
	}
	return b.Backend.Put(ctx, key, data)
}

func (b *failAfter) PutIfAbsent(ctx context.Context, key string, data []byte) (bool, error) {
	if err := b.allow(); err != nil {
		return false, err
	}
	return b.Backend.PutIfAbsent(ctx, key, data)
}

func (b *failAfter) ReplaceIf(ctx context.Context, key string, update func([]byte) ([]byte, bool)) (bool, error) {
	if err := b.allow(); err != nil {
		return false, err
	}
	return b.Backend.ReplaceIf(ctx, key, update)
}

func (b *failAfter) DeleteIf(ctx context.Context, key string, match func([]byte) bool) (bool, error) {
	if err := b.allow(); err != nil {
		return false, err
	}
	return b.Backend.DeleteIf(ctx, key, match)
}

func (b *failAfter) Delete(ctx context.Context, key string) error {
	if err := b.allow(); err != nil {
		return err
	}
	return b.Backend.Delete(ctx, key)
}

// A machine that goes down at any point of an upgrade leaves a store that every
// pravo that knows the format reads, whole, and that the upgrade finishes when
// it is run again. Once meta.json says format 2, a pravo that does not know the
// format refuses the store; before that, nothing has changed.
func TestUpgradeToImmutableSurvivesACrash(t *testing.T) {
	ctx := context.Background()
	build := func(t *testing.T) (*Store, *failAfter, Backend) {
		lb, _ := NewLocalBackend(t.TempDir())
		s, err := OpenBackendWith(ctx, lb, Options{NewFormat: Format})
		if err != nil {
			t.Fatal(err)
		}
		seedRecords(t, s)
		// Open again over the wrapper, so that the upgrade runs through it.
		fb := &failAfter{Backend: lb}
		fb.left.Store(1 << 30)
		s, err = OpenBackend(ctx, fb)
		if err != nil {
			t.Fatal(err)
		}
		return s, fb, lb
	}
	// A run to count the writes of a whole upgrade.
	s, fb, _ := build(t)
	if _, err := s.Upgrade(ctx, UpgradeOptions{To: FormatImmutable}); err != nil {
		t.Fatal(err)
	}
	total := int(fb.seen.Load())
	if total < 2*s.Len() {
		t.Fatalf("an upgrade of %d records made %d writes", s.Len(), total)
	}
	for _, n := range []int{0, 1, 2, total / 2, total - 1} {
		t.Run(fmt.Sprintf("after %d of %d writes", n, total), func(t *testing.T) {
			s, fb, lb := build(t)
			want := listJSON(t, s)
			fb.left.Store(int64(n))
			if _, err := s.Upgrade(ctx, UpgradeOptions{To: FormatImmutable}); !errors.Is(err, errCrash) {
				t.Fatalf("upgrade that crashed: %v", err)
			}
			if got, wantFormat := formatOf(t, lb), map[bool]int{true: Format, false: FormatImmutable}[n == 0]; got != wantFormat {
				t.Errorf("format after the crash = %d, want %d", got, wantFormat)
			}
			mid, err := OpenBackend(ctx, lb)
			if err != nil {
				t.Fatalf("open after the crash: %v", err)
			}
			if listJSON(t, mid) != want {
				t.Errorf("the store after the crash lists other records")
			}
			if v, err := mid.VerifyAll(ctx); err != nil || len(v.Bad) != 0 {
				t.Errorf("VerifyAll after the crash = %+v, %v", v, err)
			}

			// Run it again.
			res, err := mid.Upgrade(ctx, UpgradeOptions{To: FormatImmutable})
			if err != nil {
				t.Fatalf("second upgrade: %v", err)
			}
			if n > 0 && res.From != FormatImmutable {
				t.Errorf("second upgrade starts from format %d", res.From)
			}
			done, err := OpenBackend(ctx, lb)
			if err != nil {
				t.Fatal(err)
			}
			if listJSON(t, done) != want {
				t.Errorf("the finished store lists other records")
			}
			if v, err := done.VerifyAll(ctx); err != nil || len(v.Bad) != 0 || v.Checked != done.Len() {
				t.Errorf("VerifyAll after the second upgrade = %+v, %v", v, err)
			}
			for _, r := range done.List() {
				pointerAt(t, lb, r.Address())
			}
			if fmt.Sprint(done.Outputs()) != fmt.Sprint(s.Outputs()) {
				t.Error("the outputs changed")
			}
		})
	}
}

// A store whose upgrade was interrupted, and that a command then writes to, is
// still one store: a record that is not yet a pointer is converted by the write.
func TestImmutableReadsARecordThatIsNotYetAPointer(t *testing.T) {
	ctx := context.Background()
	b, _ := NewLocalBackend(t.TempDir())
	s := openFormat2(t, b)
	mustSet(t, s, node("a"), node("b"))
	// Put the record of b back where format 1 kept it.
	legacy, _ := indentJSON(s.Get("t.b"))
	if err := b.Put(ctx, nodeKey("t.b"), legacy); err != nil {
		t.Fatal(err)
	}
	again, err := OpenBackend(ctx, b)
	if err != nil {
		t.Fatal(err)
	}
	if listJSON(t, again) != listJSON(t, s) {
		t.Error("a store with a record that is not a pointer lists other records")
	}
	r := *again.Get("t.b")
	r.Attributes = json.RawMessage(`{"x":1}`)
	mustSet(t, again, &r)
	if got := pointerAt(t, b, "t.b"); got.Revision != 2 {
		t.Errorf("revision = %d", got.Revision)
	}
	// A command that read the record before that is told it changed.
	if err := b.Put(ctx, nodeKey("t.a"), mustIndent(t, s.Get("t.a"))); err != nil {
		t.Fatal(err)
	}
	other, err := OpenBackend(ctx, b)
	if err != nil {
		t.Fatal(err)
	}
	mustSet(t, other, node("a2"))
	if err := convertOne(t, other, "t.a"); err != nil {
		t.Fatal(err)
	}
	r2 := *other.Get("t.a")
	r2.Attributes = json.RawMessage(`{"y":1}`)
	if err := other.Set(&r2, ""); err != nil {
		t.Fatal(err)
	}
}

func mustIndent(t *testing.T, v any) []byte {
	t.Helper()
	data, err := indentJSON(v)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// convertOne converts the record at addr as an upgrade does, by hand.
func convertOne(t *testing.T, s *Store, addr string) error {
	t.Helper()
	_, err := s.convertNode(context.Background(), nodeKey(addr), rewrite{
		needs:  func(_ string, isPtr bool) bool { return !isPtr },
		encode: func(_ *Resource, raw []byte) ([]byte, string, error) { return raw, "", nil },
	})
	return err
}

// Everything a reader checks about a record: its bytes against its name, the
// pointer against the record, the key against the pointer.
func TestImmutableDetectsWhatDoesNotMatch(t *testing.T) {
	ctx := context.Background()
	type tamper struct {
		name string
		do   func(t *testing.T, b Backend)
		// unknown is set for a change that is not a verification failure: a pointer
		// that a newer pravo wrote.
		unknown bool
	}
	rewritePointer := func(addr string, f func(p *pointer)) func(t *testing.T, b Backend) {
		return func(t *testing.T, b Backend) {
			p := pointerAt(t, b, addr)
			f(&p)
			if err := b.Put(ctx, nodeKey(addr), mustIndent(t, p)); err != nil {
				t.Fatal(err)
			}
		}
	}
	for _, c := range []tamper{
		{"a byte of the record", func(t *testing.T, b Backend) {
			p := pointerAt(t, b, "t.a")
			data, _ := b.Get(ctx, p.Record)
			data = bytes.Replace(data, []byte(`"a"`), []byte(`"z"`), 1)
			if err := b.Put(ctx, p.Record, data); err != nil {
				t.Fatal(err)
			}
		}, false},
		{"the revision of the pointer", rewritePointer("t.a", func(p *pointer) { p.Revision = 9 }), false},
		{"the generation of the pointer", rewritePointer("t.a", func(p *pointer) { p.Generation = "other" }), false},
		{"the address of the pointer", rewritePointer("t.a", func(p *pointer) { p.Address = "t.b" }), false},
		{"the record of another address", func(t *testing.T, b Backend) {
			other := pointerAt(t, b, "t.b")
			rewritePointer("t.a", func(p *pointer) { p.Record = other.Record })(t, b)
		}, false},
		{"a record that is not a record name", rewritePointer("t.a", func(p *pointer) { p.Record = "rec/../meta.json" }), false},
		{"a pointer of a version that is not known", rewritePointer("t.a", func(p *pointer) { p.V = 7 }), true},
	} {
		t.Run(c.name, func(t *testing.T) {
			lb, _ := NewLocalBackend(t.TempDir())
			s := openFormat2(t, lb)
			mustSet(t, s, node("a"), node("b"))
			c.do(t, lb)
			_, err := OpenBackend(ctx, lb)
			if c.unknown {
				if err == nil || errors.Is(err, ErrUntrusted) {
					t.Fatalf("a pointer of version 7: %v, want an error that says so", err)
				}
				return
			}
			if !errors.Is(err, ErrUntrusted) {
				t.Fatalf("Open = %v, want ErrUntrusted", err)
			}
			if !strings.Contains(err.Error(), "t.a") && !strings.Contains(err.Error(), "rec/") {
				t.Errorf("the error does not name the object: %v", err)
			}
			v, verr := s.VerifyAll(ctx)
			if verr != nil || len(v.Bad) != 1 || !strings.Contains(v.Bad[0], "t.a") {
				t.Errorf("VerifyAll = %+v, %v, want t.a to be bad", v, verr)
			}
		})
	}
}

// vanishOnce hides the objects with the prefix from the first read of each.
type vanishOnce struct {
	Backend
	prefix string
	mu     sync.Mutex
	seen   map[string]bool
	always bool
}

func (b *vanishOnce) Get(ctx context.Context, key string) ([]byte, error) {
	if strings.HasPrefix(key, b.prefix) {
		b.mu.Lock()
		first := !b.seen[key]
		if b.seen == nil {
			b.seen = map[string]bool{}
		}
		b.seen[key] = true
		b.mu.Unlock()
		if first || b.always {
			return nil, ErrNotFound
		}
	}
	return b.Backend.Get(ctx, key)
}

// A record that is gone when a pointer names it is looked for again, because
// the pointer may have been replaced since it was read; one that stays gone is a
// store that fails verification.
func TestImmutableRereadsAVanishedRecord(t *testing.T) {
	ctx := context.Background()
	lb, _ := NewLocalBackend(t.TempDir())
	mustSet(t, openFormat2(t, lb), node("a"))

	if s, err := OpenBackend(ctx, &vanishOnce{Backend: lb, prefix: "rec/"}); err != nil || s.Get("t.a") == nil {
		t.Errorf("a record that vanished once: %v", err)
	}
	_, err := OpenBackend(ctx, &vanishOnce{Backend: lb, prefix: "rec/", always: true})
	if !errors.Is(err, ErrUntrusted) {
		t.Errorf("a record that is gone: %v, want ErrUntrusted", err)
	}
	if v, err := (&Store{b: &vanishOnce{Backend: lb, prefix: "rec/", always: true}, meta: meta{Format: 2}, ctx: ctx}).VerifyAll(ctx); err != nil || len(v.Bad) != 1 {
		t.Errorf("VerifyAll of a missing record = %+v, %v", v, err)
	}
}

// A copy of a state carries the immutable records, and puts them in the
// destination before the pointers that name them.
func TestCopyCarriesTheImmutableRecords(t *testing.T) {
	ctx := context.Background()
	src, _ := NewLocalBackend(t.TempDir())
	s := openFormat2(t, src)
	seedRecords(t, s)
	_, remote := newFakeS3(t)
	order := &recordingBackend{Backend: remote}
	n, err := Copy(ctx, src, order)
	if err != nil {
		t.Fatal(err)
	}
	if want := s.Len()*2 + 3; n != want { // records, pointers, a transaction, outputs and meta
		t.Errorf("copied %d objects, want %d", n, want)
	}
	if order.keys[0] != "meta.json" {
		t.Errorf("the first object copied is %s, want meta.json, so that an older pravo refuses the copy before a pointer is in it", order.keys[0])
	}
	lastRec, firstPtr := -1, len(order.keys)
	for i, k := range order.keys {
		switch {
		case strings.HasPrefix(k, "rec/"):
			lastRec = i
		case strings.HasPrefix(k, "resources/") && i < firstPtr:
			firstPtr = i
		}
	}
	if lastRec > firstPtr {
		t.Errorf("a record was copied after a pointer: %v", order.keys)
	}
	copied, err := OpenBackend(ctx, remote)
	if err != nil {
		t.Fatal(err)
	}
	if listJSON(t, copied) != listJSON(t, s) {
		t.Error("the copy lists other records")
	}
	back, _ := NewLocalBackend(t.TempDir())
	if _, err := Copy(ctx, remote, back); err != nil {
		t.Fatal(err)
	}
	if again, err := OpenBackend(ctx, back); err != nil || listJSON(t, again) != listJSON(t, s) {
		t.Errorf("the copy of the copy: %v", err)
	}
}

type recordingBackend struct {
	Backend
	mu   sync.Mutex
	keys []string
}

func (b *recordingBackend) Put(ctx context.Context, key string, data []byte) error {
	b.mu.Lock()
	b.keys = append(b.keys, key)
	b.mu.Unlock()
	return b.Backend.Put(ctx, key, data)
}

// The stores of the suites that run over format 2 are created through the same
// door as every other: NewStoreFormat is the one switch.
func TestNewStoreFormatIsTheDefault(t *testing.T) {
	if Format != 1 {
		t.Fatalf("the default format is %d; the layout of format 1 is what the documents capture", Format)
	}
	if NewStoreFormat < Format || NewStoreFormat > FormatSealed {
		t.Errorf("NewStoreFormat = %d", NewStoreFormat)
	}
}

// A transaction records the version of the pravo that began it, when the program
// has set one.
func TestTransactionRecordsThePravoVersion(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer func(v string) { PravoVersion = v }(PravoVersion)
	for _, version := range []string{"", "1.2.3-test"} {
		PravoVersion = version
		txn := s.Begin("apply", "pravo apply")
		txn.Record("t.a", "create", 0, 1)
		if err := txn.Finish("applied", "", true); err != nil {
			t.Fatal(err)
		}
		txns, err := s.Transactions()
		if err != nil || len(txns) == 0 {
			t.Fatal(err)
		}
		var got Transaction
		for _, x := range txns {
			if x.ID == txn.ID() {
				got = x
			}
		}
		if got.Version != version {
			t.Errorf("transaction version = %q, want %q", got.Version, version)
		}
		raw, _ := s.b.Get(s.ctx, "txn/"+txn.ID()+".json")
		if has := strings.Contains(string(raw), `"version"`); has != (version != "") {
			t.Errorf("version %q: the transaction file has a version key: %v", version, has)
		}
	}
}

// A store in format 1, which is what protection off means, is laid out as it always
// was: meta.json has three keys, a node is the whole record, and nothing else
// is written.
func TestFormatOneLayoutIsUnchanged(t *testing.T) {
	for _, bk := range testBackends() {
		t.Run(bk.name, func(t *testing.T) {
			ctx := context.Background()
			b := bk.make(t)
			s, err := OpenBackendWith(ctx, b, Options{NewFormat: Format})
			if err != nil {
				t.Fatal(err)
			}
			seedRecords(t, s)
			raw, _ := b.Get(ctx, "meta.json")
			var keys map[string]json.RawMessage
			if err := json.Unmarshal(raw, &keys); err != nil || len(keys) != 3 || keys["format"] == nil || keys["lineage"] == nil || keys["created"] == nil {
				t.Errorf("meta.json = %s", raw)
			}
			want := "{\n  \"format\": 1,\n  \"lineage\": \"" + s.Lineage() + "\",\n  \"created\": "
			if !strings.HasPrefix(string(raw), want) || !strings.HasSuffix(string(raw), "\"\n}\n") {
				t.Errorf("meta.json is not laid out as before:\n%s", raw)
			}
			for _, r := range s.List() {
				got, err := b.Get(ctx, nodeKey(r.Address()))
				if err != nil {
					t.Fatal(err)
				}
				if exp := mustIndent(t, r); !bytes.Equal(got, exp) {
					t.Errorf("%s is not the whole record as indentJSON writes it:\n%s", nodeKey(r.Address()), got)
				}
			}
			if keys := listKeys(t, b, "rec"); len(keys) != 0 {
				t.Errorf("a store in format 1 holds records: %v", keys)
			}
			out, _ := b.Get(ctx, "outputs.json")
			if exp := mustIndent(t, s.Outputs()); !bytes.Equal(out, exp) {
				t.Errorf("outputs.json = %s", out)
			}
			if _, err := s.Upgrade(ctx, UpgradeOptions{To: FormatImmutable}); err != nil {
				t.Fatal(err)
			}
			raw, _ = b.Get(ctx, "meta.json")
			if !strings.Contains(string(raw), `"format": 2`) || strings.Contains(string(raw), "sealed") || strings.Contains(string(raw), "migration") {
				t.Errorf("meta.json of format 2 = %s", raw)
			}
		})
	}
}

// goneBackend answers "not found" for the next gone reads of a record, as a
// bucket does for a record that a refresher deleted a moment after the pointer
// was read.
type goneBackend struct {
	Backend
	mu   sync.Mutex
	gone int
	hits int
}

func (g *goneBackend) Get(ctx context.Context, key string) ([]byte, error) {
	if strings.HasPrefix(key, recPrefix) {
		g.mu.Lock()
		g.hits++
		if g.gone > 0 {
			g.gone--
			g.mu.Unlock()
			return nil, ErrNotFound
		}
		g.mu.Unlock()
	}
	return g.Backend.Get(ctx, key)
}

func TestReadNodeReadsAgainWhenARefresherIsFaster(t *testing.T) {
	ctx := context.Background()
	local, _ := NewLocalBackend(t.TempDir())
	g := &goneBackend{Backend: local}
	s, err := OpenBackendWith(ctx, g, Options{NewFormat: FormatImmutable})
	if err != nil {
		t.Fatal(err)
	}
	mustSet(t, s, &Resource{Mode: "managed", Type: "t", Name: "a", Attributes: json.RawMessage(`{"id":"x"}`)})
	key := nodeKey("t.a")

	g.gone, g.hits = readAttempts-1, 0
	start := time.Now()
	if r, _, err := s.readNode(ctx, key); err != nil || r == nil {
		t.Fatalf("readNode after %d misses = %v, %v, want the record", readAttempts-1, r, err)
	}
	if g.hits != readAttempts {
		t.Errorf("%d reads of the record, want %d", g.hits, readAttempts)
	}
	if took := time.Since(start); took < readPause(0) {
		t.Errorf("readNode did not wait between the attempts (%v)", took)
	}

	g.gone = readAttempts
	if _, _, err := s.readNode(ctx, key); !errors.Is(err, ErrUntrusted) {
		t.Errorf("readNode of a record that stays gone = %v, want ErrUntrusted", err)
	}
}
