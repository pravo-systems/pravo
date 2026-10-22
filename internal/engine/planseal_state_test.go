package engine

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pravo-systems/pravo/internal/protect"
	"github.com/pravo-systems/pravo/internal/state"
)

const (
	plantedContent = "planted-content-of-a-record"
	plantedValue   = "planted-value-of-a-tag"
)

// unsealedStart skips a test that makes a state in the clear and seals it, when
// the suite is run over sealed stores (PRAVO_TEST_FORMAT=3).
func unsealedStart(t *testing.T) {
	t.Helper()
	if state.NewStoreFormat == state.FormatSealed {
		t.Skip("the test seals a state that it made in the clear")
	}
}

// sealedHarness is a harness whose state was upgraded to sealed, after one apply
// and one stored plan were made in the clear.
func sealedHarness(t *testing.T) (*harness, *protect.Identity, *PlanMeta) {
	t.Helper()
	unsealedStart(t)
	ctx := context.Background()
	h := newHarness(t)
	s := spec{cue: "a: " + thing("a", `content: "`+plantedContent+`"`) + "\n", resources: []string{"a"}}
	h.apply(s)
	// A plan stored before the seal.
	e := h.engine(s2(), Options{Command: "pravo plan --store -t pw=" + plantedValue, Args: []string{"plan", "--store", "-t", "pw=" + plantedValue}})
	old, err := e.StorePlan(ctx, plan(t, e), []string{"pw=" + plantedValue}, false, 0)
	if err != nil {
		t.Fatal(err)
	}
	id, err := protect.NewIdentity("ada", "")
	if err != nil {
		t.Fatal(err)
	}
	res, err := h.state.Upgrade(ctx, state.UpgradeOptions{
		To: state.FormatSealed, Readers: []protect.Reader{id.AsReader()}, Identity: id, By: "ada",
		Plans: NewPlanStoreFor(h.state).SealAll,
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.PlansSealed != 1 || res.PlansSkipped != 0 {
		t.Fatalf("upgrade sealed %d plans and skipped %d", res.PlansSealed, res.PlansSkipped)
	}
	st, err := state.OpenBackendWith(ctx, h.backend, state.Options{Identity: id})
	if err != nil {
		t.Fatal(err)
	}
	h.state = st
	return h, id, old
}

func s2() spec {
	return spec{cue: "a: " + thing("a", `content: "`+plantedContent+`-2"`) + "\n", resources: []string{"a"}}
}

// walk returns the text of every file under the state directory.
func walk(t *testing.T, dir string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(dir, p)
		out[rel] = string(data)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestSealedStateKeepsTheValuesOfPlansAndCommandsFromAThief(t *testing.T) {
	h, _, old := sealedHarness(t)
	ctx := context.Background()
	args := []string{"apply", "-t", "pw=" + plantedValue, "--reason", "r"}
	shown, _ := h.state.ShowCommand(args)
	if strings.Contains(shown, plantedValue) || !strings.Contains(shown, "pw=<sealed:") {
		t.Fatalf("the state shows %q", shown)
	}
	e := h.engine(s2(), Options{Command: shown, Args: args})
	p := plan(t, e)
	meta, err := e.StorePlan(ctx, p, []string{"pw=" + plantedValue}, false, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.Apply(ctx, p); err != nil {
		t.Fatal(err)
	}
	for name, text := range walk(t, filepath.Join(h.dir, "state")) {
		for _, v := range []string{plantedContent, plantedValue} {
			if strings.Contains(text, v) {
				t.Errorf("%s holds %q:\n%s", name, v, text)
			}
		}
	}
	// A reader gets the plan and the command back, for the plan stored before the seal as well.
	ps := NewPlanStoreFor(h.state)
	for _, m := range []*PlanMeta{old, meta} {
		got, err := ps.Meta(ctx, m.ID)
		if err != nil || !strings.Contains(got.Command, plantedValue) || len(got.Tags) != 1 || got.Tags[0] != "pw="+plantedValue || got.Unsealed {
			t.Errorf("%s: %+v, %v", m.ID, got, err)
		}
		if _, _, err := ps.ReadFile(ctx, m.ID); err != nil {
			t.Errorf("%s: %v", m.ID, err)
		}
	}
	// The history shows the form that the state gave.
	txns, err := h.state.Transactions()
	if err != nil || len(txns) == 0 {
		t.Fatal(txns, err)
	}
	if txns[0].Command != shown {
		t.Errorf("the transaction shows %q, want %q", txns[0].Command, shown)
	}
}

// A failed run in a sealed state leaves no byte of the text of its error in the
// bucket, where anyone who can read the bucket would find the value that a
// provider echoed, and keeps the text sealed for the readers.
func TestSealedStateKeepsTheTextOfAnErrorForItsReadersAndNotForTheBucket(t *testing.T) {
	h, _, _ := sealedHarness(t)
	ctx := context.Background()
	s := spec{cue: "a: " + thing("a", `content: "`+plantedContent+`-3"`) + "\n", resources: []string{"a"}}
	e := h.engine(s, Options{})
	p := plan(t, e)
	hd := h.fake.hold("a")
	done := applyingResult(e, ctx, p)
	waitFor(t, hd)
	hd.release <- errors.New("the provider said " + plantedValue)
	if got := finished(t, done); got.err == nil || !strings.Contains(got.err.Error(), plantedValue) {
		t.Fatalf("the apply should fail with the text of the provider: %v", got.err)
	}
	for name, text := range walk(t, filepath.Join(h.dir, "state")) {
		if strings.Contains(text, plantedValue) {
			t.Errorf("%s holds the text of the error:\n%s", name, text)
		}
	}

	failed := func(st *state.Store) state.Transaction {
		t.Helper()
		txns, err := st.Transactions()
		if err != nil {
			t.Fatal(err)
		}
		for _, tx := range txns {
			if tx.Status == "failed" {
				return tx
			}
		}
		t.Fatal("no failed transaction")
		return state.Transaction{}
	}
	tx := failed(h.state)
	if !strings.HasPrefix(tx.Summary, state.SealedErrorText) || tx.SummarySealed == nil {
		t.Fatalf("the transaction shows %q and a box %v", tx.Summary, tx.SummarySealed != nil)
	}
	if got := h.state.SummaryText(tx); !strings.Contains(got, "the provider said "+plantedValue) {
		t.Errorf("a reader sees %q", got)
	}

	// A store with no key sees the fixed text.
	locked, err := state.OpenBackendWith(ctx, h.backend, state.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if got := locked.SummaryText(tx); got != tx.Summary {
		t.Errorf("a store without a key sees %q", got)
	}
	// The box is bound to its transaction: moved to another one it does not open.
	moved := tx
	moved.ID = "20260101T000000Z-aaaaaa"
	if got := h.state.SummaryText(moved); got != tx.Summary {
		t.Errorf("a box moved to another transaction opens: %q", got)
	}
}

func TestTheSummaryOfAFailureInASealedStateKeepsTheNotesAndNotTheError(t *testing.T) {
	unsealedStart(t)
	plain := newHarness(t)
	ep := plain.engine(spec{}, Options{})
	if got := ep.errText(errors.New("diagnostic")); got != "diagnostic" {
		t.Errorf("an unsealed state records the error: %q", got)
	}

	h, _, _ := sealedHarness(t)
	e := h.engine(s2(), Options{})
	boom := errors.New("the provider said " + plantedValue)
	if got := e.errText(boom); strings.Contains(got, plantedValue) || got == "" {
		t.Errorf("a sealed state records %q", got)
	}
	e.unsettled = map[string]string{"c": "create of c finished, but the state does not record it; check the provider"}
	if got := e.errText(boom); strings.Contains(got, plantedValue) || !strings.Contains(got, "create of c finished") {
		t.Errorf("the notes about changes whose result is unknown stay: %q", got)
	}
}

func TestAStoreWithoutAKeyOfASealedStateListsThePlans(t *testing.T) {
	h, _, old := sealedHarness(t)
	ctx := context.Background()
	locked, err := state.OpenBackendWith(ctx, h.backend, state.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if !locked.Locked() {
		t.Fatal("a sealed state opens locked without an identity")
	}
	ps := NewPlanStoreFor(locked)
	entries, err := ps.Entries(ctx, PlanCheckFor(locked))
	if err != nil || len(entries) != 1 || !entries[0].Meta.Redacted {
		t.Fatalf("%v %v", entries, err)
	}
	if _, _, err := ps.ReadFile(ctx, old.ID); !errors.Is(err, state.ErrNotReader) {
		t.Errorf("%v", err)
	}
	if err := ps.Put(ctx, []byte("{}"), &PlanMeta{}); !errors.Is(err, state.ErrNotReader) {
		t.Errorf("a store without a key cannot store a plan: %v", err)
	}
}

func TestAPlanIsNotStoredWhileTheStateIsBeingSealed(t *testing.T) {
	unsealedStart(t)
	ctx := context.Background()
	h := newHarness(t)
	s := spec{cue: "a: " + thing("a", `content: "x"`) + "\n", resources: []string{"a"}}
	h.apply(s)
	id, err := protect.NewIdentity("ada", "")
	if err != nil {
		t.Fatal(err)
	}
	ps := NewPlanStoreFor(h.state)
	var duringSeal error
	_, err = h.state.Upgrade(ctx, state.UpgradeOptions{
		To: state.FormatSealed, Readers: []protect.Reader{id.AsReader()}, Identity: id,
		Plans: func(ctx context.Context) (int, int, error) {
			pf := secretPlan(t0, change("a", ActionCreate, 0))
			data, _ := indentJSON(pf)
			duringSeal = ps.Put(ctx, data, newPlanMeta(pf, "ada", 0, len(data)))
			return ps.SealAll(ctx)
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !errors.Is(duringSeal, state.ErrSealing) {
		t.Errorf("a plan stored while the seal runs: %v", duringSeal)
	}
	if got := listKeys(t, h.backend, PlanDir); len(got) != 0 {
		t.Errorf("objects written: %v", got)
	}
}

// otherRoot makes the state of another root with one recorded output, sealed to ada when seal is set.
func otherRoot(t *testing.T, seal bool, ada *protect.Identity) state.Backend {
	t.Helper()
	ctx := context.Background()
	b, err := state.NewLocalBackend(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	st, err := state.OpenBackend(ctx, b)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetOutputs(map[string]json.RawMessage{"url": json.RawMessage(`"https://example.com"`)}); err != nil {
		t.Fatal(err)
	}
	if seal {
		if _, err := st.Upgrade(ctx, state.UpgradeOptions{To: state.FormatSealed, Readers: []protect.Reader{ada.AsReader()}, Identity: ada}); err != nil {
			t.Fatal(err)
		}
	}
	return b
}

func TestRemoteOutputsOfASealedStateNeedAReader(t *testing.T) {
	unsealedStart(t)
	ctx := context.Background()
	ada, err := protect.NewIdentity("ada", "")
	if err != nil {
		t.Fatal(err)
	}
	dan, err := protect.NewIdentity("dan", "")
	if err != nil {
		t.Fatal(err)
	}
	h := newHarness(t)
	sealedRoot, plainRoot := otherRoot(t, true, ada), otherRoot(t, false, nil)

	asked := 0
	engineFor := func(id *protect.Identity, idErr error) *Engine {
		e := h.engine(spec{}, Options{})
		e.Opts.Identity = func() (*protect.Identity, error) { asked++; return id, idErr }
		return e
	}

	// A reader gets the outputs.
	got, err := engineFor(ada, nil).openRemoteOutputs(ctx, sealedRoot)
	if err != nil || string(got["url"]) != `"https://example.com"` {
		t.Fatalf("a reader: %v %v", got, err)
	}
	// A state that is not sealed never asks for an identity.
	asked = 0
	if got, err := engineFor(ada, nil).openRemoteOutputs(ctx, plainRoot); err != nil || string(got["url"]) != `"https://example.com"` || asked != 0 {
		t.Fatalf("unsealed: %v %v, asked %d times", got, err, asked)
	}
	// Someone who is not a reader, someone with no identity, and an identity that cannot be used.
	for name, e := range map[string]*Engine{
		"not a reader": engineFor(dan, nil),
		"no identity":  engineFor(nil, os.ErrNotExist),
		"unusable":     engineFor(nil, errors.New("the identity ada is sealed with a passphrase and stdin is not a terminal")),
		"no function":  h.engine(spec{}, Options{}),
	} {
		_, err := e.openRemoteOutputs(ctx, sealedRoot)
		if !errors.Is(err, state.ErrNotReader) {
			t.Errorf("%s: %v", name, err)
			continue
		}
		if strings.Contains(err.Error(), "example.com") {
			t.Errorf("%s: the message shows a value: %v", name, err)
		}
	}
	if _, err := engineFor(dan, nil).openRemoteOutputs(ctx, sealedRoot); err == nil || !strings.Contains(err.Error(), "dan") {
		t.Errorf("the message should name the identity: %v", err)
	}
	if _, err := engineFor(nil, errors.New("stdin is not a terminal")).openRemoteOutputs(ctx, sealedRoot); err == nil || !strings.Contains(err.Error(), "stdin is not a terminal") {
		t.Errorf("the message should say why the identity could not be used: %v", err)
	}
}

func TestRewrapAllSealsThePlansAgainForTheReadersThatStay(t *testing.T) {
	unsealedStart(t)
	ctx := context.Background()
	h := newHarness(t)
	s := spec{cue: "a: " + thing("a", `content: "x"`) + "\n", resources: []string{"a"}}
	h.apply(s)
	ada, err := protect.NewIdentity("ada", "")
	if err != nil {
		t.Fatal(err)
	}
	dan, err := protect.NewIdentity("dan", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.state.Upgrade(ctx, state.UpgradeOptions{To: state.FormatSealed, Readers: []protect.Reader{ada.AsReader(), dan.AsReader()}, Identity: ada}); err != nil {
		t.Fatal(err)
	}
	asAda, err := state.OpenBackendWith(ctx, h.backend, state.Options{Identity: ada})
	if err != nil {
		t.Fatal(err)
	}
	asDan, err := state.OpenBackendWith(ctx, h.backend, state.Options{Identity: dan})
	if err != nil {
		t.Fatal(err)
	}
	h.state = asAda
	e := h.engine(s, Options{Command: "pravo plan --store -t pw=x", Args: []string{"plan", "--store", "-t", "pw=" + plantedValue}})
	meta, err := e.StorePlan(ctx, plan(t, e), []string{"pw=" + plantedValue}, false, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := NewPlanStoreFor(asDan).ReadFile(ctx, meta.ID); err != nil {
		t.Fatalf("a reader reads the plan: %v", err)
	}

	// dan leaves; the plan is sealed again, in the new group, by the readers that stay.
	ps := NewPlanStoreFor(asAda)
	res, err := asAda.Rewrap(ctx, state.RewrapOptions{Readers: []protect.Reader{ada.AsReader()}, All: true, By: "ada", Plans: ps.ResealAll})
	if err != nil {
		t.Fatal(err)
	}
	if res.Change.NewGroup == "" {
		t.Fatalf("a removal makes a new group: %+v", res.Change)
	}
	data, _ := h.backend.Get(ctx, planKey(meta.ID))
	if env, ok := asSealedPlan(data); !ok || env.Sealed.Group != res.Change.NewGroup {
		t.Errorf("the plan is in %+v, want group %s", env, res.Change.NewGroup)
	}
	if got, _, err := ps.ReadFile(ctx, meta.ID); err != nil || got.Command != "pravo plan --store -t pw="+plantedValue {
		t.Errorf("a reader that stays: %v %v", got, err)
	}
	if _, _, err := NewPlanStoreFor(asDan).ReadFile(ctx, meta.ID); !errors.Is(err, state.ErrNotReader) {
		t.Errorf("the reader that left should not open the plan any more: %v", err)
	}
}

// In a sealed state the id of a plan is still the digest of its plan file. A plan
// file in the clear that someone put in place of the sealed one, and is not what
// the id says, is refused; the plan file itself, in the clear, is read and marked.
func TestSealedStateRefusesAPlanInTheClearThatIsNotWhatItsIDSays(t *testing.T) {
	h, _, _ := sealedHarness(t)
	ctx := context.Background()
	e := h.engine(s2(), Options{Command: "pravo plan --store", Args: []string{"plan", "--store"}})
	meta, err := e.StorePlan(ctx, plan(t, e), nil, false, 0)
	if err != nil {
		t.Fatal(err)
	}
	ps := NewPlanStoreFor(h.state)
	sealedObj, err := h.backend.Get(ctx, planKey(meta.ID))
	if err != nil {
		t.Fatal(err)
	}
	plain, err := ps.openPlan(meta.ID, sealedObj)
	if err != nil {
		t.Fatal(err)
	}
	put := func(data []byte) {
		t.Helper()
		if _, err := h.backend.ReplaceIf(ctx, planKey(meta.ID), func([]byte) ([]byte, bool) { return data, true }); err != nil {
			t.Fatal(err)
		}
	}

	put(append(append([]byte{}, plain...), '\n'))
	if _, _, err := ps.ReadFile(ctx, meta.ID); !errors.Is(err, state.ErrUntrusted) {
		t.Errorf("a plan in the clear with another digest: %v, want ErrUntrusted", err)
	}
	var forged []byte
	forged = append(forged, plain...)
	if i := strings.LastIndex(string(forged), "}"); i > 0 {
		forged = append(forged[:i], append([]byte(`, "forged": true`), forged[i:]...)...)
	}
	put(forged)
	if _, _, err := ps.ReadFile(ctx, meta.ID); !errors.Is(err, state.ErrUntrusted) {
		t.Errorf("a changed plan file in the clear: %v, want ErrUntrusted", err)
	}

	put(plain)
	pf, _, err := ps.ReadFile(ctx, meta.ID)
	if err != nil {
		t.Fatalf("the plan file itself, in the clear: %v", err)
	}
	if !pf.Unsealed {
		t.Error("a plan in the clear in a sealed state is not marked Unsealed")
	}
}
