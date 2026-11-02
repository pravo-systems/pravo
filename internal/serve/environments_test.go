package serve

import (
	"context"
	"encoding/json"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/pravo-systems/pravo/internal/config"
	"github.com/pravo-systems/pravo/internal/state"
)

// envSpecCUE is the standard specification with two environments, each with a state of its own.
const envSpecCUE = specCUE + `
$environments: {
	dev: {}
	prod: $after: "dev"
}
$settings: state: path: "state/\($environment.$name)"
`

// envProject is a project whose specification declares dev and prod: dev holds the seeded
// records and prod one record of its own. The ladder the command would read is a stub that notes
// the environment of each call.
func envProject(t *testing.T) (*project, *[]string) {
	t.Helper()
	dir := t.TempDir()
	writeSpec(t, dir, envSpecCUE)
	dev, err := state.Open(filepath.Join(dir, "state", "dev"))
	if err != nil {
		t.Fatal(err)
	}
	seed(t, dev)
	prod, err := state.Open(filepath.Join(dir, "state", "prod"))
	if err != nil {
		t.Fatal(err)
	}
	txn := prod.Begin("apply", "pravo apply")
	r := record("shared", "fake_db", `{"name":"shared","port":5432}`)
	if err := prod.Set(r, txn.ID()); err != nil {
		t.Fatal(err)
	}
	txn.Record(r.Address(), "create", 0, r.Revision)
	if err := txn.Finish("applied", "1 created", true); err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var calls []string
	stub := func(_ context.Context, root *config.Root) (*Environments, error) {
		mu.Lock()
		calls = append(calls, root.Environment)
		mu.Unlock()
		return &Environments{
			Environments: []EnvironmentEntry{
				{Name: "dev", After: []string{}, Release: "runs"},
				{Name: "prod", After: []string{"dev"}, Depth: 1, Release: "ready", Problem: "the token is " + secretBlob, Command: "pravo promote prod -t blob=" + secretBlob},
			},
			Next:    "pravo promote prod -t blob=" + secretBlob,
			Waiting: 1,
		}, nil
	}
	return start(t, dir, dev, Options{Environments: stub}), &calls
}

func (p *project) environments() Environments {
	p.t.Helper()
	var e Environments
	if err := json.Unmarshal(p.get("/api/environments"), &e); err != nil {
		p.t.Fatal(err)
	}
	return e
}

// The page reads one environment at a time: the default, else the first of the ladder. A POST
// switches it to another that the specification declares, which loads the specification for it
// and opens its state; nothing else changes it.
func TestTheViewerReadsOneEnvironmentAtATime(t *testing.T) {
	p, calls := envProject(t)
	g := p.graph()
	if g.Project.Environment != "dev" || strings.Join(g.Project.Environments, ",") != "dev,prod" {
		t.Fatalf("project: environment %q of %v, want dev of [dev prod]", g.Project.Environment, g.Project.Environments)
	}
	if !strings.HasSuffix(filepath.ToSlash(g.Project.State.Location), "state/dev") || g.Project.State.Records != 5 {
		t.Fatalf("state: %s with %d records, want the 5 of state/dev", g.Project.State.Location, g.Project.State.Records)
	}

	e := p.environments()
	if e.Selected != "dev" || e.Ladder != "dev → prod" || len(e.Environments) != 2 || e.Waiting != 1 || !strings.HasPrefix(e.Next, "pravo promote prod -t blob=") {
		t.Fatalf("environments: %+v", e)
	}
	// A problem is text from a state, and a command carries the tags: both are masked as any other text.
	noSecrets(t, "/api/environments", p.get("/api/environments"), secretBlob)

	for _, c := range []struct {
		method, name string
		code         int
	}{
		{"GET", "prod", http.StatusMethodNotAllowed},
		{"POST", "staging", http.StatusBadRequest},
		{"POST", "", http.StatusBadRequest},
	} {
		resp, body := p.do(c.method, "/api/environment?name="+queryEscape(c.name), nil)
		if resp.StatusCode != c.code {
			t.Fatalf("%s %q: %d %s, want %d", c.method, c.name, resp.StatusCode, body, c.code)
		}
	}
	if g := p.graph(); g.Project.Environment != "dev" {
		t.Fatalf("a refused switch changed the environment to %q", g.Project.Environment)
	}

	resp, body := p.do("POST", "/api/environment?name=prod", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST prod: %d %s", resp.StatusCode, body)
	}
	waitFor(t, "the page to read prod", func() bool { return p.graph().Project.Environment == "prod" })
	g = p.graph()
	if !strings.HasSuffix(filepath.ToSlash(g.Project.State.Location), "state/prod") || g.Project.State.Records != 1 {
		t.Fatalf("state: %s with %d records, want the 1 of state/prod", g.Project.State.Location, g.Project.State.Records)
	}
	if e := p.environments(); e.Selected != "prod" {
		t.Fatalf("environments: selected %q, want prod", e.Selected)
	}
	if !contains(*calls, "prod") {
		t.Fatalf("the ladder was read for %v, never for prod", *calls)
	}
}

// A specification without environments has no ladder and nothing to switch to.
func TestAViewerWithoutEnvironmentsHasNoLadder(t *testing.T) {
	p := newProject(t)
	if g := p.graph(); g.Project.Environment != "" || len(g.Project.Environments) != 0 {
		t.Fatalf("project: environment %q of %v, want none", g.Project.Environment, g.Project.Environments)
	}
	e := p.environments()
	if e.Selected != "" || len(e.Environments) != 0 || e.Error != "" {
		t.Fatalf("environments: %+v", e)
	}
	if resp, body := p.do("POST", "/api/environment?name=dev", nil); resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("POST dev: %d %s, want 400", resp.StatusCode, body)
	}
}
