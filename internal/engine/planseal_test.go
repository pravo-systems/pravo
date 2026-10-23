package engine

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/pravo-systems/pravo/internal/protect"
	"github.com/pravo-systems/pravo/internal/state"
)

const testLineage = "lineage-of-a-sealed-test-state"

// testSealer is a state.Sealer over the real cryptography of the protect package.
// A sealer without keys is the sealer of a locked store.
type testSealer struct {
	keys *protect.Keys
}

func (s *testSealer) ctx(kind, name string) protect.Ctx {
	return protect.Ctx{Lineage: testLineage, Kind: kind, Name: name}
}

func (s *testSealer) Seal(kind, name string, plain []byte) (protect.Sealed, error) {
	if s.keys == nil {
		return protect.Sealed{}, state.ErrNotReader
	}
	return s.keys.Seal(s.ctx(kind, name), plain), nil
}

func (s *testSealer) Open(kind, name string, sealed protect.Sealed) ([]byte, error) {
	if s.keys == nil {
		return nil, state.ErrNotReader
	}
	return s.keys.Open(s.ctx(kind, name), sealed)
}

func (s *testSealer) Tag(value string) string {
	if s.keys == nil {
		return ""
	}
	return s.keys.Tag(value)
}

// testReaders makes n readers with keys.
func testReaders(t *testing.T, n int) ([]protect.Reader, []*protect.ReaderKey) {
	t.Helper()
	var rs []protect.Reader
	var ks []*protect.ReaderKey
	for _, name := range []string{"ada", "dan", "eve", "fay"}[:n] {
		k, err := protect.GenerateReaderKey()
		if err != nil {
			t.Fatal(err)
		}
		pub, _, _ := ed25519.GenerateKey(rand.Reader)
		rs = append(rs, protect.Reader{Name: name, Sign: pub, Key: k.Public()})
		ks = append(ks, k)
	}
	return rs, ks
}

// sealedStore is a plan store over a backend, sealed to ada. meta and keys are
// those of the state, for tests that change the readers.
type sealedStore struct {
	*PlanStore
	meta    *protect.Meta
	keys    *protect.Keys
	readers []protect.Reader
	rkeys   []*protect.ReaderKey
}

func newSealedStore(t *testing.T, b state.Backend, clock *time.Time, readers int) *sealedStore {
	t.Helper()
	rs, ks := testReaders(t, readers)
	meta, keys, err := protect.NewMeta(testLineage, rs)
	if err != nil {
		t.Fatal(err)
	}
	ps := NewPlanStore(b)
	ps.Seal = &testSealer{keys}
	ps.Now = func() time.Time { return *clock }
	return &sealedStore{PlanStore: ps, meta: meta, keys: keys, readers: rs, rkeys: ks}
}

// as returns a plan store over the same backend that reads with the keys of the
// reader at index i, or with none when i is negative.
func (s *sealedStore) as(t *testing.T, i int, clock *time.Time) *PlanStore {
	t.Helper()
	ps := NewPlanStore(s.B)
	ps.Now = func() time.Time { return *clock }
	if i < 0 {
		ps.Seal = &testSealer{}
		return ps
	}
	keys, err := s.meta.Unlock(testLineage, s.rkeys[i])
	if err != nil {
		t.Fatal(err)
	}
	ps.Seal = &testSealer{keys}
	return ps
}

// eachSealed runs a test on a directory and on a bucket, sealed to one reader.
func eachSealed(t *testing.T, run func(t *testing.T, s *sealedStore, clock *time.Time)) {
	t.Helper()
	for name, mk := range planBackends(t) {
		t.Run(name, func(t *testing.T) {
			clock := t0
			run(t, newSealedStore(t, mk(), &clock, 1), &clock)
		})
	}
}

const (
	plantedTag     = "hunter2-planted-in-a-tag"
	plantedReason  = "planted-in-the-reasons-of-a-change"
	plantedMessage = "planted-in-a-warning"
)

// secretPlan is a plan file with values that must not be visible in a sealed state.
func secretPlan(created time.Time, changes ...savedChange) *PlanFile {
	for i := range changes {
		changes[i].Reasons = []string{plantedReason}
		changes[i].Warnings = []savedDiag{{Severity: "warning", Summary: plantedMessage}}
		changes[i].Planned = []byte(plantedMessage + "-bytes")
	}
	return &PlanFile{
		Format: PlanFileFormat, Created: created, Command: "pravo plan --store -t pw=" + plantedTag + " -t note=two words",
		Lineage: "lineage-1", ConfigFingerprint: "fp-1", Providers: map[string]string{"test": "1.0.0"},
		Changes: changes, Tags: []string{"pw=" + plantedTag, "note=two words"},
	}
}

// putSecret stores a plan with planted values through the store, with the redacted
// command line that the CLI would pass, and returns the summary as it was given.
func putSecret(t *testing.T, ps *PlanStore, created time.Time, changes ...savedChange) *PlanMeta {
	t.Helper()
	pf := secretPlan(created, changes...)
	data, err := indentJSON(pf)
	if err != nil {
		t.Fatal(err)
	}
	meta := newPlanMeta(pf, "ada", 0, len(data))
	if err := ps.Put(context.Background(), data, meta); err != nil {
		t.Fatal(err)
	}
	return meta
}

// everything returns the bytes of every object of the plans of the backend.
func everything(t *testing.T, b state.Backend) string {
	t.Helper()
	var all strings.Builder
	for _, k := range listKeys(t, b, PlanDir) {
		data, err := b.Get(context.Background(), k)
		if err != nil {
			t.Fatal(k, err)
		}
		all.WriteString(k + "\n")
		all.Write(data)
		all.WriteString("\n")
	}
	return all.String()
}

// seen lists the planted values (and their base64 forms, in which the fields of
// bytes are stored) that appear in text.
func seen(text string) []string {
	var found []string
	for _, v := range []string{plantedTag, plantedReason, plantedMessage, "two words", plantedMessage + "-bytes"} {
		if strings.Contains(text, v) || strings.Contains(text, base64.StdEncoding.EncodeToString([]byte(v))) {
			found = append(found, v)
		}
	}
	return found
}

func TestWithoutASealerPlansAreStoredAsTheyAlwaysWere(t *testing.T) {
	eachBackend(t, func(t *testing.T, ps *PlanStore, _ *time.Time) {
		m := putSecret(t, ps, t0, change("a", ActionCreate, 0))
		got := everything(t, ps.B)
		if len(seen(got)) != 5 {
			t.Fatalf("the unsealed store should show the planted values, it shows %v", seen(got))
		}
		data, _ := ps.B.Get(context.Background(), planKey(m.ID))
		if _, sealed := asSealedPlan(data); sealed || digestID(data, planIDHex) != m.ID {
			t.Errorf("an unsealed store holds a plan file whose digest is its id")
		}
		stored, err := ps.Meta(context.Background(), m.ID)
		if err != nil || stored.Sealed != nil || stored.Unsealed || stored.Redacted || stored.Command != m.Command {
			t.Errorf("summary %+v, %v", stored, err)
		}
		if bytes.Contains(data, []byte(`"sealed"`)) {
			t.Error("an unsealed plan has no sealed member")
		}
	})
}

func TestASealedPlanIsAnEnvelopeAndItsIDIsTheDigestOfThePlaintext(t *testing.T) {
	eachSealed(t, func(t *testing.T, s *sealedStore, _ *time.Time) {
		ctx := context.Background()
		m := putSecret(t, s.PlanStore, t0, change("a", ActionCreate, 0), change("b", ActionUpdate, 2))
		data, err := s.B.Get(ctx, planKey(m.ID))
		if err != nil {
			t.Fatal(err)
		}
		env, ok := asSealedPlan(data)
		if !ok || env.ID != m.ID || env.V != 1 || env.Sealed.Group != "g1" {
			t.Fatalf("envelope %+v, %v", env, ok)
		}
		var shape map[string]json.RawMessage
		if err := json.Unmarshal(data, &shape); err != nil || len(shape) != 3 {
			t.Errorf("the envelope holds v, id and sealed, and holds %v", shape)
		}
		plain, err := s.openPlan(m.ID, data)
		if err != nil {
			t.Fatal(err)
		}
		if got := digestID(plain, planIDHex); got != m.ID {
			t.Errorf("the id is %s, the digest of the plaintext gives %s", m.ID, got)
		}
		if m.Size != len(plain) {
			t.Errorf("the size is that of the plan file: %d, want %d", m.Size, len(plain))
		}
		pf, pr, err := s.ReadFile(ctx, m.ID)
		if err != nil || pr == nil || pf.Unsealed || len(pf.Changes) != 2 || pf.Command != secretPlan(t0).Command {
			t.Fatalf("read: %+v %v", pf, err)
		}
	})
}

func TestASealedStateShowsNoPlantedValueToAThief(t *testing.T) {
	eachSealed(t, func(t *testing.T, s *sealedStore, _ *time.Time) {
		m := putSecret(t, s.PlanStore, t0, change("a", ActionCreate, 0), change("b", ActionUpdate, 2))
		if err := s.BeginApply(context.Background(), m.ID, "ada@host#1"); err != nil {
			t.Fatal(err)
		}
		all := everything(t, s.B)
		if got := seen(all); len(got) != 0 {
			t.Errorf("a thief reads %v in:\n%s", got, all)
		}
		// What stays readable is what the listing needs.
		for _, want := range []string{m.ID, `"author": "ada"`, `"addr": "a"`, `"addr": "b"`, `"action"`, `"lineage": "lineage-1"`, `"prior_revision"`} {
			if want == `"prior_revision"` {
				continue // that is in the plan, which is sealed
			}
			if !strings.Contains(all, want) {
				t.Errorf("the objects no longer show %s", want)
			}
		}
		// The key of a tag stays readable in the command line that the history shows.
		meta, _ := s.B.Get(context.Background(), metaKey2(m.ID))
		var raw PlanMeta
		if err := json.Unmarshal(meta, &raw); err != nil {
			t.Fatal(err)
		}
		tok := func(canon, v string) string { return "<sealed:" + s.keys.Tag(canon+"\x00"+v) + ">" }
		if want := "pravo plan --store -t pw=" + tok("tag", "pw="+plantedTag) + " -t note=" + tok("tag", "note=two") + " " + tok("word", "words"); raw.Command != want {
			t.Errorf("command %q, want %q", raw.Command, want)
		}
		if len(raw.Tags) != 2 || !strings.HasPrefix(raw.Tags[0], "pw=<sealed:") || !strings.HasPrefix(raw.Tags[1], "note=<sealed:") {
			t.Errorf("tags %q", raw.Tags)
		}
	})
}

func TestAReaderGetsTheCommandAndTheTagsBack(t *testing.T) {
	eachSealed(t, func(t *testing.T, s *sealedStore, _ *time.Time) {
		m := putSecret(t, s.PlanStore, t0, change("a", ActionCreate, 0))
		got, err := s.Meta(context.Background(), m.ID)
		if err != nil {
			t.Fatal(err)
		}
		if got.Command != m.Command || strings.Join(got.Tags, "|") != strings.Join(m.Tags, "|") || got.Sealed != nil || got.Redacted || got.Unsealed {
			t.Errorf("summary %+v, want the command %q and tags %q", got, m.Command, m.Tags)
		}
		if want := "pravo apply " + m.ID + " -t pw=" + plantedTag + " -t 'note=two words'"; got.ApplyCommand() != want {
			t.Errorf("apply command %q, want %q", got.ApplyCommand(), want)
		}
		entries, err := s.Entries(context.Background(), nil)
		if err != nil || len(entries) != 1 || entries[0].Meta.Command != m.Command {
			t.Errorf("entries %v, %v", entries, err)
		}
	})
}

func TestAStoreWithoutAKeyListsPlansAndCannotOpenThem(t *testing.T) {
	eachSealed(t, func(t *testing.T, s *sealedStore, clock *time.Time) {
		ctx := context.Background()
		m := putSecret(t, s.PlanStore, t0, change("a", ActionCreate, 0), change("b", ActionUpdate, 2))
		raw := NewPlanStore(s.B)
		raw.Now = func() time.Time { return *clock }
		for _, locked := range []*PlanStore{s.as(t, -1, clock), raw} {
			entries, err := locked.Entries(ctx, nil)
			if err != nil || len(entries) != 1 {
				t.Fatalf("entries %v, %v", entries, err)
			}
			e := entries[0]
			if e.Status != PlanPending || len(e.Meta.Changes) != 2 || e.Meta.Author != "ada" || !e.Meta.Redacted {
				t.Errorf("entry %+v meta %+v", e, e.Meta)
			}
			if got := seen(e.Meta.Command + strings.Join(e.Meta.Tags, " ")); len(got) != 0 {
				t.Errorf("the summary shows %v", got)
			}
			if want := "pravo apply " + m.ID + " -t pw=<value> -t note=<value>"; e.Meta.ApplyCommand() != want {
				t.Errorf("apply command %q, want %q", e.Meta.ApplyCommand(), want)
			}
			if _, _, err := locked.ReadFile(ctx, m.ID); !errors.Is(err, state.ErrNotReader) {
				t.Errorf("reading a sealed plan without a key: %v", err)
			}
			if err := locked.Pull(ctx, m.ID, t.TempDir()+"/plan.json"); !errors.Is(err, state.ErrNotReader) {
				t.Errorf("pulling a sealed plan without a key: %v", err)
			}
			if _, err := locked.Resolve(ctx, m.ID[:12]); err != nil {
				t.Errorf("resolving a prefix needs no key: %v", err)
			}
			// Claims are readable, so a store without a key sees that an apply runs.
			if _, err := locked.Progress(ctx, m.ID); err != nil {
				t.Errorf("progress: %v", err)
			}
		}
	})
}

func TestAStoreThatIsNotSealedCannotOpenASealedPlan(t *testing.T) {
	eachSealed(t, func(t *testing.T, s *sealedStore, _ *time.Time) {
		m := putSecret(t, s.PlanStore, t0, change("a", ActionCreate, 0))
		plain := NewPlanStore(s.B)
		if _, _, err := plain.ReadFile(context.Background(), m.ID); !errors.Is(err, state.ErrNotReader) {
			t.Errorf("a store made from the raw backend: %v", err)
		}
	})
}

func TestASealedPlanCannotBeOpenedAsAnotherPlan(t *testing.T) {
	eachSealed(t, func(t *testing.T, s *sealedStore, _ *time.Time) {
		ctx := context.Background()
		a := putSecret(t, s.PlanStore, t0, change("a", ActionCreate, 0))
		b := putSecret(t, s.PlanStore, t0, change("b", ActionCreate, 0))
		if a.ID == b.ID {
			t.Fatal("two plans, one id")
		}
		dataA, _ := s.B.Get(ctx, planKey(a.ID))

		// The object of a under the key of b: the envelope names a.
		if err := s.B.Put(ctx, planKey(b.ID), dataA); err != nil {
			t.Fatal(err)
		}
		if _, _, err := s.ReadFile(ctx, b.ID); !errors.Is(err, state.ErrUntrusted) {
			t.Errorf("plan a under the key of b: %v", err)
		}

		// The envelope of a with the id of b written into it: the seal binds the id.
		var env sealedPlan
		if err := json.Unmarshal(dataA, &env); err != nil {
			t.Fatal(err)
		}
		env.ID = b.ID
		forged, _ := indentJSON(&env)
		if err := s.B.Put(ctx, planKey(b.ID), forged); err != nil {
			t.Fatal(err)
		}
		_, _, err := s.ReadFile(ctx, b.ID)
		if !errors.Is(err, state.ErrUntrusted) || errors.Is(err, state.ErrNotReader) {
			t.Errorf("plan a with the id of b: %v", err)
		}

		// The same for the sealed part of a summary.
		metaA, _ := s.B.Get(ctx, metaKey2(a.ID))
		var ma PlanMeta
		if err := json.Unmarshal(metaA, &ma); err != nil {
			t.Fatal(err)
		}
		ma.ID = b.ID
		forgedMeta, _ := indentJSON(&ma)
		if err := s.B.Put(ctx, metaKey2(b.ID), forgedMeta); err != nil {
			t.Fatal(err)
		}
		if _, err := s.Meta(ctx, b.ID); !errors.Is(err, state.ErrUntrusted) {
			t.Errorf("summary of a with the id of b: %v", err)
		}

		// A damaged envelope.
		env.ID = a.ID
		env.Sealed.CT = append([]byte(nil), env.Sealed.CT...)
		env.Sealed.CT[len(env.Sealed.CT)-1] ^= 1
		damaged, _ := indentJSON(&env)
		if err := s.B.Put(ctx, planKey(a.ID), damaged); err != nil {
			t.Fatal(err)
		}
		if _, _, err := s.ReadFile(ctx, a.ID); !errors.Is(err, state.ErrUntrusted) {
			t.Errorf("a damaged plan: %v", err)
		}
	})
}

func TestAPlanStoredBeforeTheSealStaysReadableAndIsReported(t *testing.T) {
	eachSealed(t, func(t *testing.T, s *sealedStore, _ *time.Time) {
		ctx := context.Background()
		old := putSecret(t, NewPlanStore(s.B), t0, change("a", ActionCreate, 0)) // before the seal
		fresh := putSecret(t, s.PlanStore, t0.Add(time.Hour), change("b", ActionCreate, 0))

		pf, _, err := s.ReadFile(ctx, old.ID)
		if err != nil || !pf.Unsealed || len(pf.Changes) != 1 {
			t.Fatalf("an old plan in a sealed store: %+v, %v", pf, err)
		}
		pf, _, err = s.ReadFile(ctx, fresh.ID)
		if err != nil || pf.Unsealed {
			t.Fatalf("a sealed plan: %+v, %v", pf, err)
		}
		entries, err := s.Entries(ctx, nil)
		if err != nil || len(entries) != 2 {
			t.Fatalf("%v, %v", entries, err)
		}
		for _, e := range entries {
			if want := e.Meta.ID == old.ID; e.Meta.Unsealed != want {
				t.Errorf("%s: Unsealed is %v, want %v", e.Meta.ID, e.Meta.Unsealed, want)
			}
			if e.Meta.Command != secretPlan(t0).Command {
				t.Errorf("%s: command %q", e.Meta.ID, e.Meta.Command)
			}
		}
		if l, err := s.List(ctx, nil); err != nil || len(l.Unsealed) != 1 || l.Unsealed[0] != old.ID {
			t.Errorf("the listing should name the plan that was stored before the seal: %+v, %v", l, err)
		}
		// Stored again, the old plan is the same plan: it keeps its id and its objects.
		again := putSecret(t, s.PlanStore, t0, change("a", ActionCreate, 0))
		if again.ID != old.ID {
			t.Errorf("stored again: %s, was %s", again.ID, old.ID)
		}
		if data, _ := s.B.Get(ctx, planKey(old.ID)); !bytes.Contains(data, []byte(plantedMessage)) {
			t.Error("storing the same plan again must not rewrite the old object")
		}
	})
}

func TestTheSamePlanStoredAgainInASealedStoreKeepsItsID(t *testing.T) {
	eachSealed(t, func(t *testing.T, s *sealedStore, _ *time.Time) {
		first := putSecret(t, s.PlanStore, t0, change("a", ActionCreate, 0))
		second := putSecret(t, s.PlanStore, t0, change("a", ActionCreate, 0))
		if first.ID != second.ID {
			t.Fatalf("%s and %s: the sealing is random, and the plan is not", first.ID, second.ID)
		}
		if got := listKeys(t, s.B, PlanDir); len(got) != 2 {
			t.Errorf("objects %v", got)
		}
		// A different plan under an id that its digest shares is not the same plan.
		data, _ := s.B.Get(context.Background(), planKey(first.ID))
		if s.samePlan(first.ID, data, []byte("another plan")) {
			t.Error("a sealed plan is not the same as another plan")
		}
		if !s.samePlan(first.ID, []byte("x"), []byte("x")) {
			t.Error("an unsealed old object is the same plan when its bytes are")
		}
	})
}

func TestSealAllSealsThePlansThatNoApplyUses(t *testing.T) {
	eachSealed(t, func(t *testing.T, s *sealedStore, _ *time.Time) {
		ctx := context.Background()
		plain := NewPlanStore(s.B)
		a := putSecret(t, plain, t0, change("a", ActionCreate, 0))
		b := putSecret(t, plain, t0.Add(time.Minute), change("b", ActionCreate, 0))
		c := putSecret(t, plain, t0.Add(2*time.Minute), change("c", ActionCreate, 0))
		if len(seen(everything(t, s.B))) != 5 {
			t.Fatal("the plans were not stored in the clear")
		}
		if err := s.BeginApply(ctx, b.ID, "dan@host#7"); err != nil {
			t.Fatal(err)
		}

		sealed, skipped, err := s.SealAll(ctx)
		if err != nil || sealed != 2 || skipped != 1 {
			t.Fatalf("sealed %d, skipped %d, %v", sealed, skipped, err)
		}
		// The plan in use is as it was; the others are sealed under the same ids.
		for _, tc := range []struct {
			m      *PlanMeta
			sealed bool
		}{{a, true}, {b, false}, {c, true}} {
			data, _ := s.B.Get(ctx, planKey(tc.m.ID))
			if _, ok := asSealedPlan(data); ok != tc.sealed {
				t.Errorf("%s: sealed is %v, want %v", tc.m.ID, ok, tc.sealed)
			}
			meta, _ := s.B.Get(ctx, metaKey2(tc.m.ID))
			if got := seen(string(meta)); (len(got) == 0) != tc.sealed {
				t.Errorf("%s: the summary shows %v", tc.m.ID, got)
			}
			pf, _, err := s.ReadFile(ctx, tc.m.ID)
			if err != nil || pf.Unsealed == tc.sealed {
				t.Errorf("%s: %+v, %v", tc.m.ID, pf, err)
			}
			got, err := s.Meta(ctx, tc.m.ID)
			if err != nil || got.Command != tc.m.Command || strings.Join(got.Tags, "|") != strings.Join(tc.m.Tags, "|") {
				t.Errorf("%s: after the seal a reader sees command %q tags %q, %v", tc.m.ID, got.Command, got.Tags, err)
			}
		}
		// Sealing again changes nothing; when the apply is over the last plan is sealed.
		before := everything(t, s.B)
		if sealed, skipped, err := s.SealAll(ctx); err != nil || sealed != 0 || skipped != 1 {
			t.Errorf("again: sealed %d, skipped %d, %v", sealed, skipped, err)
		}
		if everything(t, s.B) != before {
			t.Error("sealing again must write nothing for the plans that are sealed")
		}
		s.EndApply(ctx, b.ID, "dan@host#7")
		if sealed, skipped, err := s.SealAll(ctx); err != nil || sealed != 1 || skipped != 0 {
			t.Errorf("after the apply: sealed %d, skipped %d, %v", sealed, skipped, err)
		}
		if got := seen(everything(t, s.B)); len(got) != 0 {
			t.Errorf("after the seal a thief reads %v", got)
		}
	})
}

func TestSealAllNeedsASealedState(t *testing.T) {
	eachBackend(t, func(t *testing.T, ps *PlanStore, _ *time.Time) {
		putSecret(t, ps, t0, change("a", ActionCreate, 0))
		if _, _, err := ps.SealAll(context.Background()); err == nil {
			t.Error("a store that seals nothing cannot seal")
		}
		if _, _, err := ps.ResealAll(context.Background()); err == nil {
			t.Error("a store that seals nothing cannot seal again")
		}
	})
}

// racing replaces the plan objects when a ReplaceIf is about to look at them, as
// an apply or a collection that runs at the same time would.
type racing struct {
	state.Backend
	mu     sync.Mutex
	before func(key string)
}

func (r *racing) ReplaceIf(ctx context.Context, key string, update func(cur []byte) ([]byte, bool)) (bool, error) {
	r.mu.Lock()
	hook := r.before
	r.mu.Unlock()
	if hook != nil {
		hook(key)
	}
	return r.Backend.ReplaceIf(ctx, key, update)
}

func TestSealAllLeavesAPlanThatChangedOrWentWhileItWasSealed(t *testing.T) {
	eachSealed(t, func(t *testing.T, s *sealedStore, _ *time.Time) {
		ctx := context.Background()
		plain := NewPlanStore(s.B)
		kept := putSecret(t, plain, t0, change("a", ActionCreate, 0))
		gone := putSecret(t, plain, t0.Add(time.Minute), change("b", ActionCreate, 0))

		r := &racing{Backend: s.B}
		r.before = func(key string) {
			switch key {
			case planKey(kept.ID):
				s.B.Put(ctx, key, []byte(`{"format":1,"changes":[],"changed":"meanwhile"}`))
			case planKey(gone.ID):
				s.B.Delete(ctx, key)
			}
		}
		s.B = r
		sealed, skipped, err := s.SealAll(ctx)
		if err != nil || sealed != 0 || skipped != 2 {
			t.Fatalf("sealed %d, skipped %d, %v", sealed, skipped, err)
		}
		if data, _ := r.Backend.Get(ctx, planKey(kept.ID)); !strings.Contains(string(data), "meanwhile") {
			t.Errorf("the object that changed meanwhile was overwritten: %s", data)
		}
		if _, err := r.Backend.Get(ctx, planKey(gone.ID)); !errors.Is(err, state.ErrNotFound) {
			t.Errorf("the plan that a collection removed was written back: %v", err)
		}
	})
}

func TestResealAllMovesThePlansToTheCurrentGroup(t *testing.T) {
	eachBackend2(t, func(t *testing.T, b state.Backend) {
		clock := t0
		s := newSealedStore(t, b, &clock, 2) // ada and dan
		ctx := context.Background()
		m1 := putSecret(t, s.PlanStore, t0, change("a", ActionCreate, 0))
		m2 := putSecret(t, s.PlanStore, t0.Add(time.Minute), change("b", ActionCreate, 0))
		leaver := s.as(t, 1, &clock) // dan, who is about to leave, and keeps the key of g1
		if _, _, err := leaver.ReadFile(ctx, m1.ID); err != nil {
			t.Fatal(err)
		}

		// dan leaves: a new group, and plans written from now on are sealed in it.
		rewrapped, err := s.meta.Rewrap(s.keys, s.readers[:1])
		if err != nil || rewrapped.NewGroup != "g2" {
			t.Fatalf("%+v, %v", rewrapped, err)
		}
		m3 := putSecret(t, s.PlanStore, t0.Add(2*time.Minute), change("c", ActionCreate, 0))
		group := func(id string) string {
			data, _ := s.B.Get(ctx, planKey(id))
			env, _ := asSealedPlan(data)
			return env.Sealed.Group
		}
		if group(m1.ID) != "g1" || group(m3.ID) != "g2" {
			t.Fatalf("groups %s %s", group(m1.ID), group(m3.ID))
		}

		n, skipped, err := s.ResealAll(ctx)
		if err != nil || n != 2 || skipped != 0 {
			t.Fatalf("resealed %d and left %d, %v", n, skipped, err)
		}
		for _, id := range []string{m1.ID, m2.ID, m3.ID} {
			if group(id) != "g2" {
				t.Errorf("%s is in group %s", id, group(id))
			}
			raw, _ := s.B.Get(ctx, metaKey2(id))
			var m PlanMeta
			json.Unmarshal(raw, &m)
			if m.Sealed == nil || m.Sealed.Group != "g2" {
				t.Errorf("the summary of %s is in %+v", id, m.Sealed)
			}
			got, err := s.Meta(ctx, id)
			if err != nil || got.Command != secretPlan(t0).Command {
				t.Errorf("%s: %q, %v", id, got.Command, err)
			}
			if _, _, err := s.ReadFile(ctx, id); err != nil {
				t.Errorf("%s: %v", id, err)
			}
		}
		// The reader who left kept the key of g1, and that is all they hold: what is
		// sealed again is closed to them, and what was sealed before is not.
		if _, _, err := leaver.ReadFile(ctx, m1.ID); err == nil {
			t.Error("the reader who left opens a plan that was sealed again")
		}
		if _, err := leaver.Meta(ctx, m1.ID); err != nil {
			t.Errorf("the summary shows its readable part to anyone: %v", err)
		}
		// Again: nothing to do.
		if n, skipped, err := s.ResealAll(ctx); err != nil || n != 0 || skipped != 0 {
			t.Errorf("again: %d, %d, %v", n, skipped, err)
		}
	})
}

// eachBackend2 runs a test on every backend, without a plan store.
func eachBackend2(t *testing.T, run func(t *testing.T, b state.Backend)) {
	t.Helper()
	for name, mk := range planBackends(t) {
		t.Run(name, func(t *testing.T) { run(t, mk()) })
	}
}

func TestResealAllSealsThePlansThatWereNotSealedAndSkipsThoseInUse(t *testing.T) {
	eachSealed(t, func(t *testing.T, s *sealedStore, _ *time.Time) {
		ctx := context.Background()
		plain := NewPlanStore(s.B)
		a := putSecret(t, plain, t0, change("a", ActionCreate, 0))
		b := putSecret(t, plain, t0.Add(time.Minute), change("b", ActionCreate, 0))
		if err := s.BeginApply(ctx, b.ID, "dan@host#7"); err != nil {
			t.Fatal(err)
		}
		n, skipped, err := s.ResealAll(ctx)
		if err != nil || n != 1 || skipped != 1 {
			t.Fatalf("sealed %d and left %d, %v", n, skipped, err)
		}
		if data, _ := s.B.Get(ctx, planKey(a.ID)); !bytes.Contains(data, []byte(`"sealed"`)) {
			t.Error("a was not sealed")
		}
		if data, _ := s.B.Get(ctx, planKey(b.ID)); bytes.Contains(data, []byte(`"sealed"`)) {
			t.Error("b is in use and was sealed")
		}
	})
}

func TestACollectionRemovesSealedPlans(t *testing.T) {
	eachSealed(t, func(t *testing.T, s *sealedStore, clock *time.Time) {
		ctx := context.Background()
		short := putSecret(t, s.PlanStore, t0, change("a", ActionCreate, 0))
		_ = short
		// An orphan: a sealed plan with no summary, as a write that did not finish leaves.
		pf := secretPlan(t0, change("o", ActionCreate, 0))
		pf.Command = "pravo plan -t orphan=1"
		data, _ := indentJSON(pf)
		orphanID := digestID(data, planIDHex)
		env, err := sealPlan(s.sealer(), orphanID, data)
		if err != nil {
			t.Fatal(err)
		}
		if ok, err := s.B.PutIfAbsent(ctx, planKey(orphanID), env); err != nil || !ok {
			t.Fatal(ok, err)
		}

		*clock = t0.Add(30 * time.Minute)
		if res := collect(t, s.PlanStore, GCOptions{}); len(res.Removed) != 0 {
			t.Errorf("too early: %+v", res.Removed)
		}
		*clock = t0.Add(61 * time.Minute)
		res := collect(t, s.PlanStore, GCOptions{})
		if ids(res.Removed) != orphanID {
			t.Errorf("an orphan older than an hour goes: %+v", res.Removed)
		}
		*clock = t0.Add(DefaultPlanTTL + time.Minute)
		res = collect(t, s.PlanStore, GCOptions{})
		if ids(res.Removed) != short.ID {
			t.Errorf("an expired sealed plan goes: %+v", res.Removed)
		}
		if got := listKeys(t, s.B, PlanDir); len(got) != 0 {
			t.Errorf("left over: %v", got)
		}
	})
}

func TestACollectionWithoutAKeyRemovesASealedPlanThatExpired(t *testing.T) {
	eachSealed(t, func(t *testing.T, s *sealedStore, clock *time.Time) {
		m := putSecret(t, s.PlanStore, t0, change("a", ActionCreate, 0))
		*clock = t0.Add(DefaultPlanTTL + time.Minute)
		locked := s.as(t, -1, clock)
		if res := collect(t, locked, GCOptions{}); ids(res.Removed) != m.ID {
			t.Errorf("the id and the summary are readable, which is all a collection needs: %+v", res.Removed)
		}
	})
}

func TestAPlanThatIsNotWhatItsNameSaysIsNotTouched(t *testing.T) {
	eachSealed(t, func(t *testing.T, s *sealedStore, clock *time.Time) {
		ctx := context.Background()
		id := "plan-0badc0de"
		if ok, err := s.B.PutIfAbsent(ctx, planKey(id), []byte(`{"v":1,"id":"plan-ffffffff","sealed":{"alg":"x","group":"g1","ct":"AA=="}}`)); err != nil || !ok {
			t.Fatal(ok, err)
		}
		*clock = t0.Add(48 * time.Hour)
		s.Now = func() time.Time { return *clock }
		collect(t, s.PlanStore, GCOptions{})
		if _, err := s.B.Get(ctx, planKey(id)); err != nil {
			t.Errorf("an object whose envelope names another plan was removed: %v", err)
		}
	})
}

func TestAnEnvelopeIsToldFromAPlanFile(t *testing.T) {
	plan, _ := indentJSON(secretPlan(t0))
	if _, ok := asSealedPlan(plan); ok {
		t.Error("a plan file is not an envelope")
	}
	for _, in := range []string{``, `x`, `{}`, `{"v":1}`, `{"v":1,"id":"plan-aaaaaa"}`, `[]`, `{"format":1,"v":1,"sealed":{}}`} {
		if _, ok := asSealedPlan([]byte(in)); ok {
			t.Errorf("%q is not an envelope", in)
		}
	}
	if _, ok := asSealedPlan([]byte(` {"v": 1, "id": "plan-aaaaaa", "sealed": {"alg":"a","group":"g1","ct":"AA=="}}`)); !ok {
		t.Error("an envelope with whitespace is one")
	}
}

func TestPutStoresTheCommandThatTheCallerShows(t *testing.T) {
	eachSealed(t, func(t *testing.T, s *sealedStore, _ *time.Time) {
		pf := secretPlan(t0, change("a", ActionCreate, 0))
		data, _ := indentJSON(pf)
		meta := newPlanMeta(pf, "ada", 0, len(data))
		meta.shownCommand = "pravo plan --store -t pw=<sealed:00000000> -t 'note=<sealed:11111111>'"
		if err := s.Put(context.Background(), data, meta); err != nil {
			t.Fatal(err)
		}
		raw, _ := s.B.Get(context.Background(), metaKey2(meta.ID))
		var stored PlanMeta
		if err := json.Unmarshal(raw, &stored); err != nil {
			t.Fatal(err)
		}
		if stored.Command != meta.shownCommand {
			t.Errorf("stored %q", stored.Command)
		}
		got, err := s.Meta(context.Background(), meta.ID)
		if err != nil || got.Command != pf.Command {
			t.Errorf("a reader sees %q, %v", got.Command, err)
		}
		if meta.Command != pf.Command || len(meta.Tags) != 2 || meta.Sealed != nil {
			t.Error("Put must not change the summary it was given but for its id and size")
		}
	})
}

func TestAPlanSealedByANewerPravoIsRefusedByName(t *testing.T) {
	eachSealed(t, func(t *testing.T, s *sealedStore, _ *time.Time) {
		m := putSecret(t, s.PlanStore, t0, change("a", ActionCreate, 0))
		data, _ := s.B.Get(context.Background(), planKey(m.ID))
		var env sealedPlan
		if err := json.Unmarshal(data, &env); err != nil {
			t.Fatal(err)
		}
		env.V = sealedPlanVersion + 1
		newer, _ := indentJSON(&env)
		s.B.Put(context.Background(), planKey(m.ID), newer)
		_, _, err := s.ReadFile(context.Background(), m.ID)
		if err == nil || !strings.Contains(err.Error(), "newer pravo") {
			t.Errorf("got %v", err)
		}
	})
}

func TestStorePlanKeepsTheCommandAsTypedAndShowsTheOneItIsGiven(t *testing.T) {
	h := newHarness(t)
	s := spec{cue: "a: " + thing("a", `content: "x"`) + "\n", resources: []string{"a"}}
	e := h.engine(s, Options{
		Command: "pravo plan --store -t pw=<sealed:0a1b2c3d>",
		Args:    []string{"plan", "--store", "-t", "pw=" + plantedTag},
	})
	meta, err := e.StorePlan(context.Background(), plan(t, e), []string{"pw=" + plantedTag}, false, 0)
	if err != nil {
		t.Fatal(err)
	}
	// This state is not sealed, so nothing is hidden and the plan is as it always was.
	if want := "pravo plan --store -t pw=" + plantedTag; meta.Command != want {
		t.Errorf("the summary of an unsealed state shows %q, want %q", meta.Command, want)
	}
	ps := NewPlanStoreFor(h.state)
	pf, _, err := ps.ReadFile(context.Background(), meta.ID)
	if err != nil || pf.Command != meta.Command || pf.Unsealed {
		t.Errorf("plan file command %q, %v", pf.Command, err)
	}
	if got := e.commandLine(); got != meta.Command {
		t.Errorf("command line %q", got)
	}
	// A caller that sets only the command, as the tests and old callers do, is as before.
	e2 := h.engine(s, Options{Command: "pravo plan"})
	if got := e2.commandLine(); got != "pravo plan" {
		t.Errorf("command line %q", got)
	}
}

func TestTheOutputsOfASealedStateAreRefusedWithAMessageThatNamesTheWayOut(t *testing.T) {
	id, err := protect.NewIdentity("dan", "")
	if err != nil {
		t.Fatal(err)
	}
	for _, who := range []*protect.Identity{nil, id} {
		err := notReaderOfOutputs("s3://northwind-state/prod", who, nil)
		if !errors.Is(err, state.ErrNotReader) {
			t.Errorf("the error should wrap ErrNotReader: %v", err)
		}
		msg := err.Error()
		for _, want := range []string{"s3://northwind-state/prod", "sealed", "readers.txt"} {
			if !strings.Contains(msg, want) {
				t.Errorf("%q should say %q", msg, want)
			}
		}
		if who != nil && !strings.Contains(msg, "dan") {
			t.Errorf("%q should name the identity", msg)
		}
		if who == nil && !strings.Contains(msg, "no identity") {
			t.Errorf("%q should say that there is no identity", msg)
		}
	}
}

func TestTheOutputsMessageSaysWhyTheIdentityCouldNotBeUsed(t *testing.T) {
	err := notReaderOfOutputs("./other.state", nil, errors.New("the identity ada is sealed with a passphrase and stdin is not a terminal"))
	if !errors.Is(err, state.ErrNotReader) || !strings.Contains(err.Error(), "stdin is not a terminal") {
		t.Errorf("%v", err)
	}
}

// The hooks fit the fields of the options of Upgrade and Rewrap.
var (
	_ = state.UpgradeOptions{Plans: (*PlanStore)(nil).SealAll}
	_ = state.RewrapOptions{Plans: (*PlanStore)(nil).ResealAll}
)

func TestTheCommandsOfAPlanWhoseTagsAreSealedHavePlaceholders(t *testing.T) {
	m := &PlanMeta{ID: "plan-2f9c1a4e", Command: "pravo plan --store -t secret=<sealed:6173f96e> --webhook <sealed:0badc0de> --limit 5",
		Tags: []string{"secret=<sealed:6173f96e>", "env=<sealed:11223344>"}, SysTags: true, Redacted: true}
	if got, want := m.ApplyCommand(), "pravo apply plan-2f9c1a4e -t secret=<value> -t env=<value> -T"; got != want {
		t.Errorf("apply: %q, want %q", got, want)
	}
	if got, want := m.ApplyTemplate(), m.ApplyCommand(); got != want {
		t.Errorf("template: %q, want %q", got, want)
	}
	if got, want := m.CommandText(), "pravo plan --store -t secret=<value> --webhook <value> --limit 5"; got != want {
		t.Errorf("command: %q, want %q", got, want)
	}
	// A summary that was opened shows the command as it was typed, and the template
	// still hides the values.
	open := &PlanMeta{ID: "plan-2f9c1a4e", Command: "pravo plan --store -t secret=hunter2", Tags: []string{"secret=hunter2", "odd=a b"}}
	if got := open.CommandText(); got != open.Command {
		t.Errorf("command: %q", got)
	}
	if got, want := open.ApplyCommand(), "pravo apply plan-2f9c1a4e -t secret=hunter2 -t 'odd=a b'"; got != want {
		t.Errorf("apply: %q, want %q", got, want)
	}
	if got, want := open.ApplyTemplate(), "pravo apply plan-2f9c1a4e -t secret=<value> -t odd=<value>"; got != want {
		t.Errorf("template: %q, want %q", got, want)
	}
}

func TestUnsealAllReturnsThePlansThatNoApplyUses(t *testing.T) {
	eachSealed(t, func(t *testing.T, s *sealedStore, _ *time.Time) {
		ctx := context.Background()
		a := putSecret(t, s.PlanStore, t0, change("a", ActionCreate, 0))
		b := putSecret(t, s.PlanStore, t0.Add(time.Minute), change("b", ActionCreate, 0))
		if got := seen(everything(t, s.B)); len(got) != 0 {
			t.Fatalf("the plans were stored in the open: %v", got)
		}
		if err := s.BeginApply(ctx, b.ID, "dan@host#7"); err != nil {
			t.Fatal(err)
		}
		unsealed, skipped, err := s.UnsealAll(ctx)
		if err != nil || unsealed != 1 || skipped != 1 {
			t.Fatalf("unsealed %d, skipped %d, %v", unsealed, skipped, err)
		}
		for _, tc := range []struct {
			m      *PlanMeta
			sealed bool
		}{{a, false}, {b, true}} {
			data, _ := s.B.Get(ctx, planKey(tc.m.ID))
			if _, ok := asSealedPlan(data); ok != tc.sealed {
				t.Errorf("%s: sealed is %v, want %v", tc.m.ID, ok, tc.sealed)
			}
		}
		// The unsealed plan is the plan its id names, and its summary shows what was typed.
		data, _ := s.B.Get(ctx, planKey(a.ID))
		if digestID(data, len(a.ID)-len("plan-")) != a.ID {
			t.Error("the plan in the open is not the plan its id names")
		}
		meta, _ := s.B.Get(ctx, metaKey2(a.ID))
		if got := string(meta); !strings.Contains(got, a.Command) || strings.Contains(got, `"sealed"`) {
			t.Errorf("summary after the unseal: %s", got)
		}
		// Again: the plan in use is still there, and the rest is not written.
		before := everything(t, s.B)
		if n, k, err := s.UnsealAll(ctx); err != nil || n != 0 || k != 1 || everything(t, s.B) != before {
			t.Errorf("again: %d, %d, %v", n, k, err)
		}
		s.EndApply(ctx, b.ID, "dan@host#7")
		if n, k, err := s.UnsealAll(ctx); err != nil || n != 1 || k != 0 {
			t.Errorf("after the apply: %d, %d, %v", n, k, err)
		}
	})
}
