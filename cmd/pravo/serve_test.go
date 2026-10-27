package main

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pravo-systems/pravo/internal/engine"
	"github.com/pravo-systems/pravo/internal/env"
	"github.com/pravo-systems/pravo/internal/registry"
	"github.com/pravo-systems/pravo/internal/serve"
	"github.com/pravo-systems/pravo/internal/state"
)

// serveInfo is what "pravo serve --json" prints.
type serveInfo struct {
	URL   string `json:"url"`
	Token string `json:"token"`
	Port  int    `json:"port"`
}

// runServe starts "pravo serve" in this process and returns what it printed and
// a function that ends it the way a signal does: the context of the command is
// cancelled.
func runServe(t *testing.T, dir string, args ...string) (serveInfo, func() error) {
	t.Helper()
	wd, _ := os.Getwd()
	t.Cleanup(func() { os.Chdir(wd) }) //nolint:usetesting // the directory is put back after each call, so that a relative path in the next call means what it meant in the first
	r, w, _ := os.Pipe()
	stdout := os.Stdout
	os.Stdout = w
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		root := newRootCmd()
		root.SetArgs(normaliseArgs(append([]string{"pravo", "-C", dir, "serve", "--no-open", "--json"}, args...))[1:])
		done <- root.ExecuteContext(ctx)
	}()
	lines := make(chan string, 1)
	go func() {
		line, _ := bufio.NewReader(r).ReadString('\n')
		lines <- line
	}()
	var info serveInfo
	select {
	case line := <-lines:
		if err := json.Unmarshal([]byte(line), &info); err != nil {
			os.Stdout = stdout
			cancel()
			t.Fatalf("pravo serve printed %q: %v", line, err)
		}
	case err := <-done:
		os.Stdout = stdout
		t.Fatalf("pravo serve returned before it printed the address: %v", err)
	case <-time.After(60 * time.Second):
		os.Stdout = stdout
		cancel()
		t.Fatal("pravo serve printed nothing")
	}
	stopped := false
	stop := func() error {
		if stopped {
			return nil
		}
		stopped = true
		os.Stdout = stdout
		cancel()
		select {
		case err := <-done:
			w.Close()
			io.Copy(io.Discard, r)
			return err
		case <-time.After(15 * time.Second):
			t.Error("pravo serve did not end when its context ended")
			return nil
		}
	}
	t.Cleanup(func() { stop() })
	return info, stop
}

func fetch(t *testing.T, info serveInfo, path string) []byte {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, "http://127.0.0.1:"+itoa(info.Port)+path, nil)
	req.Header.Set("Authorization", "Bearer "+info.Token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET %s: %d %s", path, resp.StatusCode, body)
	}
	return body
}

func itoa(n int) string { b, _ := json.Marshal(n); return string(b) }

func copyTree(t *testing.T, from, to string) {
	t.Helper()
	err := filepath.Walk(from, func(p string, fi os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(from, p)
		if fi.IsDir() {
			return os.MkdirAll(filepath.Join(to, rel), 0o755)
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(to, rel), data, 0o644)
	})
	if err != nil {
		t.Fatal(err)
	}
}

// providersInstalled reports whether the providers of the example are at hand,
// so that the test can run without a network.
func providersInstalled(t *testing.T) bool {
	t.Helper()
	c := registry.NewClient(env.OS())
	for name, ver := range map[string]string{"local": "2.9.1", "null": "3.3.2", "random": "3.9.1"} {
		src, _ := registry.ParseSource("hashicorp/" + name)
		if _, ok := c.FindInstalled(src, ver); !ok {
			return false
		}
		if !schemaCached(c, src, ver) {
			return false
		}
	}
	return true
}

// schemaCached reports whether the schema of a provider version is in the cache,
// as a pack or, where an earlier pravo wrote it, as JSON.
func schemaCached(c *registry.Client, src registry.Source, ver string) bool {
	for _, path := range []string{c.SchemaPackPath(src, ver), c.SchemaCachePath(src, ver)} {
		if _, err := os.Stat(path); err == nil {
			return true
		}
	}
	return false
}

// secretsOf reads the values of the sensitive attributes that the example's
// state holds: the generated passwords and their hashes.
func secretsOf(t *testing.T, dir string) []string {
	t.Helper()
	var out []string
	for _, addr := range []string{"services.api.secret", "services.web.secret"} {
		data, err := os.ReadFile(filepath.Join(dir, "pravo.state", "resources", addr+".json"))
		if err != nil {
			t.Fatal(err)
		}
		var rec struct {
			Attributes map[string]any `json:"attributes"`
		}
		if err := json.Unmarshal(data, &rec); err != nil {
			t.Fatal(err)
		}
		for _, k := range []string{"result", "bcrypt_hash"} {
			if s, _ := rec.Attributes[k].(string); len(s) > 8 {
				out = append(out, s)
			}
		}
	}
	if len(out) != 4 {
		t.Fatalf("found %d generated secrets", len(out))
	}
	return out
}

// The viewer over a real root: the example with modules is initialised and
// applied, a value is edited and a plan saved; every endpoint is read, and the
// passwords that the providers generated are nowhere in what is sent.
func TestServeARealRoot(t *testing.T) {
	if testing.Short() {
		t.Skip("applies the example with real providers")
	}
	if !providersInstalled(t) {
		t.Skip("the local, null and random providers are not installed or their schemas are not cached")
	}
	dir := t.TempDir()
	copyTree(t, filepath.Join("..", "..", "examples", "modules-and-data"), dir)
	if text, code := runPravo(t, dir, "init"); code != 0 {
		t.Skipf("pravo init exited %d: %s", code, text)
	}
	if text, code := runPravo(t, dir, "apply", "-auto-approve"); code != 0 {
		t.Skipf("pravo apply exited %d: %s", code, text)
	}
	secrets := secretsOf(t, dir)
	main, _ := os.ReadFile(filepath.Join(dir, "main.cue"))
	edited := strings.Replace(string(main), `port: 8080`, `port: 9090`, 1)
	edited = strings.Replace(edited, `length:  24`, `length:  32`, 1)
	if edited == string(main) {
		t.Fatal("the example changed: nothing to edit")
	}
	os.WriteFile(filepath.Join(dir, "main.cue"), []byte(edited), 0o644)
	if text, code := runPravo(t, dir, "plan", "-out", "plan.pravo"); code != 0 {
		t.Fatalf("pravo plan exited %d: %s", code, text)
	}
	before := digestDir(t, dir)

	info, stop := runServe(t, dir, "--plan", "plan.pravo")
	if !strings.HasPrefix(info.URL, "http://127.0.0.1:") || !strings.Contains(info.URL, "?token="+info.Token) || info.Port == 0 {
		t.Errorf("printed %+v", info)
	}
	var g serve.Graph
	raw := fetch(t, info, "/api/graph")
	if err := json.Unmarshal(raw, &g); err != nil {
		t.Fatal(err)
	}
	// the three outputs of the example are nodes too, and are not resources
	resources, outputs := 0, 0
	for _, n := range g.Nodes {
		if n.Mode == "output" {
			outputs++
		} else {
			resources++
		}
	}
	if g.Project.SpecError != "" || resources != 9 || outputs != 3 || g.Project.State.Records != 9 || g.Plan == nil || g.Plan.Total != 9 {
		t.Fatalf("graph: spec error %q, %d resources, %d outputs, %d records, plan %v", g.Project.SpecError, resources, outputs, g.Project.State.Records, g.Plan)
	}
	if g.Plan.Summary.Add != 5 || g.Plan.Summary.Destroy != 5 || g.Plan.Stale {
		t.Errorf("plan %+v", g.Plan)
	}
	nss := map[string]serve.Namespace{}
	for _, m := range g.Namespaces {
		nss[m.Path] = m
	}
	if m := nss["services.api"]; m.Resources != 2 || m.Definition == nil || m.Declared == nil || m.Parent != "services" || m.Depth != 1 {
		t.Errorf("namespace services.api: %+v", m)
	}
	var all [][]byte
	all = append(all, raw)
	for _, n := range g.Nodes {
		all = append(all, fetch(t, info, "/api/detail?addr="+n.Addr), fetch(t, info, "/api/source?addr="+n.Addr))
	}
	all = append(all, fetch(t, info, "/api/search?q=secret"))
	for i, body := range all {
		for _, s := range secrets {
			if strings.Contains(string(body), s) {
				t.Errorf("response %d sends a generated secret", i)
			}
		}
	}
	var d serve.Detail
	json.Unmarshal(fetch(t, info, "/api/detail?addr=services.api.secret"), &d)
	if d.Change == nil || d.Change.Action != "replace" || len(d.Change.RequiresReplace) != 1 {
		t.Errorf("change of the password: %+v", d.Change)
	}
	if err := stop(); err != nil {
		t.Errorf("pravo serve returned %v after its context ended", err)
	}
	if after := digestDir(t, dir); after != before {
		t.Errorf("pravo serve changed the project directory")
	}
}

// digestDir lists every file below dir with its size and time, which says
// whether anything was written, made or removed.
func digestDir(t *testing.T, dir string) string {
	t.Helper()
	var b strings.Builder
	filepath.Walk(dir, func(p string, fi os.FileInfo, err error) error {
		if err == nil {
			b.WriteString(p + "|" + fi.ModTime().String() + "|" + itoa(int(fi.Size())) + "\n")
		}
		return nil
	})
	return b.String()
}

func TestServeFlags(t *testing.T) {
	dir := specDir(t, map[string]string{"main.cue": "package estate\n\nsize: 3\n"})
	if text, code := runPravo(t, dir, "serve", "--port", "70000", "--no-open"); code != 1 || !strings.Contains(text, "not a port number") {
		t.Errorf("a port out of range: exit %d %s", code, text)
	}
	if text, code := runPravo(t, dir, "serve", "--plan", filepath.Join(dir, "missing.pravo"), "--no-open"); code != 1 || !strings.Contains(text, "missing.pravo") {
		t.Errorf("a plan that is not there: exit %d %s", code, text)
	}
	if text, code := runPravo(t, dir, "serve", "stray"); code != 1 {
		t.Errorf("a stray argument: exit %d %s", code, text)
	}
	// The default is a free port; a port that is taken is an error, not a hang.
	info, _ := runServe(t, dir)
	if info.Port == 0 {
		t.Fatal("no port")
	}
	fetch(t, info, "/api/ping")
	if text, code := runPravo(t, dir, "serve", "--port", itoa(info.Port), "--no-open"); code != 1 {
		t.Errorf("a port in use: exit %d %s", code, text)
	}
	// Nothing was made in a directory with a specification and no state.
	if _, err := os.Stat(filepath.Join(dir, "pravo.state")); err == nil {
		t.Errorf("pravo serve made a state")
	}
}

// storeTestPlan puts a plan in the state of dir, the way "pravo plan --store"
// does, with one change that no provider of the root can decode: the viewer then
// shows its actions and counts and warns that it cannot show the values.
func storeTestPlan(t *testing.T, dir, author string, created time.Time) string {
	t.Helper()
	st, err := state.Open(filepath.Join(dir, "pravo.state"))
	if err != nil {
		t.Fatal(err)
	}
	doc, _ := json.Marshal(map[string]any{
		"format": 1, "created": created.UTC(), "lineage": st.Lineage(), "config_fingerprint": "x", "actor": author,
		"providers": map[string]string{},
		"changes":   []map[string]any{{"addr": "size", "mode": "managed", "type": "null_resource", "name": "size", "action": "create", "provider_key": "null", "prior_revision": 0}},
	})
	meta := &engine.PlanMeta{Format: engine.PlanMetaFormat, Created: created.UTC(), Expires: created.Add(engine.DefaultPlanTTL).UTC(), Author: author, Lineage: st.Lineage(),
		Summary: engine.PlanCounts{Add: 1}, Bases: map[string]engine.PlanBase{"size": {}}, Changes: []engine.PlanTouch{{Addr: "size", Mode: "managed", Type: "null_resource", Action: engine.ActionCreate}}}
	if err := engine.NewPlanStore(st.Backend()).Put(context.Background(), doc, meta); err != nil {
		t.Fatal(err)
	}
	return meta.ID
}

// pravo serve --plan ID: a plan stored in the state is the plan of the page, a
// file of that name wins, an id that nothing stores is an error, and the page
// lists the stored plans whether or not one was named.
func TestServeAStoredPlan(t *testing.T) {
	dir := specDir(t, map[string]string{"main.cue": "package estate\n\nsize: 3\n"})
	ana := storeTestPlan(t, dir, "ana@acme", time.Now().Add(-time.Hour))
	ben := storeTestPlan(t, dir, "ben@acme", time.Now())

	info, stop := runServe(t, dir, "--plan", ana[:len("plan-")+6])
	var g serve.Graph
	if err := json.Unmarshal(fetch(t, info, "/api/graph"), &g); err != nil {
		t.Fatal(err)
	}
	if g.Plan == nil || g.Plan.ID != ana || g.Plan.File != ana || g.Plan.Actor != "ana@acme" || g.Plan.Status != "pending" || g.Plan.Summary.Add != 1 {
		t.Fatalf("plan %+v", g.Plan)
	}
	var l serve.PlanList
	if err := json.Unmarshal(fetch(t, info, "/api/plans"), &l); err != nil {
		t.Fatal(err)
	}
	if len(l.Plans) != 2 || l.Plans[0].ID != ben || l.Plans[1].ID != ana || l.Plans[1].Apply != "pravo apply "+ana || l.Revision != g.Revision {
		t.Errorf("plans %+v", l)
	}
	var o serve.PlanOverlay
	if err := json.Unmarshal(fetch(t, info, "/api/plan?id="+ben), &o); err != nil || o.Plan.ID != ben || o.Plan.Actor != "ben@acme" {
		t.Errorf("overlay %+v %v", o.Plan, err)
	}
	if err := stop(); err != nil {
		t.Errorf("pravo serve returned %v", err)
	}

	// Without --plan the page lists the stored plans and lays none over the graph.
	info, _ = runServe(t, dir)
	g = serve.Graph{}
	json.Unmarshal(fetch(t, info, "/api/graph"), &g)
	l = serve.PlanList{}
	json.Unmarshal(fetch(t, info, "/api/plans"), &l)
	if g.Plan != nil || len(l.Plans) != 2 {
		t.Errorf("without --plan: plan %+v, %d stored plans", g.Plan, len(l.Plans))
	}

	if text, code := runPravo(t, dir, "serve", "--plan", "plan-ffffff", "--no-open"); code != 1 || !strings.Contains(text, "no such stored plan") {
		t.Errorf("an id that nothing stores: exit %d %s", code, text)
	}
	// A file with the form of an id is a plan file, as it is for pravo apply.
	if err := os.WriteFile(filepath.Join(dir, ana), []byte("not a plan"), 0o600); err != nil {
		t.Fatal(err)
	}
	if text, code := runPravo(t, dir, "serve", "--plan", ana, "--no-open"); code != 1 || !strings.Contains(text, "not a pravo plan file") {
		t.Errorf("a file that has the name of an id: exit %d %s", code, text)
	}
}
