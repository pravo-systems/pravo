package main

import (
	"strings"
	"testing"

	"github.com/pravo-systems/pravo/internal/registry"
)

func TestValidateAndPlanSayWhenTheSpecificationIsWrittenAsHCL(t *testing.T) {
	dir := specDir(t, map[string]string{"main.cue": "package x\n\nproviders: random: source: \"hashicorp/random\"\nresource: random_pet: pet: {length: 2}\n"})
	for _, cmd := range []string{"validate", "plan"} {
		out, code := runPravo(t, dir, cmd)
		if code != 0 {
			t.Fatalf("%s: exit %d\n%s", cmd, code, out)
		}
		for _, want := range []string{`The fields "providers", "resource" are named like blocks of HCL`, "$random: rnd.#Provider", "pravo.dev/providers/hashicorp/random"} {
			if !strings.Contains(out, want) {
				t.Errorf("%s: no %q in\n%s", cmd, want, out)
			}
		}
	}
	// --json is not changed by it.
	out, _ := runPravo(t, dir, "--json", "validate")
	if strings.Contains(out, "hint") {
		t.Errorf("--json carries the hint: %s", out)
	}
}

func TestPlainCUEGetsNoHint(t *testing.T) {
	dir := specDir(t, map[string]string{"main.cue": "package x\n\nsize: >=1 & <=20 | *5\nname: \"web\\(size)\"\n"})
	for _, cmd := range []string{"validate", "plan"} {
		if out, _ := runPravo(t, dir, cmd); strings.Contains(out, "hint") {
			t.Errorf("%s on plain CUE: %s", cmd, out)
		}
	}
}

func TestSchemaNamesAreParsed(t *testing.T) {
	got, err := parseSchemaArgs([]string{"hashicorp/random/random_pet", "registry.example.com/acme/web/web_page"})
	if err != nil || len(got) != 2 {
		t.Fatalf("%+v %v", got, err)
	}
	if got[0].typ != "random_pet" || got[0].local != "random" || got[0].src == nil || got[0].src.String() != "registry.opentofu.org/hashicorp/random" {
		t.Errorf("first %+v", got[0])
	}
	if got[1].typ != "web_page" || got[1].src.String() != "registry.example.com/acme/web" {
		t.Errorf("second %+v", got[1])
	}
	short, _ := parseSchemaArgs([]string{"random_pet", "local/local_file"})
	if short[0].local != "random" || short[0].src != nil || short[1].local != "local" || short[1].typ != "local_file" {
		t.Errorf("short forms %+v", short)
	}
	if _, err := parseSchemaArgs([]string{"random_pet", "hashicorp/random/random_id"}); err == nil || !strings.Contains(err.Error(), "the same way") {
		t.Errorf("mixed forms: %v", err)
	}
	if _, err := parseSchemaArgs([]string{"a/b/c/d/e"}); err == nil {
		t.Error("five parts are not a type")
	}
	// Two providers that share a type name get different local names.
	two, _ := parseSchemaArgs([]string{"a/web/web_x", "b/web/web_y"})
	if two[0].local == two[1].local {
		t.Errorf("one local name for two providers: %+v", two)
	}
}

func TestSchemaWithoutASpecificationSaysHowToNameTheProvider(t *testing.T) {
	out, code := runPravo(t, t.TempDir(), "schema", "random_pet")
	if code == 0 || !strings.Contains(out, "this directory has no specification") || !strings.Contains(out, "hashicorp/random/random_pet") {
		t.Errorf("exit %d\n%s", code, out)
	}
}

func TestSchemaPrefixMustNotBeAmbiguous(t *testing.T) {
	hashi := registry.Source{Host: registry.DefaultHost, Namespace: "hashicorp", Type: "random"}
	acme := registry.Source{Host: registry.DefaultHost, Namespace: "acme", Type: "random"}
	other := registry.Source{Host: registry.DefaultHost, Namespace: "hashicorp", Type: "null"}
	target := schemaTarget{arg: "random_integer", local: "random", typ: "random_integer", bare: true}
	if err := checkPrefix(target, []registry.Source{hashi, other}); err != nil {
		t.Errorf("one provider named random: %v", err)
	}
	err := checkPrefix(target, []registry.Source{hashi, acme, other})
	if err == nil || !strings.Contains(err.Error(), "ambiguous") || !strings.Contains(err.Error(), "acme/random") || !strings.Contains(err.Error(), "hashicorp/random") {
		t.Errorf("two providers named random: %v", err)
	}
	if got, _ := parseSchemaArgs([]string{"random_integer", "random/random_integer", "hashicorp/random/random_integer"}[:2]); !got[0].bare || got[1].bare {
		t.Errorf("only the prefix form is a guess: %+v", got)
	}
}
