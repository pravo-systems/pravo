package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/pravo-systems/pravo/internal/protect"
	"github.com/pravo-systems/pravo/internal/state"
)

// A roles change that takes a key from the writers while that key's apply runs would leave the
// records the apply writes signed by a non-writer (exit 7 for everybody): it is refused (F58).
func TestRolesChangeIsRefusedWhileTheDemotedKeyIsApplying(t *testing.T) {
	s := plannableBucket(t)
	s.readers(t, s.adaLine+" role:admin", s.danLine+" role:writer")
	if text, code := runPravo(t, s.dir, "--identity", "ada", "protect", "roles", "--yes"); code != 0 {
		t.Fatalf("protect roles: exit %d\n%s", code, text)
	}
	pin := protect.RolesGenesisKey + " " + loadReadersFile(s.dir, "").Pin

	st, err := state.OpenBackendWith(context.Background(), s.backend(t), state.Options{Identity: s.dan})
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

	s.readers(t, s.adaLine+" role:admin", s.danLine+" role:reader", pin)
	before := s.puts()
	text, code := runPravo(t, s.dir, "--identity", "ada", "protect", "roles", "--yes")
	if code != 4 || !strings.Contains(text, "dan has a running apply (txn "+txn.ID()) || !strings.Contains(text, "--allow-running") {
		t.Fatalf("demoting a key that is applying: exit %d\n%s", code, text)
	}
	if s.puts() != before {
		t.Error("the refusal wrote to the bucket")
	}
	// Removing the key from the readers file is the same.
	s.readers(t, s.adaLine+" role:admin", pin)
	if text, code := runPravo(t, s.dir, "--identity", "ada", "protect", "roles", "--yes"); code != 4 || !strings.Contains(text, "dan has a running apply") {
		t.Fatalf("removing a key that is applying: exit %d\n%s", code, text)
	}
	// Adding a reader is not a demotion.
	_, zedLine := person(t, s.ids, "zed")
	s.readers(t, s.adaLine+" role:admin", s.danLine+" role:writer", zedLine+" role:reader", pin)
	if text, code := runPravo(t, s.dir, "--identity", "ada", "protect", "roles", "--yes"); code != 0 {
		t.Fatalf("adding a reader while a writer applies: exit %d\n%s", code, text)
	}

	// The break-glass flag stores the roles and says what happens to the running apply.
	s.readers(t, s.adaLine+" role:admin", s.danLine+" role:reader", zedLine+" role:reader", pin)
	text, code = runPravo(t, s.dir, "--identity", "ada", "protect", "roles", "--yes", "--allow-running")
	if code != 0 || !strings.Contains(text, "dan has a running apply") || !strings.Contains(text, "stays refused") {
		t.Fatalf("--allow-running: exit %d\n%s", code, text)
	}
}

func TestDemotionAfterTheApplyEndedIsNotRefused(t *testing.T) {
	s := plannableBucket(t)
	s.readers(t, s.adaLine+" role:admin", s.danLine+" role:writer")
	if text, code := runPravo(t, s.dir, "--identity", "ada", "protect", "roles", "--yes"); code != 0 {
		t.Fatalf("protect roles: exit %d\n%s", code, text)
	}
	pin := protect.RolesGenesisKey + " " + loadReadersFile(s.dir, "").Pin
	st, err := state.OpenBackendWith(context.Background(), s.backend(t), state.Options{Identity: s.dan})
	if err != nil {
		t.Fatal(err)
	}
	txn := st.Begin("apply", "pravo apply")
	if err := txn.Start(); err != nil {
		t.Fatal(err)
	}
	if err := txn.Finish("applied", "", false); err != nil {
		t.Fatal(err)
	}
	s.readers(t, s.adaLine+" role:admin", s.danLine+" role:reader", pin)
	if text, code := runPravo(t, s.dir, "--identity", "ada", "protect", "roles", "--yes"); code != 0 {
		t.Fatalf("demoting after the apply: exit %d\n%s", code, text)
	}
}

// A killed apply (a signed start, a record, no finish, its lock gone) is vouched for when its key
// leaves the writers, so no command exits 7 afterwards (F60).
func TestDemotionAfterAKilledApplyKeepsItsRecords(t *testing.T) {
	s := plannableBucket(t)
	s.readers(t, s.adaLine+" role:admin", s.danLine+" role:writer")
	if text, code := runPravo(t, s.dir, "--identity", "ada", "protect", "roles", "--yes"); code != 0 {
		t.Fatalf("protect roles: exit %d\n%s", code, text)
	}
	pin := protect.RolesGenesisKey + " " + loadReadersFile(s.dir, "").Pin
	st, err := state.OpenBackendWith(context.Background(), s.backend(t), state.Options{Identity: s.dan})
	if err != nil {
		t.Fatal(err)
	}
	txn := st.Begin("apply", "pravo apply")
	if err := txn.Start(); err != nil {
		t.Fatal(err)
	}
	if err := st.Set(&state.Resource{Addr: "n1", Mode: "data", Type: "t_data", Name: "n1", Attributes: []byte(`{"id":"n1"}`)}, txn.ID()); err != nil {
		t.Fatal(err)
	}
	// The process is gone: no finish, no lock.

	s.readers(t, s.adaLine+" role:admin", s.danLine+" role:reader", pin)
	text, code := runPravo(t, s.dir, "--identity", "ada", "protect", "roles", "--yes")
	if code != 0 || !strings.Contains(text, "dan: vouched for 1 transaction, 1 of them interrupted") {
		t.Fatalf("demoting after a killed apply: exit %d\n%s", code, text)
	}
	for _, who := range []string{"ada", "dan"} {
		if text, code := runPravo(t, s.dir, "--identity", who, "plan"); code == 7 {
			t.Fatalf("%s: plan exit 7 after the demotion\n%s", who, text)
		}
	}
	if text, code := runPravo(t, s.dir, "--identity", "ada", "protect", "status"); code == 7 || !strings.Contains(text, "vouched for by an admin") {
		t.Fatalf("protect status: exit %d\n%s", code, text)
	}
}

// A run that was thought dead (a frozen process) is vouched for, then wakes and finishes after the
// demotion: its records stay accepted, and a second demotion vouches for the finished run (F62).
func TestAStalledApplyThatWakesAfterTheDemotionLocksNobodyOut(t *testing.T) {
	s := plannableBucket(t)
	s.readers(t, s.adaLine+" role:admin", s.danLine+" role:writer")
	if text, code := runPravo(t, s.dir, "--identity", "ada", "protect", "roles", "--yes"); code != 0 {
		t.Fatalf("protect roles: exit %d\n%s", code, text)
	}
	pin := protect.RolesGenesisKey + " " + loadReadersFile(s.dir, "").Pin
	st, err := state.OpenBackendWith(context.Background(), s.backend(t), state.Options{Identity: s.dan})
	if err != nil {
		t.Fatal(err)
	}
	txn := st.Begin("apply", "pravo apply")
	if err := txn.Start(); err != nil {
		t.Fatal(err)
	}
	set := func(addr string) {
		t.Helper()
		if err := st.Set(&state.Resource{Addr: addr, Mode: "data", Type: "t_data", Name: addr, Attributes: []byte(`{"id":"` + addr + `"}`)}, txn.ID()); err != nil {
			t.Fatal(err)
		}
		txn.Record(addr, "create", 0, 1)
	}
	set("n1")

	s.readers(t, s.adaLine+" role:admin", s.danLine+" role:reader", pin)
	if text, code := runPravo(t, s.dir, "--identity", "ada", "protect", "roles", "--yes"); code != 0 || !strings.Contains(text, "1 of them interrupted") {
		t.Fatalf("demotion: exit %d\n%s", code, text)
	}
	// The process wakes, writes one more record, and finishes.
	set("n2")
	if err := txn.Finish("applied", "", true); err != nil {
		t.Fatal(err)
	}
	for _, who := range []string{"ada", "dan"} {
		if text, code := runPravo(t, s.dir, "--identity", who, "plan"); code == 7 {
			t.Fatalf("%s: plan exit 7 after the stalled run finished\n%s", who, text)
		}
	}
	// The toggle: dan a writer again, then a reader again, vouches for the finished run.
	s.readers(t, s.adaLine+" role:admin", s.danLine+" role:writer", pin)
	if text, code := runPravo(t, s.dir, "--identity", "ada", "protect", "roles", "--yes"); code != 0 {
		t.Fatalf("writer again: exit %d\n%s", code, text)
	}
	s.readers(t, s.adaLine+" role:admin", s.danLine+" role:reader", pin)
	text, code := runPravo(t, s.dir, "--identity", "ada", "protect", "roles", "--yes")
	if code != 0 || !strings.Contains(text, "dan: vouched for 1 transaction") {
		t.Fatalf("second demotion: exit %d\n%s", code, text)
	}
	if text, code := runPravo(t, s.dir, "--identity", "ada", "plan"); code == 7 {
		t.Fatalf("plan exit 7 after the second demotion\n%s", text)
	}
}

// A run that was vouched for as interrupted and then ends with no end signature (the agent would
// not sign) must not make the record check dereference the missing signature (F65).
func TestAVouchedRunThatEndsWithoutAnEndSignatureDoesNotPanic(t *testing.T) {
	s := plannableBucket(t)
	s.readers(t, s.adaLine+" role:admin", s.danLine+" role:writer")
	if text, code := runPravo(t, s.dir, "--identity", "ada", "protect", "roles", "--yes"); code != 0 {
		t.Fatalf("protect roles: exit %d\n%s", code, text)
	}
	pin := protect.RolesGenesisKey + " " + loadReadersFile(s.dir, "").Pin
	b := s.backend(t)
	st, err := state.OpenBackendWith(context.Background(), b, state.Options{Identity: s.dan})
	if err != nil {
		t.Fatal(err)
	}
	txn := st.Begin("apply", "pravo apply")
	if err := txn.Start(); err != nil {
		t.Fatal(err)
	}
	if err := st.Set(&state.Resource{Addr: "n1", Mode: "data", Type: "t_data", Name: "n1", Attributes: []byte(`{"id":"n1"}`)}, txn.ID()); err != nil {
		t.Fatal(err)
	}
	txn.Record("n1", "create", 0, 1)
	s.readers(t, s.adaLine+" role:admin", s.danLine+" role:reader", pin)
	if text, code := runPravo(t, s.dir, "--identity", "ada", "protect", "roles", "--yes"); code != 0 || !strings.Contains(text, "1 of them interrupted") {
		t.Fatalf("demotion: exit %d\n%s", code, text)
	}
	if err := txn.Finish("applied", "", true); err != nil {
		t.Fatal(err)
	}
	// Take the end signature off the transaction, as when the signing key could not be reached.
	key := "txn/" + txn.ID() + ".json"
	data, err := b.Get(context.Background(), key)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	if doc["finish_sig"] == nil {
		t.Fatal("the transaction has no end signature to remove")
	}
	delete(doc, "finish_sig")
	data, _ = json.Marshal(doc)
	if err := b.Put(context.Background(), key, data); err != nil {
		t.Fatal(err)
	}
	text, code := runPravo(t, s.dir, "--identity", "ada", "plan")
	if code == 2 || strings.Contains(text, "nil pointer") {
		t.Fatalf("plan panicked: exit %d\n%s", code, text)
	}
}

// A run that finished but could not sign its end is accepted on its signed start (F64).
func TestARunWhoseEndCouldNotBeSignedLocksNobodyOut(t *testing.T) {
	s := plannableBucket(t)
	s.readers(t, s.adaLine+" role:admin", s.danLine+" role:writer")
	if text, code := runPravo(t, s.dir, "--identity", "ada", "protect", "roles", "--yes"); code != 0 {
		t.Fatalf("protect roles: exit %d\n%s", code, text)
	}
	b := s.backend(t)
	st, err := state.OpenBackendWith(context.Background(), b, state.Options{Identity: s.dan})
	if err != nil {
		t.Fatal(err)
	}
	txn := st.Begin("apply", "pravo apply")
	if err := txn.Start(); err != nil {
		t.Fatal(err)
	}
	if err := st.Set(&state.Resource{Addr: "n1", Mode: "data", Type: "t_data", Name: "n1", Attributes: []byte(`{"id":"n1"}`)}, txn.ID()); err != nil {
		t.Fatal(err)
	}
	txn.Record("n1", "create", 0, 1)
	if err := txn.Finish("applied", "", true); err != nil {
		t.Fatal(err)
	}
	key := "txn/" + txn.ID() + ".json"
	data, err := b.Get(context.Background(), key)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	delete(doc, "finish_sig")
	data, _ = json.Marshal(doc)
	if err := b.Put(context.Background(), key, data); err != nil {
		t.Fatal(err)
	}
	// Nothing binds the record to the run any more: it is refused until an admin vouches.
	if text, code := runPravo(t, s.dir, "--identity", "ada", "plan"); code != 7 {
		t.Fatalf("plan exit %d before the admin vouched, want 7\n%s", code, text)
	}
	if text, code := runPravo(t, s.dir, "--identity", "ada", "protect", "roles", "--adopt-unsigned", "--yes"); code != 0 || !strings.Contains(text, "end was not signed") && !strings.Contains(text, "vouched") {
		t.Fatalf("adopt: exit %d\n%s", code, text)
	}
	for _, who := range []string{"ada", "dan"} {
		if text, code := runPravo(t, s.dir, "--identity", who, "plan"); code == 7 || code == 2 {
			t.Fatalf("%s: plan exit %d after the admin vouched\n%s", who, code, text)
		}
	}
	text, code := runPravo(t, s.dir, "--identity", "ada", "protect", "status")
	if code == 7 || !strings.Contains(text, "1 vouched for by an admin") {
		t.Fatalf("protect status: exit %d\n%s", code, text)
	}

	// dan leaves: his key is removed, and what an admin vouched for stays valid.
	pin := protect.RolesGenesisKey + " " + loadReadersFile(s.dir, "").Pin
	s.readers(t, s.adaLine+" role:admin", pin)
	for _, args := range [][]string{{"protect", "rewrap", "--yes"}, {"protect", "roles", "--yes"}} {
		if text, code := runPravo(t, s.dir, append([]string{"--identity", "ada"}, args...)...); code != 0 {
			t.Fatalf("%v after dan left: exit %d\n%s", args, code, text)
		}
	}
	if text, code := runPravo(t, s.dir, "--identity", "ada", "plan"); code == 7 || code == 2 {
		t.Fatalf("plan exit %d after dan left\n%s", code, text)
	}
}

// One edit by someone who may write the bucket (deleting finish_sig of a finished transaction)
// and a record planted under it must be refused, not accepted.
func TestStrippedEndSignatureDoesNotAcceptAPlantedRecord(t *testing.T) {
	s := plannableBucket(t)
	s.readers(t, s.adaLine+" role:admin", s.danLine+" role:reader")
	if text, code := runPravo(t, s.dir, "--identity", "ada", "protect", "roles", "--yes"); code != 0 {
		t.Fatalf("roles: %d\n%s", code, text)
	}
	b := s.backend(t)
	keys, err := b.List(context.Background(), "txn")
	if err != nil || len(keys) == 0 {
		t.Fatalf("no transactions: %v %v", keys, err)
	}
	id := strings.TrimSuffix(strings.TrimPrefix(keys[0], "txn/"), ".json")
	key := "txn/" + id + ".json"
	data, err := b.Get(context.Background(), key)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	delete(doc, "finish_sig")
	data, _ = json.Marshal(doc)
	if err := b.Put(context.Background(), key, data); err != nil {
		t.Fatal(err)
	}
	st, err := state.OpenBackendWith(context.Background(), b, state.Options{Identity: s.dan})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Set(&state.Resource{Addr: "planted", Mode: "data", Type: "t_data", Name: "planted", Attributes: []byte(`{"id":"planted"}`)}, id); err != nil {
		t.Fatal(err)
	}
	if text, code := runPravo(t, s.dir, "--identity", "ada", "plan"); code != 7 {
		t.Fatalf("plan exit %d, want 7\n%s", code, text)
	}
	if text, code := runPravo(t, s.dir, "--identity", "ada", "protect", "status"); code != 7 {
		t.Fatalf("protect status exit %d, want 7\n%s", code, text)
	}
}

// A secret version whose transaction lost its end signature is refused until an admin vouches
// for the transaction, like the records of such a run (F67).
func TestSecretOfARunWithoutAnEndSignatureNeedsTheVouch(t *testing.T) {
	s := plannableBucket(t)
	s.readers(t, s.adaLine+" role:admin", s.danLine+" role:writer")
	if text, code := runPravo(t, s.dir, "--identity", "ada", "protect", "roles", "--yes"); code != 0 {
		t.Fatalf("roles: %d\n%s", code, text)
	}
	t.Setenv("S_FOR_THE_TEST", "hunter2")
	if text, code := runPravo(t, s.dir, "--identity", "dan", "secret", "set", "s2", "--from-env", "S_FOR_THE_TEST"); code != 0 {
		t.Fatalf("secret set: %d\n%s", code, text)
	}
	b := s.backend(t)
	keys, err := b.List(context.Background(), "txn")
	if err != nil {
		t.Fatal(err)
	}
	stripped := 0
	for _, key := range keys {
		data, err := b.Get(context.Background(), key)
		if err != nil {
			t.Fatal(err)
		}
		var doc map[string]any
		if err := json.Unmarshal(data, &doc); err != nil {
			t.Fatal(err)
		}
		if doc["kind"] != "secret" {
			continue
		}
		delete(doc, "finish_sig")
		data, _ = json.Marshal(doc)
		if err := b.Put(context.Background(), key, data); err != nil {
			t.Fatal(err)
		}
		stripped++
	}
	if stripped == 0 {
		t.Fatal("no secret transaction found")
	}
	if text, code := runPravo(t, s.dir, "--identity", "ada", "secret", "get", "s2"); code != 7 {
		t.Fatalf("secret get exit %d before the vouch, want 7\n%s", code, text)
	}
	if text, code := runPravo(t, s.dir, "--identity", "ada", "protect", "roles", "--adopt-unsigned", "--yes"); code != 0 {
		t.Fatalf("adopt: %d\n%s", code, text)
	}
	if text, code := runPravo(t, s.dir, "--identity", "ada", "secret", "get", "s2"); code != 0 || !strings.Contains(text, "hunter2") {
		t.Fatalf("secret get exit %d after the vouch\n%s", code, text)
	}

	// dan leaves: his key is removed, and what an admin vouched for stays valid.
	pin := protect.RolesGenesisKey + " " + loadReadersFile(s.dir, "").Pin
	s.readers(t, s.adaLine+" role:admin", pin)
	for _, args := range [][]string{{"protect", "rewrap", "--yes"}, {"protect", "roles", "--yes"}} {
		if text, code := runPravo(t, s.dir, append([]string{"--identity", "ada"}, args...)...); code != 0 {
			t.Fatalf("%v after dan left: exit %d\n%s", args, code, text)
		}
	}
	if text, code := runPravo(t, s.dir, "--identity", "ada", "secret", "get", "s2"); code != 0 || !strings.Contains(text, "hunter2") {
		t.Fatalf("secret get exit %d after dan left\n%s", code, text)
	}
}
