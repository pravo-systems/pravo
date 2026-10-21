package engine

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"cuelang.org/go/cue/cuecontext"

	"github.com/pravo-systems/pravo/internal/config"
)

// judge builds an engine that holds the given policy sources and judges view.
func judge(t *testing.T, view *View, policies ...string) error {
	t.Helper()
	dir := t.TempDir()
	root := t.TempDir()
	var paths []string
	for i, src := range policies {
		p := filepath.Join(dir, "p"+string(rune('a'+i))+".cue")
		if err := os.WriteFile(p, []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
		paths = append(paths, p)
	}
	loaded, err := LoadPolicyFiles(paths, root)
	if err != nil {
		t.Fatal(err)
	}
	e := &Engine{Root: &config.Root{Ctx: cuecontext.New()}, Opts: Options{Progress: func(string, ...any) {}}}
	if err := e.SetPolicies(loaded); err != nil {
		t.Fatal(err)
	}
	return e.CheckPolicyView(view)
}

func viewOf(changes ...ChangeView) *View {
	return &View{Format: 1, Changes: append([]ChangeView{}, changes...), Outputs: map[string]any{}, Summary: Summary{BlastRadius: len(changes)}}
}

const noDeletes = `
changes: [...{
	addr:   string
	mode:   string
	action: string
	if mode == "managed" && (action == "delete" || action == "replace") {
		error("\(addr): \(action) is not allowed")
	}
}]
summary: blast_radius: <=2
`

func TestPolicyProhibition(t *testing.T) {
	ok := viewOf(ChangeView{Addr: "a", Mode: "managed", Type: "t", Action: ActionCreate})
	if err := judge(t, ok, noDeletes); err != nil {
		t.Fatalf("a create should satisfy the policy: %v", err)
	}
	bad := viewOf(ChangeView{Addr: "db", Mode: "managed", Type: "t", Action: ActionReplace})
	err := judge(t, bad, noDeletes)
	if err == nil || !strings.Contains(err.Error(), "db: replace is not allowed") {
		t.Fatalf("a replace should be refused with the policy's message, got %v", err)
	}
	var coded *CodedError
	if !errors.As(err, &coded) || coded.Code != ExitRefused {
		t.Fatalf("a violation must carry exit code 5, got %v", err)
	}
	if err := judge(t, viewOf(ChangeView{Addr: "d", Mode: "data", Type: "t", Action: ActionDelete}), noDeletes); err != nil {
		t.Fatalf("the rule names the managed mode, so a data source delete passes: %v", err)
	}
	big := viewOf(ChangeView{Addr: "a", Mode: "managed", Action: ActionCreate}, ChangeView{Addr: "b", Mode: "managed", Action: ActionCreate}, ChangeView{Addr: "c", Mode: "managed", Action: ActionCreate})
	if err := judge(t, big, noDeletes); err == nil {
		t.Fatal("a blast radius over the limit should be refused")
	}
}

const needOwner = `
changes: [...{
	type:   string
	action: string
	if type == "aws_s3_bucket" && action == "create" {
		after: tags: owner: string & !=""
	}
}]
`

// A requirement that the plan does not meet is a violation: a value that is
// absent, null or unknown until apply does not satisfy it.
func TestPolicyRequirementsAreStrict(t *testing.T) {
	with := ChangeView{Addr: "b", Mode: "managed", Type: "aws_s3_bucket", Action: ActionCreate, After: map[string]any{"tags": map[string]any{"owner": "platform"}}}
	if err := judge(t, viewOf(with), needOwner); err != nil {
		t.Fatalf("a bucket with an owner should pass: %v", err)
	}
	for name, after := range map[string]any{
		"absent":  map[string]any{"tags": map[string]any{"team": "x"}},
		"null":    map[string]any{"tags": map[string]any{"owner": nil}},
		"empty":   map[string]any{"tags": map[string]any{"owner": ""}},
		"unknown": map[string]any{"tags": map[string]any{"owner": Unknown}},
	} {
		c := ChangeView{Addr: "b", Mode: "managed", Type: "aws_s3_bucket", Action: ActionCreate, After: after}
		if err := judge(t, viewOf(c), needOwner); err == nil {
			t.Errorf("owner %s should violate the requirement", name)
		}
	}
	other := ChangeView{Addr: "x", Mode: "managed", Type: "random_pet", Action: ActionCreate}
	if err := judge(t, viewOf(other), needOwner); err != nil {
		t.Errorf("a resource of another type is not governed: %v", err)
	}
}

// An == guard compares a value that is unknown until apply and finds it
// unequal, so a rule that prohibits a value this way lets the plan pass. The
// documentation tells authors to write the allowed set as a constraint.
func TestPolicyEqualityGuardPassesAnUnknownValue(t *testing.T) {
	guard := `changes: [...{after?: {...}, if after != _|_ { if after.acl == "public-read" { error("public") } }}]`
	c := ChangeView{Addr: "b", Mode: "managed", Type: "t", Action: ActionCreate, After: map[string]any{"acl": Unknown}}
	if err := judge(t, viewOf(c), guard); err != nil {
		t.Fatalf("documented behavior: an unknown value is not equal to the guard: %v", err)
	}
	c.After = map[string]any{"acl": "public-read"}
	if err := judge(t, viewOf(c), guard); err == nil {
		t.Fatal("a known public-read must be refused")
	}
}

func TestPolicyEmptyPlanAndHelpers(t *testing.T) {
	// changes is an empty list, not null, so a list template is satisfied.
	if err := judge(t, viewOf(), noDeletes); err != nil {
		t.Fatalf("an empty plan satisfies a prohibition: %v", err)
	}
	withHelpers := `
#Limit: 5
_limit: #Limit
summary: blast_radius: <=_limit
`
	if err := judge(t, viewOf(ChangeView{Addr: "a", Mode: "managed", Action: ActionCreate}), withHelpers); err != nil {
		t.Fatalf("definitions and hidden fields are helpers: %v", err)
	}
}

func TestPoliciesAddUp(t *testing.T) {
	bad := viewOf(ChangeView{Addr: "db", Mode: "managed", Type: "t", Action: ActionReplace})
	if err := judge(t, bad, `summary: blast_radius: <=10`, noDeletes); err == nil {
		t.Fatal("every named policy must hold, not one of them")
	}
	if err := judge(t, viewOf(), `changes: [...{}]`, `summary: blast_radius: 0`); err != nil {
		t.Fatalf("two satisfied policies pass: %v", err)
	}
}

// missingFile is what the system says of a file that does not exist.
var missingFile = func() string {
	if runtime.GOOS == "windows" {
		return "cannot find the file specified"
	}
	return "no such file"
}()

func TestLoadPolicyFilesRefusals(t *testing.T) {
	dir := t.TempDir()
	root := t.TempDir()
	write := func(dir, name, src string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	cases := map[string]struct {
		path string
		want string
	}{
		"empty value":    {"", "needs a file"},
		"missing file":   {filepath.Join(dir, "nope.cue"), missingFile},
		"bad syntax":     {write(dir, "bad.cue", "changes: [..."), "policy"},
		"foreign import": {write(dir, "imp.cue", "import \"example.com/x\"\nx: x.y\n"), "standard library"},
		"in the root":    {write(root, "p.cue", "summary: blast_radius: <=1\n"), "root directory"},
	}
	for name, c := range cases {
		_, err := LoadPolicyFiles([]string{c.path}, root)
		var coded *CodedError
		if err == nil || !strings.Contains(err.Error(), c.want) || !errors.As(err, &coded) || coded.Code != ExitRefused {
			t.Errorf("%s: want an exit-5 error containing %q, got %v", name, c.want, err)
		}
	}
	ok := write(dir, "ok.cue", "import \"strings\"\nsummary: blast_radius: <=len(strings.Repeat(\"x\", 3))\n")
	ps, err := LoadPolicyFiles([]string{ok}, root)
	if err != nil || len(ps) != 1 || len(ps[0].Digest) != 64 || ps[0].ShortDigest() != ps[0].Digest[:12] {
		t.Fatalf("a standard-library import is allowed: %v %+v", err, ps)
	}
}

// loadPolicies makes the engine judge plans against the policy sources.
func loadPolicies(t *testing.T, e *Engine, sources ...string) {
	t.Helper()
	dir := t.TempDir()
	var paths []string
	for i, src := range sources {
		p := filepath.Join(dir, fmt.Sprintf("p%d.cue", i))
		if err := os.WriteFile(p, []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
		paths = append(paths, p)
	}
	loaded, err := LoadPolicyFiles(paths, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := e.SetPolicies(loaded); err != nil {
		t.Fatal(err)
	}
}

func addrsOf(v *View) []string {
	out := []string{}
	for _, c := range v.Changes {
		out = append(out, c.Addr)
	}
	return out
}

// The plan document lists the nodes the plan ran. A policy is judged against a
// document with an entry for every node, in the order of the specification,
// wherever the plan comes from: the run that made it or a plan file.
func TestPlanDocumentAndPolicyInput(t *testing.T) {
	const n = 6
	h := newHarness(t)
	h.apply(manySpec(n, func(int) string { return "v1" }))
	s := manySpec(n, func(i int) string {
		if i == 4 {
			return "v2"
		}
		return "v1"
	})
	e := h.engine(s, Options{})
	p := plan(t, e)
	var all []string
	for i := 0; i < n; i++ {
		all = append(all, fmt.Sprintf("r%03d", i))
	}

	t.Run("the document", func(t *testing.T) {
		if got := addrsOf(e.NewView(p, ViewOptions{})); !reflect.DeepEqual(got, []string{"r004"}) {
			t.Errorf("the document lists %v, want the node that runs", got)
		}
		v := e.NewView(p, ViewOptions{Skipped: true})
		if got := addrsOf(v); !reflect.DeepEqual(got, all) {
			t.Fatalf("with the skipped nodes the document lists %v, want %v", got, all)
		}
		for _, c := range v.Changes {
			if c.Addr == "r004" {
				if c.Skipped || c.Action != ActionReplace && c.Action != ActionUpdate || len(c.Reasons) == 0 {
					t.Errorf("r004 is %+v", c)
				}
				continue
			}
			want := ChangeView{Addr: c.Addr, Mode: "managed", Type: "fake_thing", Name: c.Addr, Action: ActionNoop, Skipped: true}
			if !reflect.DeepEqual(c, want) {
				t.Errorf("the entry of a skipped node is %+v, want %+v", c, want)
			}
		}
		// A reader of the reasons asks for them.
		if full := e.NewView(p, ViewOptions{Skipped: true, Full: true}); len(full.Changes[0].Reasons) == 0 {
			t.Error("the full entry of a skipped node has no reasons")
		}
		if v := e.NewView(p, ViewOptions{}); v.Summary.Read != 0 || v.Stats.Total != n || v.Stats.NotRead != n-1 {
			t.Errorf("summary %+v, stats %+v", v.Summary, v.Stats)
		}
	})

	t.Run("a rule by position, type or address fires for a node nobody ran", func(t *testing.T) {
		e := h.engine(s, Options{})
		p := plan(t, e)
		loadPolicies(t, e, `changes: [_, _, {name: "never"}, ...]`)
		for name, view := range map[string]*View{
			"printed without them": e.NewView(p, ViewOptions{}),
			"printed with them":    e.NewView(p, ViewOptions{Skipped: true}),
		} {
			err := e.CheckPolicyView(view)
			if err == nil || !strings.Contains(err.Error(), "changes.2.name") {
				t.Errorf("%s: error = %v, want a refusal at changes.2", name, err)
			}
		}
		for _, rule := range []string{
			`changes: [...{skipped?: false}]`,
			`changes: [...{addr: string, if addr == "r001" { error("r001 is in the plan") }}]`,
			`changes: [...{type: string, action: string, if type == "fake_thing" && action == "no-op" { error("a no-op of \(type)") }}]`,
		} {
			loadPolicies(t, e, rule)
			if err := e.CheckPolicy(p); err == nil {
				t.Errorf("%s: the plan satisfies the rule", rule)
			}
		}
		loadPolicies(t, e, `changes: [...{addr: string}], summary: blast_radius: 1`)
		if err := e.CheckPolicy(p); err != nil {
			t.Errorf("a rule that holds: %v", err)
		}
	})

	t.Run("the plan file gives the same document", func(t *testing.T) {
		file := filepath.Join(t.TempDir(), "p.plan")
		if err := e.SavePlan(file, p, nil, false); err != nil {
			t.Fatal(err)
		}
		pf, err := ReadPlanFile(file)
		if err != nil {
			t.Fatal(err)
		}
		if len(pf.Changes) != 1 {
			t.Fatalf("the file holds %d changes, want the one that runs", len(pf.Changes))
		}
		e2 := h.engine(s, Options{Refresh: RefreshNone})
		loaded, err := e2.LoadPlan(pf)
		if err != nil {
			t.Fatal(err)
		}
		a, b := e.NewView(p, ViewOptions{Skipped: true}), e2.NewView(loaded, ViewOptions{Skipped: true})
		slim := func(v *View) string {
			var out []string
			for _, c := range v.Changes {
				out = append(out, fmt.Sprintf("%s %s %s %s %v", c.Addr, c.Mode, c.Type, c.Action, c.Skipped))
			}
			return strings.Join(out, "\n")
		}
		if slim(a) != slim(b) {
			t.Errorf("the plan and the file give different documents:\n%s\n---\n%s", slim(a), slim(b))
		}
		if a.Summary != b.Summary {
			t.Errorf("summary %+v and %+v", a.Summary, b.Summary)
		}
		loadPolicies(t, e2, `changes: [_, _, {name: "never"}, ...]`)
		if err := e2.CheckPolicy(loaded); err == nil || !strings.Contains(err.Error(), "changes.2.name") {
			t.Errorf("a policy judged against the file: %v", err)
		}
	})
}
