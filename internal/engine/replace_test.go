package engine

import (
	"path/filepath"
	"strings"
	"testing"

	"cuelang.org/go/cue"

	"github.com/pravo-systems/pravo/internal/config"
	"github.com/pravo-systems/pravo/internal/state"
)

func TestCheckReplace(t *testing.T) {
	st, err := state.Open(filepath.Join(t.TempDir(), "st"))
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Set(&state.Resource{Addr: "a", Mode: "managed", Type: "random_pet", Name: "a", Attributes: []byte(`{}`)}, ""); err != nil {
		t.Fatal(err)
	}
	res := func(mode, typ, addr string) *config.Resource {
		return &config.Resource{Mode: mode, Type: typ, Name: addr, Path: cue.ParsePath(addr)}
	}
	root := &config.Root{Resources: []*config.Resource{
		res("managed", "random_pet", "a"),
		res("managed", "random_pet", "mod.new"),
		res("data", "local_file", "d"),
	}}
	cases := []struct {
		replace []string
		destroy bool
		want    string // substring of the error, empty for none
	}{
		{nil, false, ""},
		{[]string{"a"}, false, ""},
		{[]string{"nope"}, false, "declares no such resource"},
		{[]string{"d"}, false, "only managed resources"},
		{[]string{"mod.new"}, false, "not in the state"},
		{[]string{"a"}, true, "-destroy"},
	}
	for _, c := range cases {
		e := &Engine{Root: root, State: st, Opts: Options{Replace: c.replace, Destroy: c.destroy}}
		err := e.checkReplace()
		switch {
		case c.want == "" && err != nil:
			t.Errorf("%v: unexpected error %v", c.replace, err)
		case c.want != "" && (err == nil || !strings.Contains(err.Error(), c.want)):
			t.Errorf("%v: error %v, want %q", c.replace, err, c.want)
		}
	}
}

func TestReplaceRequestedFromSavedPlan(t *testing.T) {
	e := &Engine{saved: map[string]*Change{
		"random_pet.a": {Forced: true},
		"random_pet.b": {Forced: true, Applied: true},
		"random_pet.c": {},
	}}
	for addr, want := range map[string]bool{"random_pet.a": true, "random_pet.b": false, "random_pet.c": false, "random_pet.z": false} {
		if got := e.replaceRequested(addr); got != want {
			t.Errorf("%s: replaceRequested = %v, want %v", addr, got, want)
		}
	}
	e.Opts.Replace = []string{"random_pet.c"}
	if !e.replaceRequested("random_pet.c") {
		t.Error("a -replace option must be honoured")
	}
}

func TestRenameHints(t *testing.T) {
	st, err := state.Open(filepath.Join(t.TempDir(), "st"))
	if err != nil {
		t.Fatal(err)
	}
	for addr, hash := range map[string]string{"old.pet": "h1", "old.file": "h2", "other": ""} {
		typ := "random_pet"
		if addr == "old.file" {
			typ = "local_file"
		}
		if err := st.Set(&state.Resource{Addr: addr, Mode: "managed", Type: typ, ConfigHash: hash, Attributes: []byte(`{}`)}, ""); err != nil {
			t.Fatal(err)
		}
	}
	e := &Engine{State: st}
	changes := []*Change{
		{Addr: "new.pet", Mode: "managed", Type: "random_pet", Action: ActionCreate, ConfigHash: "h1"},
		{Addr: "new.file", Mode: "managed", Type: "local_file", Action: ActionCreate, ConfigHash: "different"},
		{Addr: "unknown", Mode: "managed", Type: "random_pet", Action: ActionCreate}, // hash unknown: no hint
		{Addr: "old.pet", Mode: "managed", Type: "random_pet", Action: ActionDelete},
		{Addr: "old.file", Mode: "managed", Type: "local_file", Action: ActionDelete},
	}
	hints := e.renameHints(changes)
	if len(hints) != 1 || !strings.Contains(hints[0], `$from: "old.pet" to new.pet`) {
		t.Errorf("hints = %q, want exactly one for new.pet", hints)
	}
}
