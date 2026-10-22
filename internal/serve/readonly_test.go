package serve

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pravo-systems/pravo/internal/state"
)

// session sends every kind of request the page makes, for every node.
func session(t *testing.T, p *project) {
	t.Helper()
	p.get("/api/ping")
	g := p.graph()
	for _, n := range g.Nodes {
		p.get("/api/detail?addr=" + queryEscape(n.Addr))
		p.get("/api/source?addr=" + queryEscape(n.Addr))
	}
	p.get("/api/search?q=db")
	p.do("GET", "/", nil)
	p.do("GET", "/?token="+p.srv.Token(), nil)
	req, _ := http.NewRequest("GET", p.base+"/api/events", nil)
	req.Header.Set("Authorization", "Bearer "+p.srv.Token())
	if resp, err := http.DefaultClient.Do(req); err == nil {
		time.Sleep(300 * time.Millisecond) // a keepalive and a poll or two
		resp.Body.Close()
	}
	p.do("GET", "/api/nothing", nil)
	p.do("POST", "/api/graph", nil)
}

// The viewer writes nothing: not a record, a transaction, a lock, a lock file, a
// generated package or the state directory itself.
func TestReadOnly(t *testing.T) {
	for name, setup := range map[string]func(t *testing.T) (dir string, opts Options){
		"a project with state and a plan": func(t *testing.T) (string, Options) {
			dir := t.TempDir()
			writeSpec(t, dir, specCUE)
			st, _ := state.Open(filepath.Join(dir, "pravo.state"))
			seed(t, st)
			path := filepath.Join(dir, "plan.pravo")
			writePlan(t, &project{t: t, dir: dir, store: st}, path)
			return dir, Options{PlanFile: path}
		},
		"a project without state": func(t *testing.T) (string, Options) {
			dir := t.TempDir()
			writeSpec(t, dir, specCUE)
			os.Remove(filepath.Join(dir, "pravo.lock.cue"))
			return dir, Options{}
		},
		"a specification that does not load": func(t *testing.T) (string, Options) {
			dir := t.TempDir()
			writeSpec(t, dir, "package demo\n\nbroken: {\n")
			st, _ := state.Open(filepath.Join(dir, "pravo.state"))
			seed(t, st)
			return dir, Options{}
		},
		"state with a stale lock": func(t *testing.T) (string, Options) {
			dir := t.TempDir()
			writeSpec(t, dir, specCUE)
			st, _ := state.Open(filepath.Join(dir, "pravo.state"))
			seed(t, st)
			if err := st.Lock("shared", "pravo apply", "txn-1"); err != nil {
				t.Fatal(err)
			}
			return dir, Options{}
		},
	} {
		t.Run(name, func(t *testing.T) {
			dir, opts := setup(t)
			before := digestTree(t, dir)
			entries, _ := os.ReadDir(dir)
			p := start(t, dir, nil, opts)
			session(t, p)
			p.stop()
			select {
			case <-p.done:
				p.done <- nil
			case <-time.After(10 * time.Second):
				t.Fatal("Serve did not return")
			}
			if after := digestTree(t, dir); after != before {
				t.Errorf("the project directory changed during a session")
			}
			after, _ := os.ReadDir(dir)
			if len(after) != len(entries) {
				var names []string
				for _, e := range after {
					names = append(names, e.Name())
				}
				t.Errorf("entries before: %d, after: %v", len(entries), names)
			}
			for _, name := range []string{"pravo.lock.cue.tmp", "cue.mod/gen"} {
				if _, err := os.Stat(filepath.Join(dir, name)); err == nil {
					t.Errorf("%s was made", name)
				}
			}
		})
	}
}

// A state that does not exist is not made by looking at it.
func TestOpenReadOnlyMakesNothing(t *testing.T) {
	dir := t.TempDir()
	root := &configRoot{dir: dir}
	st, err := OpenReadOnly(context.Background(), root.cfg())
	if err != nil {
		t.Fatal(err)
	}
	if st.Len() != 0 || len(st.Outputs()) != 0 {
		t.Errorf("an empty state has %d records", st.Len())
	}
	if es, _ := os.ReadDir(dir); len(es) != 0 {
		t.Errorf("OpenReadOnly made %d entries", len(es))
	}
	if err := st.Set(record("x.y", "t", `{}`), ""); err == nil || !strings.Contains(err.Error(), "open for reading") {
		t.Errorf("a write to a read-only state: %v", err)
	}
	if es, _ := os.ReadDir(dir); len(es) != 0 {
		t.Errorf("a refused write left %d entries", len(es))
	}
}
