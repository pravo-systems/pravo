package serve

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/pravo-systems/pravo/internal/config"
	"github.com/pravo-systems/pravo/internal/registry"
	"github.com/pravo-systems/pravo/internal/schema"
	"github.com/pravo-systems/pravo/internal/state"
)

// The strings below are values a viewer must never send. Each is held by a
// record in a way that the redaction has to catch.
const (
	secretBlob    = "literal-blob-value-1234"  // sensitive by the schema
	secretToken   = "tok-abcdef-123456"        // sensitive by the name of the attribute
	secretMapKey  = "pw-from-map-key-77"       // a map entry whose key looks like a secret
	secretNested  = "nested-secret-key-1"      // a sensitive attribute of a nested attribute type
	secretPin     = "pin-secret-4567"          // a sensitive attribute of a nested block
	secretUnknown = "unknown-attr-value-9"     // an attribute the schema does not know
	secretWrite   = "write-only-payload-55"    // a write-only attribute
	privateBlob   = "PRIVATE-BLOB-bytes-of-pr" // the private blob of a record
	configHash    = "cfg-hash-0123456789abcd"
)

var allSecrets = []string{secretBlob, secretToken, secretMapKey, secretNested, secretPin, secretUnknown, secretWrite, privateBlob, configHash}

const specCUE = `package demo

$fake: {
	$kind:   "provider"
	$source: "example.test/x/fake"
	#Resource: {
		fake_db: {
			$kind:   "resource"
			$type:   "fake_db"
			$source: "example.test/x/fake"
			name:    string
			...
		}
		fake_file: {
			$kind:   "resource"
			$type:   "fake_file"
			$source: "example.test/x/fake"
			path:    string
			content: string
			...
		}
	}
}

// A reusable unit.
#Service: {
	name: string
	db: $fake.#Resource.fake_db & {
		"name":  name
		blob:    "` + secretBlob + `"
		port:    5432
	}
	file: $fake.#Resource.fake_file & {
		path:    "/tmp/\(name)"
		content: "id=\(db.name)/\(db.blob)"
	}
}

services: {
	a: #Service & {name: "a"}
	b: #Service & {name: "b"}
}

shared: $fake.#Resource.fake_db & {name: "shared", port: services.a.db.port}

hosts: {
	for n in ["x", "y"] {
		"\(n)": $fake.#Resource.fake_db & {name: n}
	}
}

host: {$kind: "output", value: "db.example"}
leak: {$kind: "output", value: "token=\(services.a.db.blob)"}
`

const lockCUE = `providers: "example.test/x/fake": {
	version: "1.0.0"
}
`

// fakeSchemas is the schema of the fake provider.
func fakeSchemas() Schemas {
	str := json.RawMessage(`"string"`)
	num := json.RawMessage(`"number"`)
	block := func(attrs ...*schema.Attribute) *schema.Schema {
		return &schema.Schema{Version: 1, Block: &schema.Block{Attributes: attrs}}
	}
	db := block(
		&schema.Attribute{Name: "name", TypeJSON: str, Required: true},
		&schema.Attribute{Name: "blob", TypeJSON: str, Optional: true, Sensitive: true},
		&schema.Attribute{Name: "api_token", TypeJSON: str, Optional: true},
		&schema.Attribute{Name: "port", TypeJSON: num, Optional: true, Computed: true},
		&schema.Attribute{Name: "payload", TypeJSON: str, Optional: true, WriteOnly: true},
		&schema.Attribute{Name: "tags", TypeJSON: json.RawMessage(`["map","string"]`), Optional: true},
		&schema.Attribute{Name: "zones", TypeJSON: json.RawMessage(`["list","string"]`), Optional: true},
		&schema.Attribute{Name: "settings", Optional: true, NestedType: &schema.Object{Nesting: schema.NestingSingle, Attributes: []*schema.Attribute{
			{Name: "region", TypeJSON: str, Optional: true},
			{Name: "key", TypeJSON: str, Optional: true, Sensitive: true},
		}}},
	)
	db.Block.BlockTypes = []*schema.NestedBlock{{TypeName: "rule", Nesting: schema.NestingList, Block: &schema.Block{Attributes: []*schema.Attribute{
		{Name: "cidr", TypeJSON: str, Optional: true},
		{Name: "pin", TypeJSON: str, Optional: true, Sensitive: true},
	}}}}
	file := block(
		&schema.Attribute{Name: "path", TypeJSON: str, Required: true},
		&schema.Attribute{Name: "content", TypeJSON: str, Optional: true},
	)
	return ProviderSchemas{PS: &schema.ProviderSchema{
		Resources:   map[string]*schema.Schema{"fake_db": db, "fake_file": file},
		DataSources: map[string]*schema.Schema{}, Ephemerals: map[string]*schema.Schema{}, Functions: map[string]*schema.Function{},
	}}
}

func fakeLookup(src registry.Source, version string) (Schemas, bool) {
	if src.Type == "fake" && version == "1.0.0" {
		return fakeSchemas(), true
	}
	return nil, false
}

// project is a project in a temporary directory with a server over it.
type project struct {
	t     *testing.T
	dir   string
	store *state.Store // a writable handle to the state the server reads
	srv   *Server
	base  string
	host  string
	port  int
	stop  context.CancelFunc
	done  chan error
}

// writeSpec writes the specification, the module and the lock file.
func writeSpec(t *testing.T, dir, cue string) {
	t.Helper()
	files := map[string]string{
		"cue.mod/module.cue": "module: \"pravo.local/test\"\nlanguage: version: \"v0.14.0\"\n",
		"main.cue":           cue,
		"pravo.lock.cue":     lockCUE,
	}
	for name, content := range files {
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// record is a state record of the fake provider.
func record(addr, typ, attrs string, deps ...string) *state.Resource {
	now := time.Now().UTC()
	name := addr[strings.LastIndex(addr, ".")+1:]
	return &state.Resource{Addr: addr, Mode: "managed", Type: typ, Name: name, Provider: "example.test/x/fake", ProviderKey: "fake", ProviderVersion: "1.0.0",
		Attributes: json.RawMessage(attrs), Private: []byte(privateBlob), Dependencies: deps, ConfigHash: configHash, RefreshedAt: &now, AppliedAt: &now}
}

const dbAttrs = `{"name":"%s","blob":"` + secretBlob + `","api_token":"` + secretToken + `","port":5432,"payload":"` + secretWrite + `",` +
	`"tags":{"owner":"team","db_password":"` + secretMapKey + `"},"zones":["eu-1","eu-2"],` +
	`"settings":{"region":"eu","key":"` + secretNested + `"},"rule":[{"cidr":"10.0.0.0/8","pin":"` + secretPin + `"}],"legacy":"` + secretUnknown + `"}`

// seed writes records for most of the nodes of the specification, one record the
// specification does not declare, and the outputs.
func seed(t *testing.T, st *state.Store) {
	t.Helper()
	txn := st.Begin("apply", "pravo apply")
	set := func(r *state.Resource, action string) {
		before := r.Revision
		if err := st.Set(r, txn.ID()); err != nil {
			t.Fatal(err)
		}
		txn.Record(r.Address(), action, before, r.Revision)
	}
	set(record("services.a.db", "fake_db", fmt.Sprintf(dbAttrs, "a")), "create")
	set(record("services.a.file", "fake_file", `{"path":"/tmp/a","content":"id=a/x `+secretBlob+`"}`, "services.a.db"), "create")
	set(record("services.b.db", "fake_db", fmt.Sprintf(dbAttrs, "b")), "create")
	set(record("shared", "fake_db", `{"name":"shared","port":5432}`, "services.a.db"), "create")
	set(record("old.thing", "fake_db", `{"name":"old"}`, "services.b.db"), "create")
	if err := txn.Finish("applied", "5 created", true); err != nil {
		t.Fatal(err)
	}
	// A plan that refreshed a record is not in the history of the record: it did not write it.
	plan := st.Begin("plan", "pravo plan")
	plan.Record("services.a.db", "refresh", 1, 1)
	if err := plan.Finish("planned", "1 refreshed", true); err != nil {
		t.Fatal(err)
	}
	outs := map[string]json.RawMessage{
		"host": json.RawMessage(`"db.example"`),
		"leak": json.RawMessage(`"token=` + secretBlob + `"`),
		"safe": json.RawMessage(`{"$sensitive":"` + secretBlob + `"}`),
	}
	if err := st.SetOutputs(outs); err != nil {
		t.Fatal(err)
	}
}

// newProject makes a project with the standard specification and seeded state.
func newProject(t *testing.T) *project {
	t.Helper()
	dir := t.TempDir()
	writeSpec(t, dir, specCUE)
	st, err := state.Open(filepath.Join(dir, "pravo.state"))
	if err != nil {
		t.Fatal(err)
	}
	seed(t, st)
	return start(t, dir, st, Options{})
}

// start runs a server over a project directory.
func start(t *testing.T, dir string, st *state.Store, opts Options) *project {
	t.Helper()
	opts.Dir = dir
	opts.Version = "test"
	if opts.Schemas == nil {
		opts.Schemas = fakeLookup
	}
	if opts.Poll == 0 {
		opts.Poll = 40 * time.Millisecond
	}
	if opts.KeepAlive == 0 {
		opts.KeepAlive = 200 * time.Millisecond
	}
	srv, err := New(opts)
	if err != nil {
		t.Fatal(err)
	}
	ln, err := Listen(0)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	p := &project{t: t, dir: dir, store: st, srv: srv, stop: cancel, done: make(chan error, 1)}
	p.port = ln.Addr().(*net.TCPAddr).Port
	p.host = fmt.Sprintf("127.0.0.1:%d", p.port)
	p.base = "http://" + p.host
	go func() { p.done <- srv.Serve(ctx, ln) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-p.done:
		case <-time.After(10 * time.Second):
			t.Error("Serve did not return after its context ended")
		}
	})
	return p
}

// do sends a request with the token as a bearer.
func (p *project) do(method, path string, hdr map[string]string) (*http.Response, []byte) {
	p.t.Helper()
	req, err := http.NewRequest(method, p.base+path, http.NoBody)
	if err != nil {
		p.t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+p.srv.Token())
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	return p.send(req)
}

func (p *project) send(req *http.Request) (*http.Response, []byte) {
	p.t.Helper()
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }, Timeout: 20 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		p.t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return resp, body
}

func (p *project) get(path string) []byte {
	p.t.Helper()
	resp, body := p.do("GET", path, nil)
	if resp.StatusCode != http.StatusOK {
		p.t.Fatalf("GET %s: %d %s", path, resp.StatusCode, body)
	}
	return body
}

func (p *project) graph() Graph {
	p.t.Helper()
	var g Graph
	if err := json.Unmarshal(p.get("/api/graph"), &g); err != nil {
		p.t.Fatal(err)
	}
	return g
}

func (p *project) detail(addr string) Detail {
	p.t.Helper()
	var d Detail
	if err := json.Unmarshal(p.get("/api/detail?addr="+queryEscape(addr)), &d); err != nil {
		p.t.Fatal(err)
	}
	return d
}

func (p *project) source(addr string) Source {
	p.t.Helper()
	var s Source
	if err := json.Unmarshal(p.get("/api/source?addr="+queryEscape(addr)), &s); err != nil {
		p.t.Fatal(err)
	}
	return s
}

func queryEscape(s string) string { return url.QueryEscape(s) }

// waitFor polls until the condition holds.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

// digestTree hashes the names, modes and contents of every file below dir.
func digestTree(t *testing.T, dir string) string {
	t.Helper()
	h := sha256.New()
	var names []string
	_ = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err == nil {
			names = append(names, p)
		}
		return nil
	})
	sort.Strings(names)
	for _, p := range names {
		fi, err := os.Lstat(p)
		if err != nil {
			continue
		}
		fmt.Fprintf(h, "%s|%v|%d|%d\n", p, fi.Mode(), fi.Size(), fi.ModTime().UnixNano())
		if fi.Mode().IsRegular() {
			data, _ := os.ReadFile(p)
			h.Write(data)
		}
	}
	return fmt.Sprintf("%x", h.Sum(nil))
}

// noSecrets fails the test when any of the strings shows in the text.
func noSecrets(t *testing.T, what string, text []byte, secrets ...string) {
	t.Helper()
	if len(secrets) == 0 {
		secrets = allSecrets
	}
	for _, s := range secrets {
		if strings.Contains(string(text), s) || strings.Contains(string(text), base64.StdEncoding.EncodeToString([]byte(s))) {
			t.Errorf("%s sends %q", what, s)
		}
	}
}

func jsonUnmarshal(data []byte, v any) error { return json.Unmarshal(data, v) }

func strconvItoa(n int) string { return strconv.Itoa(n) }

func atoiOr0(s string) int { n, _ := strconv.Atoi(s); return n }

func fakeLookupNone(registry.Source, string) (Schemas, bool) { return nil, false }

// configRoot makes the root OpenReadOnly needs.
type configRoot struct{ dir string }

func (c *configRoot) cfg() *config.Root {
	return &config.Root{Dir: c.dir, StatePath: config.DefaultStatePath}
}
