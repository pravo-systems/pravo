package schema

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"cuelang.org/go/cue"
	"cuelang.org/go/cue/cuecontext"
)

func listFixture() *ProviderSchema {
	ps := packFixture()
	str := json.RawMessage(`"string"`)
	ps.ListResources = map[string]*Schema{
		"x_alpha": {Version: 0, Block: &Block{
			Description: "Lists the alphas.",
			Attributes: []*Attribute{
				{Name: "region", TypeJSON: str, Optional: true, Description: "Where to look."},
				{Name: "owner", TypeJSON: str, Required: true},
			},
		}},
	}
	return ps
}

func TestPackKeepsListResources(t *testing.T) {
	orig := listFixture()
	p, err := OpenPack(writePackFile(t, orig))
	if err != nil {
		t.Fatal(err)
	}
	if got := p.SortedListNames(); len(got) != 1 || got[0] != "x_alpha" {
		t.Fatalf("list resource names = %v", got)
	}
	if len(p.ListResources) != 0 {
		t.Errorf("a list resource is decoded before it is asked for: %d", len(p.ListResources))
	}
	if a, b := asJSON(t, p.ListResource("x_alpha")), asJSON(t, orig.ListResources["x_alpha"]); a != b {
		t.Errorf("x_alpha = %s, want %s", a, b)
	}
	if p.ListResource("nope") != nil {
		t.Error("a list resource that the provider lacks was found")
	}
	if err := p.Materialize(); err != nil {
		t.Fatal(err)
	}
	a, _ := orig.MarshalPack()
	b, err := p.MarshalPack()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(a, b) {
		t.Error("a schema that was read from a pack is not written as the pack it came from")
	}
}

func TestListResourcesSurviveTheJSONCache(t *testing.T) {
	data, err := listFixture().Marshal()
	if err != nil {
		t.Fatal(err)
	}
	got, err := Unmarshal(data)
	if err != nil {
		t.Fatal(err)
	}
	if got.ListResource("x_alpha") == nil {
		t.Fatalf("the JSON cache lost the list resource: %s", data)
	}
	// A provider with none reads back with an empty set, not a nil map.
	data, _ = packFixture().Marshal()
	got, err = Unmarshal(data)
	if err != nil || got.ListResources == nil {
		t.Fatalf("%v %v", got.ListResources, err)
	}
}

func TestACacheOfAnOlderPravoLacksListResources(t *testing.T) {
	old := []byte(`{"cache_version":3,"provider":{"version":0,"block":{}},"resources":{},"data_sources":{},"ephemerals":{},"functions":{}}`)
	if _, err := Unmarshal(old); !errors.Is(err, ErrStaleCache) {
		t.Fatalf("a cache of version 3 is read as it is: %v", err)
	}
}

func TestGenerateCUEWritesDiscoveries(t *testing.T) {
	ps := listFixture()
	src := GenerateCUE(ps, "x", GenOptions{Source: "registry.opentofu.org/x/x"})
	text := string(src)
	for _, want := range []string{"\n\t#Discovery: _#Discovery\n", "\n_#Discovery: {\n", `$kind:   "discovery"`, "$limit?: int & >0"} {
		if !strings.Contains(text, want) {
			t.Errorf("the package lacks %q:\n%s", want, text)
		}
	}
	ctx := cuecontext.New()
	v := ctx.CompileBytes(src)
	if err := v.Err(); err != nil {
		t.Fatalf("generated CUE does not compile: %v\n%s", err, text)
	}
	def := v.LookupPath(cue.MakePath(cue.Def("#Provider"), cue.Def("#Discovery"), cue.Str("x_alpha")))
	for path, want := range map[string]string{"$kind": "discovery", "$type": "x_alpha", "$source": "registry.opentofu.org/x/x"} {
		got, err := def.LookupPath(cue.ParsePath(path)).String()
		if err != nil || got != want {
			t.Errorf("%s = %q, %v; want %q", path, got, err, want)
		}
	}
	// The scope is the list resource's own schema: owner is required, region is not,
	// and a field that the schema lacks is refused.
	ok := def.Unify(ctx.CompileString(`{owner: "me", region: "eu", $limit: 10}`))
	if err := ok.Validate(cue.Concrete(true)); err != nil {
		t.Fatalf("a scope that the schema allows: %v", err)
	}
	if err := def.Unify(ctx.CompileString(`{owner: "me", nope: 1}`)).Validate(); err == nil {
		t.Error("a field outside the list resource's schema was accepted")
	}
	if err := def.Unify(ctx.CompileString(`{owner: "me", $limit: 0}`)).Validate(); err == nil {
		t.Error("a limit of 0 was accepted")
	}
	ack := def.Unify(ctx.CompileString(`{owner: "me", $ack: [{names: ["batch-*"], reason: "nightly"}, {ids: ["i-1"]}]}`))
	if err := ack.Validate(cue.Concrete(true)); err != nil {
		t.Fatalf("an $ack that the definition allows: %v", err)
	}
	if err := def.Unify(ctx.CompileString(`{owner: "me", $ack: [{name: "x"}]}`)).Validate(); err == nil {
		t.Error("an $ack entry with a field it does not have was accepted")
	}
	if err := def.Validate(cue.Concrete(true)); err == nil {
		t.Error("a scope without its required attribute was concrete")
	}
}

func TestGenerateCUEWithoutListResourcesStillHasTheCatalogue(t *testing.T) {
	src := string(GenerateCUE(&ProviderSchema{Resources: map[string]*Schema{}}, "x", GenOptions{}))
	if !strings.Contains(src, "\n_#Discovery: {\n}\n") || !strings.Contains(src, "#Discovery: _#Discovery") {
		t.Errorf("a provider with no list resources lacks an empty #Discovery:\n%s", src)
	}
	if err := cuecontext.New().CompileString(src).Err(); err != nil {
		t.Fatal(err)
	}
}

func TestPackGenerateCUEWithListResourcesMatches(t *testing.T) {
	ps := listFixture()
	lazy, err := OpenPack(writePackFile(t, ps))
	if err != nil {
		t.Fatal(err)
	}
	opts := GenOptions{Header: "h", Source: "s"}
	if want, got := GenerateCUE(ps, "x", opts), GenerateCUE(lazy, "x", opts); !bytes.Equal(want, got) {
		t.Errorf("the CUE generated from the pack differs:\n%s\n---\n%s", want, got)
	}
}
