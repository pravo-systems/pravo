package main

import (
	"context"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/pravo-systems/pravo/internal/state"
)

func TestAReaderMayReadAndAWriterMayWrite(t *testing.T) {
	s := sealedBucket(t)
	s.readers(t, s.adaLine+" role:writer", s.danLine+" role:reader")

	text, code := runPravo(t, s.dir, "--identity", "dan", "state", "rm", "net.vpc")
	if code != 5 || !strings.Contains(text, "your identity is a reader in") || !strings.Contains(text, "a writer is needed to change the state") {
		t.Fatalf("a reader's write: exit %d\n%s", code, text)
	}
	for _, args := range [][]string{{"state", "log"}, {"state", "list"}} {
		if text, code := runPravo(t, s.dir, append([]string{"--identity", "dan"}, args...)...); code != 0 {
			t.Errorf("a reader's %v: exit %d\n%s", args, code, text)
		}
	}
	if text, code := runPravo(t, s.dir, "--identity", "ada", "state", "rm", "net.vpc"); code != 0 {
		t.Fatalf("a writer's write: exit %d\n%s", code, text)
	}
}

func TestWithoutRolesEveryKeyMayWrite(t *testing.T) {
	s := sealedBucket(t)
	if text, code := runPravo(t, s.dir, "--identity", "dan", "state", "rm", "net.vpc"); code != 0 {
		t.Fatalf("exit %d\n%s", code, text)
	}
}

func TestALineWithoutARoleIsAReaderOnceRolesAreOn(t *testing.T) {
	s := sealedBucket(t)
	s.readers(t, s.adaLine+" role:admin", s.danLine)
	text, code := runPravo(t, s.dir, "--identity", "dan", "state", "rm", "net.vpc")
	if code != 5 || !strings.Contains(text, "your identity is a reader") {
		t.Fatalf("exit %d\n%s", code, text)
	}
}

// objects is a copy of the objects of the bucket.
func (s *sealed) objects() map[string]string {
	out := map[string]string{}
	for k, v := range s.f.Objects {
		out[k] = string(v)
	}
	return out
}

// plannableBucket is a sealed bucket that holds one data source, which a plan can be made
// of without a provider.
func plannableBucket(t *testing.T) *sealed {
	t.Helper()
	requireSealing(t)
	ids := isolateIdentities(t)
	e := newS3Env(t, false)
	ada, adaLine := person(t, ids, "ada")
	dan, danLine := person(t, ids, "dan")
	e.readers(t, adaLine, danLine)
	if text, code := runPravo(t, e.dir, "protect", "init", "--encrypt", "--yes"); code != 0 {
		t.Fatalf("protect init exited %d:\n%s", code, text)
	}
	// ada records the data source in a transaction of her own, as a command would.
	st, err := state.OpenBackendWith(context.Background(), e.backend(t), state.Options{Identity: ada})
	if err != nil {
		t.Fatal(err)
	}
	tx := st.Begin("edit", "pravo test")
	if err := tx.Start(); err != nil {
		t.Fatal(err)
	}
	attrs := fmt.Sprintf(`{"id":"cfg","password":%q}`, secretA)
	if err := st.Set(&state.Resource{Addr: "cfg", Mode: "data", Type: "t_data", Name: "cfg", Attributes: []byte(attrs)}, tx.ID()); err != nil {
		t.Fatal(err)
	}
	tx.Record("cfg", "create", 0, 1)
	if err := tx.Finish("applied", "", true); err != nil {
		t.Fatal(err)
	}
	return &sealed{e, ids, ada, dan, adaLine, danLine}
}

func TestAReaderPlansWithoutWritingTheState(t *testing.T) {
	s := plannableBucket(t)
	s.readers(t, s.adaLine+" role:writer", s.danLine+" role:reader")
	before := s.objects()

	text, code := runPravo(t, s.dir, "--identity", "dan", "plan")
	if code != 0 {
		t.Fatalf("a reader's plan: exit %d\n%s", code, text)
	}
	after := s.objects()
	for k, v := range after {
		if old, ok := before[k]; !ok || old != v {
			t.Errorf("the plan of a reader wrote %s", k)
		}
	}
	for k := range before {
		if _, ok := after[k]; !ok {
			t.Errorf("the plan of a reader deleted %s", k)
		}
	}
}

func TestAReaderStoresAPlanAndAWriterAppliesIt(t *testing.T) {
	s := plannableBucket(t)
	s.readers(t, s.adaLine+" role:writer", s.danLine+" role:reader")
	before := s.objects()

	text, code := runPravo(t, s.dir, "--identity", "dan", "plan", "--store")
	if code != 0 || !strings.Contains(text, "plan-") {
		t.Fatalf("a reader's plan --store: exit %d\n%s", code, text)
	}
	for k, v := range s.objects() {
		if old, ok := before[k]; (!ok || old != v) && !strings.Contains(k, "/plans/") {
			t.Errorf("the stored plan of a reader wrote %s", k)
		}
	}
	id := regexp.MustCompile(`plan-[0-9a-f]+`).FindString(text)
	if text, code := runPravo(t, s.dir, "--identity", "dan", "apply", id, "--auto-approve"); code != 5 {
		t.Fatalf("a reader's apply: exit %d\n%s", code, text)
	}
	if text, code := runPravo(t, s.dir, "--identity", "ada", "apply", id, "--auto-approve"); code == 5 || strings.Contains(text, "is needed to change the state") {
		// This bucket has no provider to apply with: what matters is that the role did not stop it.
		t.Fatalf("a writer's apply of the plan of a reader was refused: exit %d\n%s", code, text)
	}
}

func TestOnlyAnAdminChangesWhoReads(t *testing.T) {
	s := sealedBucket(t)
	s.readers(t, s.adaLine+" role:writer", s.danLine+" role:admin")
	for _, args := range [][]string{{"protect", "rewrap", "--yes"}, {"protect", "gc"}, {"protect", "unseal", "--yes"}} {
		text, code := runPravo(t, s.dir, append([]string{"--identity", "ada"}, args...)...)
		if code != 5 || !strings.Contains(text, "your identity is a writer in") || !strings.Contains(text, "an admin is needed to") {
			t.Errorf("a writer's %v: exit %d\n%s", args, code, text)
		}
	}
	if text, code := runPravo(t, s.dir, "--identity", "ada", "protect", "gc", "--dry-run"); code != 0 {
		t.Errorf("a dry run of gc by a writer: exit %d\n%s", code, text)
	}
	if text, code := runPravo(t, s.dir, "--identity", "dan", "protect", "gc"); code != 0 {
		t.Errorf("gc by an admin: exit %d\n%s", code, text)
	}
}

func TestOnlyAnAdminRemovesAPolicy(t *testing.T) {
	r := newPolicyRoot(t, map[string]string{"none": "summary: destroy: 0"}, "a")
	if text, code := r.apply(t); code != 0 {
		t.Fatalf("first apply: exit %d\n%s", code, text)
	}
	// dan joins while the repository has no roles, and then it gives them: ada writes, dan administers.
	_, dan := person(t, r.ids, "dan")
	ada := r.ada.AsReader().String()
	access := filepath.Join(r.dir, ".pravo", "access")
	writeReaders(t, access, ada, dan)
	if text, code := runPravo(t, r.dir, "protect", "rewrap", "--yes"); code != 0 {
		t.Fatalf("rewrap: exit %d\n%s", code, text)
	}
	writeReaders(t, access, ada+" role:writer", dan+" role:admin")

	// A writer may add a policy and edit one.
	r.declare(t, map[string]string{"none": "summary: destroy: 0", "added": "summary: add: >=0"}, "a")
	if text, code := runPravo(t, r.dir, "--identity", "ada", "apply", "--auto-approve"); code != 0 {
		t.Fatalf("a writer adds a policy: exit %d\n%s", code, text)
	}
	r.declare(t, map[string]string{"none": "summary: destroy: <=0", "added": "summary: add: >=0"}, "a")
	if text, code := runPravo(t, r.dir, "--identity", "ada", "apply", "--auto-approve"); code != 0 {
		t.Fatalf("a writer edits a policy: exit %d\n%s", code, text)
	}

	// Only an admin removes one.
	r.declare(t, map[string]string{"added": "summary: add: >=0"}, "a")
	text, code := runPravo(t, r.dir, "--identity", "ada", "apply", "--auto-approve")
	if code != 5 || !strings.Contains(text, "removing the policy none needs an admin, and your key is a writer") {
		t.Fatalf("a writer removes a policy: exit %d\n%s", code, text)
	}
	if text, code := runPravo(t, r.dir, "--identity", "dan", "apply", "--auto-approve"); code != 0 {
		t.Fatalf("an admin removes a policy: exit %d\n%s", code, text)
	}
	st, err := state.OpenWith(filepath.Join(r.dir, "pravo.state"), state.Options{Identity: r.ada})
	if err != nil {
		t.Fatal(err)
	}
	set, err := st.PolicySet(context.Background(), loadReadersFile(r.dir, "").Readers)
	if err != nil {
		t.Fatal(err)
	}
	if head, ok := set.Head("none"); !ok || !head.Tombstone() || head.RoleShort {
		t.Errorf("head of none = %+v", head)
	}
}

// bucketRecords is the verdict of the records of the bucket, judged by the readers file.
func (s *sealed) bucketRecords(t *testing.T) state.RecordVerdict {
	t.Helper()
	st, err := state.OpenBackendWith(context.Background(), s.backend(t), state.Options{Identity: s.ada})
	if err != nil {
		t.Fatal(err)
	}
	txns, err := st.Transactions()
	if err != nil {
		t.Fatal(err)
	}
	return st.VerifyRecords(txns, loadReadersFile(s.dir, "").Readers)
}

func TestARecordOfAReaderIsNotBoundToAWriter(t *testing.T) {
	s := sealedBucket(t)
	s.readers(t, s.adaLine+" role:writer", s.danLine+" role:reader")

	// ada, a writer, removes one record and writes nothing else; the other two came before roles.
	if text, code := runPravo(t, s.dir, "--identity", "ada", "state", "rm", "net.db"); code != 0 {
		t.Fatalf("exit %d\n%s", code, text)
	}
	if v := s.bucketRecords(t); len(v.Problems) != 1 || v.Problems[0].Addr != "net.vpc" {
		// net.vpc was written with no transaction in this fixture; it is the one record that
		// nothing accounts for.
		t.Fatalf("records before a reader writes: %+v", v)
	}

	// dan, a reader, holds the data key and seals a record of his own.
	st, err := state.OpenBackendWith(context.Background(), s.backend(t), state.Options{Identity: s.dan})
	if err != nil {
		t.Fatal(err)
	}
	s.record(t, st, "net.forged", "FORGED")
	v := s.bucketRecords(t)
	found := false
	for _, p := range v.Problems {
		found = found || p.Addr == "net.forged"
	}
	if !found {
		t.Errorf("a record that a reader sealed was not found: %+v", v)
	}
}

func TestTheRecordsOfAWriterAreBoundToItsTransactions(t *testing.T) {
	r := newPolicyRoot(t, nil, "a")
	if text, code := r.apply(t); code != 0 {
		t.Fatalf("first apply: exit %d\n%s", code, text)
	}
	danID, dan := person(t, r.ids, "dan")
	ada := r.ada.AsReader().String()
	access := filepath.Join(r.dir, ".pravo", "access")
	writeReaders(t, access, ada, dan)
	if text, code := runPravo(t, r.dir, "protect", "rewrap", "--yes"); code != 0 {
		t.Fatalf("rewrap: exit %d\n%s", code, text)
	}
	writeReaders(t, access, ada+" role:writer", dan+" role:reader")

	r.declare(t, nil, "a", "b")
	if text, code := runPravo(t, r.dir, "--identity", "ada", "apply", "--auto-approve"); code != 0 {
		t.Fatalf("a writer's apply: exit %d\n%s", code, text)
	}
	st := r.store(t)
	txns, err := st.Transactions()
	if err != nil {
		t.Fatal(err)
	}
	readers := loadReadersFile(r.dir, "").Readers
	v := st.VerifyRecords(txns, readers)
	if len(v.Problems) != 0 || v.Bound != 1 || v.Earlier != 1 {
		t.Fatalf("after a writer's apply: %+v", v)
	}

	text, code := runPravo(t, r.dir, "--identity", "dan", "protect", "status")
	if code != 0 || !strings.Contains(text, "Roles        on · 0 admin · 1 writer · 1 reader · you are a reader") ||
		!strings.Contains(text, "Records      1 bound to a transaction of a writer · 1 from before roles") {
		t.Fatalf("status: exit %d\n%s", code, text)
	}

	// dan is a reader. He holds the data key, and seals a record of his own.
	dst, err := state.OpenWith(filepath.Join(r.dir, "pravo.state"), state.Options{Identity: danID})
	if err != nil {
		t.Fatal(err)
	}
	if err := dst.Set(&state.Resource{Addr: "forged", Mode: "managed", Type: "local_file", Name: "forged", Attributes: []byte(`{"id":"x"}`)}, ""); err != nil {
		t.Fatal(err)
	}
	st = r.store(t)
	if v = st.VerifyRecords(txns, readers); len(v.Problems) != 1 || v.Problems[0].Addr != "forged" {
		t.Fatalf("after a reader's write: %+v", v)
	}
	// A record that names a transaction that is not there is no better.
	if err := dst.Set(&state.Resource{Addr: "forged2", Mode: "managed", Type: "local_file", Name: "forged2", Attributes: []byte(`{"id":"y"}`)}, "20260101T000000Z-bbbbbb"); err != nil {
		t.Fatal(err)
	}
	if v = r.store(t).VerifyRecords(txns, readers); len(v.Problems) != 2 {
		t.Fatalf("after a record that names a missing transaction: %+v", v)
	}
	if text, code := runPravo(t, r.dir, "--identity", "ada", "state", "rm", "forged2"); code != 0 {
		t.Fatalf("state rm forged2: exit %d\n%s", code, text)
	}
	text, code = runPravo(t, r.dir, "--identity", "ada", "protect", "status")
	if code != 7 || !strings.Contains(text, "Untrusted: forged: it names no transaction that wrote it") {
		t.Fatalf("status after a reader's write: exit %d\n%s", code, text)
	}

	if text, code := runPravo(t, r.dir, "--identity", "ada", "state", "rm", "forged"); code != 0 {
		t.Fatalf("state rm forged: exit %d\n%s", code, text)
	}
	// An apply does not build on a record that a reader sealed: dan replaces the record of a, and
	// ada's apply that destroys a is refused.
	cur := dst.Get("a")
	if cur == nil {
		t.Fatal("no record of a")
	}
	forged := *cur
	forged.Txn = ""
	if err := dst.Set(&forged, ""); err != nil {
		t.Fatal(err)
	}
	r.declare(t, nil, "b")
	text, code = runPravo(t, r.dir, "--identity", "ada", "apply", "--auto-approve")
	if code != 7 || !strings.Contains(text, "that no transaction of a writer accounts for") {
		t.Fatalf("apply over a forged record: exit %d\n%s", code, text)
	}
}

func TestAReaderCannotReserve(t *testing.T) {
	s := plannableBucket(t)
	s.readers(t, s.adaLine+" role:writer", s.danLine+" role:reader")
	text, code := runPravo(t, s.dir, "--identity", "dan", "plan", "--reserve", "5m")
	if code != 5 || !strings.Contains(text, "a writer is needed to change the state") {
		t.Fatalf("exit %d\n%s", code, text)
	}
}

func TestAReaderMayAskWhy(t *testing.T) {
	s := plannableBucket(t)
	s.readers(t, s.adaLine+" role:writer", s.danLine+" role:reader")
	before := s.objects()
	if text, code := runPravo(t, s.dir, "--identity", "dan", "why", "cfg"); code != 0 {
		t.Fatalf("a reader's why: exit %d\n%s", code, text)
	}
	for k, v := range s.objects() {
		if old, ok := before[k]; !ok || old != v {
			t.Errorf("why wrote %s", k)
		}
	}
}
