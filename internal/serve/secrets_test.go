package serve

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pravo-systems/pravo/internal/config"
	"github.com/pravo-systems/pravo/internal/protect"
	"github.com/pravo-systems/pravo/internal/state"
)

// The values below are planted in the sealed state. No answer of the viewer may carry one.
const (
	vaultDB    = "planted-db-password-8f3a91c2"
	vaultToken = "planted-api-token-77d0e5b4"
	vaultKey   = "planted-deploy-key-31c8aa07"
	vaultEvil  = "planted-reader-value-c42b9e10"
)

var vaultValues = []string{vaultDB, vaultDB + "-2", vaultToken, vaultKey, vaultEvil}

// vault is a project whose state is sealed to ada, dan and eve. ada is an admin, dan a writer and eve a
// reader, as the rules of the viewer have it.
type vault struct {
	t     *testing.T
	dir   string
	path  string
	ids   map[string]*protect.Identity
	rules state.RecordRules
}

// vaultSpec is a specification that reads secrets the ways a specification does: the latest version of
// one, the number of the version, a version asked for, and secrets the state cannot give.
const vaultSpec = specCUE + `
db:      {$kind: "secret", name: "prod/db_password", value: string}
dbrev:   {$kind: "secret_version", name: "prod/db_password", version: int}
token:   {$kind: "secret", name: "prod/api_token", version: 1, value: string}
`

// problemSpec adds the reads that cannot be satisfied.
const problemSpec = vaultSpec + `
ahead:   {$kind: "secret", name: "prod/api_token", version: 5, value: string}
unset:   {$kind: "secret", name: "prod/never_set", value: string}
deploy:  {$kind: "secret", name: "ci/deploy_key", value: string}
`

func newVault(t *testing.T, cue string) *vault {
	t.Helper()
	dir := t.TempDir()
	writeSpec(t, dir, cue)
	v := &vault{t: t, dir: dir, path: filepath.Join(dir, "pravo.state"), ids: map[string]*protect.Identity{}}
	var plain []protect.Reader
	for _, m := range []struct {
		name string
		role protect.Role
	}{{"ada", protect.RoleAdmin}, {"dan", protect.RoleWriter}, {"eve", protect.RoleReader}} {
		id, err := protect.NewIdentity(m.name, "")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(id.Close)
		v.ids[m.name] = id
		plain = append(plain, id.AsReader())
		r := id.AsReader()
		r.Role = m.role
		v.rules.Readers = append(v.rules.Readers, r)
	}
	st, err := state.OpenWith(v.path, state.Options{NewFormat: state.Format, Identity: v.ids["ada"]})
	if err != nil {
		t.Fatal(err)
	}
	seed(t, st)
	if _, err := st.Upgrade(t.Context(), state.UpgradeOptions{To: state.FormatSealed, Readers: plain, Identity: v.ids["ada"], By: "ada"}); err != nil {
		t.Fatal(err)
	}
	return v
}

// as opens the state as one of the members.
func (v *vault) as(who string) *state.Store {
	v.t.Helper()
	st, err := state.OpenWith(v.path, state.Options{Identity: v.ids[who]})
	if err != nil {
		v.t.Fatal(err)
	}
	return st
}

func (v *vault) set(who, name, value string) {
	v.t.Helper()
	if _, err := v.as(who).SetSecret(v.ctx(), name, []byte(value), "", "pravo secret set "+name); err != nil {
		v.t.Fatalf("%s sets %s: %v", who, name, err)
	}
}

func (v *vault) remove(who, name string) {
	v.t.Helper()
	if _, err := v.as(who).RemoveSecret(v.ctx(), name, "pravo secret rm "+name); err != nil {
		v.t.Fatalf("%s removes %s: %v", who, name, err)
	}
}

func (v *vault) ctx() context.Context { return v.t.Context() }

// secretRules is what the command gives the viewer: the readers with their roles.
func (v *vault) secretRules(*state.Store) (state.RecordRules, bool, string) { return v.rules, true, "" }

// serve starts a viewer over the project that judges the secrets by the rules of the vault.
func (v *vault) serve(opts Options) *project {
	v.t.Helper()
	if opts.SecretRules == nil {
		opts.SecretRules = v.secretRules
	}
	return start(v.t, v.dir, v.as("ada"), opts)
}

func (p *project) secrets() Secrets {
	p.t.Helper()
	var s Secrets
	if err := json.Unmarshal(p.get("/api/secrets"), &s); err != nil {
		p.t.Fatal(err)
	}
	return s
}

func entryOf(t *testing.T, d Secrets, name string) SecretEntry {
	t.Helper()
	for _, e := range d.Secrets {
		if e.Name == name {
			return e
		}
	}
	t.Fatalf("no secret %q in %+v", name, d.Secrets)
	return SecretEntry{}
}

func issueOf(d Secrets, kind string) *SecretIssue {
	for i := range d.Issues {
		if d.Issues[i].Kind == kind {
			return &d.Issues[i]
		}
	}
	return nil
}

func addrsOf(uses []SecretUse) []string {
	var out []string
	for _, u := range uses {
		out = append(out, u.Addr)
	}
	return out
}

func sameSet(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	seen := map[string]bool{}
	for _, g := range got {
		seen[g] = true
	}
	for _, w := range want {
		if !seen[w] {
			return false
		}
	}
	return true
}

// A viewer over a sealed state lists the secrets with the clear parts of their versions, sets them against
// the nodes that read them, and says which reads the state cannot satisfy, with the command that mends each.
func TestSecretsPageListsTheSecretsAndWhatIsMissing(t *testing.T) {
	v := newVault(t, problemSpec)
	v.set("dan", "prod/db_password", vaultDB)
	v.set("dan", "prod/db_password", vaultDB+"-2")
	v.set("dan", "prod/api_token", vaultToken)
	v.set("ada", "ci/deploy_key", vaultKey)
	v.remove("ada", "ci/deploy_key")
	d := v.serve(Options{}).secrets()

	if !d.Sealed || !d.Checked || !d.SpecOK || d.Version != APIVersion {
		t.Fatalf("the state of the page: %+v", d)
	}
	if len(d.Secrets) != 3 || d.Secrets[0].Name != "ci/deploy_key" || d.Secrets[1].Name != "prod/api_token" || d.Secrets[2].Name != "prod/db_password" {
		t.Fatalf("the secrets, by name: %+v", d.Secrets)
	}
	if e := entryOf(t, d, "ci/deploy_key"); e.Status != "removed" || e.Version != 2 || e.Versions != 2 || e.By != "ada" || e.Trust != "ok" || !sameSet(addrsOf(e.UsedBy), []string{"deploy"}) {
		t.Errorf("a removed secret: %+v", e)
	}
	if e := entryOf(t, d, "prod/api_token"); e.Status != "set" || e.Version != 1 || e.By != "dan" || e.Trust != "ok" || !sameSet(addrsOf(e.UsedBy), []string{"token", "ahead"}) {
		t.Errorf("a secret read at two versions: %+v", e)
	}
	e := entryOf(t, d, "prod/db_password")
	if e.Status != "set" || e.Version != 2 || e.Versions != 2 || e.By != "dan" || e.Bad != 0 || e.At.IsZero() {
		t.Errorf("a secret with two versions: %+v", e)
	}
	for _, u := range e.UsedBy {
		if (u.Addr == "dbrev") != u.Number {
			t.Errorf("only the node of the version number reads the number: %+v", u)
		}
	}
	if !sameSet(addrsOf(e.UsedBy), []string{"db", "dbrev"}) {
		t.Errorf("the nodes that read the password: %+v", e.UsedBy)
	}
	for _, u := range entryOf(t, d, "prod/api_token").UsedBy {
		if (u.Addr == "token" && u.Pin != 1) || (u.Addr == "ahead" && u.Pin != 5) {
			t.Errorf("a pin is the version that the node asks for: %+v", u)
		}
	}

	// One issue: three reads the state cannot satisfy. The secrets are in order.
	if len(d.Issues) != 1 || d.Problems != 1 || d.AllClear != "" {
		t.Fatalf("issues %+v, problems %d, all clear %q", d.Issues, d.Problems, d.AllClear)
	}
	is := d.Issues[0]
	if is.Kind != "unset" || is.Level != "bad" || !strings.Contains(is.Title, "3 secrets") || len(is.Items) != 3 {
		t.Fatalf("the issue: %+v", is)
	}
	want := []struct{ name, why, command string }{
		{"ci/deploy_key", "was removed", "pravo secret set ci/deploy_key"},
		{"prod/api_token", "version 5 is asked for, and the latest is 1", ""},
		{"prod/never_set", "not set", "pravo secret set prod/never_set"},
	}
	for i, w := range want {
		it := is.Items[i]
		if it.Name != w.name || it.Why != w.why || it.Command != w.command {
			t.Errorf("item %d is %+v, want %+v", i, it, w)
		}
	}
	if it := is.Items[1]; it.Version != 5 || !sameSet(it.UsedBy, []string{"ahead", "token"}) {
		t.Errorf("the item of a version that is not there names the version and the nodes: %+v", it)
	}
}

// A state in which every read is satisfied and every version was written by someone who may write it says
// so in one line, and has nothing to flag.
func TestSecretsPageSaysAllClear(t *testing.T) {
	v := newVault(t, vaultSpec)
	v.set("dan", "prod/db_password", vaultDB)
	v.set("dan", "prod/api_token", vaultToken)
	v.set("ada", "ci/deploy_key", vaultKey) // read by nothing
	d := v.serve(Options{}).secrets()
	if len(d.Issues) != 0 || d.Problems != 0 || d.Note != "" {
		t.Fatalf("issues %+v, problems %d, note %q", d.Issues, d.Problems, d.Note)
	}
	if !strings.HasPrefix(d.AllClear, "Every version was written by someone who may set secrets") || !strings.Contains(d.AllClear, "every secret the specification reads is set") {
		t.Errorf("all clear: %q", d.AllClear)
	}
	if e := entryOf(t, d, "ci/deploy_key"); len(e.UsedBy) != 0 || e.UsedBy == nil {
		t.Errorf("a secret that nothing reads has an empty list, and not null: %+v", e.UsedBy)
	}
}

// A version that a reader wrote is found by its signature, and the page names the version, the nodes it stops
// and the command that replaces it. After a writer sets the secret again, the old version is still flagged,
// and the command is the one that shows who wrote each version.
func TestSecretsPageFlagsAVersionThatNoWriterAccountsFor(t *testing.T) {
	v := newVault(t, vaultSpec)
	v.set("dan", "prod/db_password", vaultDB)
	v.set("dan", "prod/api_token", vaultToken)
	v.set("eve", "prod/db_password", vaultEvil)
	p := v.serve(Options{})
	d := p.secrets()
	is := issueOf(d, "untrusted")
	if is == nil || is.Level != "bad" || len(is.Items) != 1 || len(d.Issues) != 1 || d.Problems != 1 || d.AllClear != "" {
		t.Fatalf("issues %+v", d.Issues)
	}
	it := is.Items[0]
	if it.Name != "prod/db_password" || it.Version != 2 || it.Command != "pravo secret set prod/db_password" || !sameSet(it.UsedBy, []string{"db", "dbrev"}) {
		t.Errorf("the item: %+v", it)
	}
	if !strings.Contains(it.Why, "eve") || !strings.Contains(it.Why, "a reader") || !strings.Contains(it.Why, "this needs a writer") {
		t.Errorf("why: %q", it.Why)
	}
	if e := entryOf(t, d, "prod/db_password"); e.Trust != "problem" || e.Bad != 1 || e.By != "eve" {
		t.Errorf("the entry: %+v", e)
	}
	if e := entryOf(t, d, "prod/api_token"); e.Trust != "ok" || e.Bad != 0 {
		t.Errorf("the other secret is in order: %+v", e)
	}

	// A writer sets it again. The page is live: the new version is a transaction, which makes a new revision.
	v.set("dan", "prod/db_password", vaultDB+"-2")
	waitFor(t, "the page to see the new version", func() bool { return entryOf(t, p.secrets(), "prod/db_password").Version == 3 })
	d = p.secrets()
	if e := entryOf(t, d, "prod/db_password"); e.Trust != "ok" || e.Bad != 1 {
		t.Errorf("the head is in order and the old version is still flagged: %+v", e)
	}
	is = issueOf(d, "untrusted")
	if is == nil || len(is.Items) != 1 || is.Items[0].Version != 2 || is.Items[0].Command != "pravo secret history prod/db_password" {
		t.Errorf("an old version is mended by looking at the history: %+v", d.Issues)
	}
}

// A repository with no readers file has nothing to judge the signatures by: the page lists the secrets, says that
// it did not check who set them, and marks none of them.
func TestSecretsPageWithoutAReadersFile(t *testing.T) {
	v := newVault(t, vaultSpec)
	v.set("dan", "prod/db_password", vaultDB)
	v.set("eve", "prod/api_token", vaultToken)
	d := v.serve(Options{SecretRules: func(*state.Store) (state.RecordRules, bool, string) { return state.RecordRules{}, false, "" }}).secrets()
	if d.Checked || !d.Sealed || len(d.Secrets) != 2 {
		t.Fatalf("the state of the page: %+v", d)
	}
	for _, e := range d.Secrets {
		if e.Trust != "" || e.Bad != 0 {
			t.Errorf("nothing was judged: %+v", e)
		}
	}
	if is := issueOf(d, "unchecked-all"); is == nil || is.Level != "chg" || d.Problems != 1 || d.AllClear != "" {
		t.Errorf("issues %+v, problems %d", d.Issues, d.Problems)
	}
}

// Roles that fail their check are told, once, and no version is vouched for.
func TestSecretsPageSaysWhenTheRolesFail(t *testing.T) {
	v := newVault(t, vaultSpec)
	v.set("dan", "prod/db_password", vaultDB)
	d := v.serve(Options{SecretRules: func(*state.Store) (state.RecordRules, bool, string) {
		return v.rules, true, "roles/00000002.json is not signed by an admin of the roles it follows"
	}}).secrets()
	is := issueOf(d, "roles")
	if is == nil || is.Level != "bad" || !strings.Contains(is.Text, "roles/00000002.json is not signed by an admin") || !strings.Contains(is.Text, "pravo protect status") {
		t.Fatalf("issues %+v", d.Issues)
	}
	if d.AllClear != "" || d.Problems != len(d.Issues) {
		t.Errorf("all clear %q, problems %d of %d", d.AllClear, d.Problems, len(d.Issues))
	}
}

// A state that is not sealed keeps no secret. If the specification reads one, the page says that a command that
// reads it stops, and offers nothing to run: the way to seal the state is on the State page.
func TestSecretsPageOfAStateThatIsNotSealed(t *testing.T) {
	dir := t.TempDir()
	writeSpec(t, dir, vaultSpec)
	st, err := state.Open(filepath.Join(dir, "pravo.state"))
	if err != nil {
		t.Fatal(err)
	}
	seed(t, st)
	p := start(t, dir, st, Options{})
	d := p.secrets()
	if d.Sealed || d.Checked || len(d.Secrets) != 0 || d.AllClear != "" {
		t.Fatalf("the state of the page: %+v", d)
	}
	is := issueOf(d, "unsealed")
	if is == nil || is.Level != "bad" || len(d.Issues) != 1 || d.Problems != 1 || len(is.Items) != 0 || !strings.Contains(is.Text, "3 nodes of the specification read a secret") {
		t.Fatalf("issues %+v", d.Issues)
	}
	// The nodes are still there for the graph, and say the same.
	n := p.detail("db")
	if !strings.Contains(strings.Join(n.Notes, " "), "not sealed") {
		t.Errorf("the notes of a node: %v", n.Notes)
	}

	// With nothing that reads a secret there is nothing to say.
	q := newProject(t)
	if d := q.secrets(); d.Sealed || len(d.Issues) != 0 || d.Problems != 0 || d.AllClear != "" || len(d.Secrets) != 0 {
		t.Errorf("a state with no secret and no read: %+v", d)
	}
}

// A secret of the state that nothing reads is listed. A name that depends on a command is listed apart, and
// the nodes of one secret are bounded.
func TestSecretsPageBoundsWhatItLists(t *testing.T) {
	var cue strings.Builder
	cue.WriteString(specCUE)
	cue.WriteString("late: {$kind: \"secret\", name: string, value: string}\n")
	for i := range 45 {
		fmt.Fprintf(&cue, "r%02d: {$kind: \"secret\", name: \"prod/shared\", value: string}\n", i)
	}
	v := newVault(t, cue.String())
	v.set("dan", "prod/shared", vaultToken)
	d := v.serve(Options{}).secrets()
	e := entryOf(t, d, "prod/shared")
	if len(e.UsedBy) != maxSecretUsers || e.UsedMore != 5 {
		t.Errorf("%d nodes listed and %d more", len(e.UsedBy), e.UsedMore)
	}
	if len(d.Unresolved) != 1 || d.Unresolved[0] != "late" {
		t.Errorf("a name that is not known: %v", d.Unresolved)
	}
	if len(d.Issues) != 0 {
		t.Errorf("an unresolved name is not a problem: %+v", d.Issues)
	}
}

// The detail of a node that reads a secret has the schema and no value, and says what the state holds.
func TestTheDetailOfASecretNode(t *testing.T) {
	v := newVault(t, problemSpec)
	v.set("dan", "prod/db_password", vaultDB)
	v.set("dan", "prod/db_password", vaultDB+"-2")
	v.set("dan", "prod/api_token", vaultToken)
	v.set("ada", "ci/deploy_key", vaultKey)
	v.remove("ada", "ci/deploy_key")
	p := v.serve(Options{})
	notes := func(addr string) string { return strings.Join(p.detail(addr).Notes, " ") }

	d := p.detail("db")
	if got := attrOf(t, d.Attrs, "value"); !got.Sensitive || got.Redacted != "sensitive" || got.Value != nil {
		t.Errorf("a secret shows no value: %+v", got)
	}
	if got := attrOf(t, d.Attrs, "name"); got.Value != "prod/db_password" {
		t.Errorf("the name is not secret: %+v", got)
	}
	if len(d.Schema) != 3 || !attrOf(t, d.Schema, "value").Sensitive || attrOf(t, d.Schema, "name").Kind != "required" || attrOf(t, d.Schema, "version").Kind != "optional" {
		t.Errorf("the schema of #Secret: %+v", d.Schema)
	}
	if !strings.Contains(notes("db"), "version 2, the latest of 2 versions") || !strings.Contains(notes("db"), "shows no value") {
		t.Errorf("notes: %q", notes("db"))
	}

	// The node of the version number reads a number, which is no secret.
	r := p.detail("dbrev")
	if got := attrOf(t, r.Attrs, "version"); got.Value != float64(2) || got.Sensitive || got.Redacted != "" {
		t.Errorf("the version number is an ordinary value: %+v", got)
	}
	if hasName(r.Attrs, "value") || len(r.Schema) != 2 {
		t.Errorf("the node of a number has no value: %+v %+v", r.Attrs, r.Schema)
	}

	if got := attrOf(t, p.detail("token").Attrs, "version"); got.Value != float64(1) {
		t.Errorf("a pin is shown: %+v", got)
	}
	if !strings.Contains(notes("token"), "reads version 1, as this node asks") {
		t.Errorf("notes of a pin: %q", notes("token"))
	}
	if !strings.Contains(notes("unset"), `holds no secret named prod/never_set`) || !strings.Contains(notes("unset"), `"pravo secret set prod/never_set"`) {
		t.Errorf("notes of a secret that is not set: %q", notes("unset"))
	}
	if !strings.Contains(notes("deploy"), "was removed") {
		t.Errorf("notes of a secret that was removed: %q", notes("deploy"))
	}
	if n := nodeOf(p.graph(), "db"); n == nil || n.Mode != "ephemeral" || n.Type != "secret" {
		t.Errorf("the node: %+v", n)
	}
	if n := nodeOf(p.graph(), "dbrev"); n == nil || n.Mode != "builtin" || n.Type != "secret_version" {
		t.Errorf("the node of a number: %+v", n)
	}
	// The source of the node is the text of the specification, with no value in it.
	if s := p.source("db"); s.Addr != "db" || len(s.Blocks) == 0 {
		t.Errorf("the source: %+v", s)
	}
}

// Nothing the viewer sends carries a value, whether or not the process holds a key that would open one: the
// page lists the clear parts of the versions and never reads the rest.
func TestNoAnswerOfTheViewerCarriesASecretValue(t *testing.T) {
	v := newVault(t, problemSpec)
	v.set("dan", "prod/db_password", vaultDB)
	v.set("dan", "prod/db_password", vaultDB+"-2")
	v.set("dan", "prod/api_token", vaultToken)
	v.set("ada", "ci/deploy_key", vaultKey)
	v.set("eve", "prod/db_password", vaultEvil)
	keyed := func(ctx context.Context, root *config.Root) (*state.Store, error) {
		return state.OpenBackendWith(ctx, ReadOnly(&state.LocalBackend{Dir: root.StateFile()}), state.Options{Identity: v.ids["ada"]})
	}
	for name, opts := range map[string]Options{"no key": {}, "a key that opens the state": {OpenState: keyed}} {
		t.Run(name, func(t *testing.T) {
			p := v.serve(opts)
			paths := []string{"/api/graph", "/api/secrets", "/api/access", "/api/policies", "/api/plans", "/api/discovery", "/api/search?q=prod", "/api/search?q=planted", "/api/studio"}
			for _, addr := range []string{"db", "dbrev", "token", "ahead", "unset", "deploy"} {
				paths = append(paths, "/api/detail?addr="+addr, "/api/source?addr="+addr)
			}
			for _, path := range paths {
				body := p.get(path)
				noSecrets(t, path, body, vaultValues...)
				for _, s := range vaultValues {
					if strings.Contains(string(body), base64.RawURLEncoding.EncodeToString([]byte(s))) {
						t.Errorf("%s sends %q in base64", path, s)
					}
				}
			}
		})
	}
	// The files of the project hold no value either: the viewer writes nothing, and the state is sealed.
	_ = filepath.WalkDir(v.dir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		data, rerr := os.ReadFile(path)
		if rerr != nil {
			return nil
		}
		for _, s := range vaultValues {
			if strings.Contains(string(data), s) {
				t.Errorf("%s holds %q in the clear", path, s)
			}
		}
		return nil
	})
}

// The server lists the clear parts of the versions and nothing else: it calls no function of the store that opens
// a value or a note, and none that writes a secret, so that no change to this package can make the page show one.
func TestTheViewerNeverOpensOrWritesASecret(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	banned := map[string]bool{"ReadSecret": true, "SecretNotes": true, "SecretValues": true, "SetSecret": true, "RemoveSecret": true, "PurgeSecret": true}
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		fset := token.NewFileSet()
		parsed, err := parser.ParseFile(fset, f, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		ast.Inspect(parsed, func(n ast.Node) bool {
			if sel, ok := n.(*ast.SelectorExpr); ok && banned[sel.Sel.Name] {
				t.Errorf("%s: %s calls %s, which opens or writes a secret", fset.Position(sel.Pos()), f, sel.Sel.Name)
			}
			return true
		})
	}
}

// A secret that is set while the page is open shows up without a reload: the write is a transaction, and the
// viewer sees a new revision.
func TestSecretsPageFollowsTheState(t *testing.T) {
	v := newVault(t, vaultSpec)
	p := v.serve(Options{})
	if d := p.secrets(); len(d.Secrets) != 0 || len(d.Issues) != 1 || d.Issues[0].Kind != "unset" {
		t.Fatalf("before: %+v", d)
	}
	rev := p.secrets().Revision
	v.set("dan", "prod/db_password", vaultDB)
	v.set("dan", "prod/api_token", vaultToken)
	waitFor(t, "the secrets to show", func() bool { return len(p.secrets().Secrets) == 2 })
	d := p.secrets()
	if d.Revision == rev || len(d.Issues) != 0 || d.AllClear == "" {
		t.Errorf("after: revision %q (was %q), issues %+v, all clear %q", d.Revision, rev, d.Issues, d.AllClear)
	}
}

// The viewer stays read-only: a request for the secrets writes nothing to the project.
func TestSecretsPageWritesNothing(t *testing.T) {
	v := newVault(t, problemSpec)
	v.set("dan", "prod/db_password", vaultDB)
	p := v.serve(Options{})
	p.secrets()
	before := digestTree(t, v.dir)
	for range 3 {
		p.secrets()
		p.detail("db")
	}
	if after := digestTree(t, v.dir); after != before {
		t.Error("a request changed the project")
	}
}
