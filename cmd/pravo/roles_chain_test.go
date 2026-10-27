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

const rolesObject = "prod/roles/00000001.json"

// pinnedRoles makes ada an admin and dan a reader, stores the roles and returns the pin line.
func pinnedRoles(t *testing.T, s *sealed) string {
	t.Helper()
	s.readers(t, s.adaLine+" role:admin", s.danLine+" role:reader")
	if text, code := runPravo(t, s.dir, "--identity", "ada", "protect", "roles", "--yes"); code != 0 {
		t.Fatalf("protect roles: exit %d\n%s", code, text)
	}
	pin := loadReadersFile(s.dir, "").Pin
	if pin == "" {
		t.Fatal("protect roles did not pin the roles in the readers file")
	}
	return protect.RolesGenesisKey + " " + pin
}

func (s *sealed) readersFileText(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(s.dir, ".pravo", "access", readersFileName))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestRolesAreStoredInTheStateAndPinned(t *testing.T) {
	s := plannableBucket(t)
	s.readers(t, s.adaLine+" role:admin", s.danLine+" role:reader")

	text, code := runPravo(t, s.dir, "--identity", "dan", "protect", "roles", "--yes")
	if code != 5 || !strings.Contains(text, "an admin is needed to store the roles") {
		t.Fatalf("a reader stores roles: exit %d\n%s", code, text)
	}
	if _, ok := s.f.Objects[rolesObject]; ok {
		t.Fatal("a reader stored the roles")
	}

	text, code = runPravo(t, s.dir, "--identity", "ada", "protect", "roles", "--yes")
	if code != 0 || !strings.Contains(text, "Stored the roles as version 1") || !strings.Contains(text, "roles-genesis") {
		t.Fatalf("an admin stores roles: exit %d\n%s", code, text)
	}
	if _, ok := s.f.Objects[rolesObject]; !ok {
		t.Fatal("the roles are not in the state")
	}
	if !strings.Contains(s.readersFileText(t), "roles-genesis ") {
		t.Fatal("the readers file has no pin")
	}
	text, code = runPravo(t, s.dir, "--identity", "ada", "protect", "status")
	if code != 0 || !strings.Contains(text, "pinned, version 1 in the state") {
		t.Fatalf("status: exit %d\n%s", code, text)
	}
	// Again: nothing to store.
	text, code = runPravo(t, s.dir, "--identity", "ada", "protect", "roles", "--yes")
	if code != 0 || !strings.Contains(text, "already holds these roles") {
		t.Fatalf("a second run: exit %d\n%s", code, text)
	}
}

func TestEditingYourOwnRoleChangesNothingOncePinned(t *testing.T) {
	s := plannableBucket(t)
	s.readers(t, s.adaLine+" role:admin", s.danLine+" role:reader")
	// Before the roles are in the state, the file decides: this is the limit the pin removes.
	s.readers(t, s.adaLine+" role:admin", s.danLine+" role:writer")
	if text, code := runPravo(t, s.dir, "--identity", "dan", "state", "rm", "cfg"); code != 0 {
		t.Fatalf("without a pin the file decides: exit %d\n%s", code, text)
	}

	s = plannableBucket(t)
	pin := pinnedRoles(t, s)
	s.readers(t, s.adaLine+" role:admin", s.danLine+" role:admin", pin)
	text, code := runPravo(t, s.dir, "--identity", "dan", "state", "rm", "cfg")
	if code != 5 || !strings.Contains(text, "your identity is a reader") {
		t.Fatalf("a reader who edited the file: exit %d\n%s", code, text)
	}
	text, code = runPravo(t, s.dir, "--identity", "dan", "protect", "rewrap", "--yes")
	if code != 5 {
		t.Fatalf("rewrap by a reader who edited the file: exit %d\n%s", code, text)
	}
	text, code = runPravo(t, s.dir, "--identity", "ada", "protect", "status")
	if code != 0 || !strings.Contains(text, "the readers file asks for other roles") {
		t.Fatalf("status: exit %d\n%s", code, text)
	}
}

func TestAnAdminChangesARoleWithANewVersion(t *testing.T) {
	s := plannableBucket(t)
	pin := pinnedRoles(t, s)
	s.readers(t, s.adaLine+" role:admin", s.danLine+" role:writer", pin)
	text, code := runPravo(t, s.dir, "--identity", "ada", "protect", "roles", "--yes")
	if code != 0 || !strings.Contains(text, "version 2") || !strings.Contains(text, "dan: reader -> writer") {
		t.Fatalf("a role change: exit %d\n%s", code, text)
	}
	if !strings.Contains(s.readersFileText(t), pin) || strings.Count(s.readersFileText(t), "roles-genesis") != 1 {
		t.Errorf("the pin changed:\n%s", s.readersFileText(t))
	}
	if text, code := runPravo(t, s.dir, "--identity", "dan", "state", "rm", "cfg"); code != 0 {
		t.Fatalf("dan as a writer: exit %d\n%s", code, text)
	}
	// dan cannot store roles: he is a writer.
	if text, code := runPravo(t, s.dir, "--identity", "dan", "protect", "roles", "--yes"); code != 5 {
		t.Fatalf("a writer stores roles: exit %d\n%s", code, text)
	}
}

// putRolesAs writes a version of the roles into the bucket as a holder of the data key would.
func putRolesAs(t *testing.T, s *sealed, id *protect.Identity, seq int, readers []protect.Reader) {
	t.Helper()
	st, err := state.OpenBackendWith(context.Background(), s.backend(t), state.Options{Identity: id})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.PutRolesVersion(context.Background(), seq, protect.FormatRolesSet(readers), time.Now()); err != nil {
		t.Fatal(err)
	}
}

func rolesOf(t *testing.T, s *sealed, adaRole, danRole protect.Role) []protect.Reader {
	t.Helper()
	rs := loadReadersFile(s.dir, "").Readers
	for i := range rs {
		switch rs[i].Name {
		case "ada":
			rs[i].Role = adaRole
		case "dan":
			rs[i].Role = danRole
		}
	}
	return rs
}

func TestAVersionOfARoleSignedByAReaderIsRefused(t *testing.T) {
	s := plannableBucket(t)
	pinnedRoles(t, s)
	putRolesAs(t, s, s.dan, 2, rolesOf(t, s, protect.RoleAdmin, protect.RoleAdmin))
	for _, args := range [][]string{{"state", "rm", "cfg"}, {"protect", "status"}, {"protect", "roles", "--yes"}} {
		text, code := runPravo(t, s.dir, append([]string{"--identity", "ada"}, args...)...)
		if code != 7 || !strings.Contains(text, "roles/00000002.json") {
			t.Errorf("%v with a forged version: exit %d\n%s", args, code, text)
		}
	}
}

func TestAFirstVersionOfTheRolesThatTheFilePinsIsNotReplaced(t *testing.T) {
	s := plannableBucket(t)
	pinnedRoles(t, s)
	delete(s.f.Objects, rolesObject)
	// dan signs a first version that makes him an admin, which is valid by itself.
	putRolesAs(t, s, s.dan, 1, rolesOf(t, s, protect.RoleAdmin, protect.RoleAdmin))
	text, code := runPravo(t, s.dir, "--identity", "dan", "state", "rm", "cfg")
	if code != 7 || !strings.Contains(text, "not the ones") {
		t.Fatalf("a replaced first version: exit %d\n%s", code, text)
	}
}

func TestADeletedChainIsRefusedWhenThefilePinsIt(t *testing.T) {
	s := plannableBucket(t)
	pinnedRoles(t, s)
	delete(s.f.Objects, rolesObject)
	text, code := runPravo(t, s.dir, "--identity", "ada", "state", "rm", "cfg")
	if code != 7 || !strings.Contains(text, "pins the roles") && !strings.Contains(text, "has none") {
		t.Fatalf("a deleted chain: exit %d\n%s", code, text)
	}
}

func TestARolesChainThatTheFileDoesNotPinIsOnlyReported(t *testing.T) {
	s := plannableBucket(t)
	pinnedRoles(t, s)
	s.readers(t, s.adaLine+" role:admin", s.danLine+" role:writer") // the pin is dropped
	text, code := runPravo(t, s.dir, "--identity", "ada", "protect", "status")
	if code != 0 || !strings.Contains(text, "it does not pin") {
		t.Fatalf("status: exit %d\n%s", code, text)
	}
}

func TestAGapInTheChainOfRolesIsRefused(t *testing.T) {
	s := plannableBucket(t)
	pinnedRoles(t, s)
	putRolesAs(t, s, s.ada, 3, rolesOf(t, s, protect.RoleAdmin, protect.RoleWriter))
	text, code := runPravo(t, s.dir, "--identity", "ada", "state", "rm", "cfg")
	if code != 7 || !strings.Contains(text, "no version 2") {
		t.Fatalf("a gap: exit %d\n%s", code, text)
	}
}

func TestAnUnsignedVersionOfTheRolesIsRefused(t *testing.T) {
	s := plannableBucket(t)
	pinnedRoles(t, s)
	s.f.Objects["prod/roles/00000002.json"] = []byte(`{"text":"ada ssh-ed25519 AAAA role:admin\n","at":"2026-01-01T00:00:00Z"}`)
	out, code := runPravo(t, s.dir, "--identity", "ada", "state", "rm", "cfg")
	if code != 7 || !strings.Contains(out, "roles/00000002.json") {
		t.Fatalf("an unsigned version: exit %d\n%s", code, out)
	}
}
