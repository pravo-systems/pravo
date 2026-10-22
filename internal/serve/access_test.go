package serve

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"go/parser"
	"go/token"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pravo-systems/pravo/internal/config"
	"github.com/pravo-systems/pravo/internal/state"
)

// fakeProtection stands in for the protection of the state until a sealed store
// can be made here. It returns the facts it holds, with the path of the readers
// file the server asks about.
type fakeProtection struct {
	mu    sync.Mutex
	facts Facts
	paths []string
}

func (f *fakeProtection) fn(ctx context.Context, st *state.Store, path string) Facts {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.paths = append(f.paths, path)
	out := f.facts
	out.File.Path = path
	return out
}

func (f *fakeProtection) set(fn func(*Facts)) {
	f.mu.Lock()
	fn(&f.facts)
	f.mu.Unlock()
}

// key and blob make reader keys and signing keys that look like the real ones.
func key(c byte) string { return "x25519:" + strings.Repeat(string(c), 43) }

func blob(c byte) string {
	raw := append([]byte{0, 0, 0, 11}, []byte("ssh-ed25519")...)
	raw = append(raw, 0, 0, 0, 32)
	raw = append(raw, []byte(strings.Repeat(string(c), 32))...)
	return base64.StdEncoding.EncodeToString(raw)
}

func rf(name string, c byte, line int) ReaderFacts {
	return ReaderFacts{Name: name, Key: key(c), SSH: blob(c), Line: line}
}

func rs(name string, c byte) ReaderFacts { return ReaderFacts{Name: name, Key: key(c)} }

// sealedFacts is a sealed state with readers ada, dan and eve, a readers file that
// says the same, and ada as the identity.
func sealedFacts() Facts {
	return Facts{
		Store: StoreFacts{State: "sealed", Format: 3, Readers: []ReaderFacts{rs("ada", 'a'), rs("dan", 'd'), rs("eve", 'e')},
			Groups: []GroupFacts{{ID: "g1", Current: true, Locks: 3, Records: 5}}, Records: 5, Sealed: 5,
			History: []ChangeFacts{{At: time.Date(2026, 10, 22, 10, 41, 0, 0, time.UTC), By: "ada", What: "rewrap", Added: []string{"eve"}}}},
		File: FileFacts{State: "ok", Readers: []ReaderFacts{rf("ada", 'a', 1), rf("dan", 'd', 2), rf("eve", 'e', 3)}},
		You:  &YouFacts{State: "reader", Name: "ada", Key: key('a')},
	}
}

// accessProject starts a viewer with the protection f over the standard project.
// The state itself is the plain one the helpers make: what is sealed is told by f.
func accessProject(t *testing.T, f *fakeProtection) *project {
	t.Helper()
	dir := t.TempDir()
	writeSpec(t, dir, specCUE)
	st, err := state.Open(filepath.Join(dir, "pravo.state"))
	if err != nil {
		t.Fatal(err)
	}
	seed(t, st)
	opts := Options{AccessDir: filepath.Join(dir, ".pravo", "access")}
	if f != nil {
		opts.Protection = f.fn
	}
	return start(t, dir, st, opts)
}

func (p *project) access() Access {
	p.t.Helper()
	var a Access
	if err := json.Unmarshal(p.get("/api/access"), &a); err != nil {
		p.t.Fatal(err)
	}
	return a
}

func suggestion(a Access, id string) *Suggestion {
	for i := range a.Suggestions {
		if a.Suggestions[i].ID == id {
			return &a.Suggestions[i]
		}
	}
	return nil
}

func stepTexts(s *Suggestion, kind string) []string {
	var out []string
	for _, st := range s.Steps {
		if kind == "" || st.Kind == kind {
			out = append(out, st.Text)
		}
	}
	return out
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestAccessOff(t *testing.T) {
	p := accessProject(t, nil)
	a := p.access()
	g := p.graph()
	if a.Version != APIVersion || a.Revision != g.Revision {
		t.Errorf("version %d revision %q, graph revision %q", a.Version, a.Revision, g.Revision)
	}
	if a.State != "off" || a.Format != 1 || a.Backend != "local" || a.You != nil || a.Migration != nil {
		t.Errorf("state %q format %d backend %q you %+v", a.State, a.Format, a.Backend, a.You)
	}
	if !strings.HasPrefix(a.Summary, "The state is not sealed. Attributes, outputs and stored plans are stored as plain text, and anyone who can read ") || !strings.Contains(a.Summary, "pravo.state") {
		t.Errorf("summary %q", a.Summary)
	}
	if a.File.Verdict != "none" || len(a.Readers) != 0 || a.Recovery.State != "none" {
		t.Errorf("file %+v readers %v recovery %+v", a.File, a.Readers, a.Recovery)
	}
	if len(a.Suggestions) != 1 || a.Suggestions[0].ID != "seal" || !a.Suggestions[0].Primary {
		t.Fatalf("suggestions %+v", a.Suggestions)
	}
	// A local directory is sealed only with --allow-local: the page says why, and
	// the command carries the flag.
	cmds := stepTexts(&a.Suggestions[0], "command")
	if !equalStrings(cmds, []string{"pravo id new --name <name>", "pravo protect init --encrypt --allow-local"}) {
		t.Errorf("commands %q", cmds)
	}
	var local bool
	for _, n := range stepTexts(&a.Suggestions[0], "note") {
		local = local || strings.Contains(n, "local directory")
	}
	if !local {
		t.Errorf("no note about the local directory: %+v", a.Suggestions[0].Steps)
	}
	for _, n := range g.Nodes {
		if n.Sealed {
			t.Errorf("%s is sealed in a state that is not", n.Addr)
		}
	}
	if g.Project.State.Sealed {
		t.Error("the project says the state is sealed")
	}
}

func TestAccessImmutable(t *testing.T) {
	f := &fakeProtection{facts: Facts{Store: StoreFacts{State: "immutable", Format: 2, Records: 5}, File: FileFacts{State: "missing"}}}
	a := accessProject(t, f).access()
	if a.State != "immutable" || a.Format != 2 || a.Summary != "Records are stored as immutable objects (format 2). They are not encrypted." {
		t.Errorf("%q %d %q", a.State, a.Format, a.Summary)
	}
	if s := suggestion(a, "seal"); s == nil || !s.Primary {
		t.Errorf("seal: %+v", s)
	}
	if a.You != nil {
		t.Errorf("you %+v in a state that is not sealed", a.You)
	}
}

func TestAccessSealedReader(t *testing.T) {
	f := &fakeProtection{facts: sealedFacts()}
	p := accessProject(t, f)
	a := p.access()
	if a.State != "sealed" || a.Format != 3 {
		t.Fatalf("state %q format %d", a.State, a.Format)
	}
	if want := "The state is sealed to 3 readers. Attributes, outputs and stored plans are encrypted. Addresses, types, names, dependencies and revisions stay readable, so the graph and the plans keep working."; a.Summary != want {
		t.Errorf("summary %q", a.Summary)
	}
	if a.File.Verdict != "match" || a.File.Text != "The readers file matches the store." || a.File.Count != 3 || len(a.File.Added)+len(a.File.Removed) != 0 {
		t.Errorf("file %+v", a.File)
	}
	if a.You == nil || a.You.State != "reader" || a.You.Text != "You are ada, a reader." {
		t.Errorf("you %+v", a.You)
	}
	if len(a.Readers) != 3 {
		t.Fatalf("readers %+v", a.Readers)
	}
	for i, r := range a.Readers {
		if !r.InFile || !r.InStore || !r.NameOK || r.Line != i+1 || r.Fingerprint == "" || !strings.HasPrefix(r.Fingerprint, "SHA256:") {
			t.Errorf("reader %d: %+v", i, r)
		}
	}
	if !a.Readers[0].You || a.Readers[1].You || a.Readers[0].KeyShort != "x25519:aaaaaa…" || a.Readers[0].Key != key('a') {
		t.Errorf("you mark or key: %+v", a.Readers[0])
	}
	if len(a.Groups) != 1 || a.Groups[0] != (AccessGroup{ID: "g1", Current: true, Locks: 3, Records: 5}) {
		t.Errorf("groups %+v", a.Groups)
	}
	if a.LastChange == nil || a.LastChange.Text != "Last change: readers rewrapped by ada on 2026-10-22 10:41 UTC (eve added)." {
		t.Errorf("last change %+v", a.LastChange)
	}
	if want := "No recovery recipient. If every reader loses their key, the state cannot be read."; a.Recovery.Text != want {
		t.Errorf("recovery %+v", a.Recovery)
	}

	// The suggestions: the commands are the ones of the design, the names are quoted, and nothing is primary.
	var ids []string
	for _, s := range a.Suggestions {
		ids = append(ids, s.ID)
		if s.Primary {
			t.Errorf("%s is primary in a state that needs nothing", s.ID)
		}
	}
	if want := "add-reader remove-reader:ada remove-reader:dan remove-reader:eve status"; strings.Join(ids, " ") != want {
		t.Errorf("suggestions %q, want %q", strings.Join(ids, " "), want)
	}
	file := filepath.ToSlash(filepath.Join(".pravo", "access", "readers.txt"))
	add := suggestion(a, "add-reader")
	if got, want := stepTexts(add, "command"), []string{"pravo id new --name <name>", "git add " + file, "git commit -m 'Add a reader'", "git push", "pravo protect rewrap"}; !equalStrings(got, want) {
		t.Errorf("add-reader commands %q, want %q", got, want)
	}
	if !add.Steps[1].Placeholder || add.Steps[1].Kind != "command" || add.Steps[0].Kind != "note" {
		t.Errorf("add-reader steps %+v", add.Steps)
	}
	rm := suggestion(a, "remove-reader:dan")
	line := "dan ssh-ed25519 " + blob('d') + " " + key('d')
	if got := stepTexts(rm, "remove-line"); !equalStrings(got, []string{line}) || rm.Steps[0].File != file {
		t.Errorf("remove-line %q %q", got, rm.Steps[0].File)
	}
	if got, want := stepTexts(rm, "command"), []string{"git add " + file, "git commit -m 'dan leaves'", "git push", "pravo protect rewrap", "pravo protect rewrap --all"}; !equalStrings(got, want) {
		t.Errorf("remove-reader commands %q, want %q", got, want)
	}
	if got := stepTexts(suggestion(a, "status"), "command"); !equalStrings(got, []string{"pravo protect status"}) {
		t.Errorf("status %q", got)
	}
	if suggestion(a, "rewrap") != nil || suggestion(a, "identity") != nil || suggestion(a, "seal") != nil {
		t.Error("a suggestion for a state that needs none")
	}

	// A reader sees the values as they always have; the graph says the state is sealed.
	g := p.graph()
	if !g.Project.State.Sealed {
		t.Error("the project does not say the state is sealed")
	}
	for _, n := range g.Nodes {
		if n.Sealed {
			t.Errorf("%s is sealed for a reader", n.Addr)
		}
	}
	if d := p.detail("services.a.db"); len(d.Attrs) == 0 {
		t.Error("a reader sees no attributes")
	}
}

func TestAccessSealedWithoutIdentity(t *testing.T) {
	for name, you := range map[string]*YouFacts{
		"no identity":  {State: "no-identity"},
		"nil":          nil,
		"not a reader": {State: "not-reader", Name: "gus", Key: key('g')},
		"locked":       {State: "locked"},
	} {
		t.Run(name, func(t *testing.T) {
			facts := sealedFacts()
			facts.Store.Locked, facts.You = true, you
			facts.Store.Unreadable = 5
			p := accessProject(t, &fakeProtection{facts: facts})
			a := p.access()
			if a.You == nil || a.You.State == "reader" || !strings.HasSuffix(a.You.Text, "Resources are listed and no value is shown.") {
				t.Fatalf("you %+v", a.You)
			}
			if s := suggestion(a, "identity"); s == nil || !s.Primary || a.Suggestions[0].ID != "identity" {
				t.Errorf("identity: %+v", s)
			}
			if a.Records.Unreadable != 5 || !strings.Contains(a.Records.Text, "5 records are sealed") {
				t.Errorf("records %+v", a.Records)
			}

			// The records are listed, with their types and dependencies, and no value. The
			// plain state has the values; the viewer must not send them.
			g := p.graph()
			sealed := 0
			for _, n := range g.Nodes {
				if n.Presence != "state" && n.Presence != "both" {
					continue
				}
				if n.Sealed {
					sealed++
					if n.Sensitive != 0 || n.NoSchema || n.Type == "" {
						t.Errorf("%s: sensitive %d no_schema %v type %q", n.Addr, n.Sensitive, n.NoSchema, n.Type)
					}
				}
			}
			if sealed != 5 {
				t.Errorf("%d nodes are sealed, want the 5 records", sealed)
			}
			if !strings.Contains(strings.Join(g.Warnings, "\n"), "The state is sealed and 5 records could not be opened, so their values are not shown.") {
				t.Errorf("warnings %q", g.Warnings)
			}
			d := p.detail("services.a.db")
			if len(d.Attrs) != 0 || !d.Node.Sealed || len(d.Notes) == 0 || !strings.Contains(strings.Join(d.Notes, "\n"), "sealed") {
				t.Errorf("detail attrs %d sealed %v notes %q", len(d.Attrs), d.Node.Sealed, d.Notes)
			}
			if len(d.Reach) != 2 || len(d.DependsOn) != 0 {
				// services.a.db depends on nothing; the file and "shared" depend on it.
				t.Errorf("depends on %v reach %v", d.DependsOn, d.Reach)
			}
			if f := p.detail("services.a.file"); len(f.DependsOn) != 1 || f.DependsOn[0] != "services.a.db" || f.Node.Revision == 0 {
				t.Errorf("a sealed record keeps its dependencies and revision: %+v", f.Node)
			}
			for _, o := range g.Outputs {
				if o.Redacted != "sealed" || !o.Sensitive || o.Value != nil {
					t.Errorf("output %+v", o)
				}
			}
			for _, path := range []string{"/api/graph", "/api/detail?addr=services.a.db", "/api/detail?addr=services.a.file", "/api/access", "/api/search?q=a", "/api/plans"} {
				noSecrets(t, path, p.get(path))
			}
			noSecrets(t, "the graph", p.get("/api/graph"), "db.example", "10.0.0.0/8", "eu-1")
			noSecrets(t, "the detail", p.get("/api/detail?addr=services.a.db"), "db.example", "10.0.0.0/8", "eu-1", `"5432"`)
		})
	}
}

func TestAccessDiffers(t *testing.T) {
	facts := sealedFacts()
	// dan is in the file and not in the store; fay is in the store and not in the file.
	facts.Store.Readers = []ReaderFacts{rs("ada", 'a'), rs("eve", 'e'), rs("fay", 'f')}
	facts.File.Readers = []ReaderFacts{rf("ada", 'a', 1), rf("dan", 'd', 2), rf("eve", 'e', 3)}
	a := accessProject(t, &fakeProtection{facts: facts}).access()
	if a.File.Verdict != "differs" || !equalStrings(a.File.Added, []string{"dan"}) || !equalStrings(a.File.Removed, []string{"fay"}) {
		t.Fatalf("file %+v", a.File)
	}
	if want := "The readers file and the store differ: dan is in the file and not in the store; fay is in the store and not in the file. Writers refuse to write until they match."; a.File.Text != want {
		t.Errorf("text %q", a.File.Text)
	}
	if a.Suggestions[0].ID != "rewrap" || !a.Suggestions[0].Primary {
		t.Fatalf("first suggestion %+v", a.Suggestions[0])
	}
	if got, want := stepTexts(&a.Suggestions[0], "command"), []string{"pravo protect rewrap", "git log -p -- .pravo/access/readers.txt"}; !equalStrings(got, want) {
		t.Errorf("rewrap commands %q", got)
	}
	marks := map[string][2]bool{}
	for _, r := range a.Readers {
		marks[r.Name] = [2]bool{r.InFile, r.InStore}
	}
	if marks["ada"] != [2]bool{true, true} || marks["dan"] != [2]bool{true, false} || marks["fay"] != [2]bool{false, true} {
		t.Errorf("marks %v", marks)
	}
	// A reader who is only in the store has no line to remove from the file.
	if suggestion(a, "remove-reader:fay") != nil {
		t.Error("a removal for a reader the file does not list")
	}
	if suggestion(a, "remove-reader:dan") == nil {
		t.Error("no removal for dan, who is in the file")
	}
	// Two names for the same key are one reader.
	facts = sealedFacts()
	facts.File.Readers[1].Name = "daniel"
	if v := accessProject(t, &fakeProtection{facts: facts}).access().File.Verdict; v != "match" {
		t.Errorf("a renamed reader (same key): %q", v)
	}
}

func TestAccessMissingFile(t *testing.T) {
	facts := sealedFacts()
	facts.File = FileFacts{State: "missing"}
	p := accessProject(t, &fakeProtection{facts: facts})
	a := p.access()
	if a.File.Verdict != "missing" || !strings.HasPrefix(a.File.Text, "No readers file was found in .pravo/access. Writers take the list of readers from the repository, so a sealed state cannot be written from this checkout.") {
		t.Errorf("file %+v", a.File)
	}
	for _, r := range a.Readers {
		if r.InFile || !r.InStore {
			t.Errorf("reader %+v", r)
		}
	}
	// The readers of the store have no line to remove.
	if suggestion(a, "remove-reader:ada") != nil {
		t.Error("a removal without a line")
	}
}

func TestAccessInvalidFile(t *testing.T) {
	facts := sealedFacts()
	facts.File = FileFacts{State: "invalid", Errors: []LineError{
		{Line: 3, Reason: `want 4 fields: NAME ssh-ed25519 BASE64 x25519:BASE64URL (got 3)`},
		{Line: 5, Reason: `name "x; touch /tmp/pwned": use letters, digits, . _ - (at most 64, starting with a letter or digit)`},
		{Line: 7, Reason: "signing key type \"\u202erm -rf\" is not supported\nsecond line"},
	}}
	p := accessProject(t, &fakeProtection{facts: facts})
	a := p.access()
	if a.File.Verdict != "invalid" || a.File.Text != "The readers file does not load: line 3: want 4 fields: NAME ssh-ed25519 BASE64 x25519:BASE64URL (got 3)." {
		t.Errorf("file %+v", a.File)
	}
	if len(a.File.Errors) != 3 || a.File.Errors[0].Line != 3 {
		t.Fatalf("errors %+v", a.File.Errors)
	}
	body := string(p.get("/api/access"))
	for _, leak := range []string{"touch", "pwned", "rm -rf", "\u202e", "\n"} {
		if strings.Contains(body, leak) {
			t.Errorf("a reason echoes %q: %+v", leak, a.File.Errors)
		}
	}
	// With no file to read, the store's readers are listed and nothing is added or removed.
	if len(a.File.Added)+len(a.File.Removed) != 0 || a.Suggestions[0].Primary && a.Suggestions[0].ID == "rewrap" {
		t.Errorf("suggestions for an invalid file: %+v", a.Suggestions[0])
	}
}

func TestAccessMigrating(t *testing.T) {
	facts := sealedFacts()
	facts.Store.State = "migrating"
	facts.Store.Migration = &MigrationFacts{Started: time.Date(2026, 10, 22, 9, 30, 0, 0, time.UTC), By: "ada", Done: 1204, Total: 2001}
	a := accessProject(t, &fakeProtection{facts: facts}).access()
	if a.State != "migrating" || a.Migration == nil || a.Migration.Done != 1204 {
		t.Fatalf("%q %+v", a.State, a.Migration)
	}
	if want := "Sealing started by ada on 2026-10-22 09:30 UTC and has not finished: 1,204 of 2,001 records. Writers are refused until it finishes."; a.Summary != want {
		t.Errorf("summary %q", a.Summary)
	}
	if a.Suggestions[0].ID != "finish" || !a.Suggestions[0].Primary || !equalStrings(stepTexts(&a.Suggestions[0], "command"), []string{"pravo protect init --encrypt --allow-local"}) {
		t.Errorf("suggestion %+v", a.Suggestions[0])
	}
	if suggestion(a, "add-reader") != nil {
		t.Error("a reader is added while the seal has not finished")
	}
}

// The finish step of a local state names --allow-local as the seal does; the step
// of a state in a bucket does not.
func TestAccessFinishOfALocalStateAllowsLocal(t *testing.T) {
	facts := sealedFacts()
	facts.Store.State = "migrating"
	facts.Store.Migration = &MigrationFacts{Started: time.Date(2026, 10, 22, 9, 30, 0, 0, time.UTC), By: "ada", Done: 1, Total: 2}
	in := accessInput{rev: "r", location: "x", backend: "local", format: 3, facts: facts, path: "/p/.pravo/access/readers.txt", dir: "/p"}
	a := buildAccess(in)
	if got := stepTexts(suggestion(*a, "finish"), "command"); !equalStrings(got, []string{"pravo protect init --encrypt --allow-local"}) {
		t.Errorf("finish for a local state: %q", got)
	}
	in.backend = "s3"
	a = buildAccess(in)
	if got := stepTexts(suggestion(*a, "finish"), "command"); !equalStrings(got, []string{"pravo protect init --encrypt"}) {
		t.Errorf("finish for a bucket: %q", got)
	}
}

// A name that comes from the store or the file can be anything. It is listed, with
// the characters that hide things taken out, and it reaches no command and no line.
func TestAccessHostileNames(t *testing.T) {
	hostile := []string{"x; touch /tmp/pwned", "$(id)", "`id`", "a\nb", "it's", "../x", "ev\u202eil", "a b", "-rf", "", strings.Repeat("a", 65), "a|b", "a&&b", "a>b", "ünï"}
	facts := sealedFacts()
	var file, store []ReaderFacts
	for i, n := range hostile {
		c := byte('h' + i)
		file = append(file, rf(n, c, i+4))
		store = append(store, rs(n+"!", c+20))
	}
	facts.File.Readers = append(facts.File.Readers, file...)
	facts.Store.Readers = append(facts.Store.Readers, store...)
	p := accessProject(t, &fakeProtection{facts: facts})
	a := p.access()
	bad := 0
	for _, r := range a.Readers {
		if strings.ContainsAny(r.Name, "\n\u202e") {
			t.Errorf("a name with a control character: %q", r.Name)
		}
		if !r.NameOK {
			bad++
		}
	}
	if bad != 2*len(hostile) {
		t.Errorf("%d names are marked as not usable, want %d", bad, 2*len(hostile))
	}
	safe := regexp.MustCompile(`^(pravo|git) [A-Za-z0-9 _.,:=/@+%<>'-]*$`)
	for _, s := range a.Suggestions {
		if strings.Contains(s.ID, ";") || strings.ContainsAny(s.ID, " `$\n") {
			t.Errorf("suggestion id %q", s.ID)
		}
		for _, st := range s.Steps {
			text := st.Text
			if strings.ContainsAny(text, "\n\r") {
				t.Errorf("%s: a step with a newline: %q", s.ID, text)
			}
			if st.Kind == "command" && !safe.MatchString(text) {
				t.Errorf("%s: a command with a character the templates do not use: %q", s.ID, text)
			}
			for _, n := range hostile {
				if n == "" || len(n) < 3 {
					continue
				}
				if strings.Contains(text, n) {
					t.Errorf("%s: a step contains the name %q: %q", s.ID, n, text)
				}
			}
		}
		if strings.HasPrefix(s.ID, "remove-reader:") {
			if name := strings.TrimPrefix(s.ID, "remove-reader:"); !readerNameRE.MatchString(name) {
				t.Errorf("a removal for %q", name)
			}
		}
	}
	// The three readers of the file whose names are fine are the ones that can be removed.
	removals := 0
	for _, s := range a.Suggestions {
		if strings.HasPrefix(s.ID, "remove-reader:") {
			removals++
		}
	}
	if removals != 3+0 {
		t.Errorf("%d removals, want 3 (ada, dan, eve)", removals)
	}
}

// A name that passes the rule but not the shell's idea of a word is quoted.
func TestAccessCommandsQuoteTheName(t *testing.T) {
	facts := sealedFacts()
	facts.File.Readers = []ReaderFacts{rf("a.b-c_d", 'a', 1), rf("dan", 'd', 2), rf("eve", 'e', 3)}
	a := accessProject(t, &fakeProtection{facts: facts}).access()
	s := suggestion(a, "remove-reader:a.b-c_d")
	if s == nil {
		t.Fatal("no removal")
	}
	if got := stepTexts(s, "command")[1]; got != "git commit -m 'a.b-c_d leaves'" {
		t.Errorf("commit %q", got)
	}
}

func TestAccessNeverEchoesTheReadersFile(t *testing.T) {
	// The file is not read by the viewer, the adapter reads it; what the viewer sends of it is the
	// parsed parts. A line that fails to parse is a number and a reason.
	facts := sealedFacts()
	secret := "PRIVATE-X25519-BYTES-0123456789"
	facts.File = FileFacts{State: "invalid", Errors: []LineError{{Line: 1, Reason: "want 4 fields (got 1)"}}}
	p := accessProject(t, &fakeProtection{facts: facts})
	noSecrets(t, "access", p.get("/api/access"), secret, "passphrase")
	if strings.Contains(string(p.get("/api/access")), "PRIVATE") {
		t.Error("access names a private key")
	}
}

func TestAccessWatched(t *testing.T) {
	f := &fakeProtection{facts: sealedFacts()}
	f.set(func(fa *Facts) { fa.File.Readers = fa.File.Readers[:2] })
	p := accessProject(t, f)
	events := eventsOf(t, p)
	events(500 * time.Millisecond) // the held revision
	before := p.access()
	if before.File.Verdict != "differs" {
		t.Fatalf("verdict %q", before.File.Verdict)
	}
	// Someone commits the file.
	path := filepath.Join(p.dir, ".pravo", "access", "readers.txt")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("ada ssh-ed25519 x x25519:y\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	f.set(func(fa *Facts) { fa.File.Readers = sealedFacts().File.Readers })
	ev, ok := events(5 * time.Second)
	if !ok || len(ev.What) != 1 || ev.What[0] != "access" {
		t.Fatalf("event %+v %v, want what=[access]", ev, ok)
	}
	waitFor(t, "the verdict to change", func() bool { return p.access().File.Verdict == "match" })
	if a := p.access(); a.Revision == before.Revision || a.Revision != p.graph().Revision {
		t.Errorf("revision %q, before %q, graph %q", a.Revision, before.Revision, p.graph().Revision)
	}
	// The path that was asked about is the resolved one.
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, got := range f.paths {
		if got != path {
			t.Errorf("the readers file was looked for at %q, want %q", got, path)
		}
	}
}

func TestAccessDirIsFoundAboveTheProject(t *testing.T) {
	repo := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repo, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(repo, ".pravo", "access"), 0o755); err != nil {
		t.Fatal(err)
	}
	proj := filepath.Join(repo, "infra", "prod")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	if got, want := defaultAccessDir(proj), filepath.Join(repo, ".pravo", "access"); got != want {
		t.Errorf("found %q, want %q", got, want)
	}
	// Nothing found: the project directory's.
	bare := t.TempDir()
	if err := os.MkdirAll(filepath.Join(bare, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	sub := filepath.Join(bare, "x")
	os.MkdirAll(sub, 0o755)
	if got, want := defaultAccessDir(sub), filepath.Join(sub, ".pravo", "access"); got != want {
		t.Errorf("default %q, want %q", got, want)
	}
}

// Nothing the viewer does writes: not the project, not the state, not the bucket,
// whatever the state is, and the requests for the Access view are among them.
func TestAccessSessionWritesNothing(t *testing.T) {
	var attempts atomic.Int32
	dir := t.TempDir()
	writeSpec(t, dir, specCUE)
	st, err := state.Open(filepath.Join(dir, "pravo.state"))
	if err != nil {
		t.Fatal(err)
	}
	seed(t, st)
	facts := sealedFacts()
	facts.Store.Locked, facts.You = true, &YouFacts{State: "no-identity"}
	f := &fakeProtection{facts: facts}
	p := start(t, dir, st, Options{
		Protection: f.fn,
		AccessDir:  filepath.Join(dir, ".pravo", "access"),
		OpenState: func(ctx context.Context, root *config.Root) (*state.Store, error) {
			return state.OpenBackend(ctx, &countingBackend{Backend: ReadOnly(&state.LocalBackend{Dir: root.StateFile()}), n: &attempts})
		},
	})
	before := digestTree(t, dir)
	session(t, p)
	for _, path := range []string{"/api/access", "/api/access?x=1"} {
		p.get(path)
	}
	p.do("POST", "/api/access", nil)
	if after := digestTree(t, dir); after != before {
		t.Error("the project directory or the state changed during a session on a sealed store")
	}
	if n := attempts.Load(); n != 0 {
		t.Errorf("%d writes to the bucket were attempted", n)
	}
}

// countingBackend counts the writes asked of a backend, whether or not it refuses
// them.
type countingBackend struct {
	state.Backend
	n *atomic.Int32
}

func (c *countingBackend) Put(ctx context.Context, key string, data []byte) error {
	if key != "meta.json" {
		c.n.Add(1)
	}
	return c.Backend.Put(ctx, key, data)
}

func (c *countingBackend) PutIfAbsent(ctx context.Context, key string, data []byte) (bool, error) {
	c.n.Add(1)
	return c.Backend.PutIfAbsent(ctx, key, data)
}

func (c *countingBackend) Delete(ctx context.Context, key string) error {
	c.n.Add(1)
	return c.Backend.Delete(ctx, key)
}

func (c *countingBackend) DeleteIf(ctx context.Context, key string, match func([]byte) bool) (bool, error) {
	c.n.Add(1)
	return c.Backend.DeleteIf(ctx, key, match)
}

func (c *countingBackend) ReplaceIf(ctx context.Context, key string, update func([]byte) ([]byte, bool)) (bool, error) {
	c.n.Add(1)
	return c.Backend.ReplaceIf(ctx, key, update)
}

// The read-only backend refuses every write the interface has: a method added to
// state.Backend does not compile there until someone decides.
func TestReadOnlyBackendRefusesEveryWrite(t *testing.T) {
	dir := t.TempDir()
	b := ReadOnly(&state.LocalBackend{Dir: dir})
	ctx := context.Background()
	if err := b.Put(ctx, "resources/x.json", []byte("{}")); err == nil {
		t.Error("Put")
	}
	if ok, err := b.PutIfAbsent(ctx, "rec/x", []byte("{}")); err == nil || ok {
		t.Error("PutIfAbsent")
	}
	if ok, err := b.ReplaceIf(ctx, "resources/x.json", func([]byte) ([]byte, bool) { return []byte("{}"), true }); err == nil || ok {
		t.Error("ReplaceIf")
	}
	if ok, err := b.DeleteIf(ctx, "resources/x.json", func([]byte) bool { return true }); err == nil || ok {
		t.Error("DeleteIf")
	}
	if err := b.Delete(ctx, "resources/x.json"); err == nil {
		t.Error("Delete")
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Errorf("the directory holds %d entries", len(entries))
	}
}

// The server does not start a process, call another program or reach out: the
// page offers text, and a person runs it.
func TestViewerCannotRunAnything(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		parsed, err := parser.ParseFile(token.NewFileSet(), f, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatal(err)
		}
		for _, imp := range parsed.Imports {
			switch path := strings.Trim(imp.Path.Value, `"`); path {
			case "os/exec", "plugin", "net/rpc":
				t.Errorf("%s imports %s", f, path)
			}
		}
		src, _ := os.ReadFile(f)
		for _, bad := range []string{"syscall.Exec", "exec.Command", "http.Get(", "http.Post(", "http.DefaultClient", "http.Client{"} {
			if strings.Contains(string(src), bad) {
				t.Errorf("%s uses %s", f, bad)
			}
		}
	}
}

func TestAccessNeedsAToken(t *testing.T) {
	p := accessProject(t, &fakeProtection{facts: sealedFacts()})
	req, _ := http.NewRequest("GET", p.base+"/api/access", nil)
	resp, body := p.send(req)
	if resp.StatusCode != 401 || strings.Contains(string(body), "ada") {
		t.Errorf("without a token: %d %s", resp.StatusCode, body)
	}
}

// A record is sealed for the viewer when the state is sealed and the store gave no
// attributes for it, or no key was given at all.
func TestSealedRecord(t *testing.T) {
	plain := &state.Resource{Attributes: []byte(`{"a":1}`)}
	empty := &state.Resource{}
	for _, c := range []struct {
		name string
		a    accessModel
		rec  *state.Resource
		want bool
	}{
		{"not sealed", accessModel{}, empty, false},
		{"sealed, opened", accessModel{protected: true}, plain, false},
		{"sealed, no attributes, none unreadable", accessModel{protected: true}, empty, false},
		{"sealed, no attributes, some unreadable", accessModel{protected: true, unreadable: true}, empty, true},
		{"sealed, attributes, some unreadable", accessModel{protected: true, unreadable: true}, plain, false},
		{"sealed, no key", accessModel{protected: true, locked: true}, plain, true},
		{"no record", accessModel{protected: true, locked: true}, nil, false},
	} {
		if got := c.a.sealedRec(c.rec); got != c.want {
			t.Errorf("%s: %v, want %v", c.name, got, c.want)
		}
	}
}

// A protection that panics, or names a state that does not exist, does not take the
// viewer down: the page says so and shows what the format says.
func TestAccessSurvivesBadFacts(t *testing.T) {
	dir := t.TempDir()
	writeSpec(t, dir, specCUE)
	st, err := state.Open(filepath.Join(dir, "pravo.state"))
	if err != nil {
		t.Fatal(err)
	}
	seed(t, st)
	p := start(t, dir, st, Options{Protection: func(context.Context, *state.Store, string) Facts { panic("boom") }})
	a := p.access()
	if a.State != "off" || len(a.Problems) != 1 || !strings.Contains(a.Problems[0], "could not be read") {
		t.Errorf("a protection that panics: %q %v", a.State, a.Problems)
	}
	if g := p.graph(); len(g.Nodes) == 0 {
		t.Error("the graph is gone")
	}
	dir2 := t.TempDir()
	writeSpec(t, dir2, specCUE)
	st2, _ := state.Open(filepath.Join(dir2, "pravo.state"))
	seed(t, st2)
	p2 := start(t, dir2, st2, Options{Protection: func(context.Context, *state.Store, string) Facts { return Facts{Store: StoreFacts{State: "<script>"}} }})
	a2 := p2.access()
	if a2.State != "off" || len(a2.Problems) != 1 || strings.Contains(string(p2.get("/api/access")), "<script>") {
		t.Errorf("an unknown state: %q %v", a2.State, a2.Problems)
	}
}

// The page shows what the server sends as text: no module builds markup from a
// string, so a name or a command from the store cannot become an element.
func TestInterfaceBuildsNoMarkupFromStrings(t *testing.T) {
	files, err := filepath.Glob(filepath.Join("web", "assets", "*.js"))
	if err != nil || len(files) == 0 {
		t.Skip("the interface is not in the tree")
	}
	for _, f := range files {
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, bad := range []string{"innerHTML", "outerHTML", "insertAdjacentHTML", "document.write", "eval(", "new Function"} {
			if strings.Contains(string(src), bad) {
				t.Errorf("%s uses %s", f, bad)
			}
		}
	}
}

// With no key every sealed record is out of reach, and the page counts them even when
// the facts do not.
func TestAccessCountsWhatNoKeyOpens(t *testing.T) {
	facts := sealedFacts()
	facts.Store.Locked, facts.Store.Unreadable, facts.Store.Records, facts.Store.Plain = true, 0, 5, 1
	a := buildAccess(accessInput{rev: "r", location: "x", backend: "s3", format: 3, facts: facts, path: "/p/.pravo/access/readers.txt", dir: "/p"})
	if a.Records.Unreadable != 4 || a.Records.Text != "4 records are sealed and no key was given to open them." {
		t.Errorf("records %+v", a.Records)
	}
	facts.You = &YouFacts{State: "reader", Key: key('a')}
	facts.Store.Locked = false
	a = buildAccess(accessInput{rev: "r", location: "x", backend: "s3", format: 3, facts: facts, path: "/p/.pravo/access/readers.txt", dir: "/p"})
	if a.Records.Unreadable != 0 || a.You.Text != "You are a reader." {
		t.Errorf("records %+v you %+v", a.Records, a.You)
	}
}

// An identity that is a reader but whose passphrase was not available is not told to
// send its line to a reader, and a key that is not a reader is not told that none
// was given.
func TestAccessWordsFitTheIdentity(t *testing.T) {
	facts := sealedFacts()
	facts.Store.Locked = true
	facts.You = &YouFacts{State: "locked"}
	a := buildAccess(accessInput{rev: "r", location: "x", backend: "s3", format: 3, facts: facts, path: "/p/.pravo/access/readers.txt", dir: "/p", locked: true})
	var got *Suggestion
	for i := range a.Suggestions {
		if a.Suggestions[i].ID == "identity" {
			got = &a.Suggestions[i]
		}
	}
	if got == nil || !got.Primary || strings.Contains(got.Why, "not a reader") || !strings.Contains(got.Why, "passphrase") {
		t.Fatalf("the suggestion for a locked identity: %+v", got)
	}
	for _, st := range got.Steps {
		if strings.Contains(st.Text, "id new") || strings.Contains(st.Text, "id show") {
			t.Errorf("a locked identity is told to make or print one: %q", st.Text)
		}
	}
	facts.You = &YouFacts{State: "no-identity"}
	a = buildAccess(accessInput{rev: "r", location: "x", backend: "s3", format: 3, facts: facts, path: "/p/.pravo/access/readers.txt", dir: "/p", locked: true})
	for _, sg := range a.Suggestions {
		if sg.ID == "identity" && strings.Contains(sg.Why, "This key is not a reader") {
			t.Errorf("a viewer that was given no identity is told its key is not a reader: %q", sg.Why)
		}
	}
	facts.You = &YouFacts{State: "not-reader", Name: "gus", Key: key('g')}
	a = buildAccess(accessInput{rev: "r", location: "x", backend: "s3", format: 3, facts: facts, path: "/p/.pravo/access/readers.txt", dir: "/p", locked: true})
	if strings.Contains(a.Records.Text, "no key was given") || !strings.Contains(a.Records.Text, "this key cannot open") {
		t.Errorf("records text for a key that is not a reader: %q", a.Records.Text)
	}
}

// A readers file kept somewhere else than .pravo/access is named on the commands
// that read it, and one kept there is not: a paste must read the file the page shows.
func TestAccessCommandsNameAnOddReadersFile(t *testing.T) {
	facts := sealedFacts()
	facts.You = &YouFacts{State: "reader", Key: key('a')}
	odd := buildAccess(accessInput{rev: "r", location: "x", backend: "s3", format: 3, facts: facts, path: "/p/who reads/readers.txt", dir: "/p"})
	var cmds []string
	for _, s := range odd.Suggestions {
		cmds = append(cmds, stepTexts(&s, "command")...)
	}
	n := 0
	for _, c := range cmds {
		if strings.HasPrefix(c, "pravo protect") {
			n++
			if !strings.HasSuffix(c, " --access-dir 'who reads'") {
				t.Errorf("command %q does not name the directory of the readers file", c)
			}
		}
	}
	if n == 0 {
		t.Fatal("no protect command was suggested")
	}
	plain := buildAccess(accessInput{rev: "r", location: "x", backend: "s3", format: 3, facts: facts, path: "/p/.pravo/access/readers.txt", dir: "/p"})
	for _, s := range plain.Suggestions {
		for _, c := range stepTexts(&s, "command") {
			if strings.Contains(c, "--access-dir") {
				t.Errorf("command %q names the default directory", c)
			}
		}
	}
}

// A sealed state with more than one group suggests dropping the groups that nothing
// is sealed in, and says to seal the records again first when some are under an old
// group. The commands only suggest: the viewer runs none of them.
func TestAccessSuggestsPruningOldGroups(t *testing.T) {
	facts := sealedFacts()
	facts.Store.Groups = []GroupFacts{{ID: "g1", Locks: 3, Records: 2}, {ID: "g2", Current: true, Locks: 2, Records: 3}}
	p := accessProject(t, &fakeProtection{facts: facts})
	a := p.access()
	s := suggestion(a, "prune")
	if s == nil || s.Primary {
		t.Fatalf("prune: %+v", s)
	}
	if got, want := stepTexts(s, "command"), []string{"pravo protect rewrap --all", "pravo protect gc --dry-run", "pravo protect gc"}; !equalStrings(got, want) {
		t.Errorf("commands %q, want %q", got, want)
	}
	if suggestion(a, "reseal") == nil {
		t.Error("reseal is not suggested although records are under an old group")
	}
	// Everything is under the current group already: there is nothing to seal again.
	facts.Store.Groups = []GroupFacts{{ID: "g1", Locks: 3}, {ID: "g2", Current: true, Locks: 2, Records: 5}}
	p = accessProject(t, &fakeProtection{facts: facts})
	if got, want := stepTexts(suggestion(p.access(), "prune"), "command"), []string{"pravo protect gc --dry-run", "pravo protect gc"}; !equalStrings(got, want) {
		t.Errorf("commands %q, want %q", got, want)
	}
	if suggestion(p.access(), "reseal") != nil {
		t.Error("reseal is suggested when no record is under an old group")
	}
	// One group, and a state that is not sealed: no suggestion.
	if suggestion(accessProject(t, &fakeProtection{facts: sealedFacts()}).access(), "prune") != nil {
		t.Error("prune is suggested for a state with one group")
	}
	if suggestion(accessProject(t, nil).access(), "prune") != nil {
		t.Error("prune is suggested for a state that is not sealed")
	}
}

// A readers file that loads, and a state that is not sealed: the repository
// expects a seal that the state does not have.
func TestAccessExpectedSealed(t *testing.T) {
	for _, state := range []string{"off", "immutable"} {
		t.Run(state, func(t *testing.T) {
			format := map[string]int{"off": 1, "immutable": 2}[state]
			f := &fakeProtection{facts: Facts{Store: StoreFacts{State: state, Format: format, Records: 5},
				File: FileFacts{State: "ok", Readers: []ReaderFacts{rf("ada", 'a', 1)}}}}
			p := accessProject(t, f)
			a := p.access()
			if a.File.Verdict != "expected-sealed" || a.File.Count != 1 {
				t.Fatalf("file %+v", a.File)
			}
			if want := "The repository lists readers in .pravo/access/readers.txt, but the state is not sealed. Its meta.json may have been changed."; a.File.Text != want {
				t.Errorf("text %q", a.File.Text)
			}
			if a.State != state || a.You != nil || len(a.File.Added)+len(a.File.Removed) != 0 {
				t.Errorf("state %q you %+v file %+v", a.State, a.You, a.File)
			}
			s := suggestion(a, "check-downgrade")
			if s == nil || !s.Primary || a.Suggestions[0].ID != "check-downgrade" {
				t.Fatalf("suggestions %+v", a.Suggestions)
			}
			// The project is a local directory, so sealing it needs --allow-local.
			if got, want := stepTexts(s, "command"), []string{"pravo protect status", "pravo protect init --encrypt --allow-local"}; !equalStrings(got, want) {
				t.Errorf("commands %q, want %q", got, want)
			}
			if got := stepTexts(s, "note"); len(got) != 1 || !strings.HasPrefix(got[0], "If you did not mean to seal this state, remove the readers file. To seal it: pravo protect init --encrypt") {
				t.Errorf("notes %q", got)
			}
			if suggestion(a, "seal") == nil {
				t.Error("the seal card is gone")
			}
		})
	}
	t.Run("a file that does not load, or is missing, says what it did", func(t *testing.T) {
		for _, c := range []struct{ file, verdict string }{{"missing", "none"}, {"invalid", "none"}} {
			f := &fakeProtection{facts: Facts{Store: StoreFacts{State: "off", Format: 1}, File: FileFacts{State: c.file, Errors: []LineError{{Line: 1, Reason: "x"}}}}}
			a := accessProject(t, f).access()
			if a.File.Verdict != c.verdict || suggestion(a, "check-downgrade") != nil {
				t.Errorf("%s: %+v", c.file, a.File)
			}
		}
	})
}

// Records that are not sealed yet are the ones a seal that did not finish left, and
// the sentence says what seals them, in the singular as well.
func TestAccessSaysWhatSealsTheRecordsThatAreNot(t *testing.T) {
	for n, want := range map[int]string{1: "1 record is not sealed yet: finishing the seal seals it.", 3: "3 records are not sealed yet: finishing the seal seals them."} {
		if got := recordsText(AccessRecords{Plain: n}, false); got != want {
			t.Errorf("%d plain: %q, want %q", n, got, want)
		}
	}
}
