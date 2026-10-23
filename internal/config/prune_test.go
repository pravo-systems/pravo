package config

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"testing"

	"cuelang.org/go/cue"
	"cuelang.org/go/cue/cuecontext"

	"github.com/pravo-systems/pravo/internal/schema"
)

// prunePackage is the generated package of a provider with some types of each
// kind, as pravo writes it.
func prunePackage(t *testing.T) string {
	t.Helper()
	str := json.RawMessage(`"string"`)
	block := func(attrs ...string) *schema.Schema {
		b := &schema.Block{}
		for _, a := range attrs {
			b.Attributes = append(b.Attributes, &schema.Attribute{Name: a, TypeJSON: str, Optional: true, Description: "the " + a})
		}
		return &schema.Schema{Block: b}
	}
	ps := &schema.ProviderSchema{
		Provider: block("region"),
		Resources: map[string]*schema.Schema{
			"x_alpha": block("name"), "x_beta": block("name", "size"), "x_gamma": block("name"), "x_delta": block("name"),
		},
		DataSources: map[string]*schema.Schema{"x_lookup": block("key"), "x_other": block("key")},
		Ephemerals:  map[string]*schema.Schema{"x_token": block("value")},
		Functions: map[string]*schema.Function{
			"parse": {Parameters: []*schema.Parameter{{Name: "s", TypeJSON: str}}, ReturnJSON: str},
			"other": {Parameters: []*schema.Parameter{{Name: "s", TypeJSON: str}}, ReturnJSON: str},
		},
	}
	return string(schema.GenerateCUE(ps, "x", schema.GenOptions{Header: "test", Source: "registry.opentofu.org/x/x"}))
}

func useOf(text string) *typeUse {
	u := &typeUse{names: map[string]map[string]bool{}}
	u.scan([]byte(text))
	return u
}

func TestPruneGeneratedBlanksTheTypesThatAreNotUsed(t *testing.T) {
	src := prunePackage(t)
	pruned, ok := pruneGenerated([]byte(src), useOf(`
a: $p.#Resource.x_alpha & {}
b: $p.#Resource.x_gamma & {}
d: $p.#Data.x_lookup & {}
`))
	if !ok {
		t.Fatal("nothing was left out")
	}
	text := string(pruned)
	if strings.Count(text, "\n") != strings.Count(src, "\n") {
		t.Errorf("the lines are not the lines of the file: %d and %d", strings.Count(text, "\n"), strings.Count(src, "\n"))
	}
	for _, kept := range []string{"x_alpha: {", "x_gamma: {", "x_lookup: {"} {
		if !strings.Contains(text, "\t"+kept) {
			t.Errorf("%s is gone", kept)
		}
	}
	for _, gone := range []string{"x_beta", "x_delta", "x_other", "x_token", "parse", "other"} {
		if strings.Contains(text, "\t"+gone+": {") || strings.Contains(text, "the size") && gone == "x_beta" {
			t.Errorf("%s is still there", gone)
		}
	}
	// A type that is kept is on the line that it was on.
	for _, kept := range []string{"x_alpha: {", "x_gamma: {", "x_lookup: {"} {
		if lineOf(text, "\t"+kept) != lineOf(src, "\t"+kept) {
			t.Errorf("%s moved from line %d to %d", kept, lineOf(src, "\t"+kept), lineOf(text, "\t"+kept))
		}
	}
	// What is left defines the same types.
	for _, p := range []struct{ field, want string }{{"#Resource.x_alpha.$kind", "resource"}, {"#Data.x_lookup.$kind", "data"}} {
		if got := lookupString(t, text, p.field); got != p.want {
			t.Errorf("%s = %q", p.field, got)
		}
	}
	if got := lookupString(t, text, "#Resource.x_beta.$kind"); got != "" {
		t.Errorf("x_beta is still defined: %q", got)
	}
}

func TestPruneGeneratedLeavesAnotherLayoutAlone(t *testing.T) {
	use := useOf(`a: $p.#Resource.x_alpha & {}`)
	// Nothing to leave out.
	all := useOf(`a: $p.#Resource.x_alpha & {}
	b: $p.#Resource.x_beta & {}
	c: $p.#Resource.x_gamma & {}
	d: $p.#Resource.x_delta & {}
	e: $p.#Data.x_lookup
	f: $p.#Data.x_other
	g: $p.#Ephemeral.x_token
	h: $p.#Function.parse
	i: $p.#Function.other`)
	if _, ok := pruneGenerated([]byte(prunePackage(t)), all); ok {
		t.Error("a package of which every type is used is not pruned")
	}
	// A package whose catalogues are inside #Provider, as an earlier pravo wrote it.
	old := strings.Replace(prunePackage(t), "\n_#Resource: {\n", "\n\t#Resource: {\n", 1)
	if _, ok := pruneGenerated([]byte(old), use); ok {
		t.Error("a package of another layout is pruned")
	}
	if _, ok := pruneGenerated([]byte("package x\n\n#Provider: {}\n"), use); ok {
		t.Error("a file without catalogues is pruned")
	}
	// A catalogue that does not end is not read.
	cut := prunePackage(t)
	cut = cut[:strings.LastIndex(cut, "\n}\n")]
	if _, ok := pruneGenerated([]byte(cut), use); ok {
		t.Error("a file that stops inside a catalogue is pruned")
	}
}

func TestScanTypeUse(t *testing.T) {
	u := useOf(`
a: $aws.#Resource.aws_s3_bucket & {}
b: aws.#Resource."aws-odd" & {}
c: $r.#Data.aws_ami
f: $r.#Function.arn_parse
// $aws.#Resource.in_a_comment
`)
	if u.dynamic {
		t.Error("every use is of a named type")
	}
	for kind, names := range map[string][]string{"#Resource": {"aws_s3_bucket", "aws-odd", "in_a_comment"}, "#Data": {"aws_ami"}, "#Function": {"arn_parse"}} {
		for _, n := range names {
			if !u.names[kind][n] {
				t.Errorf("%s.%s is not found: %v", kind, n, u.names)
			}
		}
	}
	for name, text := range map[string]string{
		"a whole catalogue":     `all: $aws.#Resource`,
		"a computed name":       `x: $aws.#Resource."\(kind)"`,
		"an index":              `x: $aws.#Resource[name]`,
		"a loop":                `for k, v in $aws.#Resource {}`,
		"a catalogue passed on": `x: f($aws.#Data)`,
	} {
		if !useOf(text).dynamic {
			t.Errorf("%s is not dynamic", name)
		}
	}
}

func lineOf(text, sub string) int {
	i := strings.Index(text, sub)
	if i < 0 {
		return -1
	}
	return strings.Count(text[:i], "\n")
}

func lookupString(t *testing.T, pkg, path string) string {
	t.Helper()
	v := cueCompile(t, pkg)
	s, _ := v.LookupPath(cueParsePath("#Provider." + path)).String()
	return s
}

// A root loads the same with the packages pruned as with them whole.
func TestLoadWithPrunedProviderPackages(t *testing.T) {
	old := pruneMinSize
	pruneMinSize = 0
	t.Cleanup(func() { pruneMinSize = old })
	files := func(spec string) map[string]string {
		return map[string]string{
			"cue.mod/module.cue":                            "module: \"example.com/t\"\nlanguage: version: \"v0.14.0\"\n",
			"cue.mod/gen/pravo.dev/providers/x/x/x_gen.cue": prunePackage(t),
			"main.cue": "package t\n\nimport x \"pravo.dev/providers/x/x\"\n\n$p: x.#Provider\n\n" + spec,
		}
	}
	describe := func(r *Root) string {
		var out []string
		for _, res := range r.Resources {
			out = append(out, fmt.Sprintf("%s %s %s %s", res.Address(), res.Mode, res.Type, res.ProviderKey))
		}
		sort.Strings(out)
		return strings.Join(out, "\n")
	}
	const spec = `
a: $p.#Resource.x_alpha & {name: "a"}
b: $p.#Resource.x_beta & {name: "b", size: a.name}
l: $p.#Data.x_lookup & {key: "k"}
m: {n: $p.#Resource.x_alpha & {name: "n"}}
`
	pruned, err := Load(writeFiles(t, files(spec)), LoadOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if !pruned.Pruned {
		t.Error("the package was not pruned")
	}
	t.Setenv("PRAVO_NO_PRUNE", "1")
	whole, err := Load(writeFiles(t, files(spec)), LoadOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if whole.Pruned {
		t.Error("PRAVO_NO_PRUNE did not stop the pruning")
	}
	if a, b := describe(pruned), describe(whole); a != b || a == "" {
		t.Errorf("the roots differ:\n%s\n---\n%s", a, b)
	}
}

func TestLoadPrunedFailureIsTheFailureOfTheWholePackage(t *testing.T) {
	old := pruneMinSize
	pruneMinSize = 0
	t.Cleanup(func() { pruneMinSize = old })
	load := func(spec string) (*Root, error) {
		return Load(writeFiles(t, map[string]string{
			"cue.mod/module.cue":                            "module: \"example.com/t\"\nlanguage: version: \"v0.14.0\"\n",
			"cue.mod/gen/pravo.dev/providers/x/x/x_gen.cue": prunePackage(t),
			"main.cue": "package t\n\nimport x \"pravo.dev/providers/x/x\"\n\n$p: x.#Provider\n\n" + spec,
		}), LoadOptions{})
	}
	// A value that the schema refuses: the message is the same, and its
	// position in the generated file is a position in the file on disk.
	const bad = `a: $p.#Resource.x_alpha & {name: 1}`
	_, prunedErr := load(bad)
	t.Setenv("PRAVO_NO_PRUNE", "1")
	_, wholeErr := load(bad)
	if prunedErr == nil || wholeErr == nil {
		t.Fatalf("errors: %v, %v", prunedErr, wholeErr)
	}
	strip := func(err error) string { return regexpTempDir.ReplaceAllString(err.Error(), "<dir>") }
	if strip(prunedErr) != strip(wholeErr) {
		t.Errorf("the errors differ:\n%s\n---\n%s", prunedErr, wholeErr)
	}
	// A type that the package does not have.
	_, e1 := load(`a: $p.#Resource.x_missing & {}`)
	if e1 == nil {
		t.Error("a type that is not defined was accepted")
	}
	// A catalogue used as a whole may need any type: the package is whole. The
	// scan reads text, so a mention in a comment counts too.
	t.Setenv("PRAVO_NO_PRUNE", "")
	r, err := load("// every type of $p.#Resource\n" + `a: $p.#Resource.x_alpha & {name: "a"}`)
	if err != nil {
		t.Fatal(err)
	}
	if r.Pruned {
		t.Error("the package was pruned although the specification uses #Resource as a whole")
	}
}

var regexpTempDir = regexp.MustCompile(`[^\s"]*[/\\](?:T|tmp|Temp)[/\\][^\s"]*?[/\\](?:001|002|003|004)`)

func cueCompile(t *testing.T, src string) cue.Value {
	t.Helper()
	v := cuecontext.New().CompileString(src)
	if err := v.Err(); err != nil {
		t.Fatal(err)
	}
	return v
}

func cueParsePath(p string) cue.Path { return cue.ParsePath(p) }
