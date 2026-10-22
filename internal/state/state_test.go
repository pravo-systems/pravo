package state

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestStoreRoundTrip(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "pravo.state")
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if s.Len() != 0 || s.Lineage() == "" {
		t.Fatalf("unexpected fresh store: %d nodes, lineage %q", s.Len(), s.Lineage())
	}
	txn := s.Begin("apply", "test")
	a := &Resource{Mode: "managed", Type: "random_pet", Name: "a", Attributes: json.RawMessage(`{"id":"x"}`), Dependencies: []string{"random_pet.b"}, RefreshedAt: Now()}
	b := &Resource{Mode: "managed", Type: "random_pet", Name: "b", Attributes: json.RawMessage(`{"id":"y"}`)}
	d := &Resource{Mode: "data", Type: "local_file", Name: "f", Attributes: json.RawMessage(`{}`), Dependencies: []string{"random_pet.a"}}
	for _, r := range []*Resource{a, b, d} {
		if err := s.Set(r, txn.ID()); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Set(a, txn.ID()); err != nil { // second write bumps the revision
		t.Fatal(err)
	}
	txn.Record("random_pet.a", "create", 0, a.Revision)
	if err := txn.Finish("applied", "ok", true); err != nil {
		t.Fatal(err)
	}
	if err := s.SetOutputs(map[string]json.RawMessage{"pet": json.RawMessage(`"x"`)}); err != nil {
		t.Fatal(err)
	}

	s2, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if s2.Lineage() != s.Lineage() {
		t.Error("lineage changed on reopen")
	}
	if got := s2.Addresses(); len(got) != 3 || got[0] != "data.local_file.f" || got[1] != "random_pet.a" {
		t.Errorf("addresses = %v", got)
	}
	ra := s2.Get("random_pet.a")
	if ra == nil || ra.Revision != 2 || ra.Txn != txn.ID() || ra.RefreshedAt == nil {
		t.Errorf("node a = %+v", ra)
	}
	if string(s2.Outputs()["pet"]) != `"x"` {
		t.Errorf("outputs = %v", s2.Outputs())
	}
	if deps := s2.Dependents("random_pet.b"); len(deps) != 1 || deps[0] != "random_pet.a" {
		t.Errorf("dependents of b = %v", deps)
	}
	if reach := s2.Reach("random_pet.b"); len(reach) != 2 {
		t.Errorf("reach of b = %v", reach)
	}
	txns, err := s2.Transactions()
	if err != nil || len(txns) != 1 || txns[0].Kind != "apply" || len(txns[0].Changes) != 1 {
		t.Errorf("transactions = %+v (%v)", txns, err)
	}
	if ok, err := s2.Remove("random_pet.a"); !ok || err != nil {
		t.Errorf("remove: %v %v", ok, err)
	}
	if ok, _ := s2.Remove("random_pet.a"); ok {
		t.Error("second remove should report absent")
	}
}

func TestLocks(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "st"))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Lock("a.b", "test", ""); err != nil {
		t.Fatal(err)
	}
	locks, _ := s.Locks()
	if len(locks) != 1 || locks[0].Addr != "a.b" || locks[0].PID != os.Getpid() {
		t.Errorf("locks = %+v", locks)
	}
	// A lock file from a dead process on this host is reclaimed.
	stale := Lock{Addr: "c.d", PID: 999999, Host: locks[0].Host, Since: time.Now()}
	data, _ := json.Marshal(stale)
	if err := s.b.Put(context.Background(), lockKey("c.d"), data); err != nil {
		t.Fatal(err)
	}
	if err := s.Lock("c.d", "test", ""); err != nil {
		t.Errorf("stale lock not reclaimed: %v", err)
	}
	// A lock from a live process elsewhere is honoured.
	live := Lock{Addr: "e.f", PID: os.Getpid(), Host: "other-host", Since: time.Now(), Command: "x"}
	data, _ = json.Marshal(live)
	_ = s.b.Put(context.Background(), lockKey("e.f"), data)
	if err := s.Lock("e.f", "test", ""); err == nil {
		t.Error("expected ErrLocked")
	} else if _, ok := err.(*ErrLocked); !ok {
		t.Errorf("expected *ErrLocked, got %T", err)
	}
	if err := s.Unlock("a.b"); err != nil {
		t.Error(err)
	}
	if ok, _ := s.TryLock("a.b", "test", ""); !ok {
		t.Error("lock should be free after unlock")
	}
}

func TestOpenRefusesAFile(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"terraform.tfstate", "pravo.state.json", "state"} {
		file := filepath.Join(dir, name)
		content := `{"version":4,"serial":1,"lineage":"abc","outputs":{},"resources":[]}`
		if err := os.WriteFile(file, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		s, err := Open(file)
		if err == nil || s != nil {
			t.Fatalf("Open(%s) = %v, %v; want an error", name, s, err)
		}
		for _, want := range []string{file, "is a file", "directory", "$settings.state.path", "import-tfstate"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("Open(%s) error %q lacks %q", name, err, want)
			}
		}
		// Nothing was read as a state, renamed, or created beside the file.
		if data, err := os.ReadFile(file); err != nil || string(data) != content {
			t.Errorf("%s changed: %q, %v", name, data, err)
		}
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	if got := strings.Join(names, " "); got != "pravo.state.json state terraform.tfstate" {
		t.Errorf("the directory holds %q, want only the three files", got)
	}
}

func TestOpenIgnoresAJSONFileBesideThePath(t *testing.T) {
	dir := t.TempDir()
	old := `{"version":1,"serial":3,"lineage":"abc","outputs":{"o":"1"},"resources":[{"mode":"managed","type":"t","name":"n","provider":"p","provider_key":"t","schema_version":0,"attributes":{"id":"1"}}]}`
	if err := os.WriteFile(filepath.Join(dir, "pravo.state.json"), []byte(old), 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := Open(filepath.Join(dir, "pravo.state"))
	if err != nil {
		t.Fatal(err)
	}
	if s.Lineage() == "abc" || s.Len() != 0 || len(s.Outputs()) != 0 {
		t.Errorf("a file beside the path was read as a state: lineage %s, %d nodes, outputs %v", s.Lineage(), s.Len(), s.Outputs())
	}
	if _, err := os.Stat(filepath.Join(dir, "pravo.state.json")); err != nil {
		t.Errorf("the file beside the path was moved: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "pravo.state.json.migrated")); err == nil {
		t.Error("the file beside the path was renamed")
	}
}

func TestRecordsKeepMarkupCharacters(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "st")
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	html := "<h1>Tom & Jerry</h1>"
	if err := s.SetOutputs(map[string]json.RawMessage{"page": json.RawMessage(`"` + html + `"`)}); err != nil {
		t.Fatal(err)
	}
	attrs := json.RawMessage(`{"content":"` + html + `"}`)
	if err := s.Set(&Resource{Mode: "managed", Type: "t", Name: "n", Attributes: attrs}, ""); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"outputs.json", nodeKey("t.n")} {
		data, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(key)))
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(data), `\u00`) || !json.Valid(data) {
			t.Errorf("%s is escaped or invalid:\n%s", key, data)
		}
	}
	// The values read back as they were written.
	r, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(r.Outputs()["page"]); got != `"`+html+`"` {
		t.Errorf("output = %s", got)
	}
	var back map[string]string
	if err := json.Unmarshal(r.Get("t.n").Attributes, &back); err != nil || back["content"] != html {
		t.Errorf("attributes = %s, %v", r.Get("t.n").Attributes, err)
	}
}

func TestParseAddress(t *testing.T) {
	for _, tc := range []struct{ in, mode, typ, name string }{
		{"random_pet.a", "managed", "random_pet", "a"},
		{"data.local_file.f", "data", "local_file", "f"},
	} {
		m, ty, n, err := ParseAddress(tc.in)
		if err != nil || m != tc.mode || ty != tc.typ || n != tc.name {
			t.Errorf("ParseAddress(%q) = %s %s %s %v", tc.in, m, ty, n, err)
		}
	}
	if _, _, _, err := ParseAddress("a.b.c"); err == nil {
		t.Error("expected error for a.b.c")
	}
	if encodeAddr(`aws_s3_bucket.acme-logs`) != "aws_s3_bucket.acme-logs" || encodeAddr("t.a b") != "t.a%0020b" {
		t.Errorf("encodeAddr: %q %q", encodeAddr("aws_s3_bucket.acme-logs"), encodeAddr("t.a b"))
	}
}

func TestRefreshKeepsRevisionWhenUnchanged(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "st"))
	if err != nil {
		t.Fatal(err)
	}
	mk := func(attrs string) *Resource {
		return &Resource{Mode: "managed", Type: "random_pet", Name: "x", Attributes: []byte(attrs)}
	}
	if err := s.Set(mk(`{"id":"a"}`), ""); err != nil {
		t.Fatal(err)
	}
	if err := s.Refresh(mk(`{"id":"a"}`), ""); err != nil {
		t.Fatal(err)
	}
	if got := s.Get("random_pet.x").Revision; got != 1 {
		t.Fatalf("unchanged refresh bumped the revision to %d", got)
	}
	if err := s.Refresh(mk(`{"id":"b"}`), ""); err != nil {
		t.Fatal(err)
	}
	if got := s.Get("random_pet.x").Revision; got != 2 {
		t.Fatalf("changed refresh left the revision at %d", got)
	}
}

// openAs opens the store in dir as a separate command run by the given actor
// label; "" is a run without one.
func openAs(t *testing.T, dir, actor string) *Store {
	t.Helper()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	s.SetActor(actor, "", "")
	return s
}

func reservation(t *testing.T, s *Store, addr string) Lock {
	t.Helper()
	l, err := s.readLock(addr)
	if err != nil {
		t.Fatalf("reservation of %s: %v", addr, err)
	}
	if l.Kind != "reservation" {
		t.Fatalf("lock of %s is a %q, want a reservation", addr, l.Kind)
	}
	return l
}

func isLocked(err error) bool {
	_, ok := err.(*ErrLocked)
	return ok
}

// A reservation taken without an actor label is owned by the operating system
// user, so the same user's later commands recognise it.
func TestUnlabelledReservationBelongsToTheOSUser(t *testing.T) {
	if osUser() == "" {
		t.Skip("no operating system user")
	}
	dir := filepath.Join(t.TempDir(), "st")
	first := openAs(t, dir, "")
	if first.Owner() != osUser() {
		t.Fatalf("owner = %q, want %q", first.Owner(), osUser())
	}
	until := time.Now().Add(time.Hour)
	if _, err := first.ReserveAll([]string{"a"}, "plan", until); err != nil {
		t.Fatal(err)
	}

	again := openAs(t, dir, "")
	if !again.OwnsReservation(reservation(t, again, "a")) {
		t.Error("a later command of the same user does not own the reservation")
	}
	if _, err := again.ReserveAll([]string{"a"}, "plan", until); err != nil {
		t.Errorf("second reserve by the same user: %v", err)
	}
	if err := again.Lock("a", "apply", ""); err != nil {
		t.Errorf("apply by the same user: %v", err)
	}
	_ = again.Unlock("a")

	// Another label is another owner, and so is a label for the same user.
	if _, err := first.ReserveAll([]string{"b"}, "plan", until); err != nil {
		t.Fatal(err)
	}
	for _, other := range []string{"agent", "someone-else"} {
		o := openAs(t, dir, other)
		if o.OwnsReservation(reservation(t, o, "b")) {
			t.Errorf("actor %q owns the reservation of an unlabelled run", other)
		}
		if _, err := o.ReserveAll([]string{"b"}, "plan", until); !isLocked(err) {
			t.Errorf("actor %q reserved the node of an unlabelled run: %v", other, err)
		}
		if err := o.Lock("b", "apply", ""); !isLocked(err) {
			t.Errorf("actor %q locked the node of an unlabelled run: %v", other, err)
		}
	}
	// A labelled reservation is not the unlabelled user's.
	labelled := openAs(t, dir, "agent")
	if _, err := labelled.ReserveAll([]string{"c"}, "plan", until); err != nil {
		t.Fatal(err)
	}
	if _, err := openAs(t, dir, "").ReserveAll([]string{"c"}, "plan", until); !isLocked(err) {
		t.Errorf("an unlabelled run reserved a node reserved by a label: %v", err)
	}
}

// A reservation made by an older build without an actor is read as the
// operating system user's.
func TestReservationWithoutActorIsMatchedByUser(t *testing.T) {
	if osUser() == "" {
		t.Skip("no operating system user")
	}
	s := openAs(t, filepath.Join(t.TempDir(), "st"), "")
	exp := time.Now().Add(time.Hour)
	old := Lock{Addr: "a", PID: 1, Host: "elsewhere", User: osUser(), Kind: "reservation", Expires: &exp, Since: time.Now()}
	data, _ := json.Marshal(old)
	if err := s.b.Put(context.Background(), lockKey("a"), data); err != nil {
		t.Fatal(err)
	}
	if !s.OwnsReservation(reservation(t, s, "a")) {
		t.Error("the user does not own a reservation recorded with their name and no actor")
	}
	foreign := Lock{Addr: "b", PID: 1, Host: "elsewhere", User: "not-" + osUser(), Kind: "reservation", Expires: &exp, Since: time.Now()}
	data, _ = json.Marshal(foreign)
	_ = s.b.Put(context.Background(), lockKey("b"), data)
	if s.OwnsReservation(reservation(t, s, "b")) {
		t.Error("the user owns the reservation of another user")
	}
}

// Writing a refreshed record takes the node's lock for the write and leaves
// the owner's reservation in place.
func TestTryHoldKeepsTheOwnersReservation(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "st")
	owner := openAs(t, dir, "agent")
	until := time.Now().Add(time.Hour).UTC()
	if _, err := owner.ReserveAll([]string{"a"}, "plan --reserve", until); err != nil {
		t.Fatal(err)
	}
	before := reservation(t, owner, "a")

	// The owner's refresh, from another process.
	refresher := openAs(t, dir, "agent")
	release, ok, err := refresher.TryHold("a", "plan refresh", "")
	if err != nil || !ok {
		t.Fatalf("TryHold = %v, %v", ok, err)
	}
	release()
	after := reservation(t, owner, "a")
	if after.Nonce != before.Nonce || after.Command != before.Command || !after.Expires.Equal(*before.Expires) {
		t.Errorf("the reservation changed: before %+v, after %+v", before, after)
	}

	// Another actor's refresh does not write, and does not disturb it either.
	if _, ok, err := openAs(t, dir, "bob").TryHold("a", "plan refresh", ""); ok || err != nil {
		t.Errorf("another actor's TryHold = %v, %v; want it refused without an error", ok, err)
	}
	if got := reservation(t, owner, "a"); got.Nonce != before.Nonce {
		t.Error("another actor's refresh changed the reservation")
	}

	// A free node is locked for the write and free again afterwards.
	release, ok, err = refresher.TryHold("free", "plan refresh", "")
	if err != nil || !ok {
		t.Fatalf("TryHold of a free node = %v, %v", ok, err)
	}
	if l, err := refresher.readLock("free"); err != nil || l.Kind != "lock" {
		t.Errorf("free node during the write: %+v, %v", l, err)
	}
	release()
	if _, err := refresher.readLock("free"); !errors.Is(err, ErrNotFound) {
		t.Errorf("free node after the write: %v", err)
	}

	// A lock this process holds is not released by its own refresh.
	if err := refresher.Lock("held", "apply", ""); err != nil {
		t.Fatal(err)
	}
	release, ok, _ = refresher.TryHold("held", "plan refresh", "")
	if !ok {
		t.Fatal("a process cannot write the node it has locked")
	}
	release()
	if l, err := refresher.readLock("held"); err != nil || l.Command != "apply" {
		t.Errorf("the apply's lock is gone after the refresh: %+v, %v", l, err)
	}

	// An apply by the owner still takes the reservation over.
	if err := refresher.Lock("a", "apply", ""); err != nil {
		t.Fatal(err)
	}
	if l := mustRead(t, refresher, "a"); l.Kind != "lock" {
		t.Errorf("after the apply took it: %+v", l)
	}
}

func mustRead(t *testing.T, s *Store, addr string) Lock {
	t.Helper()
	l, err := s.readLock(addr)
	if err != nil {
		t.Fatal(err)
	}
	return l
}

// deleteCounter counts deletions of lock objects.
type deleteCounter struct {
	Backend
	deletes int
}

func (d *deleteCounter) Delete(ctx context.Context, key string) error {
	if strings.HasPrefix(key, "locks/") {
		d.deletes++
	}
	return d.Backend.Delete(ctx, key)
}

// A second reservation by the owner rewrites the first in place, never leaves
// the node without one, and ends at the later of the two expiries.
func TestReserveExtendsAndNeverShortens(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "st")
	s := openAs(t, dir, "agent")
	counter := &deleteCounter{Backend: s.b}
	s.b = counter
	now := time.Now().UTC()
	long, short, longer := now.Add(30*time.Minute), now.Add(time.Minute), now.Add(2*time.Hour)

	ends, err := s.ReserveAll([]string{"a", "b"}, "plan one", long)
	if err != nil || !ends["a"].Equal(long) {
		t.Fatalf("first reserve: %v, %v", ends, err)
	}
	first := reservation(t, s, "a")

	// A shorter one keeps the later end; the command is the latest plan's.
	ends, err = s.ReserveAll([]string{"a", "b"}, "plan two", short)
	if err != nil {
		t.Fatal(err)
	}
	got := reservation(t, s, "a")
	if !got.Expires.Equal(long) || !ends["a"].Equal(long) {
		t.Errorf("shorter reserve: expires %v, reported %v; want %v", got.Expires, ends["a"], long)
	}
	if got.Command != "plan two" || !got.Since.Equal(first.Since) {
		t.Errorf("command %q, since %v; want the latest command and the original start %v", got.Command, got.Since, first.Since)
	}

	// A longer one extends it.
	if ends, err = s.ReserveAll([]string{"a"}, "plan three", longer); err != nil || !ends["a"].Equal(longer) {
		t.Fatalf("longer reserve: %v, %v", ends, err)
	}
	if got := reservation(t, s, "a"); !got.Expires.Equal(longer) {
		t.Errorf("longer reserve: expires %v, want %v", got.Expires, longer)
	}
	if counter.deletes != 0 {
		t.Errorf("%d lock objects were deleted: the reservation was not rewritten in place", counter.deletes)
	}

	// A reservation that has lapsed is replaced, with the new end.
	expired := now.Add(-time.Minute)
	old := first
	old.Expires = &expired
	data, _ := json.Marshal(old)
	_ = s.b.Put(context.Background(), lockKey("a"), data)
	if ends, err = s.ReserveAll([]string{"a"}, "plan four", short); err != nil || !ends["a"].Equal(short) {
		t.Fatalf("reserve after expiry: %v, %v", ends, err)
	}
}

// When one node cannot be reserved, no node is left reserved by the attempt,
// and the reservations held from an earlier plan are as they were.
func TestFailedReserveRestoresWhatTheOwnerHeld(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "st")
	s := openAs(t, dir, "agent")
	now := time.Now().UTC()
	earlier := now.Add(30 * time.Minute)
	if _, err := s.ReserveAll([]string{"a"}, "plan one", earlier); err != nil {
		t.Fatal(err)
	}
	before := reservation(t, s, "a")

	// c is applied right now by another actor, from another process.
	live := Lock{Addr: "c", PID: os.Getpid(), Host: "other-host", Actor: "bob", Kind: "lock", Command: "apply", Since: now, RenewedAt: now}
	data, _ := json.Marshal(live)
	_ = s.b.Put(context.Background(), lockKey("c"), data)

	_, err := openAs(t, dir, "agent").ReserveAll([]string{"a", "b", "c", "d"}, "plan two", now.Add(2*time.Hour))
	if !isLocked(err) {
		t.Fatalf("reserve = %v, want ErrLocked", err)
	}
	after := reservation(t, s, "a")
	if after.Nonce != before.Nonce || after.Command != "plan one" || !after.Expires.Equal(earlier) || !after.Since.Equal(before.Since) {
		t.Errorf("the reservation of a was not restored: before %+v, after %+v", before, after)
	}
	for _, addr := range []string{"b", "d"} {
		if l, err := s.readLock(addr); !errors.Is(err, ErrNotFound) {
			t.Errorf("%s is left locked by the failed attempt: %+v, %v", addr, l, err)
		}
	}
	if l := mustRead(t, s, "c"); l.Actor != "bob" || l.Kind != "lock" {
		t.Errorf("the lock of the other actor was changed: %+v", l)
	}
}

func formatOf(t *testing.T, b Backend) int {
	t.Helper()
	data, err := b.Get(context.Background(), "meta.json")
	if err != nil {
		t.Fatal(err)
	}
	var m meta
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatal(err)
	}
	return m.Format
}

// A new store is format 1, marks survive a reopen, and a store with a higher
// format is refused.
func TestFormat(t *testing.T) {
	ctx := context.Background()
	b, err := NewLocalBackend(filepath.Join(t.TempDir(), "st"))
	if err != nil {
		t.Fatal(err)
	}
	s, err := OpenBackend(ctx, b)
	if err != nil {
		t.Fatal(err)
	}
	// A new store is format 1, or the format that PRAVO_TEST_FORMAT asks the suite
	// to run over.
	want := NewStoreFormat
	if got := formatOf(t, b); got != want || s.Format() != want {
		t.Fatalf("a new store is format %d (in memory %d), want %d", got, s.Format(), want)
	}
	plain := &Resource{Mode: "managed", Type: "t", Name: "plain", Attributes: json.RawMessage(`{"id":"1"}`)}
	if err := s.Set(plain, ""); err != nil {
		t.Fatal(err)
	}
	when := time.Now().UTC()
	marked := *plain
	marked.Name, marked.DriftedAt = "drifted", &when
	if err := s.Set(&marked, ""); err != nil {
		t.Fatal(err)
	}
	if got := formatOf(t, b); got != want {
		t.Errorf("a store with a mark is format %d, want %d", got, want)
	}
	s2, err := OpenBackend(ctx, b)
	if err != nil {
		t.Fatal(err)
	}
	if got := s2.Get("t.drifted"); got == nil || got.DriftedAt == nil || !got.DriftedAt.Equal(when) {
		t.Errorf("the mark did not survive a reopen: %+v", got)
	}
	if got := s2.Get("t.plain"); got == nil || got.Marked() {
		t.Errorf("a record without a mark has one: %+v", got)
	}
	if err := b.Put(ctx, "meta.json", []byte(`{"format":99,"lineage":"abc","created":"2026-01-01T00:00:00Z"}`)); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenBackend(ctx, b); err == nil || !strings.Contains(err.Error(), "written by a newer pravo (format 99)") {
		t.Errorf("a store of format 99 opened: %v", err)
	}
}

// A refresh that changes a mark moves the revision, so that a saved plan made
// before it is stale; one that leaves the marks as they are keeps it.
func TestRefreshCountsMarksAsChanges(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "st"))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Set(&Resource{Mode: "managed", Type: "t", Name: "a", Attributes: json.RawMessage(`{"id":"1"}`)}, ""); err != nil {
		t.Fatal(err)
	}
	next := func(mod func(*Resource)) int {
		t.Helper()
		n := *s.Get("t.a")
		mod(&n)
		if err := s.Refresh(&n, ""); err != nil {
			t.Fatal(err)
		}
		return s.Get("t.a").Revision
	}
	if got := next(func(n *Resource) { n.RefreshedAt = Now() }); got != 1 {
		t.Errorf("a refresh that finds nothing moved the revision to %d", got)
	}
	when := time.Now().UTC()
	if got := next(func(n *Resource) { n.DriftedAt = &when }); got != 2 {
		t.Errorf("a new drifted_at left the revision at %d, want 2", got)
	}
	again := when
	if got := next(func(n *Resource) { n.DriftedAt = &again; n.RefreshedAt = Now() }); got != 2 {
		t.Errorf("the same drifted_at moved the revision to %d", got)
	}
	if got := next(func(n *Resource) { n.Gone = true }); got != 3 {
		t.Errorf("gone left the revision at %d, want 3", got)
	}
	if got := next(func(n *Resource) { n.Gone = false }); got != 4 {
		t.Errorf("clearing gone left the revision at %d, want 4", got)
	}
}

// Invalidate writes a node again without its refresh time. The revision moves,
// as for any write, and a node that is not there is not made.
func TestInvalidateMarksANodeForARead(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "pravo.state"))
	if err != nil {
		t.Fatal(err)
	}
	if _, _, found, err := st.Invalidate("absent", "t0"); err != nil || found {
		t.Fatalf("Invalidate of an absent node: found %v, err %v", found, err)
	}
	if st.Get("absent") != nil {
		t.Fatal("Invalidate made a node")
	}
	r := &Resource{Addr: "a", Mode: "managed", Type: "t", Name: "a", Attributes: []byte(`{"x":1}`), ConfigHash: "h", RefreshedAt: Now(), AppliedAt: Now()}
	if err := st.Set(r, "t1"); err != nil {
		t.Fatal(err)
	}
	gen := st.Get("a").Generation
	before, after, found, err := st.Invalidate("a", "t2")
	if err != nil || !found || before != 1 || after != 2 {
		t.Fatalf("Invalidate = %d, %d, %v, %v; want 1, 2, true", before, after, found, err)
	}
	got := st.Get("a")
	if got.RefreshedAt != nil || got.Revision != 2 || got.Txn != "t2" || got.Generation != gen || got.ConfigHash != "h" || string(got.Attributes) != `{"x":1}` || got.AppliedAt == nil {
		t.Errorf("after Invalidate: %+v", got)
	}
	reopened, err := Open(st.Path())
	if err != nil {
		t.Fatal(err)
	}
	if n := reopened.Get("a"); n == nil || n.RefreshedAt != nil || n.Revision != 2 {
		t.Errorf("the written record: %+v", n)
	}
}

// A node that an apply wrote and that has no refresh time is one that an apply
// could not settle. A node with no hash was not written by an apply: an import
// leaves it so, and every plan reaches it.
func TestNoReadTime(t *testing.T) {
	now := Now()
	for _, c := range []struct {
		name string
		r    Resource
		want bool
	}{
		{"read", Resource{ConfigHash: "h", RefreshedAt: now}, false},
		{"invalidated", Resource{ConfigHash: "h"}, true},
		{"imported", Resource{}, false},
		{"stopped halfway", Resource{AppliedAt: now, RefreshedAt: now}, false},
	} {
		if got := c.r.NoReadTime(); got != c.want {
			t.Errorf("%s: NoReadTime = %v, want %v", c.name, got, c.want)
		}
	}
}

// The digest of the provider's configuration is bookkeeping of the same kind as
// the refresh time: a refresh that changes it and nothing else is written and
// keeps the revision, so a saved plan made against the node stays current.
func TestRefreshKeepsTheRevisionWhenOnlyTheProviderDigestChanges(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "pravo.state"))
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Set(&Resource{Addr: "a", Mode: "managed", Type: "t", Name: "a", Attributes: []byte(`{"x":1}`), ProviderConfig: "one", RefreshedAt: Now()}, "t1"); err != nil {
		t.Fatal(err)
	}
	n := *st.Get("a")
	n.ProviderConfig = "two"
	n.RefreshedAt = Now()
	if err := st.Refresh(&n, "t2"); err != nil {
		t.Fatal(err)
	}
	got := st.Get("a")
	if got.Revision != 1 || got.ProviderConfig != "two" {
		t.Errorf("after the refresh: revision %d, digest %q; want revision 1 and digest two", got.Revision, got.ProviderConfig)
	}
	reopened, err := Open(st.Path())
	if err != nil {
		t.Fatal(err)
	}
	if r := reopened.Get("a"); r.ProviderConfig != "two" || r.Revision != 1 {
		t.Errorf("the written record: %+v", r)
	}
}
