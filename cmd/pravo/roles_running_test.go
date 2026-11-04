package main

import (
	"context"
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
