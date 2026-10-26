package engine

import (
	"bytes"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/pravo-systems/pravo/internal/state"
)

// A sealed plan file holds none of the plan in the clear, opens for a reader only,
// and stays sealed when an apply marks what it applied.
func TestASealedPlanFileOpensForAReaderAndStaysSealed(t *testing.T) {
	file, _ := markedPlan(t, 3)
	b, err := state.NewLocalBackend(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	h := newSealedStore(t, b, new(time.Time), 2)
	plain, err := ReadPlanFile(file)
	if err != nil {
		t.Fatal(err)
	}
	reader := &testSealer{h.keys}
	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := sealPlanFile(reader, data)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(sealed, []byte("r000")) || bytes.Contains(sealed, []byte(`"changes"`)) {
		t.Fatalf("the sealed plan file shows the plan:\n%s", sealed)
	}
	if err := os.WriteFile(file, sealed, 0o600); err != nil {
		t.Fatal(err)
	}
	if ok, err := PlanFileSealed(file); err != nil || !ok {
		t.Fatalf("PlanFileSealed = %v, %v", ok, err)
	}
	if _, err := ReadPlanFile(file); !errors.Is(err, state.ErrNotReader) {
		t.Errorf("a plan file read without a sealer: %v, want ErrNotReader", err)
	}
	if _, err := ReadPlanFileWith(file, &testSealer{}); !errors.Is(err, state.ErrNotReader) {
		t.Errorf("a plan file read by a non-reader: %v, want ErrNotReader", err)
	}
	pf, err := ReadPlanFileWith(file, reader)
	if err != nil {
		t.Fatal(err)
	}
	if !pf.Sealed || pf.Digest != plain.Digest || len(pf.Changes) != len(plain.Changes) {
		t.Errorf("sealed = %v, digest %s (plain %s), %d changes", pf.Sealed, pf.Digest, plain.Digest, len(pf.Changes))
	}
	if err := MarkApplied(file, pf, []string{"r000"}, "txn-1"); err != nil {
		t.Fatal(err)
	}
	if ok, _ := PlanFileSealed(file); !ok {
		t.Fatal("the plan file is not sealed after an apply marked it")
	}
	after, err := ReadPlanFileWith(file, reader)
	if err != nil {
		t.Fatal(err)
	}
	if after.Changes[0].AppliedTxn != "txn-1" {
		t.Errorf("the mark is not in the sealed plan file: %+v", after.Changes[0])
	}
	// Moved to another name, the envelope still opens: its id is the digest of what it holds.
	data, _ = os.ReadFile(file)
	data = bytes.Replace(data, []byte(`"id": "plan-`), []byte(`"id": "plan-0`), 1)
	if err := os.WriteFile(file, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadPlanFileWith(file, reader); !errors.Is(err, state.ErrUntrusted) {
		t.Errorf("a plan file with another id: %v, want ErrUntrusted", err)
	}
}
