package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pravo-systems/pravo/internal/state"
)

// gcFormat2 is a bucket of format 2 with two resources, one of them written three
// times and one removed, so that it holds records that no pointer names.
func gcFormat2(t *testing.T) *s3Env {
	t.Helper()
	e := newS3Env(t, false)
	st, err := state.OpenBackendWith(context.Background(), e.backend(t), state.Options{NewFormat: state.FormatImmutable})
	if err != nil {
		t.Fatal(err)
	}
	e.record(t, st, "net.vpc", secretA)
	e.record(t, st, "net.db", secretB)
	e.record(t, st, "net.old", "destroyed")
	for i := 2; i <= 3; i++ {
		r := *st.Get("net.vpc")
		r.Attributes = []byte(fmt.Sprintf(`{"id":"net.vpc","rev":%d}`, i))
		if err := st.Set(&r, ""); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := st.Remove("net.old"); err != nil {
		t.Fatal(err)
	}
	return e
}

// recKeys lists the records of the bucket.
func (e *s3Env) recKeys() []string {
	var out []string
	for k := range e.f.Objects {
		if strings.HasPrefix(k, "prod/rec/") {
			out = append(out, k)
		}
	}
	return out
}

// hasMarks says whether a collection left its marks in the bucket.
func (e *s3Env) hasMarks() bool {
	_, ok := e.f.Objects["prod/gc/marks.json"]
	return ok
}

// plantOldRecord adds a copy of a record that no pointer names, written two days
// ago, and returns its key.
func (e *s3Env) plantOldRecord(t *testing.T) string {
	t.Helper()
	for k, data := range e.f.Objects {
		if !strings.HasPrefix(k, "prod/rec/") {
			continue
		}
		var rec map[string]any
		if err := json.Unmarshal(data, &rec); err != nil {
			t.Fatal(err)
		}
		rec["updated_at"], rec["revision"] = time.Now().UTC().Add(-48*time.Hour), 99
		planted, _ := json.MarshalIndent(rec, "", "  ")
		sum := sha256.Sum256(planted)
		key := "prod/rec/" + hex.EncodeToString(sum[:])
		e.f.Objects[key] = planted
		return key
	}
	t.Fatal("no record to copy")
	return ""
}

// ageRecords rewrites every record that no pointer names, as one that was written
// long ago: the same record with an updated_at two days back.
func (e *s3Env) ageRecords(t *testing.T) int {
	t.Helper()
	named := map[string]bool{}
	for k, data := range e.f.Objects {
		if strings.HasPrefix(k, "prod/resources/") {
			var p struct{ Record string }
			if json.Unmarshal(data, &p) == nil {
				named["prod/"+p.Record] = true
			}
		}
	}
	n := 0
	for _, k := range e.recKeys() {
		if named[k] {
			continue
		}
		var rec map[string]any
		if err := json.Unmarshal(e.f.Objects[k], &rec); err != nil {
			t.Fatal(err)
		}
		rec["updated_at"] = time.Now().UTC().Add(-48 * time.Hour)
		data, _ := json.MarshalIndent(rec, "", "  ")
		sum := sha256.Sum256(data)
		delete(e.f.Objects, k)
		e.f.Objects["prod/rec/"+hex.EncodeToString(sum[:])] = data
		n++
	}
	return n
}

func TestProtectGCOfAStateOfFormatTwo(t *testing.T) {
	requireSealing(t)
	e := gcFormat2(t)
	total := len(e.recKeys())
	if total != 5 {
		t.Fatalf("%d records, want 5", total)
	}

	// Everything is young.
	text, code := runPravo(t, e.dir, "protect", "gc")
	if code != 0 || !strings.Contains(text, "No record is older than 24h: 3 records that no pointer names are kept.") {
		t.Fatalf("exit %d\n%s", code, text)
	}
	if aged := e.ageRecords(t); aged != 3 {
		t.Fatalf("aged %d records", aged)
	}

	// A dry run says what it would mark, and writes nothing: no marks either.
	snapshot, puts := e.bucketText(), e.puts()
	text, code = runPravo(t, e.dir, "protect", "gc", "--dry-run")
	if code != 0 || !strings.Contains(text, "Would mark 3 records that no pointer names. A collection at least 15m later deletes the ones that are still unnamed.") || strings.Contains(text, "Would delete") || e.bucketText() != snapshot || e.puts() != puts {
		t.Fatalf("dry run: exit %d, changed %v\n%s", code, e.bucketText() != snapshot, text)
	}
	type gcDoc struct {
		Format  int  `json:"format"`
		DryRun  bool `json:"dry_run"`
		Older   int  `json:"older_than_seconds"`
		Settle  int  `json:"settle_seconds"`
		Records struct{ Listed, Named, Unnamed, Marked, Settling, Deleted int }
		Groups  any `json:"groups"`
	}
	text, code = runPravo(t, e.dir, "--json", "protect", "gc", "--dry-run")
	if code != 0 {
		t.Fatalf("exit %d\n%s", code, text)
	}
	var doc gcDoc
	if err := json.Unmarshal([]byte(text), &doc); err != nil || doc.Format != 2 || !doc.DryRun || doc.Older != 86400 || doc.Settle != 900 || doc.Records.Listed != 5 || doc.Records.Named != 2 || doc.Records.Unnamed != 3 || doc.Records.Marked != 3 || doc.Records.Deleted != 0 || doc.Groups != nil {
		t.Fatalf("%v: %+v\n%s", err, doc, text)
	}
	if e.bucketText() != snapshot || e.puts() != puts {
		t.Fatal("a dry run with --json changed the bucket")
	}

	// The first collection marks and deletes nothing, and so does one inside the
	// settle interval.
	text, code = runPravo(t, e.dir, "protect", "gc")
	if code != 0 || !strings.Contains(text, "Marked 3 records that no pointer names. A collection at least 15m later deletes the ones that are still unnamed.") || len(e.recKeys()) != 5 || !e.hasMarks() {
		t.Fatalf("the first run: exit %d, %d records\n%s", code, len(e.recKeys()), text)
	}
	text, code = runPravo(t, e.dir, "protect", "gc")
	if code != 0 || !strings.Contains(text, "Kept 3 records that no pointer names: marked less than 15m ago.") || len(e.recKeys()) != 5 {
		t.Fatalf("a run inside the settle interval: exit %d, %d records\n%s", code, len(e.recKeys()), text)
	}

	// After it, a dry run says what it would delete.
	settled := state.GCSettle
	state.GCSettle = 0
	t.Cleanup(func() { state.GCSettle = settled })
	snapshot, puts = e.bucketText(), e.puts()
	text, code = runPravo(t, e.dir, "protect", "gc", "--dry-run")
	if code != 0 || !strings.Contains(text, "Would delete 3 records older than 24h (") || e.bucketText() != snapshot || e.puts() != puts {
		t.Fatalf("dry run after the settle: exit %d, changed %v\n%s", code, e.bucketText() != snapshot, text)
	}
	text, code = runPravo(t, e.dir, "--json", "protect", "gc", "--dry-run")
	doc = gcDoc{}
	if err := json.Unmarshal([]byte(text), &doc); code != 0 || err != nil || doc.Records.Deleted != 3 || doc.Records.Marked != 0 || doc.Settle != 0 {
		t.Fatalf("exit %d, %v: %+v\n%s", code, err, doc, text)
	}

	text, code = runPravo(t, e.dir, "protect", "gc")
	if code != 0 || !strings.Contains(text, "Deleted 3 records older than 24h (") || len(e.recKeys()) != 2 {
		t.Fatalf("exit %d, %d records\n%s", code, len(e.recKeys()), text)
	}
	if strings.Contains(e.bucketText(), "destroyed") {
		t.Error("the record of the destroyed resource is still in the bucket")
	}
	text, code = runPravo(t, e.dir, "protect", "gc")
	if code != 0 || !strings.Contains(text, "No record is garbage: a pointer names every record.") {
		t.Fatalf("a second run: exit %d\n%s", code, text)
	}
	// The state is as it was.
	if text, code = runPravo(t, e.dir, "protect", "status"); code != 0 || !strings.Contains(text, "off · format 2 · immutable records") {
		t.Fatalf("status: exit %d\n%s", code, text)
	}

	// A grace below the least is raised, and says so.
	warned := withStderr(t, func() {
		if text, code = runPravo(t, e.dir, "protect", "gc", "--older-than", "1s", "--dry-run"); code != 0 {
			t.Errorf("exit %d\n%s", code, text)
		}
	})
	if !strings.Contains(warned, "Warning: --older-than 1s is below the least, 65s") {
		t.Errorf("standard error: %q", warned)
	}
	if text, code = runPravo(t, e.dir, "protect", "gc", "--older-than", "soon"); code != 1 || !strings.Contains(text, "--older-than") {
		t.Errorf("a bad span: exit %d\n%s", code, text)
	}
}

func TestProtectGCOfAStateWithNothingToCollect(t *testing.T) {
	e := newS3Env(t, false)
	text, code := runPravo(t, e.dir, "protect", "gc")
	if code != 0 || !strings.Contains(text, "The state is empty: there is nothing to collect.") || len(e.f.Objects) != 0 {
		t.Fatalf("an empty bucket: exit %d, %d objects\n%s", code, len(e.f.Objects), text)
	}
	st := e.store(t)
	e.record(t, st, "net.vpc", secretA)
	before := e.bucketText()
	text, code = runPravo(t, e.dir, "protect", "gc")
	if code != 0 || !strings.Contains(text, "The state is format 1: it keeps one object for each resource and has nothing to collect.") || e.bucketText() != before {
		t.Fatalf("format 1: exit %d\n%s", code, text)
	}
}

// removeDan takes dan out of the readers file and runs rewrap, with --all if asked.
func (s *sealed) removeDan(t *testing.T, all bool) {
	t.Helper()
	s.readers(t, s.adaLine)
	args := []string{"protect", "rewrap"}
	if all {
		args = append(args, "--all")
	}
	if text, code := runPravo(t, s.dir, args...); code != 0 {
		t.Fatalf("%v: exit %d\n%s", args, code, text)
	}
}

func (s *sealed) groups(t *testing.T) []string {
	t.Helper()
	var m struct {
		Sealed struct {
			Groups []struct{ ID string }
		}
	}
	if err := json.Unmarshal(s.f.Objects["prod/meta.json"], &m); err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, g := range m.Sealed.Groups {
		out = append(out, g.ID)
	}
	return out
}

// ageHistory makes every entry of the history of meta.json two hours old, as a
// rewrap that was made a while ago: gc keeps the groups of a state whose readers
// changed a moment ago.
func (e *s3Env) ageHistory(t *testing.T) {
	t.Helper()
	const key = "prod/meta.json"
	var m map[string]any
	if err := json.Unmarshal(e.f.Objects[key], &m); err != nil {
		t.Fatal(err)
	}
	hist, _ := m["history"].([]any)
	for _, h := range hist {
		h.(map[string]any)["at"] = time.Now().UTC().Add(-2 * time.Hour)
	}
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	e.f.Objects[key] = data
}

func TestProtectGCDropsTheGroupOfARemovedReader(t *testing.T) {
	s := sealedBucket(t)
	s.removeDan(t, false)
	s.ageHistory(t)

	// The records are still in g1: it stays, and the command says what to do.
	text, code := runPravo(t, s.dir, "protect", "gc")
	if code != 0 || !strings.Contains(text, "Kept group g1: 2 records are sealed in it.") || !strings.Contains(text, "pravo protect rewrap --all") || fmt.Sprint(s.groups(t)) != "[g1 g2]" {
		t.Fatalf("exit %d, groups %v\n%s", code, s.groups(t), text)
	}
	if text, code = runPravo(t, s.dir, "--json", "protect", "gc"); code != 0 || !strings.Contains(text, `"examined": true`) || !strings.Contains(text, `"records": 2`) {
		t.Fatalf("exit %d\n%s", code, text)
	}

	s.removeDan(t, true)
	// A moment after the rewrap, a command that opened the state before it may still
	// seal in g1, so g1 stays.
	if text, code := runPravo(t, s.dir, "protect", "gc"); code != 0 || !strings.Contains(text, "Groups were kept: the readers or the records were changed") || fmt.Sprint(s.groups(t)) != "[g1 g2]" {
		t.Fatalf("gc right after the rewrap: exit %d, groups %v\n%s", code, s.groups(t), text)
	}
	s.ageHistory(t)
	snapshot, puts := s.bucketText(), s.puts()
	text, code = runPravo(t, s.dir, "protect", "gc", "--dry-run")
	if code != 0 || !strings.Contains(text, "Would drop group g1: nothing refers to it.") || s.bucketText() != snapshot || s.puts() != puts {
		t.Fatalf("dry run: exit %d\n%s", code, text)
	}

	// Dan was a reader of g1 only. A reader who is not one now collects records and
	// leaves the groups.
	d := t.TempDir()
	putIdentity(t, d, "dan", s.identityFile(t, "dan"))
	t.Setenv("PRAVO_IDENTITY_DIR", d)
	resetIdentityState(t)
	text, code = runPravo(t, s.dir, "protect", "gc")
	if code != 0 || !strings.Contains(text, "Groups were not looked at: identity dan is not a reader.") || fmt.Sprint(s.groups(t)) != "[g1 g2]" {
		t.Fatalf("a removed reader: exit %d\n%s", code, text)
	}
	s.asNobody(t)
	if text, code = runPravo(t, s.dir, "protect", "gc"); code != 0 || !strings.Contains(text, "Groups were not looked at: no identity found.") {
		t.Fatalf("no identity: exit %d\n%s", code, text)
	}
	t.Setenv("PRAVO_IDENTITY_DIR", s.ids)
	resetIdentityState(t)

	// The readers file and the state must agree, as for any writer.
	s.readers(t, s.adaLine, s.danLine)
	if text, code = runPravo(t, s.dir, "protect", "gc"); code != 5 || !strings.Contains(text, "differ") || fmt.Sprint(s.groups(t)) != "[g1 g2]" {
		t.Fatalf("a readers file that differs: exit %d\n%s", code, text)
	}
	s.readers(t, s.adaLine)

	text, code = runPravo(t, s.dir, "protect", "gc")
	if code != 0 || !strings.Contains(text, "Dropped group g1: nothing refers to it.") || fmt.Sprint(s.groups(t)) != "[g2]" {
		t.Fatalf("exit %d, groups %v\n%s", code, s.groups(t), text)
	}
	if text, code = runPravo(t, s.dir, "protect", "gc"); code != 0 || !strings.Contains(text, "No group needs to be dropped") {
		t.Fatalf("a second run: exit %d\n%s", code, text)
	}
	// Ada reads everything; dan opens nothing, with the key he had.
	if text, code = runPravo(t, s.dir, "state", "show", "net.vpc"); code != 0 || !strings.Contains(text, secretA) {
		t.Fatalf("ada: exit %d\n%s", code, text)
	}
	for _, secret := range []string{secretA, secretB} {
		if strings.Contains(s.bucketText(), secret) {
			t.Errorf("%q is in the bucket", secret)
		}
	}
	t.Setenv("PRAVO_IDENTITY_DIR", d)
	resetIdentityState(t)
	if text, code = runPravo(t, s.dir, "state", "show", "net.vpc"); code != 5 {
		t.Errorf("dan: exit %d\n%s", code, text)
	}
}

// The groups are looked at while nothing runs; the records are collected anyway.
func TestProtectGCOfGroupsNeedsTheStateAtRest(t *testing.T) {
	s := sealedBucket(t)
	s.removeDan(t, true)
	s.ageHistory(t)
	st, err := state.OpenBackendWith(context.Background(), s.backend(t), state.Options{Identity: s.ada})
	if err != nil {
		t.Fatal(err)
	}
	txn := st.Begin("apply", "pravo apply")
	if err := txn.Start(); err != nil {
		t.Fatal(err)
	}
	if err := st.Lock("net.vpc", "pravo apply", txn.ID()); err != nil {
		t.Fatal(err)
	}
	snapshot := s.bucketText()
	text, code := runPravo(t, s.dir, "protect", "gc")
	if code != 4 || !strings.Contains(text, "the groups were not looked at: not ready: 1 transaction is running and 1 lock is held") || s.bucketText() != snapshot {
		t.Fatalf("exit %d\n%s", code, text)
	}
	text, code = runPravo(t, s.dir, "--json", "protect", "gc")
	if code != 4 || !strings.Contains(text, `"skipped": "not ready`) {
		t.Fatalf("as JSON: exit %d\n%s", code, text)
	}
	if err := st.Unlock("net.vpc"); err != nil {
		t.Fatal(err)
	}
	if err := txn.Finish("applied", "", false); err != nil {
		t.Fatal(err)
	}
	if text, code = runPravo(t, s.dir, "protect", "gc"); code != 0 || !strings.Contains(text, "Dropped group g1") {
		t.Fatalf("at rest: exit %d\n%s", code, text)
	}
}

// The readers file is the one that PRAVO_ACCESS_DIR names, and a file that differs
// from the state stops the part that changes meta.json.
func TestProtectGCHonoursTheAccessDirectory(t *testing.T) {
	s := sealedBucket(t)
	s.removeDan(t, true)
	s.ageHistory(t)
	other := filepath.Join(t.TempDir(), "access")
	writeReaders(t, other, s.adaLine, s.danLine)
	t.Setenv("PRAVO_ACCESS_DIR", other)
	planted := s.plantOldRecord(t)
	text, code := runPravo(t, s.dir, "protect", "gc")
	if code != 5 || !strings.Contains(text, "differ") || fmt.Sprint(s.groups(t)) != "[g1 g2]" {
		t.Fatalf("a file that differs: exit %d, groups %v\n%s", code, s.groups(t), text)
	}
	// The records do not wait for the file: the first run marks the old one.
	if _, there := s.f.Objects[planted]; !there || !strings.Contains(text, "Marked 1 record that no pointer names.") {
		t.Errorf("the old record is gone, or not reported as marked:\n%s", text)
	}
	settled := state.GCSettle
	state.GCSettle = 0
	t.Cleanup(func() { state.GCSettle = settled })
	text, code = runPravo(t, s.dir, "protect", "gc")
	if code != 5 || fmt.Sprint(s.groups(t)) != "[g1 g2]" {
		t.Fatalf("a file that differs, again: exit %d, groups %v\n%s", code, s.groups(t), text)
	}
	if _, there := s.f.Objects[planted]; there || !strings.Contains(text, "Deleted 1 record older than 24h (") {
		t.Errorf("the old record is still there, or not reported:\n%s", text)
	}
	writeReaders(t, other, s.adaLine)
	text, code = runPravo(t, s.dir, "--json", "protect", "gc")
	if code != 0 || !strings.Contains(text, `"dropped": [`) || fmt.Sprint(s.groups(t)) != "[g2]" {
		t.Fatalf("a file that matches: exit %d, groups %v\n%s", code, s.groups(t), text)
	}
	// A readers file that is missing stops the command, as it stops any command that
	// writes a sealed state.
	t.Setenv("PRAVO_ACCESS_DIR", filepath.Join(t.TempDir(), "none"))
	if text, code = runPravo(t, s.dir, "protect", "gc"); code != 5 || !strings.Contains(text, "readers file was not found") {
		t.Fatalf("no readers file: exit %d\n%s", code, text)
	}
}
