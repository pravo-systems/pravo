package convert

import (
	"testing"

	"cuelang.org/go/cue"
	"cuelang.org/go/cue/cuecontext"
	"github.com/hashicorp/terraform-plugin-go/tftypes"

	"github.com/pravo-systems/pravo/internal/schema"
)

const testCfg = `
env: *"dev" | "staging" | "prod" @tag(env)
pet: {
	length: number | null
	id: string | null
	keepers?: {[string]: string}
	prefix?: string
}
pet: {
	length: 3
	keepers: environment: env
}
hello: {
	filename: string
	content: string
	id: string | null
}
// a plain field standing between the task and its user
label: string | null
label: pet.id
hello: {
	filename: "hello-\(env).txt"
	content: "Hello from \(pet.id)"
}
viaLabel: {
	filename: "x"
	content: "Hello from \(label)"
}
`

// tasks resolves references to the two tasks of the test configuration.
func tasks(p cue.Path) (cue.Path, cue.Path, bool) {
	sels := p.Selectors()
	if len(sels) > 0 {
		switch sels[0].String() {
		case "pet", "hello", "viaLabel":
			t := cue.MakePath(sels[0])
			return t, t, true
		}
	}
	return cue.Path{}, cue.Path{}, false
}

func petBlock() *schema.Block {
	return &schema.Block{Attributes: []*schema.Attribute{
		{Name: "length", TypeJSON: []byte(`"number"`), Optional: true, Computed: true},
		{Name: "id", TypeJSON: []byte(`"string"`), Computed: true},
		{Name: "keepers", TypeJSON: []byte(`["map","string"]`), Optional: true},
		{Name: "prefix", TypeJSON: []byte(`"string"`), Optional: true},
	}}
}

func fileBlock() *schema.Block {
	return &schema.Block{Attributes: []*schema.Attribute{
		{Name: "filename", TypeJSON: []byte(`"string"`), Required: true},
		{Name: "content", TypeJSON: []byte(`"string"`), Optional: true},
		{Name: "id", TypeJSON: []byte(`"string"`), Computed: true},
	}}
}

func TestBlockClassification(t *testing.T) {
	v := cuecontext.New().CompileString(testCfg)
	if err := v.Err(); err != nil {
		t.Fatal(err)
	}
	c := &Converter{Resolve: ResolverFunc(tasks)}
	pet := c.Block("pet", petBlock(), v.LookupPath(cue.ParsePath("pet")))
	if err := c.Err(); err != nil {
		t.Fatal(err)
	}
	var m map[string]tftypes.Value
	_ = pet.As(&m)
	t.Logf("pet = %s", pet)
	if !m["id"].IsNull() {
		t.Errorf("id should be null (unset computed), got %s", m["id"])
	}
	var keepers map[string]tftypes.Value
	if err := m["keepers"].As(&keepers); err != nil {
		t.Fatalf("keepers: %v (%s)", err, m["keepers"])
	}
	var env string
	if err := keepers["environment"].As(&env); err != nil || env != "dev" {
		t.Errorf("keepers.environment = %s, want \"dev\"", keepers["environment"])
	}
	if !m["prefix"].IsNull() {
		t.Errorf("prefix should be null")
	}

	c = &Converter{Resolve: ResolverFunc(tasks), Self: cue.ParsePath("hello")}
	file := c.Block("hello", fileBlock(), v.LookupPath(cue.ParsePath("hello")))
	if err := c.Err(); err != nil {
		t.Fatal(err)
	}
	_ = file.As(&m)
	t.Logf("file = %s", file)
	if m["content"].IsKnown() {
		t.Errorf("content should be unknown, got %s", m["content"])
	}
	var fn string
	if err := m["filename"].As(&fn); err != nil || fn != "hello-dev.txt" {
		t.Errorf("filename = %s", m["filename"])
	}
	if !c.Unknown || len(c.Deps) != 1 {
		t.Errorf("expected one dependency, got %v", c.Deps)
	}
}

func TestDependencyThroughPlainField(t *testing.T) {
	v := cuecontext.New().CompileString(testCfg)
	if err := v.Err(); err != nil {
		t.Fatal(err)
	}
	c := &Converter{Resolve: ResolverFunc(tasks), Self: cue.ParsePath("viaLabel")}
	file := c.Block("viaLabel", fileBlock(), v.LookupPath(cue.ParsePath("viaLabel")))
	if err := c.Err(); err != nil {
		t.Fatalf("a reference through a plain field was rejected: %v", err)
	}
	var m map[string]tftypes.Value
	_ = file.As(&m)
	if m["content"].IsKnown() {
		t.Errorf("content should be unknown, got %s", m["content"])
	}
	if got := c.DepAddrs(); len(got) != 1 || got[0] != "pet" {
		t.Errorf("deps = %v, want [pet]", got)
	}
}

// A task inside the instance of a definition must not depend on what else the
// instance holds, only on what its own fields refer to.
func TestReferenceToEnclosingInstanceIsNotADependency(t *testing.T) {
	v := cuecontext.New().CompileString(`
pet: {id: string | null}
#S: {
	pre: string
	tp:  "file.txt"
	t:   {filename: tp, content: string | null}
	a:   {content: "x\(t.content)\(pre)"}
}
s: #S & {pre: pet.id}
`)
	if err := v.Err(); err != nil {
		t.Fatal(err)
	}
	resolve := func(p cue.Path) (cue.Path, cue.Path, bool) {
		sels := p.Selectors()
		switch {
		case len(sels) >= 1 && sels[0].String() == "pet":
			t := cue.MakePath(sels[0])
			return t, t, true
		case len(sels) >= 2 && sels[0].String() == "s" && (sels[1].String() == "t" || sels[1].String() == "a"):
			t := cue.MakePath(sels[:2]...)
			return t, t, true
		}
		return cue.Path{}, cue.Path{}, false
	}
	deps, _ := Dependencies(v.LookupPath(cue.ParsePath("s.t")), cue.ParsePath("s.t"), ResolverFunc(resolve))
	if len(deps) != 0 {
		t.Errorf("s.t depends on %v, want nothing: it reads a constant", deps)
	}
	deps, _ = Dependencies(v.LookupPath(cue.ParsePath("s.a")), cue.ParsePath("s.a"), ResolverFunc(resolve))
	got := map[string]bool{}
	for _, d := range deps {
		got[d.String()] = true
	}
	if !got["s.t"] || !got["pet"] || len(got) != 2 {
		t.Errorf("s.a depends on %v, want s.t and pet", got)
	}
}
