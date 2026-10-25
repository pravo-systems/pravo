package state

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/pravo-systems/pravo/internal/protect"
)

func TestPolicyVersionsFormAChain(t *testing.T) {
	skipWhenSealed(t)
	ctx := context.Background()
	b := testBackends()[0].make(t)
	ada, dan := testIdentity(t, "ada"), testIdentity(t, "dan")
	s := sealedStore(t, b, ada, dan)
	readers := readersOf(ada, dan)
	at := time.Date(2026, 10, 25, 12, 0, 0, 123, time.UTC)

	set, err := s.PolicySet(ctx, readers)
	if err != nil || len(set.InForce()) != 0 || s.Format() != FormatSealed {
		t.Fatalf("a state without policies: %v %v format %d", set, err, s.Format())
	}
	if err := s.PutPolicyVersion(ctx, "no_destroy", 1, "summary: destroy: 0", at); err != nil {
		t.Fatal(err)
	}
	if s.Format() != FormatPolicy {
		t.Fatalf("the first version must raise the format, got %d", s.Format())
	}
	other := openIdentity(t, b, dan)
	if err := other.PutPolicyVersion(ctx, "no_destroy", 2, "summary: destroy: <=1", at.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := s.PutPolicyVersion(ctx, "encrypted", 1, "changes: [...]", at); err != nil {
		t.Fatal(err)
	}
	set, err = s.PolicySet(ctx, readers)
	if err != nil {
		t.Fatal(err)
	}
	force := set.InForce()
	if len(force) != 2 || force[0].Name != "encrypted" || force[1].Ref() != "no_destroy@2:"+TextDigest("summary: destroy: <=1") {
		t.Fatalf("in force: %+v", force)
	}
	if force[1].Signer != "dan" || force[1].Status != protect.SigVerified {
		t.Errorf("the head was signed by dan: %+v", force[1])
	}

	// A removal is a tombstone: the name has a head and is not in force.
	if err := s.PutPolicyVersion(ctx, "no_destroy", 3, "", at.Add(2*time.Second)); err != nil {
		t.Fatal(err)
	}
	set, _ = s.PolicySet(ctx, readers)
	if head, ok := set.Head("no_destroy"); !ok || !head.Tombstone() || len(set.InForce()) != 1 {
		t.Errorf("tombstone: %+v in force %d", head, len(set.InForce()))
	}

	// A version is never replaced: another text at a taken number is refused, the
	// same text is a resend.
	if err := s.PutPolicyVersion(ctx, "encrypted", 1, "changes: []", at); !errors.Is(err, ErrPolicyChanged) {
		t.Errorf("another text at the same number: %v", err)
	}
	if err := s.PutPolicyVersion(ctx, "encrypted", 1, "changes: [...]", at.Add(time.Hour)); err != nil {
		t.Errorf("a resend: %v", err)
	}
}

func TestPolicySignatureStatuses(t *testing.T) {
	skipWhenSealed(t)
	ctx := context.Background()
	b := testBackends()[0].make(t)
	ada, dan := testIdentity(t, "ada"), testIdentity(t, "dan")
	s := sealedStore(t, b, ada, dan)
	at := time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)
	if err := s.PutPolicyVersion(ctx, "p", 1, "a: 1", at); err != nil {
		t.Fatal(err)
	}
	// A reader who left: accepted, and shown.
	set, err := s.PolicySet(ctx, readersOf(dan))
	if err != nil {
		t.Fatalf("a key that is not in the file: %v", err)
	}
	if v, _ := set.Head("p"); v.Status != protect.SigUnknownKey {
		t.Errorf("status %v", v.Status)
	}
	// Edited text under a listed key does not verify.
	data, _ := b.Get(ctx, policyKey("p", 1))
	var obj map[string]any
	_ = json.Unmarshal(data, &obj)
	obj["text"] = "a: 2"
	forged, _ := json.Marshal(obj)
	if err := b.Put(ctx, policyKey("p", 1), forged); err != nil {
		t.Fatal(err)
	}
	if _, err := s.PolicySet(ctx, readersOf(ada, dan)); !errors.Is(err, ErrUntrusted) {
		t.Errorf("a listed key that does not verify: %v", err)
	}
	// No signature.
	delete(obj, "signature")
	bare, _ := json.Marshal(obj)
	_ = b.Put(ctx, policyKey("p", 1), bare)
	if _, err := s.PolicySet(ctx, readersOf(ada, dan)); !errors.Is(err, ErrUntrusted) {
		t.Errorf("no signature: %v", err)
	}
	// A transplant from another state does not verify: the lineage is signed.
	_ = b.Put(ctx, policyKey("p", 1), data)
	if _, err := s.PolicySet(ctx, readersOf(ada, dan)); err != nil {
		t.Fatalf("restored: %v", err)
	}
	// A gap in the numbers, and an object that is no version.
	_ = b.Put(ctx, policyKey("p", 3), data)
	if _, err := s.PolicySet(ctx, readersOf(ada, dan)); !errors.Is(err, ErrUntrusted) {
		t.Errorf("a gap: %v", err)
	}
	_ = b.Delete(ctx, policyKey("p", 3))
	_ = b.Put(ctx, "policy/junk.json", []byte("{}"))
	if _, err := s.PolicySet(ctx, readersOf(ada, dan)); !errors.Is(err, ErrUntrusted) {
		t.Errorf("junk: %v", err)
	}
}

func TestPoliciesNeedASealedState(t *testing.T) {
	skipWhenSealed(t)
	ctx := context.Background()
	s, err := OpenBackend(ctx, testBackends()[0].make(t))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.PutPolicyVersion(ctx, "p", 1, "a: 1", time.Now()); !errors.Is(err, ErrNotReader) {
		t.Errorf("an unsealed state signs nothing: %v", err)
	}
	if s.Format() >= FormatPolicy {
		t.Errorf("a refused version must not raise the format")
	}
}

func TestCopyKeepsThePolicyChain(t *testing.T) {
	skipWhenSealed(t)
	ctx := context.Background()
	ada := testIdentity(t, "ada")
	src := testBackends()[0].make(t)
	s := sealedStore(t, src, ada)
	at := time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)
	for seq, text := range []string{"a: 1", "a: 2"} {
		if err := s.PutPolicyVersion(ctx, "p", seq+1, text, at); err != nil {
			t.Fatal(err)
		}
	}
	dst := testBackends()[0].make(t)
	if _, err := Copy(ctx, src, dst); err != nil {
		t.Fatal(err)
	}
	if keys, _ := dst.List(ctx, policyDir); len(keys) != 2 {
		t.Fatalf("copied %v", keys)
	}
	if _, err := Copy(ctx, src, dst); err != nil {
		t.Errorf("a second copy of the same versions: %v", err)
	}
	// A destination that holds other bytes under a name, or a version the source lacks,
	// is not overwritten.
	_ = dst.Delete(ctx, policyKey("p", 2))
	if _, err := dst.PutIfAbsent(ctx, policyKey("p", 2), []byte(`{"text":"other"}`)); err != nil {
		t.Fatal(err)
	}
	if _, err := Copy(ctx, src, dst); !errors.Is(err, ErrCopyRefused) {
		t.Errorf("another version at the same name: %v", err)
	}
	_, _ = dst.PutIfAbsent(ctx, policyKey("q", 1), []byte(`{}`))
	if _, err := Copy(ctx, src, dst); !errors.Is(err, ErrCopyRefused) {
		t.Errorf("a version the source lacks: %v", err)
	}
	if empty, _ := holdsNothing(ctx, testBackends()[0].make(t)); !empty {
		t.Error("a new backend holds nothing")
	}
	if empty, _ := holdsNothing(ctx, dst); empty {
		t.Error("a backend with policies holds something")
	}
}

func TestTheStartPartOfPoliciesIsSignedOnlyWhenThereAreAny(t *testing.T) {
	plain := Transaction{ID: "t", Kind: "apply"}
	withNone := plain
	withNone.Policies = []string{}
	if len(txnStartParts(&plain)) != len(txnStartParts(&withNone)) || len(txnStartParts(&plain)) != 13 {
		t.Fatalf("a transaction without policies must keep its 13 parts, got %d", len(txnStartParts(&plain)))
	}
	with := plain
	with.Policies = []string{"a@1:00", "b@2:11"}
	parts := txnStartParts(&with)
	if len(parts) != 14 || string(parts[13]) != "a@1:00\nb@2:11" {
		t.Errorf("parts: %q", parts)
	}
}

func TestATransactionSignsThePoliciesItJudgedBy(t *testing.T) {
	skipWhenSealed(t)
	ada := testIdentity(t, "ada")
	s := sealedStore(t, testBackends()[0].make(t), ada)
	tx := s.Begin("apply", "pravo apply")
	tx.SetPlan("", "d1g3st")
	tx.SetPolicies([]string{"b@2:22", "a@1:11"})
	if err := tx.Start(); err != nil {
		t.Fatal(err)
	}
	tx.Record("net.vpc", "update", 1, 2)
	if err := tx.FinishError("applied", "", "", true); err != nil {
		t.Fatal(err)
	}
	got := txnOf(t, s, tx.ID())
	if len(got.Policies) != 2 || got.Policies[0] != "a@1:11" {
		t.Fatalf("policies %v", got.Policies)
	}
	readers := readersOf(ada)
	if v := VerifyTransaction(s.Lineage(), &got, readers); v.Overall() != protect.SigVerified {
		t.Fatalf("verdict %+v", v)
	}
	got.Policies = got.Policies[:1] // a list that omits a policy no longer verifies
	if v := VerifyTransaction(s.Lineage(), &got, readers); v.Overall() != protect.SigInvalid {
		t.Errorf("a shortened list: %+v", v)
	}
	// Old transactions, written without the field, verify as before.
	old := txnOf(t, s, run(t, s, "applied", ""))
	if len(old.Policies) != 0 || VerifyTransaction(s.Lineage(), &old, readers).Overall() != protect.SigVerified {
		t.Errorf("a transaction without policies must still verify")
	}
}

func TestPolicyHistoryListsEveryVersionOldestFirst(t *testing.T) {
	skipWhenSealed(t)
	ctx := context.Background()
	b := testBackends()[0].make(t)
	ada, dan := testIdentity(t, "ada"), testIdentity(t, "dan")
	s := sealedStore(t, b, ada, dan)
	at := time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)
	if h, err := s.PolicyHistory(ctx, readersOf(ada)); err != nil || len(h) != 0 {
		t.Fatalf("a state without policies: %v %v", h, err)
	}
	for i, text := range []string{"a: 1", "a: 2", ""} {
		if err := s.PutPolicyVersion(ctx, "p", i+1, text, at.Add(time.Duration(i)*time.Minute)); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.PutPolicyVersion(ctx, "q", 1, "b: 1", at); err != nil {
		t.Fatal(err)
	}
	h, err := s.PolicyHistory(ctx, readersOf(ada, dan))
	if err != nil {
		t.Fatal(err)
	}
	if len(h) != 2 || len(h["p"]) != 3 || len(h["q"]) != 1 {
		t.Fatalf("history: %+v", h)
	}
	for i, v := range h["p"] {
		if v.Seq != i+1 || v.Status != protect.SigVerified {
			t.Errorf("version %d: %+v", i+1, v)
		}
	}
	if !h["p"][2].Tombstone() || h["p"][1].Text != "a: 2" {
		t.Errorf("texts: %+v", h["p"])
	}
	// A version that does not verify refuses the whole history.
	data, _ := b.Get(ctx, policyKey("q", 1))
	var obj map[string]any
	_ = json.Unmarshal(data, &obj)
	obj["text"] = "b: 2"
	forged, _ := json.Marshal(obj)
	_ = b.Put(ctx, policyKey("q", 1), forged)
	if _, err := s.PolicyHistory(ctx, readersOf(ada, dan)); !errors.Is(err, ErrUntrusted) {
		t.Errorf("a forged version: %v", err)
	}
}
