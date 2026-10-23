package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pravo-systems/pravo/internal/protect"
	"github.com/pravo-systems/pravo/internal/serve"
	"github.com/pravo-systems/pravo/internal/state"
)

// The viewer reads a state that is really sealed: with the identity of a reader the
// values come as they always did, and with no identity, or with one that is not a
// reader, the resources are listed and no value is sent, and nothing is written.
func TestServeReadsASealedState(t *testing.T) {
	const secret = "s3cr3t-value-in-a-sealed-record"
	idDir := t.TempDir()
	t.Setenv("PRAVO_IDENTITY_DIR", idDir)
	t.Setenv("PRAVO_IDENTITY", "")
	t.Setenv("PRAVO_PASSPHRASE", "correct horse battery")
	t.Setenv("PRAVO_IDENTITY_PASSPHRASE", "")
	ids := map[string]*protect.Identity{}
	for _, name := range []string{"ada", "dan", "gus"} {
		id, err := protect.NewIdentity(name, "")
		if err != nil {
			t.Fatal(err)
		}
		defer id.Close()
		ids[name] = id
		var data []byte
		if name == "ada" {
			data, err = id.MarshalSealed([]byte("correct horse battery"))
		} else {
			data, err = id.MarshalUnsealed()
		}
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(idDir, name), data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	dir := specDir(t, map[string]string{"main.cue": "package estate\n\nsize: 3\n"})
	st, err := state.Open(filepath.Join(dir, "pravo.state"))
	if err != nil {
		t.Fatal(err)
	}
	for _, addr := range []string{"a.db", "a.file"} {
		r := &state.Resource{Addr: addr, Mode: "managed", Type: "fake_db", Name: addr[2:], Provider: "example.test/x/fake", ProviderKey: "fake",
			Attributes: json.RawMessage(`{"name":"` + addr + `","password":"` + secret + `","note":"` + secret + `"}`)}
		if addr == "a.file" {
			r.Dependencies = []string{"a.db"}
		}
		if err := st.Set(r, ""); err != nil {
			t.Fatal(err)
		}
	}
	readers := []protect.Reader{ids["ada"].AsReader(), ids["dan"].AsReader()}
	_, err = st.Upgrade(context.Background(), state.UpgradeOptions{To: state.FormatSealed, Readers: readers, Identity: ids["ada"], By: "ada"})
	if errors.Is(err, state.ErrNotImplemented) {
		t.Skip("the state package does not seal yet")
	}
	if err != nil {
		t.Fatal(err)
	}
	viewerReadersFile(t, dir, readers)

	for _, c := range []struct {
		name, identity string
		you            string
		sealed         bool
	}{
		{"a reader", "ada", "reader", false},
		{"no identity", "none", "no-identity", true},
		{"an identity that is not a reader", "gus", "not-reader", true},
	} {
		t.Run(c.name, func(t *testing.T) {
			before := digestDir(t, dir)
			info, stop := runServe(t, dir, "--identity", c.identity)
			var a serve.Access
			if err := json.Unmarshal(fetch(t, info, "/api/access"), &a); err != nil {
				t.Fatal(err)
			}
			if a.State != "sealed" || a.Format != state.FormatSealed || a.You == nil || a.You.State != c.you || a.File.Verdict != "match" || len(a.Readers) != 2 {
				t.Errorf("state %q format %d you %+v verdict %q readers %d", a.State, a.Format, a.You, a.File.Verdict, len(a.Readers))
			}
			var g serve.Graph
			if err := json.Unmarshal(fetch(t, info, "/api/graph"), &g); err != nil {
				t.Fatal(err)
			}
			if len(g.Nodes) != 2 {
				t.Fatalf("%d nodes", len(g.Nodes))
			}
			var d serve.Detail
			if err := json.Unmarshal(fetch(t, info, "/api/detail?addr=a.file"), &d); err != nil {
				t.Fatal(err)
			}
			for _, n := range g.Nodes {
				if n.Sealed != c.sealed {
					t.Errorf("%s: sealed %v, want %v", n.Addr, n.Sealed, c.sealed)
				}
			}
			if c.sealed && (len(d.Attrs) != 0 || len(d.DependsOn) != 1 || d.Node.Revision == 0) {
				t.Errorf("a sealed record: attrs %d depends on %v revision %d", len(d.Attrs), d.DependsOn, d.Node.Revision)
			}
			if !c.sealed && len(d.Attrs) == 0 {
				t.Error("a reader sees no attributes")
			}
			for _, path := range []string{"/api/access", "/api/graph", "/api/detail?addr=a.db", "/api/detail?addr=a.file", "/api/plans", "/api/search?q=a"} {
				if body := fetch(t, info, path); strings.Contains(string(body), secret) {
					t.Errorf("%s sends the secret", path)
				}
			}
			if err := stop(); err != nil {
				t.Error(err)
			}
			if after := digestDir(t, dir); after != before {
				t.Error("the viewer changed the project or the state")
			}
		})
	}
}

// A state of immutable records (format 2) is read as any other, says so in the
// Access view, asks for no identity, and is not written.
func TestServeReadsAnImmutableState(t *testing.T) {
	t.Setenv("PRAVO_IDENTITY_DIR", filepath.Join(t.TempDir(), "none"))
	t.Setenv("PRAVO_IDENTITY", "")
	dir := specDir(t, map[string]string{"main.cue": "package estate\n\nsize: 3\n"})
	st, err := state.Open(filepath.Join(dir, "pravo.state"))
	if err != nil {
		t.Fatal(err)
	}
	r := &state.Resource{Addr: "a.db", Mode: "managed", Type: "fake_db", Name: "db", Provider: "example.test/x/fake", ProviderKey: "fake", Attributes: json.RawMessage(`{"name":"a"}`)}
	if err := st.Set(r, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Upgrade(context.Background(), state.UpgradeOptions{To: state.FormatImmutable, By: "ada"}); err != nil {
		if errors.Is(err, state.ErrNotImplemented) {
			t.Skip("the state package does not keep immutable records yet")
		}
		t.Fatal(err)
	}
	before := digestDir(t, dir)
	info, stop := runServe(t, dir)
	var a serve.Access
	if err := json.Unmarshal(fetch(t, info, "/api/access"), &a); err != nil {
		t.Fatal(err)
	}
	if a.State != "immutable" || a.Format != state.FormatImmutable || a.You != nil || a.Suggestions[0].ID != "seal" {
		t.Errorf("state %q format %d you %+v suggestions %d", a.State, a.Format, a.You, len(a.Suggestions))
	}
	var g serve.Graph
	if err := json.Unmarshal(fetch(t, info, "/api/graph"), &g); err != nil {
		t.Fatal(err)
	}
	if len(g.Nodes) != 1 || g.Nodes[0].Sealed || g.Project.State.Sealed {
		t.Errorf("nodes %+v", g.Nodes)
	}
	if err := stop(); err != nil {
		t.Error(err)
	}
	if after := digestDir(t, dir); after != before {
		t.Error("the viewer changed the project or the state")
	}
}
