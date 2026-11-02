package state

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/pravo-systems/pravo/internal/protect"
)

// The record of the environment is a chain: the first version by a writer, a version that
// changes it by an admin, and one that only fills in a lineage by anyone who may write.
func TestEnvironmentRecordChain(t *testing.T) {
	skipWhenSealed(t)
	ctx := context.Background()
	b := testBackends()[0].make(t)
	ada, bob := testIdentity(t, "ada"), testIdentity(t, "bob")
	s := sealedStore(t, b, ada, bob)
	readers := readersOf(ada, bob)
	readers[0].Role, readers[1].Role = protect.RoleAdmin, protect.RoleWriter

	if rec, err := s.EnvironmentRecord(ctx, readers); rec != nil || err != nil {
		t.Fatalf("no record yet: %v %v", rec, err)
	}
	asBob := openIdentity(t, b, bob)
	if err := asBob.PutEnvironmentRecord(ctx, 1, EnvironmentRecord{Name: "prod", After: []Upstream{{Name: "staging"}}}); err != nil {
		t.Fatal(err)
	}
	if asBob.Format() != FormatEnvironment {
		t.Fatalf("format %d", asBob.Format())
	}
	// Bob fills in the lineage of staging: a writer may.
	if err := asBob.PutEnvironmentRecord(ctx, 2, EnvironmentRecord{Name: "prod", After: []Upstream{{Name: "staging", Lineage: "abc"}}}); err != nil {
		t.Fatal(err)
	}
	rec, err := s.EnvironmentRecord(ctx, readers)
	if err != nil || rec.Seq != 2 || rec.Lineage("staging") != "abc" || rec.Signer != "bob" {
		t.Fatalf("filled: %+v %v", rec, err)
	}
	// Bob changes what prod follows: the record is not trusted.
	if err := asBob.PutEnvironmentRecord(ctx, 3, EnvironmentRecord{Name: "prod"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.EnvironmentRecord(ctx, readers); !errors.Is(err, ErrUntrusted) || !strings.Contains(err.Error(), "is not an admin") {
		t.Fatalf("a writer's change: %v", err)
	}
	// Taken numbers are refused.
	if err := s.PutEnvironmentRecord(ctx, 2, EnvironmentRecord{Name: "staging"}); !errors.Is(err, ErrEnvironmentChanged) {
		t.Fatalf("taken: %v", err)
	}
}

// What a state runs is its newest note; a note dated far ahead is passed over and named.
func TestLatestRun(t *testing.T) {
	skipWhenSealed(t)
	ctx := context.Background()
	b := testBackends()[0].make(t)
	ada := testIdentity(t, "ada")
	s := sealedStore(t, b, ada)
	readers := readersOf(ada)
	if n, _, err := s.LatestRun(ctx, time.Now()); n != nil || err != nil {
		t.Fatalf("no note: %v %v", n, err)
	}
	tags := SpecTagDigests([]string{"image=1.4"})
	if err := s.PutRunNote(ctx, &RunNote{Environment: "staging", Fingerprint: strings.Repeat("a", 32), Tags: tags, Txn: "t1"}); err != nil {
		t.Fatal(err)
	}
	n, ignored, err := s.LatestRun(ctx, time.Now())
	if err != nil || n == nil || len(ignored) != 0 || n.Txn != "t1" {
		t.Fatalf("latest: %+v %v %v", n, ignored, err)
	}
	if st, who := n.Verify(s.Lineage(), readers); st != protect.SigVerified || who != "ada" {
		t.Errorf("verify: %v %s", st, who)
	}
	if st, _ := n.Verify("another-lineage", readers); st != protect.SigInvalid {
		t.Errorf("a note verifies only in its state: %v", st)
	}
	// A note a year ahead.
	future := *n
	future.ID = time.Now().Add(365*24*time.Hour).UTC().Format("20060102T150405Z") + "-abcdef"
	data, _ := indentJSON(future)
	if err := b.Put(ctx, RunDir+"/"+future.ID+".json", data); err != nil {
		t.Fatal(err)
	}
	n2, ignored, err := s.LatestRun(ctx, time.Now())
	if err != nil || n2.ID != n.ID || len(ignored) != 1 {
		t.Fatalf("future: %+v %v %v", n2, ignored, err)
	}
}

// A promotion is kept for each release, verifies only in its state, and a waiver is never
// replaced.
func TestPromotions(t *testing.T) {
	skipWhenSealed(t)
	ctx := context.Background()
	b := testBackends()[0].make(t)
	ada := testIdentity(t, "ada")
	s := sealedStore(t, b, ada)
	readers := readersOf(ada)
	fp := strings.Repeat("b", 32)
	for _, image := range []string{"1.4", "1.5"} {
		p := &Promotion{Kind: PromotionPromoted, Environment: "prod", Fingerprint: fp, Tags: SpecTagDigests([]string{"image=" + image}),
			From: []PromotionSource{{Environment: "staging", Lineage: "l", Run: &RunNote{ID: "x"}}}}
		if err := s.PutPromotion(ctx, p); err != nil {
			t.Fatal(err)
		}
	}
	w := &Promotion{Kind: PromotionWaived, Environment: "prod", Fingerprint: fp, Tags: SpecTagDigests([]string{"image=hotfix"}), Note: "INC-1"}
	if err := s.PutPromotion(ctx, w); err != nil {
		t.Fatal(err)
	}
	w2 := &Promotion{Kind: PromotionWaived, Environment: "prod", Fingerprint: fp, Tags: SpecTagDigests([]string{"image=hotfix"}), Note: "other"}
	if err := s.PutPromotion(ctx, w2); err != nil {
		t.Fatal(err)
	}
	list, err := s.Promotions(ctx, fp)
	if err != nil || len(list) != 3 {
		t.Fatalf("promotions: %d %v", len(list), err)
	}
	for _, o := range list {
		if o.Err != nil {
			t.Fatalf("%s: %v", o.Key, o.Err)
		}
		if st, _ := o.Verify(s.Lineage(), readers); st != protect.SigVerified {
			t.Errorf("%s: %v", o.Key, st)
		}
		if o.Kind == PromotionWaived && o.Note != "INC-1" {
			t.Errorf("the waiver was replaced: %q", o.Note)
		}
	}
	if other, _ := s.Promotions(ctx, strings.Repeat("c", 32)); len(other) != 0 {
		t.Errorf("another release: %v", other)
	}
}

// Without a seal the objects are written, unsigned, and the format stays.
func TestEnvironmentObjectsUnsealed(t *testing.T) {
	skipWhenSealed(t)
	ctx := context.Background()
	s, err := OpenBackendWith(ctx, testBackends()[0].make(t), Options{})
	if err != nil {
		t.Fatal(err)
	}
	before := s.Format()
	if err := s.PutEnvironmentRecord(ctx, 1, EnvironmentRecord{Name: "dev"}); err != nil {
		t.Fatal(err)
	}
	if err := s.PutRunNote(ctx, &RunNote{Environment: "dev", Fingerprint: strings.Repeat("a", 32)}); err != nil {
		t.Fatal(err)
	}
	if s.Format() != before {
		t.Errorf("format %d -> %d", before, s.Format())
	}
	rec, err := s.EnvironmentRecord(ctx, nil)
	if err != nil || rec.Name != "dev" || rec.Signature != nil {
		t.Fatalf("%+v %v", rec, err)
	}
	if err := s.PutPromotion(ctx, &Promotion{Kind: PromotionPromoted, Fingerprint: strings.Repeat("a", 32)}); !errors.Is(err, ErrNotReader) {
		t.Errorf("a promotion needs a seal: %v", err)
	}
}
