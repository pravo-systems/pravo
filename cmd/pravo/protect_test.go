package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/pravo-systems/pravo/internal/fakes3"
	"github.com/pravo-systems/pravo/internal/protect"
	"github.com/pravo-systems/pravo/internal/state"
	"github.com/pravo-systems/pravo/internal/ui"
)

// s3Env is a specification whose state is on a fake bucket.
type s3Env struct {
	dir string
	f   *fakes3.Server
	url string
}

// newS3Env makes the bucket and a specification that names it. A store that
// ignores conditional writes is made with ignore.
func newS3Env(t *testing.T, ignore bool) *s3Env {
	t.Helper()
	t.Setenv("PRAVO_HOME", t.TempDir())
	t.Setenv("PRAVO_S3_PROBE", "off")
	f := fakes3.New()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if ignore {
			r.Header.Del("If-None-Match")
			r.Header.Del("If-Match")
		}
		f.ServeHTTP(w, r)
	}))
	t.Cleanup(srv.Close)
	dir := specDir(t, map[string]string{"main.cue": "package t\n\n" +
		`$settings: state: {backend: "s3", bucket: "northwind-state", prefix: "prod", region: "eu-west-1", endpoint: "` + srv.URL + `", path_style: true, access_key: "test", secret_key: "test"}` + "\n"})
	return &s3Env{dir: dir, f: f, url: srv.URL}
}

func (e *s3Env) backend(t *testing.T) state.Backend {
	t.Helper()
	b, err := state.NewS3Backend(context.Background(), state.S3Config{
		Bucket: "northwind-state", Prefix: "prod", Region: "eu-west-1", Endpoint: e.url, PathStyle: true, AccessKey: "test", SecretKey: "test",
	})
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func (e *s3Env) store(t *testing.T) *state.Store {
	t.Helper()
	st, err := state.OpenBackend(context.Background(), e.backend(t))
	if err != nil {
		t.Fatal(err)
	}
	return st
}

// record adds a resource whose attributes hold a value that must never be
// readable in a sealed bucket.
func (e *s3Env) record(t *testing.T, st *state.Store, addr, secret string) {
	t.Helper()
	attrs := fmt.Sprintf(`{"id":%q,"password":%q}`, addr, secret)
	if err := st.Set(&state.Resource{Addr: addr, Mode: "managed", Type: "t_thing", Name: addr, Attributes: []byte(attrs)}, ""); err != nil {
		t.Fatal(err)
	}
}

// bucketText is every object of the bucket, joined.
func (e *s3Env) bucketText() string {
	keys := make([]string, 0, len(e.f.Objects))
	for k := range e.f.Objects {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	for _, k := range keys {
		b.WriteString(k + "\n" + string(e.f.Objects[k]) + "\n")
	}
	return b.String()
}

func (e *s3Env) puts() int { return e.f.Puts }

// readers writes the readers file.
func (e *s3Env) readers(t *testing.T, lines ...string) {
	t.Helper()
	writeReaders(t, filepath.Join(e.dir, ".pravo", "access"), lines...)
}

// person makes an identity, files it in the identity directory without a
// passphrase, and returns it with its line for the readers file.
func person(t *testing.T, ids, name string) (*protect.Identity, string) {
	t.Helper()
	id, data := newIdentityFile(t, name, true)
	putIdentity(t, ids, name, data)
	return id, id.Line()
}

// withStderr runs fn and returns what it wrote to standard error.
func withStderr(t *testing.T, fn func()) string {
	t.Helper()
	r, w, _ := os.Pipe()
	old := os.Stderr
	os.Stderr = w
	done := make(chan []byte)
	go func() { b, _ := io.ReadAll(r); done <- b }()
	fn()
	w.Close()
	os.Stderr = old
	return string(<-done)
}

// requireSealing skips a test that needs the sealing of the state package.
func requireSealing(t *testing.T) {
	t.Helper()
	id, err := protect.NewIdentity("probe", "")
	if err != nil {
		t.Fatal(err)
	}
	st, err := state.Open(filepath.Join(t.TempDir(), "probe.state"))
	if err != nil {
		t.Fatal(err)
	}
	_, err = st.Upgrade(context.Background(), state.UpgradeOptions{To: state.FormatSealed, Readers: []protect.Reader{id.AsReader()}, Identity: id, By: "probe"})
	if errors.Is(err, state.ErrNotImplemented) {
		t.Skip("the sealing of the state package is not in this tree yet")
	}
}

func TestProtectCheckAStoreThatIsReady(t *testing.T) {
	ids := isolateIdentities(t)
	e := newS3Env(t, false)
	st := e.store(t)
	e.record(t, st, "network.vpn", "hunter2")
	e.record(t, st, "network.vpc", "hunter3")
	_, ada := person(t, ids, "ada")
	_, dan := person(t, ids, "dan")
	e.readers(t, ada, dan)
	before := len(e.f.Objects)

	text, code := runPravo(t, e.dir, "protect", "check")
	if code != 0 {
		t.Fatalf("exit %d\n%s", code, text)
	}
	for _, want := range []string{
		"Store            s3://northwind-state/prod  (eu-west-1)\n",
		"Conditional      create-only ok · replace ok · delete ok\n",
		"                 8 concurrent replaces: 1 winner · listing sees a new key at once\n",
		"State            2 records · format 1",
		"Readers file     .pravo/access/readers.txt · 2 readers (ada dan)\n",
		"Running          0 transactions · 0 locks held\n",
		"Machines         ",
		"Ready to seal. Next: pravo protect init --encrypt\n",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("the output lacks %q:\n%s", want, text)
		}
	}
	if strings.HasSuffix(text, "init --encrypt\n") == false {
		t.Errorf("the verdict is not the last line:\n%s", text)
	}
	if len(e.f.Objects) != before {
		t.Errorf("check left objects behind: %d before, %d after", before, len(e.f.Objects))
	}
}

func TestProtectCheckJSON(t *testing.T) {
	ids := isolateIdentities(t)
	e := newS3Env(t, false)
	_, ada := person(t, ids, "ada")
	e.readers(t, ada)
	text, code := runPravo(t, e.dir, "--json", "protect", "check")
	var doc checkResult
	if code != 0 || json.Unmarshal([]byte(text), &doc) != nil {
		t.Fatalf("exit %d\n%s", code, text)
	}
	if !doc.Ready || doc.Format != 1 || doc.Store != "s3://northwind-state/prod" || len(doc.Readers) != 1 {
		t.Errorf("document = %+v", doc)
	}
}

func TestProtectCheckRefusesALocalDirectory(t *testing.T) {
	ids := isolateIdentities(t)
	dir, _ := pendingDir(t)
	_, ada := person(t, ids, "ada")
	writeReaders(t, filepath.Join(dir, ".pravo", "access"), ada)

	text, code := runPravo(t, dir, "protect", "check")
	if code != 1 || !strings.Contains(text, "Error: the state at ./pravo.state is a local directory. Its compare-and-swap is a per-user mutex, so two users on one directory would not be excluded. Move the state to a bucket, or pass --allow-local for a directory that only you use.") {
		t.Fatalf("exit %d\n%s", code, text)
	}
	text, code = runPravo(t, dir, "protect", "check", "--allow-local")
	if code != 0 || !strings.Contains(text, "Store            ./pravo.state  (local directory)\n") || !strings.Contains(text, "Ready to seal.") {
		t.Fatalf("with --allow-local: exit %d\n%s", code, text)
	}
}

func TestProtectCheckCreatesNoState(t *testing.T) {
	isolateIdentities(t)
	dir := specDir(t, map[string]string{"main.cue": "package estate\n\nsize: 3\n"})
	if _, code := runPravo(t, dir, "protect", "check", "--allow-local"); code != 6 {
		t.Errorf("exit %d, want 6 for a root with no readers file", code)
	}
	if got := entries(t, dir); strings.Join(got, ",") != "cue.mod,main.cue" {
		t.Errorf("the directory holds %v; check must not create the state", got)
	}
}

func TestProtectCheckReadersFile(t *testing.T) {
	ids := isolateIdentities(t)
	e := newS3Env(t, false)
	_, ada := person(t, ids, "ada")

	t.Run("missing", func(t *testing.T) {
		text, code := runPravo(t, e.dir, "protect", "check")
		if code != 6 || !strings.Contains(text, "Readers file     not found (looked in .pravo/access)\n") || strings.Contains(text, "Ready") {
			t.Fatalf("exit %d\n%s", code, text)
		}
	})
	t.Run("invalid, and the line is not shown", func(t *testing.T) {
		e.readers(t, ada, "SECRETNAME ssh-ed25519 notakey x25519:alsonotakey")
		text, code := runPravo(t, e.dir, "protect", "check")
		if code != 6 || !strings.Contains(text, "invalid: line 2:") || strings.Contains(text, "SECRETNAME") || strings.Contains(text, "notakey") || strings.Contains(text, "Ready") {
			t.Fatalf("exit %d\n%s", code, text)
		}
	})
	t.Run("empty", func(t *testing.T) {
		e.readers(t, "# nobody yet")
		text, code := runPravo(t, e.dir, "protect", "check")
		if code != 6 || !strings.Contains(text, "0 readers") || strings.Contains(text, "Ready") {
			t.Fatalf("exit %d\n%s", code, text)
		}
	})
	t.Run("--access-dir and PRAVO_ACCESS_DIR", func(t *testing.T) {
		other := filepath.Join(e.dir, "elsewhere")
		writeReaders(t, other, ada)
		text, code := runPravo(t, e.dir, "protect", "check", "--access-dir", "elsewhere")
		if code != 0 || !strings.Contains(text, "Readers file     elsewhere/readers.txt · 1 reader (ada)\n") {
			t.Fatalf("flag: exit %d\n%s", code, text)
		}
		flagAccessDir = ""
		t.Setenv("PRAVO_ACCESS_DIR", other)
		text, code = runPravo(t, e.dir, "protect", "check")
		if code != 0 || !strings.Contains(text, "elsewhere/readers.txt") {
			t.Fatalf("environment: exit %d\n%s", code, text)
		}
	})
}

func TestProtectCheckRunningWork(t *testing.T) {
	ids := isolateIdentities(t)
	e := newS3Env(t, false)
	_, ada := person(t, ids, "ada")
	e.readers(t, ada)
	st := e.store(t)
	e.record(t, st, "a", "x")
	if err := st.Lock("a", "pravo apply", ""); err != nil {
		t.Fatal(err)
	}
	text, code := runPravo(t, e.dir, "protect", "check")
	if code != 4 || !strings.Contains(text, "1 lock held") && !strings.Contains(text, "1 locks held") || !strings.Contains(text, "Not ready: wait for them, or release stale locks with pravo state unlock --stale.") || strings.Contains(text, "Ready to seal") {
		t.Fatalf("exit %d\n%s", code, text)
	}
	if err := st.Unlock("a"); err != nil {
		t.Fatal(err)
	}
	if _, code := runPravo(t, e.dir, "protect", "check"); code != 0 {
		t.Errorf("exit %d after the lock was released", code)
	}
}

func TestProtectCheckRefusesAStoreThatIgnoresConditionalWrites(t *testing.T) {
	ids := isolateIdentities(t)
	e := newS3Env(t, true)
	_, ada := person(t, ids, "ada")
	e.readers(t, ada)
	text, code := runPravo(t, e.dir, "protect", "check")
	if code != 1 || !strings.Contains(text, "FAILED") || !strings.Contains(text, "cannot hold a state") || strings.Contains(text, "Ready") {
		t.Fatalf("exit %d\n%s", code, text)
	}
	for k := range e.f.Objects {
		t.Errorf("check left %q behind", k)
	}
}

func TestMachinesLine(t *testing.T) {
	now := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	day := func(n int, v string) state.Transaction {
		return state.Transaction{Started: now.AddDate(0, 0, -n), Version: v}
	}
	cases := []struct {
		name string
		txns []state.Transaction
		want string
	}{
		{"none", nil, "no writer in the last 30 days"},
		{"old ones only", []state.Transaction{day(40, "")}, "no writer in the last 30 days"},
		{"all versions", []state.Transaction{day(1, "0.10.0"), day(2, "0.9.2"), day(3, "0.10.1")}, "every writer in the last 30 days used pravo >= 0.9.2"},
		{"a pre-release is before its release", []state.Transaction{day(1, "0.1.0"), day(2, "0.1.0-dev")}, "every writer in the last 30 days used pravo >= 0.1.0-dev"},
		{"some without", []state.Transaction{day(1, "0.2.0"), day(2, ""), day(3, "")}, "versions are not recorded by this state's older writers (2 of 3 transactions in 30 days)"},
	}
	for _, c := range cases {
		if got := machines(c.txns, now); got != c.want {
			t.Errorf("%s: %q, want %q", c.name, got, c.want)
		}
	}
}

func TestProtectStatusOfAnUnsealedState(t *testing.T) {
	ids := isolateIdentities(t)
	e := newS3Env(t, false)
	e.record(t, e.store(t), "a", "x")
	text, code := runPravo(t, e.dir, "protect", "status")
	if code != 0 {
		t.Fatalf("exit %d\n%s", code, text)
	}
	for _, want := range []string{
		"Sealed       off · format 1\n",
		"Store        s3://northwind-state/prod\n",
		"Readers      file not found (looked in .pravo/access)\n",
		"Roles        off\n",
		"Unsettled    none\n",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("the output lacks %q:\n%s", want, text)
		}
	}
	_, ada := person(t, ids, "ada")
	e.readers(t, ada)
	text, _ = runPravo(t, e.dir, "protect", "status")
	if !strings.Contains(text, "Readers      .pravo/access/readers.txt · 1 reader\n") {
		t.Errorf("the readers line:\n%s", text)
	}
	text, code = runPravo(t, e.dir, "--json", "protect", "status")
	var doc struct {
		State  string `json:"state"`
		Format int    `json:"format"`
	}
	if code != 0 || json.Unmarshal([]byte(text), &doc) != nil || doc.State != "off" || doc.Format != 1 {
		t.Errorf("exit %d, document %s", code, text)
	}
}

func TestProtectStatusWritesNothing(t *testing.T) {
	isolateIdentities(t)
	e := newS3Env(t, false)
	e.record(t, e.store(t), "a", "x")
	before := e.puts()
	for _, args := range [][]string{{"protect", "status"}, {"protect", "check"}} {
		runPravo(t, e.dir, args...)
	}
	// check writes scratch objects for the probe; status writes none.
	runPravo(t, e.dir, "protect", "status")
	puts := e.puts()
	runPravo(t, e.dir, "protect", "status")
	if e.puts() != puts {
		t.Errorf("protect status wrote %d objects", e.puts()-puts)
	}
	_ = before
}

func TestProtectInitRefusals(t *testing.T) {
	ids := isolateIdentities(t)
	e := newS3Env(t, false)
	e.record(t, e.store(t), "a", "TOPSECRET")
	ada, adaLine := person(t, ids, "ada")
	_, danLine := person(t, ids, "dan")
	_ = ada
	puts := e.puts()
	unchanged := func(t *testing.T) {
		t.Helper()
		if e.puts() != puts {
			t.Errorf("the refusal wrote %d objects", e.puts()-puts)
		}
	}

	t.Run("it needs --encrypt", func(t *testing.T) {
		e.readers(t, adaLine)
		text, code := runPravo(t, e.dir, "protect", "init")
		if code != 1 || !strings.Contains(text, "--encrypt") {
			t.Fatalf("exit %d\n%s", code, text)
		}
		unchanged(t)
	})
	t.Run("a readers file that is missing", func(t *testing.T) {
		os.RemoveAll(filepath.Join(e.dir, ".pravo"))
		text, code := runPravo(t, e.dir, "protect", "init", "--encrypt", "--yes")
		if code != 6 || !strings.Contains(text, "looked in .pravo/access") {
			t.Fatalf("exit %d\n%s", code, text)
		}
		unchanged(t)
	})
	t.Run("a readers file that is invalid", func(t *testing.T) {
		e.readers(t, adaLine, "SECRETNAME one two")
		text, code := runPravo(t, e.dir, "protect", "init", "--encrypt", "--yes")
		if code != 6 || !strings.Contains(text, "line 2") || strings.Contains(text, "SECRETNAME") {
			t.Fatalf("exit %d\n%s", code, text)
		}
		unchanged(t)
	})
	t.Run("you are not on the list", func(t *testing.T) {
		e.readers(t, danLine)
		t.Setenv("PRAVO_IDENTITY_DIR", "")
		flagIdentity = "ada"
		defer func() { flagIdentity = "" }()
		text, code := runPravo(t, e.dir, "protect", "init", "--encrypt", "--yes", "--identity", "ada")
		want := "Error: your identity ada is not in .pravo/access/readers.txt. Sealing to a list you are not on would lock you out."
		if code != 5 || !strings.Contains(text, want) {
			t.Fatalf("exit %d\n%s", code, text)
		}
		unchanged(t)
	})
	t.Run("you have no identity", func(t *testing.T) {
		e.readers(t, adaLine)
		t.Setenv("PRAVO_IDENTITY_DIR", t.TempDir())
		text, code := runPravo(t, e.dir, "protect", "init", "--encrypt", "--yes")
		if code != 5 || !strings.Contains(text, "no identity found") {
			t.Fatalf("exit %d\n%s", code, text)
		}
		unchanged(t)
	})
	t.Run("a local directory", func(t *testing.T) {
		dir, _ := pendingDir(t)
		writeReaders(t, filepath.Join(dir, ".pravo", "access"), adaLine)
		text, code := runPravo(t, dir, "protect", "init", "--encrypt", "--yes")
		if code != 1 || !strings.Contains(text, "--allow-local") {
			t.Fatalf("exit %d\n%s", code, text)
		}
	})
	t.Run("a transaction is running", func(t *testing.T) {
		e.readers(t, adaLine)
		st := e.store(t)
		txn := st.Begin("apply", "pravo apply")
		if err := txn.Start(); err != nil {
			t.Fatal(err)
		}
		// A transaction whose command was killed holds no lock and does not count.
		if text, code := runPravo(t, e.dir, "protect", "check"); code != 0 {
			t.Fatalf("an interrupted transaction stopped check: exit %d\n%s", code, text)
		}
		if err := st.Lock("a", "pravo apply", txn.ID()); err != nil {
			t.Fatal(err)
		}
		defer st.Unlock("a")
		before := e.puts()
		text, code := runPravo(t, e.dir, "protect", "init", "--encrypt", "--yes")
		if code != 4 || !strings.Contains(text, "not ready: 1 transaction is running and 1 lock is held. Wait for them") {
			t.Fatalf("exit %d\n%s", code, text)
		}
		if e.puts() != before {
			t.Error("a refusal wrote to the bucket")
		}
	})
	t.Run("a lock is held", func(t *testing.T) {
		e.readers(t, adaLine)
		st := e.store(t)
		if err := st.Lock("a", "pravo apply", ""); err != nil {
			t.Fatal(err)
		}
		defer st.Unlock("a")
		before := e.puts()
		text, code := runPravo(t, e.dir, "protect", "init", "--encrypt", "--yes")
		if code != 4 || !strings.Contains(text, "not ready: 1 lock is held. Wait for them") {
			t.Fatalf("exit %d\n%s", code, text)
		}
		if e.puts() != before {
			t.Error("a refusal wrote to the bucket")
		}
	})
}

func TestProtectInitAsksBeforeItChangesAnything(t *testing.T) {
	ids := isolateIdentities(t)
	e := newS3Env(t, false)
	e.record(t, e.store(t), "a", "TOPSECRET")
	_, ada := person(t, ids, "ada")
	_, dan := person(t, ids, "dan")
	e.readers(t, ada, dan)
	snapshot := e.bucketText()
	puts := e.puts()

	t.Run("it refuses to ask without a terminal", func(t *testing.T) {
		text, code := runPravo(t, e.dir, "protect", "init", "--encrypt")
		if code != 1 || !strings.Contains(text, "Error: refusing to ask, stdin is not a terminal; pass --yes") {
			t.Fatalf("exit %d\n%s", code, text)
		}
		for _, want := range []string{
			"Readers  .pravo/access/readers.txt  (ada dan)\n",
			"This will:\n",
			"  1  write 1 record as immutable objects  rec/<sha256>\n",
			"  2  encrypt their attributes and private data to 2 readers (ada dan)\n",
			"  3  seal outputs.json, stored-plan values and the values in transaction command lines the same way\n",
			"  4  set the state to format 3. Earlier versions of pravo will refuse it\n",
		} {
			if !strings.Contains(text, want) {
				t.Errorf("the output lacks %q:\n%s", want, text)
			}
		}
		if e.puts() != puts || e.bucketText() != snapshot {
			t.Error("the bucket changed")
		}
	})
	t.Run("it refuses to ask under --json", func(t *testing.T) {
		stdinIsTerminal = func() bool { return true }
		defer func() { stdinIsTerminal = func() bool { return false } }()
		text, code := runPravo(t, e.dir, "--json", "protect", "init", "--encrypt")
		if code != 1 || !strings.Contains(text, "--yes") {
			t.Fatalf("exit %d\n%s", code, text)
		}
	})
	t.Run("an answer other than yes", func(t *testing.T) {
		stdinIsTerminal = func() bool { return true }
		defer func() { stdinIsTerminal = func() bool { return false } }()
		defer stdin(t, "n\n")()
		text, code := runPravo(t, e.dir, "protect", "init", "--encrypt")
		if code != 1 || !strings.Contains(text, "Proceed? [y/N] ") || !strings.Contains(text, "protect init cancelled") {
			t.Fatalf("exit %d\n%s", code, text)
		}
		if e.puts() != puts || e.bucketText() != snapshot {
			t.Error("the bucket changed")
		}
	})
}

func TestProtectRewrapOfAStateThatIsNotSealed(t *testing.T) {
	ids := isolateIdentities(t)
	e := newS3Env(t, false)
	e.record(t, e.store(t), "a", "x")
	_, ada := person(t, ids, "ada")
	e.readers(t, ada)
	text, code := runPravo(t, e.dir, "protect", "rewrap")
	if code != 1 || !strings.Contains(text, "Error: the state is not sealed; see pravo protect check") {
		t.Fatalf("exit %d\n%s", code, text)
	}
}

func TestProtectCommandsTakeNoStrayArguments(t *testing.T) {
	isolateIdentities(t)
	for _, args := range [][]string{{"protect", "stray"}, {"protect", "check", "stray"}, {"protect", "status", "stray"}, {"protect", "init", "stray"}, {"protect", "rewrap", "stray"}} {
		if text, code := runPravo(t, t.TempDir(), args...); code != 1 || !strings.Contains(text, "stray") {
			t.Errorf("%v: exit %d\n%s", args, code, text)
		}
	}
}

func TestVersionLess(t *testing.T) {
	for _, c := range []struct{ a, b string }{{"0.1.0", "0.2.0"}, {"0.9.2", "0.10.0"}, {"0.1.0-dev", "0.1.0"}, {"1.0", "1.0.1"}, {"v0.1.0", "0.1.1"}} {
		if !versionLess(c.a, c.b) || versionLess(c.b, c.a) {
			t.Errorf("%s should be before %s", c.a, c.b)
		}
	}
	if versionLess("0.1.0", "0.1.0") {
		t.Error("equal versions are not less")
	}
}

func TestSmallHelpersOfProtect(t *testing.T) {
	for n, want := range map[int]string{0: "0", 7: "7", 999: "999", 1000: "1,000", 2001: "2,001", 1234567: "1,234,567"} {
		if got := commas(n); got != want {
			t.Errorf("commas(%d) = %q, want %q", n, got, want)
		}
	}
	if got := counted(1, "record", "records"); got != "1 record" {
		t.Errorf("counted(1) = %q", got)
	}
	if got := counted(2001, "record", "records"); got != "2,001 records" {
		t.Errorf("counted(2001) = %q", got)
	}
	for d, want := range map[time.Duration]string{400 * time.Millisecond: "0.4 s", 9 * time.Second: "9.0 s", 41 * time.Second: "41 s", 90 * time.Second: "90 s"} {
		if got := elapsed(d); got != want {
			t.Errorf("elapsed(%v) = %q, want %q", d, got, want)
		}
	}
	if got := diffText([]string{"dan"}, []string{"fay"}); got != "dan added, fay removed" {
		t.Errorf("diffText = %q", got)
	}
	if got := diffText([]string{"dan", "eve"}, nil); got != "dan eve added" {
		t.Errorf("diffText = %q", got)
	}
}

func TestConfirm(t *testing.T) {
	isolateIdentities(t)
	prev := out
	out = ui.NewPrinter(io.Discard, true)
	t.Cleanup(func() { out = prev })
	if ok, err := confirm("Proceed?", true); !ok || err != nil {
		t.Errorf("--yes: %v %v", ok, err)
	}
	if _, err := confirm("Proceed?", false); err == nil || !strings.Contains(err.Error(), "stdin is not a terminal; pass --yes") {
		t.Errorf("no terminal: %v", err)
	}
	stdinIsTerminal = func() bool { return true }
	for answer, want := range map[string]bool{"y\n": true, "Yes\n": true, "YES\n": true, "n\n": false, "\n": false, "yes please\n": false, "": false} {
		restore := stdin(t, answer)
		ok, err := confirm("Proceed?", false)
		restore()
		if err != nil || ok != want {
			t.Errorf("answer %q: %v %v, want %v", answer, ok, err, want)
		}
	}
}

func TestPlansInUseLineAgreesWithTheNumber(t *testing.T) {
	for _, c := range []struct {
		n     int
		again bool
		want  string
	}{
		{1, false, "1 stored plan in use was not sealed; run pravo protect rewrap --all when it is done."},
		{2, false, "2 stored plans in use were not sealed; run pravo protect rewrap --all when they are done."},
		{1, true, "1 stored plan in use was not sealed again; run pravo protect rewrap --all when it is done."},
		{3, true, "3 stored plans in use were not sealed again; run pravo protect rewrap --all when they are done."},
	} {
		if got := plansInUseLine(c.n, c.again); got != c.want {
			t.Errorf("plansInUseLine(%d, %v) = %q, want %q", c.n, c.again, got, c.want)
		}
	}
}

func TestDisplayPathKeepsAPathThatClimbsFar(t *testing.T) {
	top := t.TempDir()
	if r, err := filepath.EvalSymlinks(top); err == nil {
		top = r
	}
	deep := filepath.Join(top, "a", "b", "c")
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(deep)
	for _, c := range []struct{ path, want string }{
		{filepath.Join(deep, ".pravo", "access"), ".pravo/access"},
		{filepath.Join(top, "a", "b", "x"), "../x"},
		{filepath.Join(top, "a", "x"), "../../x"},
		{filepath.Join(top, "x"), filepath.Join(top, "x")}, // three levels up
		{filepath.Join(filepath.Dir(top), "x"), filepath.Join(filepath.Dir(top), "x")},
	} {
		if got := displayPath(c.path); got != c.want {
			t.Errorf("displayPath(%s) = %s, want %s", c.path, got, c.want)
		}
	}
}
