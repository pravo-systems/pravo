package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/pravo-systems/pravo/internal/config"
	"github.com/pravo-systems/pravo/internal/protect"
	"github.com/pravo-systems/pravo/internal/serve"
	"github.com/pravo-systems/pravo/internal/state"
	"github.com/pravo-systems/pravo/internal/ui"
)

// protectedMeta makes the meta.json of a state sealed to the named readers, and the
// readers.
func protectedMeta(t *testing.T, names ...string) (*protect.Meta, []protect.Reader) {
	t.Helper()
	var readers []protect.Reader
	for _, n := range names {
		id, err := protect.NewIdentity(n, "")
		if err != nil {
			t.Fatal(err)
		}
		readers = append(readers, id.AsReader())
		id.Close()
	}
	m, k, err := protect.NewMeta("lineage-test", readers)
	if err != nil {
		t.Fatal(err)
	}
	k.Close()
	return m, readers
}

func viewerReadersFile(t *testing.T, dir string, readers []protect.Reader) {
	t.Helper()
	acc := filepath.Join(dir, ".pravo", "access")
	if err := os.MkdirAll(acc, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(acc, "readers.txt"), protect.FormatReaders(readers), 0o644); err != nil {
		t.Fatal(err)
	}
}

// viewer starts the server of "pravo serve" over a project directory with the
// protection that a test gives it, and returns a function that reads an endpoint.
func viewer(t *testing.T, dir string, prot func(accessDir string) serve.Protection) func(path string) []byte {
	t.Helper()
	opts := serve.Options{Dir: dir, Version: "test", Poll: 40 * time.Millisecond, AccessDir: loadReadersFile(dir, "").Dir,
		OpenState: func(ctx context.Context, root *config.Root) (*state.Store, error) {
			return state.Open(filepath.Join(dir, "pravo.state"))
		}}
	if prot != nil {
		opts.Protection = prot(opts.AccessDir)
	}
	srv, err := serve.New(opts)
	if err != nil {
		t.Fatal(err)
	}
	ln, err := serve.Listen(0)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- srv.Serve(ctx, ln) }()
	t.Cleanup(func() { cancel(); <-done })
	base := "http://127.0.0.1:" + itoa(ln.Addr().(*net.TCPAddr).Port)
	return func(path string) []byte {
		t.Helper()
		req, _ := http.NewRequest("GET", base+path, nil)
		req.Header.Set("Authorization", "Bearer "+srv.Token())
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var body strings.Builder
		buf := make([]byte, 4096)
		for {
			n, err := resp.Body.Read(buf)
			body.Write(buf[:n])
			if err != nil {
				break
			}
		}
		if resp.StatusCode != 200 {
			t.Fatalf("GET %s: %d %s", path, resp.StatusCode, body.String())
		}
		return []byte(body.String())
	}
}

const s3Settings = `$settings: state: {backend: "s3", bucket: "state", prefix: "team/app", region: "us-east-1", endpoint: "http://127.0.0.1:1", path_style: true, access_key: "test", secret_key: "test"}
`

// facts is the protection of a state for a test: the state that meta.json says, with
// the records spread over its groups.
func sealedProtection(m *protect.Meta, you state.You) state.Protection {
	return state.Protection{State: "sealed", Format: 3, Location: "fake", Sealed: m, Records: 4, ByGroup: map[string]int{m.Current: 4}, You: you,
		History: []state.HistoryEntry{{At: time.Date(2026, 10, 22, 10, 41, 0, 0, time.UTC), By: "ada", What: "sealed", Added: []string{"ada"}}}}
}

func protectionOf(p state.Protection, locked bool, id viewerIdentity) func(accessDir string) serve.Protection {
	return func(string) serve.Protection {
		return func(ctx context.Context, st *state.Store, path string) serve.Facts {
			return accessFacts(p, locked, id, loadReadersFile(filepath.Dir(path), filepath.Dir(path)))
		}
	}
}

// Every command that the page offers to copy names a command and flags that this
// build has. The state is made to look like each state in turn.
func TestViewerSuggestsOnlyRealCommands(t *testing.T) {
	m, readers := protectedMeta(t, "ada", "dan", "eve")
	adaKey := readers[0].Key.String()
	reader := state.You{State: "reader", Name: "ada", Key: adaKey}
	type scene struct {
		name   string
		s3     bool
		file   []protect.Reader
		prot   state.Protection
		locked bool
		id     viewerIdentity
	}
	migrating := sealedProtection(m, reader)
	migrating.State = "migrating"
	migrating.Migration = &state.Migration{Started: time.Now(), By: "ada", Done: 3, Total: 9}
	partial := sealedProtection(m, reader)
	partial.Unreadable = 2
	partial.ByGroup = map[string]int{"g1": 2, "g0": 2}
	two := *m
	two.Groups = append(slices.Clone(m.Groups), protect.Group{ID: "g2", Locks: m.Groups[0].Locks})
	two.Current = "g2"
	older := sealedProtection(&two, reader)
	older.ByGroup = map[string]int{"g1": 2, "g2": 2}
	scenes := []scene{
		{name: "not sealed, a local directory", prot: state.Protection{State: "off", Format: 1}},
		{name: "not sealed, a bucket", s3: true, prot: state.Protection{State: "off", Format: 1}},
		{name: "not sealed, a bucket, readers listed", s3: true, file: readers, prot: state.Protection{State: "off", Format: 1}},
		{name: "immutable", s3: true, prot: state.Protection{State: "immutable", Format: 2}},
		{name: "immutable, readers listed", s3: true, file: readers, prot: state.Protection{State: "immutable", Format: 2}},
		{name: "not sealed, a local directory, readers listed", file: readers, prot: state.Protection{State: "off", Format: 1}},
		{name: "sealed, a reader, in step", s3: true, file: readers, prot: sealedProtection(m, reader)},
		{name: "sealed, the file differs", s3: true, file: readers[:2], prot: sealedProtection(m, reader)},
		{name: "sealed, no file", s3: true, prot: sealedProtection(m, reader)},
		{name: "sealed, records of an old group", s3: true, file: readers, prot: partial},
		{name: "sealed, two groups", s3: true, file: readers, prot: older},
		{name: "sealed, no identity", s3: true, file: readers, prot: sealedProtection(m, state.You{State: "no-identity"}), locked: true},
		{name: "sealed, identity locked", s3: true, file: readers, prot: sealedProtection(m, state.You{State: "no-identity"}), locked: true, id: viewerIdentity{locked: true}},
		{name: "sealed, not a reader", s3: true, file: readers, prot: sealedProtection(m, state.You{State: "no-identity"}), locked: true, id: viewerIdentity{refused: true, name: "gus", key: readers[0].Key.String()}},
		{name: "migrating", s3: true, file: readers, prot: migrating},
	}
	seen := map[string]bool{}
	checked := 0
	for _, sc := range scenes {
		t.Run(sc.name, func(t *testing.T) {
			spec := "package estate\n\nsize: 3\n"
			if sc.s3 {
				spec += s3Settings
			}
			dir := specDir(t, map[string]string{"main.cue": spec})
			if sc.file != nil {
				viewerReadersFile(t, dir, sc.file)
			}
			get := viewer(t, dir, protectionOf(sc.prot, sc.locked, sc.id))
			var a serve.Access
			if err := json.Unmarshal(get("/api/access"), &a); err != nil {
				t.Fatal(err)
			}
			if len(a.Suggestions) == 0 {
				t.Fatalf("no suggestions in a state of %q", a.State)
			}
			root := newRootCmd()
			// The commands exist once the id and protect commands are in this build; a
			// build without them has nothing to check a suggestion against.
			real := findSub(root, "protect") != nil && findSub(root, "id") != nil
			for _, s := range a.Suggestions {
				seen[strings.SplitN(s.ID, ":", 2)[0]] = true
				for _, st := range s.Steps {
					if st.Kind != "command" {
						continue
					}
					switch {
					case strings.HasPrefix(st.Text, "pravo "):
						if !real {
							continue
						}
						checked++
						if msg := checkPravoLine(root, st.Text); msg != "" {
							t.Errorf("%s: %q: %s", s.ID, st.Text, msg)
						}
					case strings.HasPrefix(st.Text, "git "):
					default:
						t.Errorf("%s: a command that is neither pravo nor git: %q", s.ID, st.Text)
					}
				}
			}
		})
	}
	if checked < 20 && findSub(newRootCmd(), "protect") != nil {
		t.Errorf("only %d pravo commands were checked", checked)
	}
	for _, id := range []string{"seal", "check-downgrade", "finish", "identity", "rewrap", "add-reader", "remove-reader", "reseal", "prune", "status"} {
		if !seen[id] {
			t.Errorf("no scene offered the suggestion %s", id)
		}
	}
}

// The verdict of the page between the readers file and the store is the one of
// protect.Meta.Status, which the commands use.
func TestViewerVerdictAgreesWithProtect(t *testing.T) {
	m, readers := protectedMeta(t, "ada", "dan", "eve")
	other, others := protectedMeta(t, "fay", "gus")
	_ = other
	renamed := append([]protect.Reader(nil), readers...)
	renamed[1].Name = "daniel"
	cases := map[string][]protect.Reader{
		"the same":         readers,
		"in another order": {readers[2], readers[0], readers[1]},
		"one fewer":        readers[:2],
		"one more":         append(append([]protect.Reader(nil), readers...), others[0]),
		"one replaced":     {readers[0], readers[1], others[1]},
		"renamed":          renamed,
		"another set":      others,
	}
	for name, file := range cases {
		t.Run(name, func(t *testing.T) {
			dir := specDir(t, map[string]string{"main.cue": "package estate\n\nsize: 3\n" + s3Settings})
			viewerReadersFile(t, dir, file)
			prot := sealedProtection(m, state.You{State: "reader", Name: "ada", Key: readers[0].Key.String()})
			get := viewer(t, dir, protectionOf(prot, false, viewerIdentity{}))
			var a serve.Access
			if err := json.Unmarshal(get("/api/access"), &a); err != nil {
				t.Fatal(err)
			}
			st := m.Status(file)
			if match := a.File.Verdict == "match"; match != st.Matches {
				t.Errorf("the page says %q, protect says matches=%v (missing %v, extra %v)", a.File.Verdict, st.Matches, st.Missing, st.Extra)
			}
			if len(a.File.Added) != len(st.Missing) || len(a.File.Removed) != len(st.Extra) {
				t.Errorf("added %v removed %v, protect says missing %v extra %v", a.File.Added, a.File.Removed, st.Missing, st.Extra)
			}
		})
	}
}

// The readers file that does not load is a line and a reason, and the facts hold no
// lock, no key of an identity and no line of the file.
func TestAccessFactsHoldNoSecret(t *testing.T) {
	m, readers := protectedMeta(t, "ada", "dan")
	prot := sealedProtection(m, state.You{State: "reader", Name: "ada", Key: readers[0].Key.String()})
	f := accessFacts(prot, false, viewerIdentity{}, readersFile{Path: "/x/readers.txt", Found: true, Readers: readers})
	data, err := json.Marshal(f)
	if err != nil {
		t.Fatal(err)
	}
	for _, g := range m.Groups {
		for _, l := range g.Locks {
			if strings.Contains(string(data), base64.StdEncoding.EncodeToString(l.Box)) {
				t.Error("the facts hold a lock")
			}
		}
	}
	if len(f.Store.Readers) != 2 || f.Store.Readers[0].Name != "ada" || f.Store.Readers[0].Key != readers[0].Key.String() || f.Store.Sealed != 4 {
		t.Errorf("store %+v", f.Store)
	}
	if f.You == nil || f.You.State != "reader" || f.File.State != "ok" || len(f.File.Readers) != 2 || f.File.Readers[1].Line != 2 && f.File.Readers[1].Line != 0 {
		t.Errorf("you %+v file %+v", f.You, f.File)
	}

	// A file that does not load: the line and the reason.
	bad := accessFacts(prot, false, viewerIdentity{}, readersFile{Path: "/x/readers.txt", Found: true, Err: &readersFileError{Line: 3, Reason: "want 4 fields"}})
	if bad.File.State != "invalid" || len(bad.File.Errors) != 1 || bad.File.Errors[0] != (serve.LineError{Line: 3, Reason: "want 4 fields"}) || len(bad.File.Readers) != 0 {
		t.Errorf("invalid file: %+v", bad.File)
	}
	// A file with no reader in it does not load either: protect refuses it (exit 6),
	// so the page must not call the state different and offer a rewrap.
	empty := accessFacts(prot, false, viewerIdentity{}, readersFile{Path: "/x/readers.txt", Found: true})
	if empty.File.State != "invalid" || len(empty.File.Errors) != 1 || empty.File.Errors[0].Reason != "lists no readers" {
		t.Errorf("a readers file with no reader: %+v", empty.File)
	}
	if missing := accessFacts(prot, false, viewerIdentity{}, readersFile{Path: "/x/readers.txt"}); missing.File.State != "missing" {
		t.Errorf("missing file: %+v", missing.File)
	}
	// A state that is not sealed has no identity to speak of.
	if off := accessFacts(state.Protection{State: "off", Format: 1}, false, viewerIdentity{}, readersFile{}); off.You != nil {
		t.Errorf("you %+v in a state that is not sealed", off.You)
	}
	// An identity that could not be opened, or that is not a reader, is told apart from none.
	for _, c := range []struct {
		id   viewerIdentity
		want string
	}{{viewerIdentity{}, "no-identity"}, {viewerIdentity{locked: true}, "locked"}, {viewerIdentity{refused: true, name: "gus", key: readers[1].Key.String()}, "not-reader"}} {
		f := accessFacts(sealedProtection(m, state.You{State: "no-identity"}), true, c.id, readersFile{})
		if f.You == nil || f.You.State != c.want || !f.Store.Locked {
			t.Errorf("%+v: %+v", c.id, f.You)
		}
		if c.want == "not-reader" && (f.You.Name != "gus" || f.You.Key != readers[1].Key.String()) {
			t.Errorf("not-reader: %+v", f.You)
		}
	}
}

// resolve opens the identity once, and only a state that is sealed asks for one.
func TestViewerResolvesTheIdentityOnce(t *testing.T) {
	reset := func() {
		taken.mu.Lock()
		taken.loaded, taken.identity, taken.passphrase = nil, "", ""
		taken.mu.Unlock()
	}
	reset()
	t.Cleanup(reset)
	var said bytes.Buffer
	saved := out
	out = ui.NewPrinter(&said, true)
	t.Cleanup(func() { out = saved })
	idDir := t.TempDir()
	t.Setenv("PRAVO_IDENTITY_DIR", idDir)
	t.Setenv("PRAVO_IDENTITY", "")
	t.Setenv("PRAVO_PASSPHRASE", "")
	t.Setenv("PRAVO_IDENTITY_PASSPHRASE", "")
	asked := 0
	oldAsk, oldTerm := askPassphrase, stdinIsTerminal
	askPassphrase = func(string) ([]byte, error) { asked++; return []byte("correct horse battery"), nil }
	stdinIsTerminal = func() bool { return false }
	t.Cleanup(func() { askPassphrase, stdinIsTerminal = oldAsk, oldTerm })

	// None found: the viewer goes on, locked.
	v := &viewerState{}
	if err := v.resolve(); err != nil {
		t.Fatalf("no identity: %v", err)
	}
	if id, locked, resolved := v.identity(); id != nil || locked || !resolved {
		t.Errorf("no identity: %v %v %v", id, locked, resolved)
	}
	if !strings.Contains(said.String(), "no identity was found") {
		t.Errorf("the viewer did not say it has no identity: %q", said.String())
	}
	// A name that is not there is an error: the person asked for it.
	if err := (&viewerState{name: "nobody"}).resolve(); err == nil {
		t.Error("an identity that does not exist was accepted")
	}
	// "none" asks for nothing and needs no directory.
	t.Setenv("PRAVO_IDENTITY_DIR", filepath.Join(idDir, "missing"))
	none := &viewerState{name: "none"}
	if err := none.resolve(); err != nil {
		t.Fatal(err)
	}
	if id, _, resolved := none.identity(); id != nil || !resolved {
		t.Error("an identity was opened for --identity none")
	}
	t.Setenv("PRAVO_IDENTITY_DIR", idDir)

	// A sealed identity, with no terminal and no passphrase: locked, not an error.
	id, err := protect.NewIdentity("ada", "")
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := id.MarshalSealed([]byte("correct horse battery"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(idDir, "ada"), sealed, 0o600); err != nil {
		t.Fatal(err)
	}
	reset()
	lockedV := &viewerState{}
	if err := lockedV.resolve(); err != nil {
		t.Fatalf("a sealed identity and no passphrase: %v", err)
	}
	if got, locked, _ := lockedV.identity(); got != nil || !locked {
		t.Errorf("sealed identity, no passphrase: %v locked %v", got, locked)
	}
	if !strings.Contains(said.String(), "sealed with a passphrase") || strings.Contains(said.String(), "correct horse") {
		t.Errorf("said %q", said.String())
	}
	// With the passphrase in the environment: opened, and asked once however often it is resolved.
	reset()
	t.Setenv("PRAVO_PASSPHRASE", "correct horse battery")
	opened := &viewerState{}
	for i := 0; i < 3; i++ {
		if err := opened.resolve(); err != nil {
			t.Fatal(err)
		}
	}
	if got, locked, _ := opened.identity(); got == nil || got.Name != "ada" || locked {
		t.Errorf("sealed identity with a passphrase: %v locked %v", got, locked)
	}
	// Once the server listens nothing is asked and nothing fails: the viewer reads with no key.
	reset()
	t.Setenv("PRAVO_PASSPHRASE", "")
	late := &viewerState{listening: true}
	if err := late.resolve(); err != nil {
		t.Fatalf("a sealed identity after the server listens: %v", err)
	}
	if got, locked, _ := late.identity(); got != nil || !locked {
		t.Errorf("after the server listens: %v locked %v", got, locked)
	}
	reset()
	if err := (&viewerState{listening: true, name: "nobody"}).resolve(); err != nil {
		t.Errorf("an identity that is not there, after the server listens: %v", err)
	}
	t.Setenv("PRAVO_PASSPHRASE", "not the passphrase")
	reset()
	// A wrong passphrase is an error before anything listens.
	reset()
	t.Setenv("PRAVO_PASSPHRASE", "not the passphrase")
	if err := (&viewerState{}).resolve(); err == nil {
		t.Error("a wrong passphrase was accepted")
	}
	opened.close()
	if got, _, _ := opened.identity(); got != nil {
		t.Error("the identity is held after close")
	}
	if asked != 0 {
		t.Errorf("the passphrase was asked for %d times with no terminal", asked)
	}
}

func TestServeFlagsOfTheAccessView(t *testing.T) {
	t.Setenv("PRAVO_IDENTITY_DIR", filepath.Join(t.TempDir(), "none-here"))
	t.Setenv("PRAVO_IDENTITY", "")
	dir := specDir(t, map[string]string{"main.cue": "package estate\n\nsize: 3\n"})
	acc := filepath.Join(dir, "elsewhere")
	if err := os.MkdirAll(acc, 0o755); err != nil {
		t.Fatal(err)
	}
	m, readers := protectedMeta(t, "ada")
	_ = m
	if err := os.WriteFile(filepath.Join(acc, "readers.txt"), protect.FormatReaders(readers), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"--identity", "none", "--access-dir", "elsewhere"}, {"--access-dir", "elsewhere"}} {
		info, stop := runServe(t, dir, args...)
		var a serve.Access
		if err := json.Unmarshal(fetch(t, info, "/api/access"), &a); err != nil {
			t.Fatal(err)
		}
		want, _ := filepath.EvalSymlinks(filepath.Join(dir, "elsewhere", "readers.txt"))
		if got, _ := filepath.EvalSymlinks(a.File.Path); a.State != "off" || got != want || a.File.Count != 1 || a.You != nil {
			t.Errorf("%v: state %q file %+v you %+v", args, a.State, a.File, a.You)
		}
		if err := stop(); err != nil {
			t.Errorf("%v: %v", args, err)
		}
	}
}

// An identity that is not a reader is not an error for the viewer: the store opens
// without a key, and the viewer says the identity is not a reader. The state package
// refuses such an identity, which this stands in for.
func TestViewerOpensLockedForAnIdentityThatIsNotAReader(t *testing.T) {
	var said bytes.Buffer
	saved := out
	out = ui.NewPrinter(&said, true)
	t.Cleanup(func() { out = saved })
	dir := t.TempDir()
	if _, err := state.Open(dir); err != nil {
		t.Fatal(err)
	}
	gus, err := protect.NewIdentity("gus", "")
	if err != nil {
		t.Fatal(err)
	}
	defer gus.Close()
	var withKey, without int
	v := &viewerState{resolved: true, ident: gus, openStore: func(ctx context.Context, b state.Backend, o state.Options) (*state.Store, error) {
		if o.Identity != nil {
			withKey++
			return nil, state.ErrNotReader
		}
		without++
		return state.OpenBackend(ctx, b)
	}}
	st, err := v.openWith(context.Background(), &state.LocalBackend{Dir: dir}, gus)
	if err != nil || st == nil {
		t.Fatalf("an identity that is not a reader: %v", err)
	}
	if withKey != 1 || without != 1 {
		t.Errorf("opened %d times with the key and %d without", withKey, without)
	}
	if !strings.Contains(said.String(), "identity gus is not a reader") {
		t.Errorf("the viewer did not say that gus is not a reader: %q", said.String())
	}
	w := v.who()
	if !w.refused || w.name != "gus" || w.key != gus.Reader.Public().String() || w.locked {
		t.Errorf("who %+v", w)
	}
	// Any other error is an error.
	v.openStore = func(context.Context, state.Backend, state.Options) (*state.Store, error) {
		return nil, os.ErrPermission
	}
	if _, err := v.openWith(context.Background(), &state.LocalBackend{Dir: dir}, gus); err == nil {
		t.Error("an error of the store was swallowed")
	}
	// And an identity that is a reader clears the mark.
	v.openStore = func(ctx context.Context, b state.Backend, o state.Options) (*state.Store, error) {
		return state.OpenBackend(ctx, b)
	}
	if _, err := v.openWith(context.Background(), &state.LocalBackend{Dir: dir}, gus); err != nil || v.who().refused {
		t.Errorf("a reader: %v %+v", err, v.who())
	}
}

// A repository that lists readers and a state that is not sealed: the page says
// that the state was expected to be sealed, from the same readers file that the
// commands read.
func TestViewerSaysThatAStateIsNotSealedWhereTheRepositoryExpectsOne(t *testing.T) {
	_, readers := protectedMeta(t, "ada", "dan")
	for _, st := range []state.Protection{{State: "off", Format: 1}, {State: "immutable", Format: 2}} {
		t.Run(st.State, func(t *testing.T) {
			dir := specDir(t, map[string]string{"main.cue": "package estate\n\nsize: 3\n" + s3Settings})
			viewerReadersFile(t, dir, readers)
			get := viewer(t, dir, protectionOf(st, false, viewerIdentity{}))
			var a serve.Access
			if err := json.Unmarshal(get("/api/access"), &a); err != nil {
				t.Fatal(err)
			}
			if a.File.Verdict != "expected-sealed" || a.File.Count != 2 || len(a.Suggestions) == 0 || a.Suggestions[0].ID != "check-downgrade" || !a.Suggestions[0].Primary {
				t.Fatalf("file %+v, suggestions %+v", a.File, a.Suggestions)
			}
			if !strings.Contains(a.File.Text, "The repository lists readers in .pravo/access/readers.txt, but the state is not sealed.") {
				t.Errorf("text %q", a.File.Text)
			}
		})
	}
	t.Run("a readers file that does not load says so", func(t *testing.T) {
		dir := specDir(t, map[string]string{"main.cue": "package estate\n\nsize: 3\n" + s3Settings})
		acc := filepath.Join(dir, ".pravo", "access")
		if err := os.MkdirAll(acc, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(acc, "readers.txt"), []byte("ada one two\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		get := viewer(t, dir, protectionOf(state.Protection{State: "off", Format: 1}, false, viewerIdentity{}))
		var a serve.Access
		if err := json.Unmarshal(get("/api/access"), &a); err != nil {
			t.Fatal(err)
		}
		if a.File.Verdict != "none" || !strings.Contains(a.File.Text, "does not load") {
			t.Errorf("file %+v", a.File)
		}
	})
}
