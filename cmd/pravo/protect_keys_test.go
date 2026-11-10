package main

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/pravo-systems/pravo/internal/protect"
)

// sealedMeta is the sealed part of the meta.json in the bucket, and the lineage.
func (s *sealed) sealedMeta(t *testing.T) (protect.Meta, string) {
	t.Helper()
	var m struct {
		Lineage string       `json:"lineage"`
		Sealed  protect.Meta `json:"sealed"`
	}
	if err := json.Unmarshal(s.f.Objects["prod/meta.json"], &m); err != nil {
		t.Fatal(err)
	}
	return m.Sealed, m.Lineage
}

// editSealed changes the sealed part of meta.json as someone who can write the bucket
// and holds no key of a reader.
func (s *sealed) editSealed(t *testing.T, change func(m *protect.Meta, lineage string)) {
	t.Helper()
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(s.f.Objects["prod/meta.json"], &doc); err != nil {
		t.Fatal(err)
	}
	m, lineage := s.sealedMeta(t)
	change(&m, lineage)
	doc["sealed"], _ = json.Marshal(m)
	out, _ := json.MarshalIndent(doc, "", "  ")
	s.f.Set("prod/meta.json", out)
}

// keys says what ada and dan make of the list of key groups in the bucket.
func (s *sealed) keys(t *testing.T) string {
	t.Helper()
	m, lineage := s.sealedMeta(t)
	st, name := m.KeysStatus([]protect.Reader{s.ada.AsReader(), s.dan.AsReader()}, lineage)
	return m.Current + " " + st.Key() + " " + name
}

// plant adds a group whose key the caller made, locked for ada and dan, as the current one.
func (s *sealed) plant(t *testing.T) {
	t.Helper()
	s.editSealed(t, func(m *protect.Meta, lineage string) {
		g, _, err := protect.NewGroup("g2", lineage, []protect.Reader{s.ada.AsReader(), s.dan.AsReader()})
		if err != nil {
			t.Fatal(err)
		}
		m.Groups, m.Current = append(m.Groups, g), "g2"
	})
}

func TestTheKeysOfASealedStateAreSigned(t *testing.T) {
	rt := &runContext{}
	s := sealedBucket(t, rt)
	if got := s.keys(t); got != "g1 verified ada" {
		t.Fatalf("keys after protect init = %s", got)
	}
	text, code := runPravo(t, rt, s.dir, "protect", "status")
	if code != 0 || strings.Contains(text, "Keys ") {
		t.Fatalf("status of a state whose keys a reader signed: exit %d\n%s", code, text)
	}
	text, code = runPravo(t, rt, s.dir, "--json", "protect", "status")
	var doc struct {
		Keys struct{ Signature, By string }
	}
	if err := json.Unmarshal([]byte(text), &doc); err != nil || code != 0 || doc.Keys.Signature != "verified" || doc.Keys.By != "ada" {
		t.Fatalf("status as JSON: exit %d, keys %+v (%v)\n%s", code, doc.Keys, err, text)
	}
}

// A state that an earlier pravo sealed has a list of keys that nobody signed. Reading it
// changes nothing. The first command that writes makes a new data key and signs, and says so.
func TestAnUnsignedListOfKeysGetsANewKeyFromTheFirstWriter(t *testing.T) {
	rt := &runContext{}
	s := sealedBucket(t, rt)
	s.editSealed(t, func(m *protect.Meta, _ string) {
		m.Signed = nil
		for i := range m.Groups {
			m.Groups[i].Commit = nil
		}
	})
	text, code := runPravo(t, rt, s.dir, "protect", "status")
	if code != 0 || !strings.Contains(text, "Keys         the list of keys is not signed by a reader · the next command that writes makes a new data key and signs it\n") {
		t.Fatalf("status: exit %d\n%s", code, text)
	}
	stderr := withStderr(t, func() { text, code = runPravo(t, rt, s.dir, "state", "list") })
	if code != 0 || stderr != "" || s.keys(t) != "g1 unsigned " {
		t.Fatalf("a command that reads: exit %d, standard error %q, keys %s", code, stderr, s.keys(t))
	}
	stderr = withStderr(t, func() { text, code = runPravo(t, rt, s.dir, "state", "rm", "net.vpc") })
	want := "Note: the list of keys in meta.json had no signature: an earlier pravo sealed the state, the state was recovered with its shares, or meta.json was changed. Made a new data key (group g2) and signed the list"
	if code != 0 || !strings.Contains(stderr, want) {
		t.Fatalf("the first command that writes: exit %d\n%s\nstandard error: %s", code, text, stderr)
	}
	if got := s.keys(t); got != "g2 verified ada" {
		t.Fatalf("keys after the first write = %s", got)
	}
	stderr = withStderr(t, func() { text, code = runPravo(t, rt, s.dir, "state", "rm", "net.db") })
	if code != 0 || stderr != "" || s.keys(t) != "g2 verified ada" {
		t.Fatalf("the second command that writes: exit %d, standard error %q, keys %s", code, stderr, s.keys(t))
	}
	if text, code = runPravo(t, rt, s.dir, "protect", "status"); code != 0 || strings.Contains(text, "Keys ") {
		t.Fatalf("status afterwards: exit %d\n%s", code, text)
	}
}

// Someone who can write the bucket plants a group with a key of their own and takes the
// signature off. The next command that writes seals in a group that it made.
func TestAPlantedGroupWithoutASignatureIsReplaced(t *testing.T) {
	rt := &runContext{}
	s := sealedBucket(t, rt)
	s.plant(t)
	s.editSealed(t, func(m *protect.Meta, _ string) { m.Signed = nil })
	var code int
	stderr := withStderr(t, func() { _, code = runPravo(t, rt, s.dir, "state", "rm", "net.vpc") })
	if code != 0 || !strings.Contains(stderr, "Made a new data key (group g3)") {
		t.Fatalf("exit %d, standard error: %s", code, stderr)
	}
	if got := s.keys(t); got != "g3 verified ada" {
		t.Fatalf("keys = %s", got)
	}
}

// The same group planted under the signature of a reader: the signature does not verify.
// A command that writes stops with exit code 7, one that reads warns, status says so, and
// a rewrap makes a new data key and signs.
func TestAPlantedGroupUnderASignatureIsRefused(t *testing.T) {
	rt := &runContext{}
	s := sealedBucket(t, rt)
	s.plant(t)
	text, code := runPravo(t, rt, s.dir, "state", "rm", "net.vpc")
	want := "the list of keys in meta.json carries a signature of ada that does not verify: meta.json was changed after it was signed."
	if code != 7 || !strings.Contains(text, want) {
		t.Fatalf("a command that writes: exit %d\n%s", code, text)
	}
	if _, ok := s.f.Objects["prod/resources/net.vpc.json"]; !ok {
		t.Error("the refused command removed the record")
	}
	stderr := withStderr(t, func() { text, code = runPravo(t, rt, s.dir, "state", "list") })
	if code != 0 || !strings.Contains(stderr, "Warning: "+want) {
		t.Fatalf("a command that reads: exit %d, standard error %q", code, stderr)
	}
	text, code = runPravo(t, rt, s.dir, "protect", "status")
	if code != 7 || !strings.Contains(text, "Keys         the signature of ada on the list of keys does not verify: meta.json was changed (pravo protect rewrap)\n") {
		t.Fatalf("status: exit %d\n%s", code, text)
	}
	text, code = runPravo(t, rt, s.dir, "protect", "rewrap")
	want = "Readers in .pravo/access/readers.txt: ada dan\nThe list of keys in meta.json had no signature of a reader in .pravo/access/readers.txt. It has yours now, over a data key that this command made.\nNew data keys for 1 reader group. Records are sealed again as they are next written.\nmeta.json updated\n"
	if code != 0 || text != want {
		t.Fatalf("rewrap: exit %d\n%s", code, text)
	}
	if got := s.keys(t); got != "g3 verified ada" {
		t.Fatalf("keys after the rewrap = %s", got)
	}
	if text, code = runPravo(t, rt, s.dir, "state", "rm", "net.vpc"); code != 0 {
		t.Fatalf("a command that writes, after the rewrap: exit %d\n%s", code, text)
	}
}
