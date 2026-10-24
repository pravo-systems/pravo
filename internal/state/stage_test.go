package state

import (
	"encoding/json"
	"testing"
)

func TestStagedNodesAreSeenAndNotWritten(t *testing.T) {
	s, dir := openTemp(t)
	a := &Resource{Mode: "managed", Type: "t", Name: "a", Attributes: json.RawMessage(`{"id":"1"}`)}
	d := &Resource{Mode: "managed", Type: "t", Name: "d", Attributes: json.RawMessage(`{}`), Dependencies: []string{"t.a"}}
	for _, r := range []*Resource{a, d} {
		if err := s.Set(r, "txn"); err != nil {
			t.Fatal(err)
		}
	}
	rev := s.Get("t.a").Revision
	if rev == 0 {
		t.Fatal("a written node has a revision")
	}

	// A move: the record is at the new address, and gone from the old one, and what
	// depends on it depends on the new address.
	moved := *s.Get("t.a")
	moved.Name = "b"
	s.StageRemove("t.a")
	s.Stage(&moved)
	dep := *s.Get("t.d")
	dep.Dependencies = []string{"t.b"}
	s.Stage(&dep)
	if s.Get("t.a") != nil || s.Get("t.b") == nil {
		t.Fatalf("a: %v, b: %v", s.Get("t.a"), s.Get("t.b"))
	}
	if b := s.Get("t.b"); b.Revision != 0 || b.Generation != "" {
		t.Errorf("a record that is not written has no revision: %+v", b)
	}
	if d := s.Get("t.d"); d.Revision != rev && d.Revision == 0 {
		t.Errorf("a staged copy of a record keeps its revision: %+v", d)
	}
	if got := s.Addresses(); len(got) != 2 || got[0] != "t.b" || got[1] != "t.d" {
		t.Errorf("addresses %v", got)
	}
	if got := s.Dependents("t.b"); len(got) != 1 || got[0] != "t.d" {
		t.Errorf("dependents %v", got)
	}
	if s.Len() != 2 || !s.Staged("t.a") || !s.Staged("t.b") || s.Staged("zz") {
		t.Errorf("len %d, staged %v %v", s.Len(), s.Staged("t.a"), s.Staged("t.b"))
	}

	// A write to a staged address stays in memory, as does a refresh and a removal.
	upd := *s.Get("t.b")
	upd.Attributes = json.RawMessage(`{"id":"2"}`)
	if err := s.Refresh(&upd, "txn"); err != nil {
		t.Fatal(err)
	}
	if err := s.Set(&upd, "txn"); err != nil {
		t.Fatal(err)
	}
	if string(s.Get("t.b").Attributes) != `{"id":"2"}` || s.Get("t.b").Revision != 0 {
		t.Errorf("%+v", s.Get("t.b"))
	}
	if err := s.Verify("t.b"); err != nil {
		t.Errorf("a staged address has nothing in the backend to differ from: %v", err)
	}

	// The backend holds what it held.
	other, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got := other.Addresses(); len(got) != 2 || got[0] != "t.a" || got[1] != "t.d" {
		t.Errorf("the backend was written: %v", got)
	}
	if other.Get("t.a").Revision != rev || len(other.Get("t.d").Dependencies) != 1 || other.Get("t.d").Dependencies[0] != "t.a" {
		t.Errorf("%+v %+v", other.Get("t.a"), other.Get("t.d"))
	}

	s.DropStaged()
	if s.Get("t.a") == nil || s.Get("t.b") != nil || s.Get("t.d").Dependencies[0] != "t.a" || s.Staged("t.a") {
		t.Errorf("dropped: a %v, b %v", s.Get("t.a"), s.Get("t.b"))
	}
	if got := s.Dependents("t.a"); len(got) != 1 {
		t.Errorf("dependents %v", got)
	}
	// What was staged can be written for real, as the apply does.
	if err := s.Set(&Resource{Mode: "managed", Type: "t", Name: "c", Attributes: json.RawMessage(`{}`)}, "txn"); err != nil {
		t.Fatal(err)
	}
	if s.Get("t.c").Revision == 0 {
		t.Error("a record that is not staged is written")
	}
}

func TestStagedRemoveOfAStagedNode(t *testing.T) {
	s, _ := openTemp(t)
	s.Stage(&Resource{Mode: "managed", Type: "t", Name: "n", Attributes: json.RawMessage(`{}`)})
	if ok, err := s.Remove("t.n"); err != nil || !ok || s.Get("t.n") != nil {
		t.Errorf("%v %v", ok, err)
	}
	if ok, _ := s.Remove("t.n"); ok {
		t.Error("removed twice")
	}
	s.DropStaged()
	if s.Len() != 0 {
		t.Error("something was left")
	}
}
