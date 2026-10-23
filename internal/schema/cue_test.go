package schema

import (
	"encoding/json"
	"strings"
	"testing"

	"cuelang.org/go/cue"
	"cuelang.org/go/cue/cuecontext"
)

func TestGenerateCUECompilesAndShadowSafe(t *testing.T) {
	ps := &ProviderSchema{
		Provider: &Schema{Block: &Block{Attributes: []*Attribute{{Name: "region", TypeJSON: json.RawMessage(`"string"`), Optional: true}}}},
		Resources: map[string]*Schema{
			"x_thing": {Block: &Block{
				Attributes: []*Attribute{
					{Name: "id", TypeJSON: json.RawMessage(`"string"`), Computed: true},
					{Name: "number", TypeJSON: json.RawMessage(`"bool"`), Optional: true},
					{Name: "length", TypeJSON: json.RawMessage(`"number"`), Required: true},
					{Name: "for", TypeJSON: json.RawMessage(`["list","string"]`), Optional: true},
					{Name: "tags", TypeJSON: json.RawMessage(`["map","string"]`), Optional: true, Computed: true},
					{Name: "obj", TypeJSON: json.RawMessage(`["object",{"a":"string","b":["set","number"]}]`), Optional: true},
				},
				BlockTypes: []*NestedBlock{{TypeName: "rule", Nesting: NestingList, Block: &Block{Attributes: []*Attribute{
					{Name: "port", TypeJSON: json.RawMessage(`"number"`), Required: true, Description: "The port"},
				}}}},
			}},
		},
		DataSources: map[string]*Schema{},
	}
	src := GenerateCUE(ps, "x", GenOptions{Header: "test"})
	t.Logf("\n%s", src)
	ctx := cuecontext.New()
	v := ctx.CompileBytes(src)
	if err := v.Err(); err != nil {
		t.Fatalf("generated CUE does not compile: %v", err)
	}
	def := v.LookupPath(cue.MakePath(cue.Def("#Provider"), cue.Def("#Resource"), cue.Str("x_thing")))
	inst := def.Unify(ctx.CompileString(`{length: 24, number: true, "for": ["a"], rule: [{port: 80}]}`))
	if err := inst.Validate(); err != nil {
		t.Fatalf("instance does not validate: %v", err)
	}
	bad := def.Unify(ctx.CompileString(`{length: 24, nope: 1}`))
	if err := bad.Validate(); err == nil {
		t.Fatal("unknown field should be rejected")
	}
	if !def.LookupPath(cue.ParsePath("id")).Exists() {
		t.Fatal("computed attribute must be a regular field")
	}
}

// The definitions of a provider are written once, at package level, and the
// members of #Provider refer to them: CUE copies what a struct holds every time
// it is unified, and an AWS provider holds thousands of definitions.
func TestGenerateCUEWritesTheDefinitionsOnce(t *testing.T) {
	ps := &ProviderSchema{
		Resources:   map[string]*Schema{"x_thing": {Block: &Block{}}},
		DataSources: map[string]*Schema{"x_lookup": {Block: &Block{}}},
	}
	src := string(GenerateCUE(ps, "x", GenOptions{}))
	for _, want := range []string{
		"\n\t#Resource: _#Resource\n", "\n\t#Data: _#Data\n", "\n\t#Ephemeral: _#Ephemeral\n", "\n\t#Function: _#Function\n",
		"\n_#Resource: {\n", "\n_#Data: {\n", "\n_#Ephemeral: {\n", "\n_#Function: {\n",
	} {
		if !strings.Contains(src, want) {
			t.Errorf("the package lacks %q:\n%s", want, src)
		}
	}
	ctx := cuecontext.New()
	v := ctx.CompileBytes([]byte(src))
	if err := v.Err(); err != nil {
		t.Fatal(err)
	}
	// A provider struct reaches a definition as before.
	p := v.LookupPath(cue.MakePath(cue.Def("#Provider")))
	for path, want := range map[string]string{"#Resource.x_thing.$kind": "resource", "#Data.x_lookup.$kind": "data"} {
		got, err := p.LookupPath(cue.ParsePath(path)).String()
		if err != nil || got != want {
			t.Errorf("%s = %q, %v; want %q", path, got, err, want)
		}
	}
}
