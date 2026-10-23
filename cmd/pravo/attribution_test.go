package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pravo-systems/pravo/internal/engine"
	"github.com/pravo-systems/pravo/internal/state"
)

// txnIDs lists the ids of the transactions of the state, newest first.
func txnIDs(t *testing.T, s *sealed) []string {
	t.Helper()
	text, code := runPravo(t, s.dir, "--identity", "ada", "state", "log", "--json")
	if code != 0 {
		t.Fatalf("state log exited %d:\n%s", code, text)
	}
	var got []struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(text), &got); err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, g := range got {
		ids = append(ids, g.ID)
	}
	return ids
}

func signedBucket(t *testing.T) *sealed {
	t.Helper()
	s := sealedBucket(t)
	for _, args := range [][]string{{"--identity", "ada", "state", "rm", "net.vpc"}, {"--identity", "dan", "state", "rm", "net.db"}} {
		if text, code := runPravo(t, s.dir, args...); code != 0 {
			t.Fatalf("%v exited %d:\n%s", args, code, text)
		}
	}
	return s
}

func TestTransactionsAreSignedAndShown(t *testing.T) {
	s := signedBucket(t)
	text, code := runPravo(t, s.dir, "--identity", "ada", "state", "log")
	if code != 0 || !strings.Contains(text, "signed by ada (verified)") || !strings.Contains(text, "signed by dan (verified)") {
		t.Fatalf("log: exit %d\n%s", code, text)
	}
	text, code = runPravo(t, s.dir, "--identity", "ada", "protect", "status")
	if code != 0 || !strings.Contains(text, "Signatures   2 of 2 transactions verified\n") {
		t.Fatalf("status: exit %d\n%s", code, text)
	}
	text, code = runPravo(t, s.dir, "--identity", "ada", "protect", "status", "--json")
	if code != 0 || !strings.Contains(text, `"signatures": {`) || !strings.Contains(text, `"verified": 2`) {
		t.Fatalf("status --json: exit %d\n%s", code, text)
	}
	text, code = runPravo(t, s.dir, "--identity", "ada", "state", "log", "--json")
	if code != 0 || strings.Count(text, `"status": "verified"`) != 2 {
		t.Fatalf("log --json: exit %d\n%s", code, text)
	}
}

func TestATamperedTransactionIsUntrusted(t *testing.T) {
	s := signedBucket(t)
	id := txnIDs(t, s)[0]
	key := "prod/txn/" + id + ".json"
	var m map[string]any
	if err := json.Unmarshal(s.f.Objects[key], &m); err != nil {
		t.Fatal(err)
	}
	m["actor"] = "mallory"
	data, _ := json.Marshal(m)
	s.f.Objects[key] = data

	text, code := runPravo(t, s.dir, "--identity", "ada", "state", "log")
	if code != 7 || !strings.Contains(text, "the signature does not verify: the transaction was changed") {
		t.Fatalf("log: exit %d\n%s", code, text)
	}
	text, code = runPravo(t, s.dir, "--identity", "ada", "protect", "status")
	if code != 7 || !strings.Contains(text, "Untrusted: txn/"+id+".json has a signature that does not verify") ||
		!strings.Contains(text, "1 with a signature that does not verify") {
		t.Fatalf("status: exit %d\n%s", code, text)
	}
}

func TestASignatureOfALeaverIsNotAFailure(t *testing.T) {
	s := signedBucket(t)
	s.readers(t, s.danLine)
	text, code := runPravo(t, s.dir, "--identity", "dan", "state", "log")
	if code != 0 || !strings.Contains(text, "signed by ada, whose key is not in the readers file") || !strings.Contains(text, "signed by dan (verified)") {
		t.Fatalf("log: exit %d\n%s", code, text)
	}
	text, code = runPravo(t, s.dir, "--identity", "dan", "protect", "status")
	if code != 0 || !strings.Contains(text, "1 of 2 transactions verified · 1 signed with a key that is not in the file") {
		t.Fatalf("status: exit %d\n%s", code, text)
	}
	text, code = runPravo(t, s.dir, "--identity", "dan", "state", "log", "--json")
	if code != 0 || !strings.Contains(text, `"status": "key_not_in_file"`) || !strings.Contains(text, `"claimed": "ada"`) {
		t.Fatalf("log --json: exit %d\n%s", code, text)
	}
}

func TestWithoutAReadersFileNothingIsChecked(t *testing.T) {
	s := signedBucket(t)
	if err := os.Remove(filepath.Join(s.dir, ".pravo", "access", "readers.txt")); err != nil {
		t.Fatal(err)
	}
	text, code := runPravo(t, s.dir, "--identity", "ada", "state", "log")
	if code != 0 || strings.Contains(text, "signed by") || strings.Contains(text, "unsigned") {
		t.Fatalf("log: exit %d\n%s", code, text)
	}
	text, code = runPravo(t, s.dir, "--identity", "ada", "protect", "status")
	if code != 0 || !strings.Contains(text, "Signatures   not checked: the readers file is needed to check them") {
		t.Fatalf("status: exit %d\n%s", code, text)
	}
}

func TestATransactionBeforeSigningIsUnsignedAndNoFailure(t *testing.T) {
	s := signedBucket(t)
	id := txnIDs(t, s)[0]
	key := "prod/txn/" + id + ".json"
	var m map[string]any
	_ = json.Unmarshal(s.f.Objects[key], &m)
	delete(m, "start_sig")
	delete(m, "finish_sig")
	data, _ := json.Marshal(m)
	s.f.Objects[key] = data
	text, code := runPravo(t, s.dir, "--identity", "ada", "state", "log")
	if code != 0 || !strings.Contains(text, "    unsigned\n") {
		t.Fatalf("log: exit %d\n%s", code, text)
	}
	if text, code = runPravo(t, s.dir, "--identity", "ada", "protect", "status"); code != 0 || !strings.Contains(text, "1 unsigned") {
		t.Fatalf("status: exit %d\n%s", code, text)
	}
}

func TestAStoredPlanShowsWhoSignedItAndAnApplyNamesIt(t *testing.T) {
	dir := localRoot(t, "")
	ids := isolateIdentities(t)
	if text, code := runPravo(t, dir, "apply", "-auto-approve"); code != 0 {
		t.Fatalf("apply exited %d:\n%s", code, text)
	}
	adaID, ada := person(t, ids, "ada")
	writeReaders(t, filepath.Join(dir, ".pravo", "access"), ada)
	if text, code := runPravo(t, dir, "protect", "init", "--encrypt", "--yes", "--allow-local"); code != 0 {
		t.Fatalf("protect init exited %d:\n%s", code, text)
	}
	spec, _ := os.ReadFile(filepath.Join(dir, "main.cue"))
	more := string(spec) + "\nthere: $local.#Resource.local_file & {filename: \"there.txt\", content: \"there\\n\"}\n"
	if err := os.WriteFile(filepath.Join(dir, "main.cue"), []byte(more), 0o644); err != nil {
		t.Fatal(err)
	}
	id := storePlanAs(t, dir, "ci")
	text, code := runPravo(t, dir, "plan", "show", id)
	if code != 0 || !strings.Contains(text, "  signed:  ada (verified)\n") {
		t.Fatalf("plan show: exit %d\n%s", code, text)
	}
	text, code = runPravo(t, dir, "plan", "show", id, "--json")
	if code != 0 || !strings.Contains(text, `"status": "verified"`) {
		t.Fatalf("plan show --json: exit %d\n%s", code, text)
	}
	if text, code = runPravo(t, dir, "apply", id); code != 0 {
		t.Fatalf("apply exited %d:\n%s", code, text)
	}
	// The transaction of the apply names the plan and its digest, under its signature.
	text, code = runPravo(t, dir, "state", "log", "--json")
	var log []struct {
		Kind       string `json:"kind"`
		Plan       string `json:"plan"`
		PlanDigest string `json:"plan_digest"`
		Signature  struct {
			Status string `json:"status"`
		} `json:"signature"`
	}
	if code != 0 || json.Unmarshal([]byte(text), &log) != nil || len(log) == 0 {
		t.Fatalf("state log: exit %d\n%s", code, text)
	}
	var found bool
	for _, l := range log {
		if l.Plan == id {
			found = true
			if len(l.PlanDigest) != 64 || l.Signature.Status != "verified" {
				t.Errorf("the apply: %+v", l)
			}
		}
	}
	if !found {
		t.Errorf("no transaction names %s:\n%s", id, text)
	}
	// The viewer's plan view gets the same verdicts.
	st, err := state.OpenWith(filepath.Join(dir, "pravo.state"), state.Options{Identity: adaID})
	if err != nil {
		t.Fatal(err)
	}
	digest, err := engine.NewPlanStoreFor(st).PlanDigest(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	v := &viewerState{dir: dir}
	if sg, ok := v.verifyPlan(context.Background(), st, id, digest); !ok || len(sg.Items) != 1 || sg.Items[0].Status != "verified" || sg.Items[0].Signer != "ada" {
		t.Errorf("the plan view: %+v %v", sg, ok)
	}
	if sg, _ := v.verifyPlan(context.Background(), st, id, "00"); len(sg.Items) != 1 || sg.Items[0].Status != "invalid" {
		t.Errorf("another digest: %+v", sg)
	}
	// The readers file no longer lists the signer.
	_, dan := person(t, ids, "dan")
	writeReaders(t, filepath.Join(dir, ".pravo", "access"), dan)
	text, _ = runPravo(t, dir, "--identity", "ada", "plan", "show", id)
	if !strings.Contains(text, "ada (key not in the readers file)") {
		t.Errorf("a leaver's plan:\n%s", text)
	}
}

// A run that was killed leaves a transaction that state log calls interrupted; its start
// signature still verifies, and it is not reported as unsigned.
func TestAKilledRunStillShowsItsStartSignature(t *testing.T) {
	s := sealedBucket(t)
	st, err := state.OpenBackendWith(context.Background(), s.backend(t), state.Options{Identity: s.ada})
	if err != nil {
		t.Fatal(err)
	}
	tx := st.Begin("apply", "pravo apply")
	if err := tx.Start(); err != nil {
		t.Fatal(err)
	}
	text, code := runPravo(t, s.dir, "--identity", "ada", "state", "log")
	if code != 0 || !strings.Contains(text, "interrupted") || !strings.Contains(text, "start signed by ada (verified)") {
		t.Fatalf("log: exit %d\n%s", code, text)
	}
	text, code = runPravo(t, s.dir, "--identity", "ada", "protect", "status")
	if code != 0 || !strings.Contains(text, "1 of 1 transaction verified") {
		t.Fatalf("status: exit %d\n%s", code, text)
	}
}
