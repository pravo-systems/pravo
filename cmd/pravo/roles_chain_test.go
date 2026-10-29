package main

import (
	"context"
	"encoding/json"
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
	if err := st.PutRolesVersion(context.Background(), seq, protect.FormatRolesSet(readers, nil, nil), time.Now()); err != nil {
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

// writeAs records addr in a transaction of its own that id signs, as a command would.
func writeAs(t *testing.T, s *sealed, id *protect.Identity, addr string) {
	t.Helper()
	st, err := state.OpenBackendWith(context.Background(), s.backend(t), state.Options{Identity: id})
	if err != nil {
		t.Fatal(err)
	}
	st.BindRecords(true)
	tx := st.Begin("edit", "pravo test")
	if err := tx.Start(); err != nil {
		t.Fatal(err)
	}
	attrs := `{"id":"` + addr + `"}`
	if err := st.Set(&state.Resource{Addr: addr, Mode: "data", Type: "t_data", Name: addr, Attributes: []byte(attrs)}, tx.ID()); err != nil {
		t.Fatal(err)
	}
	tx.Record(addr, "create", 0, 1)
	if err := tx.Finish("applied", "", true); err != nil {
		t.Fatal(err)
	}
}

func TestAKeyThatLeftIsFormerAndItsVouchedRecordsStayUsable(t *testing.T) {
	s := plannableBucket(t)
	pin := pinnedRoles(t, s)
	s.readers(t, s.adaLine+" role:admin", s.danLine+" role:writer", pin)
	if text, code := runPravo(t, s.dir, "--identity", "ada", "protect", "roles", "--yes"); code != 0 {
		t.Fatalf("protect roles: exit %d\n%s", code, text)
	}
	writeAs(t, s, s.dan, "extra")
	if text, code := runPravo(t, s.dir, "--identity", "ada", "protect", "status"); code != 0 {
		t.Fatalf("status with dan a writer: exit %d\n%s", code, text)
	}

	// dan leaves. Until an admin stores the roles again, the chain still has him as a writer.
	s.readers(t, s.adaLine+" role:admin", pin)
	if text, code := runPravo(t, s.dir, "--identity", "ada", "protect", "rewrap", "--yes"); code != 0 {
		t.Fatalf("rewrap: exit %d\n%s", code, text)
	}
	text, code := runPravo(t, s.dir, "--identity", "ada", "protect", "status")
	if code != 0 || !strings.Contains(text, "1 bound to a transaction of a writer") || !strings.Contains(text, "the readers file asks for other roles") {
		t.Fatalf("status after dan left: exit %d\n%s", code, text)
	}
	text, code = runPravo(t, s.dir, "--identity", "ada", "protect", "roles", "--yes")
	if code != 0 || !strings.Contains(text, "dan: removed, now a former key") || !strings.Contains(text, "dan: vouched for 1 transaction") {
		t.Fatalf("protect roles after dan left: exit %d\n%s", code, text)
	}
	obj := string(s.f.Objects["prod/roles/00000003.json"])
	if !strings.Contains(obj, "role:former") || !strings.Contains(obj, "@vouch") {
		t.Fatalf("dan is not a former key with a vouch:\n%s", obj)
	}
	text, code = runPravo(t, s.dir, "--identity", "ada", "protect", "status")
	if code != 0 || !strings.Contains(text, "1 vouched for by an admin") || !strings.Contains(text, "pinned, version 3") {
		t.Fatalf("status with dan a former key: exit %d\n%s", code, text)
	}
	if text, code := runPravo(t, s.dir, "--identity", "ada", "state", "rm", "extra"); code != 0 {
		t.Fatalf("the record of a former key: exit %d\n%s", code, text)
	}
	// Run again: nothing changes.
	if text, code := runPravo(t, s.dir, "--identity", "ada", "protect", "roles", "--yes"); code != 0 || !strings.Contains(text, "already holds these roles") {
		t.Fatalf("a second protect roles: exit %d\n%s", code, text)
	}
}

// vouchedBucket is a pinned bucket in which dan wrote the record "extra" as a writer, and ada
// then dropped him from the roles, with the transaction of that record vouched for. dan still
// holds the data key, as someone who left and kept it.
func vouchedBucket(t *testing.T, demote bool) *sealed {
	t.Helper()
	s := plannableBucket(t)
	pin := pinnedRoles(t, s)
	s.readers(t, s.adaLine+" role:admin", s.danLine+" role:writer", pin)
	if text, code := runPravo(t, s.dir, "--identity", "ada", "protect", "roles", "--yes"); code != 0 {
		t.Fatalf("protect roles: exit %d\n%s", code, text)
	}
	writeAs(t, s, s.dan, "extra")
	if demote {
		s.readers(t, s.adaLine+" role:admin", s.danLine+" role:reader", pin)
	} else {
		s.readers(t, s.adaLine+" role:admin", pin)
	}
	if text, code := runPravo(t, s.dir, "--identity", "ada", "protect", "roles", "--yes"); code != 0 || !strings.Contains(text, "vouched for 1 transaction") {
		t.Fatalf("protect roles: exit %d\n%s", code, text)
	}
	return s
}

func TestADemotedWritersRecordsStayAndItsNewWritesAreRefused(t *testing.T) {
	s := vouchedBucket(t, true)
	text, code := runPravo(t, s.dir, "--identity", "ada", "protect", "status")
	if code != 0 || !strings.Contains(text, "1 vouched for by an admin") {
		t.Fatalf("status after the demotion: exit %d\n%s", code, text)
	}
	if text, code := runPravo(t, s.dir, "--identity", "ada", "plan"); code != 0 {
		t.Fatalf("plan after the demotion: exit %d\n%s", code, text)
	}
	// dan, a reader now, writes a record with a transaction of his own.
	writeAs(t, s, s.dan, "forged")
	text, code = runPravo(t, s.dir, "--identity", "ada", "protect", "status")
	if code != 7 || !strings.Contains(text, "Untrusted: forged:") || !strings.Contains(text, "who is a reader and may not write") {
		t.Fatalf("status after a demoted key wrote: exit %d\n%s", code, text)
	}
	if strings.Contains(text, "Untrusted: extra") {
		t.Errorf("the vouched record was refused:\n%s", text)
	}
}

func TestAKeyThatLeftCannotWriteWithTheOldDataKey(t *testing.T) {
	s := vouchedBucket(t, false)
	if text, code := runPravo(t, s.dir, "--identity", "ada", "protect", "status"); code != 0 {
		t.Fatalf("status after dan left: exit %d\n%s", code, text)
	}
	// Nobody cut his credentials or ran rewrap --all: he seals a record and signs it with his old key.
	writeAs(t, s, s.dan, "forged")
	text, code := runPravo(t, s.dir, "--identity", "ada", "protect", "status")
	if code != 7 || !strings.Contains(text, "Untrusted: forged:") || !strings.Contains(text, "no admin vouched for it") {
		t.Fatalf("status after a leaver wrote: exit %d\n%s", code, text)
	}
}

func TestAVouchedTransactionRewrittenInPlaceIsNotVouchedFor(t *testing.T) {
	s := vouchedBucket(t, false)
	n := 0
	for k, v := range s.f.Objects {
		if !strings.HasPrefix(k, "prod/txn/") {
			continue
		}
		var m map[string]any
		if json.Unmarshal(v, &m) != nil {
			continue
		}
		changes, _ := m["changes"].([]any)
		for _, c := range changes {
			if c, ok := c.(map[string]any); ok && c["addr"] == "extra" {
				// The signed parts are not changed back: the key is dan's, and he can sign anything.
				c["record"] = "rec/" + strings.Repeat("0", 64)
				data, _ := json.Marshal(m)
				s.f.Objects[k] = data
				n++
			}
		}
	}
	if n == 0 {
		t.Fatal("no transaction wrote extra")
	}
	text, code := runPravo(t, s.dir, "--identity", "ada", "protect", "status")
	if code != 7 || !strings.Contains(text, "Untrusted: extra:") {
		t.Fatalf("status after a rewrite in place: exit %d\n%s", code, text)
	}
}

// signedByAKeyNobodyListed makes the transactions of dan look as if a key that is in no list
// had signed them, as a holder of the data key could do with a key of their own.
func signedByAKeyNobodyListed(t *testing.T, s *sealed) {
	t.Helper()
	fp := protect.SSHFingerprint(s.dan.SignPub)
	fake := "SHA256:" + strings.Repeat("A", 43)
	n := 0
	for k, v := range s.f.Objects {
		if strings.HasPrefix(k, "prod/txn/") && strings.Contains(string(v), fp) {
			s.f.Objects[k] = []byte(strings.ReplaceAll(string(v), fp, fake))
			n++
		}
	}
	if n == 0 {
		t.Fatal("no transaction of dan")
	}
}

func TestAKeyThatWasNeverListedIsRefusedOncePinned(t *testing.T) {
	s := plannableBucket(t)
	pin := pinnedRoles(t, s)
	s.readers(t, s.adaLine+" role:admin", s.danLine+" role:writer", pin)
	if text, code := runPravo(t, s.dir, "--identity", "ada", "protect", "roles", "--yes"); code != 0 {
		t.Fatalf("protect roles: exit %d\n%s", code, text)
	}
	writeAs(t, s, s.dan, "extra")
	signedByAKeyNobodyListed(t, s)

	text, code := runPravo(t, s.dir, "--identity", "ada", "protect", "status")
	if code != 7 || !strings.Contains(text, "no admin vouched for it") {
		t.Fatalf("status: exit %d\n%s", code, text)
	}
	text, code = runPravo(t, s.dir, "--identity", "ada", "plan")
	if code != 7 || !strings.Contains(text, "no admin vouched for it") {
		t.Fatalf("plan: exit %d\n%s", code, text)
	}
	// An admin who stores the roles again is shown the key, and records it as former.
	text, code = runPravo(t, s.dir, "--identity", "ada", "protect", "roles", "--yes")
	if code != 0 || !strings.Contains(text, "is in no list: recorded as former") {
		t.Fatalf("protect roles: exit %d\n%s", code, text)
	}
	if text, code := runPravo(t, s.dir, "--identity", "ada", "protect", "status"); code != 0 {
		t.Fatalf("status after the key is recorded: exit %d\n%s", code, text)
	}
}

func TestAKeyThatWasNeverListedIsOnlyCountedWithoutAPin(t *testing.T) {
	s := plannableBucket(t)
	s.readers(t, s.adaLine+" role:admin", s.danLine+" role:writer")
	writeAs(t, s, s.dan, "extra")
	signedByAKeyNobodyListed(t, s)
	text, code := runPravo(t, s.dir, "--identity", "ada", "protect", "status")
	if code != 0 || !strings.Contains(text, "1 not checked") {
		t.Fatalf("status without a pin: exit %d\n%s", code, text)
	}
}

// The viewer is given the roles that decide: the keys with their roles, where the roles come from,
// and a verdict on the records.
func TestTheViewerIsToldTheRolesOfThePinnedChain(t *testing.T) {
	s := vouchedBucket(t, false)
	st, err := state.OpenBackendWith(context.Background(), s.backend(t), state.Options{Identity: s.ada})
	if err != nil {
		t.Fatal(err)
	}
	f := rolesFacts(context.Background(), st, loadReadersFile(s.dir, ""))
	if f == nil || f.Source != "state" || f.Error != "" || f.You != "admin" {
		t.Fatalf("roles facts: %+v", f)
	}
	if len(f.Keys) != 1 || f.Keys[0].Name != "ada" || f.Keys[0].Role != "admin" {
		t.Fatalf("keys: %+v", f.Keys)
	}
	// dan left, and an admin vouched for his transaction: his record is not a problem.
	if r := f.Records; r == nil || r.Skipped != "" || len(r.Problems) != 0 {
		t.Fatalf("records: %+v", r)
	}

	// A chain that the readers file does not pin is reported, and the file decides.
	rf := loadReadersFile(s.dir, "")
	rf.Pin = ""
	if f := rolesFacts(context.Background(), st, rf); f == nil || f.Source != "file" || !f.Unpinned {
		t.Fatalf("unpinned roles facts: %+v", f)
	}
	// A pin that the chain does not start with is an error, and the page says so.
	rf.Pin = strings.Repeat("0", 64)
	if f := rolesFacts(context.Background(), st, rf); f == nil || f.Error == "" {
		t.Fatalf("a wrong pin: %+v", f)
	}
}

// unsign removes the signatures of the transaction that wrote addr, as if no identity had run it.
func unsign(t *testing.T, s *sealed, addr string) {
	t.Helper()
	n := 0
	for k, v := range s.f.Objects {
		if !strings.HasPrefix(k, "prod/txn/") {
			continue
		}
		var m map[string]any
		if json.Unmarshal(v, &m) != nil {
			continue
		}
		for _, c := range m["changes"].([]any) {
			if c, ok := c.(map[string]any); ok && c["addr"] == addr {
				delete(m, "start_sig")
				delete(m, "finish_sig")
				data, _ := json.Marshal(m)
				s.f.Objects[k] = data
				n++
			}
		}
	}
	if n == 0 {
		t.Fatalf("no transaction wrote %s", addr)
	}
}

func TestUnsignedHistoryIsAdoptedWhenTheRolesAreFirstStored(t *testing.T) {
	s := plannableBucket(t)
	writeAs(t, s, s.ada, "legacy")
	unsign(t, s, "legacy")
	s.readers(t, s.adaLine+" role:admin", s.danLine+" role:reader")
	text, code := runPravo(t, s.dir, "--identity", "ada", "protect", "roles", "--yes")
	if code != 0 || !strings.Contains(text, "unsigned: vouched for 1 transaction") {
		t.Fatalf("protect roles over unsigned history: exit %d\n%s", code, text)
	}
	text, code = runPravo(t, s.dir, "--identity", "ada", "protect", "status")
	if code != 0 || !strings.Contains(text, "1 vouched for by an admin") {
		t.Fatalf("status after adopting: exit %d\n%s", code, text)
	}
}

func TestUnsignedHistoryOfALaterVersionNeedsTheFlag(t *testing.T) {
	s := plannableBucket(t)
	pin := pinnedRoles(t, s)
	s.readers(t, s.adaLine+" role:admin", s.danLine+" role:reader", pin)
	writeAs(t, s, s.ada, "legacy")
	unsign(t, s, "legacy")
	text, code := runPravo(t, s.dir, "--identity", "ada", "protect", "status")
	if code != 7 || !strings.Contains(text, "Untrusted: legacy:") {
		t.Fatalf("status over unsigned history: exit %d\n%s", code, text)
	}
	text, code = runPravo(t, s.dir, "--identity", "ada", "protect", "roles", "--yes")
	if !strings.Contains(text, "--adopt-unsigned") {
		t.Fatalf("protect roles without the flag: exit %d\n%s", code, text)
	}
	if _, code := runPravo(t, s.dir, "--identity", "ada", "protect", "status"); code != 7 {
		t.Fatalf("still untrusted without the flag: exit %d", code)
	}
	text, code = runPravo(t, s.dir, "--identity", "ada", "protect", "roles", "--yes", "--adopt-unsigned")
	if code != 0 || !strings.Contains(text, "unsigned: vouched for 1 transaction") {
		t.Fatalf("protect roles --adopt-unsigned: exit %d\n%s", code, text)
	}
	if text, code := runPravo(t, s.dir, "--identity", "ada", "protect", "status"); code != 0 || !strings.Contains(text, "1 vouched for by an admin") {
		t.Fatalf("status after adopting: exit %d\n%s", code, text)
	}
	// A transaction rewritten after it was vouched for is not the one that was.
	for k, v := range s.f.Objects {
		if strings.HasPrefix(k, "prod/txn/") && strings.Contains(string(v), `"legacy"`) {
			s.f.Objects[k] = []byte(strings.Replace(string(v), `"record":"`, `"record":"x`, 1))
		}
	}
	if _, code := runPravo(t, s.dir, "--identity", "ada", "protect", "status"); code != 7 {
		t.Fatalf("a rewritten unsigned transaction: exit %d", code)
	}
}
