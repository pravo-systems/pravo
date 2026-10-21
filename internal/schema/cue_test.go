package schema

import (
	"encoding/json"
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
