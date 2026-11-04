package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pravo-systems/pravo/internal/protect"
	"github.com/pravo-systems/pravo/internal/state"
)

// A state whose roles are pinned, with every admin's key lost: recovery is break-glass. It stores
// the roles again with the recovering key as an admin, so that the team is not left with a
// reader that nothing in the CLI can promote.
func TestRecoverStoresRolesWithTheRecoveringKeyAsAnAdmin(t *testing.T) {
	r := recoveredBucket(t)
	r.readers(t, r.adaLine+" role:admin", r.danLine+" role:writer")
	if text, code := runPravo(t, r.dir, "--identity", "ada", "protect", "roles", "--yes"); code != 0 {
		t.Fatalf("protect roles: exit %d\n%s", code, text)
	}
	oldPin := loadReadersFile(r.dir, "").Pin
	if oldPin == "" {
		t.Fatal("no pin")
	}

	d := t.TempDir()
	eve, eveData := newIdentityFile(t, "eve", true)
	putIdentity(t, d, "eve", eveData)
	t.Setenv("PRAVO_IDENTITY_DIR", d)
	resetIdentityState(t)
	r.readers(t, eve.Line()+" role:admin", protect.RolesGenesisKey+" "+oldPin)

	text, code := runPravo(t, r.dir, "protect", "recover", "--yes", "--share", r.shares[0], "--share", r.shares[1])
	if code != 0 || !strings.Contains(text, "Stored the roles again as version 1, with eve as an admin") {
		t.Fatalf("recover: exit %d\n%s", code, text)
	}
	newPin := loadReadersFile(r.dir, "").Pin
	if newPin == "" || newPin == oldPin || strings.Count(r.readersFileText(t), protect.RolesGenesisKey) != 1 {
		t.Fatalf("the pin was not replaced: %q -> %q\n%s", oldPin, newPin, r.readersFileText(t))
	}
	// She writes, and she stores roles: nothing else is needed to get back in.
	if text, code := runPravo(t, r.dir, "state", "rm", "net.vpc"); code != 0 {
		t.Fatalf("a write after recovery: exit %d\n%s", code, text)
	}
	if text, code := runPravo(t, r.dir, "protect", "roles", "--yes"); code != 0 || !strings.Contains(text, "already holds these roles (version 1)") {
		t.Fatalf("protect roles after recovery: exit %d\n%s", code, text)
	}
}

func TestTheLastAdminCannotLeaveOrStepDown(t *testing.T) {
	s := plannableBucket(t)
	pin := pinnedRoles(t, s) // ada admin, dan reader

	// ada removes herself from the readers file and rewraps.
	s.readers(t, s.danLine+" role:reader", pin)
	text, code := runPravo(t, s.dir, "--identity", "ada", "protect", "rewrap", "--yes")
	if code != 5 || !strings.Contains(text, "no admin would be left") || !strings.Contains(text, "--allow-no-admin") {
		t.Fatalf("the last admin leaves: exit %d\n%s", code, text)
	}
	// ada demotes herself.
	s.readers(t, s.adaLine+" role:writer", s.danLine+" role:reader", pin)
	text, code = runPravo(t, s.dir, "--identity", "ada", "protect", "roles", "--yes")
	if code != 5 || !strings.Contains(text, "no admin would be left") {
		t.Fatalf("the last admin steps down: exit %d\n%s", code, text)
	}
	// Another admin makes both fine.
	s.readers(t, s.adaLine+" role:admin", s.danLine+" role:admin", pin)
	if text, code := runPravo(t, s.dir, "--identity", "ada", "protect", "roles", "--yes"); code != 0 {
		t.Fatalf("adding an admin: exit %d\n%s", code, text)
	}
	s.readers(t, s.danLine+" role:admin", pin)
	if text, code := runPravo(t, s.dir, "--identity", "ada", "protect", "rewrap", "--yes"); code != 0 {
		t.Fatalf("an admin leaves while another stays: exit %d\n%s", code, text)
	}
}

func TestTheOptOutLetsTheLastAdminStepDown(t *testing.T) {
	s := plannableBucket(t)
	pin := pinnedRoles(t, s)
	s.readers(t, s.adaLine+" role:writer", s.danLine+" role:reader", pin)
	if text, code := runPravo(t, s.dir, "--identity", "ada", "protect", "roles", "--yes", "--allow-no-admin"); code != 0 {
		t.Fatalf("--allow-no-admin: exit %d\n%s", code, text)
	}
}

// Deleting the pin line from a local readers file, and marking yourself admin, must not be
// enough to re-key a state whose roles are stored.
func TestDroppingThePinLineDoesNotMakeAReaderAnAdmin(t *testing.T) {
	s := plannableBucket(t)
	pinnedRoles(t, s) // ada admin, dan reader
	s.readers(t, s.adaLine+" role:admin", s.danLine+" role:admin")
	for _, args := range [][]string{{"protect", "rewrap", "--yes"}, {"protect", "roles", "--yes"}, {"state", "rm", "cfg"}} {
		text, code := runPravo(t, s.dir, append([]string{"--identity", "dan"}, args...)...)
		if code != 5 || !strings.Contains(text, "your identity is a reader in the roles") {
			t.Errorf("a reader without the pin line runs %v: exit %d\n%s", args, code, text)
		}
	}
	text, code := runPravo(t, s.dir, "--identity", "dan", "protect", "status")
	if code != 0 || !strings.Contains(text, "you are a reader in the roles in the state") {
		t.Errorf("status: exit %d\n%s", code, text)
	}
}

// The refusal names the roles that decided, not a file that did not.
func TestTheRefusalNamesWhereTheRolesCameFrom(t *testing.T) {
	s := plannableBucket(t)
	pin := pinnedRoles(t, s)
	text, code := runPravo(t, s.dir, "--identity", "dan", "state", "rm", "cfg")
	if code != 5 || !strings.Contains(text, "your identity is a reader in the roles in the state, and a writer is needed") {
		t.Fatalf("pinned: exit %d\n%s", code, text)
	}
	if strings.Contains(text, filepath.Join(".pravo", "access")) {
		t.Errorf("the refusal names the readers file:\n%s", text)
	}
	_ = pin
}

// A checkout whose readers file still lists an offboarded key must not add it back.
func TestAFormerKeyIsNotAddedBackByAStaleReadersFile(t *testing.T) {
	s := plannableBucket(t)
	pin := pinnedRoles(t, s)
	s.readers(t, s.adaLine+" role:admin", s.danLine+" role:writer", pin)
	if text, code := runPravo(t, s.dir, "--identity", "ada", "protect", "roles", "--yes"); code != 0 {
		t.Fatalf("roles: exit %d\n%s", code, text)
	}
	// ada offboards dan.
	s.readers(t, s.adaLine+" role:admin", pin)
	for _, args := range [][]string{{"protect", "rewrap", "--yes"}, {"protect", "roles", "--yes"}} {
		if text, code := runPravo(t, s.dir, append([]string{"--identity", "ada"}, args...)...); code != 0 {
			t.Fatalf("%v: exit %d\n%s", args, code, text)
		}
	}
	// A stale checkout lists dan again.
	s.readers(t, s.adaLine+" role:admin", s.danLine+" role:writer", pin)
	for _, args := range [][]string{{"protect", "rewrap", "--yes"}, {"protect", "roles", "--yes"}} {
		text, code := runPravo(t, s.dir, append([]string{"--identity", "ada"}, args...)...)
		if code != 5 || !strings.Contains(text, "dan was taken out of the roles") || !strings.Contains(text, "--readd dan") {
			t.Fatalf("%v with a stale file: exit %d\n%s", args, code, text)
		}
	}
	for _, args := range [][]string{{"protect", "rewrap", "--yes", "--readd", "dan"}, {"protect", "roles", "--yes", "--readd", "dan"}} {
		if text, code := runPravo(t, s.dir, append([]string{"--identity", "ada"}, args...)...); code != 0 {
			t.Fatalf("%v: exit %d\n%s", args, code, text)
		}
	}
}

// Handing the admin role over in one edit (ada out, zoe in as admin) must not store roles whose
// only admin has no lock in the state: nobody could then rewrap, store roles or write.
func TestAdminHandoverInOneEditIsRefusedAndTheRightOrderWorks(t *testing.T) {
	s := plannableBucket(t)
	pin := pinnedRoles(t, s) // ada admin, dan reader
	_, zoeLine := person(t, filepath.Join(os.Getenv("PRAVO_CONFIG_DIR"), "id"), "zoe")

	s.readers(t, s.danLine+" role:reader", zoeLine+" role:admin", pin)
	text, code := runPravo(t, s.dir, "--identity", "ada", "protect", "rewrap", "--yes")
	if code != 5 || !strings.Contains(text, "no admin would be left") || strings.Contains(text, "Make another key an admin first") {
		t.Fatalf("rewrap of the one-edit hand-over: exit %d\n%s", code, text)
	}
	text, code = runPravo(t, s.dir, "--identity", "ada", "protect", "roles", "--yes")
	if code != 5 || !strings.Contains(text, "no admin in these roles can read the state yet") {
		t.Fatalf("roles of the one-edit hand-over: exit %d\n%s", code, text)
	}
	// ada is still an admin of the stored roles, so nothing was lost.
	text, code = runPravo(t, s.dir, "--identity", "ada", "protect", "status")
	if code != 0 {
		t.Fatalf("status: exit %d\n%s", code, text)
	}

	// The right order.
	s.readers(t, s.adaLine+" role:admin", s.danLine+" role:reader", zoeLine+" role:admin", pin)
	for _, args := range [][]string{{"protect", "rewrap", "--yes"}, {"protect", "roles", "--yes"}} {
		if text, code := runPravo(t, s.dir, append([]string{"--identity", "ada"}, args...)...); code != 0 {
			t.Fatalf("%v: exit %d\n%s", args, code, text)
		}
	}
	s.readers(t, s.danLine+" role:reader", zoeLine+" role:admin", pin)
	if text, code := runPravo(t, s.dir, "--identity", "ada", "protect", "rewrap", "--yes"); code != 0 {
		t.Fatalf("ada leaves: exit %d\n%s", code, text)
	}
	if text, code := runPravo(t, s.dir, "--identity", "zoe", "protect", "roles", "--yes"); code != 0 {
		t.Fatalf("zoe stores roles: exit %d\n%s", code, text)
	}
}

// Roles are read back before a command reports success or pins them: a version that does not
// parse is reported, not pinned.
func TestRolesAreReadBackBeforeTheyAreReportedStored(t *testing.T) {
	s := plannableBucket(t)
	ctx := context.Background()
	st, err := state.OpenBackendWith(ctx, s.backend(t), state.Options{Identity: s.ada})
	if err != nil {
		t.Fatal(err)
	}
	p := &protectRun{ctx: ctx}
	if err := st.PutRolesVersion(ctx, 1, "this is not a roles set\n", time.Now()); err != nil {
		t.Fatal(err)
	}
	err = p.verifyRolesStored(st, 1, "this is not a roles set\n")
	if err == nil || !strings.Contains(err.Error(), "reading them back failed") {
		t.Fatalf("a version that does not parse was reported stored: %v", err)
	}
}

// Roles that would not parse again are refused before they are stored.
func TestUnreadableRolesAreRefusedBeforeTheyAreStored(t *testing.T) {
	s := plannableBucket(t)
	ctx := context.Background()
	st, err := state.OpenBackendWith(ctx, s.backend(t), state.Options{Identity: s.ada})
	if err != nil {
		t.Fatal(err)
	}
	if err := checkRolesText("this is not a roles set\n"); err == nil || !strings.Contains(err.Error(), "nothing was stored") {
		t.Fatalf("unreadable roles passed the check: %v", err)
	}
	if err := checkRolesText(""); err == nil {
		t.Fatal("empty roles passed the check")
	}
	chain, err := st.RolesChain(ctx)
	if err != nil || chain.Head() != nil {
		t.Fatalf("the state holds roles after a refusal: %v", err)
	}
}
