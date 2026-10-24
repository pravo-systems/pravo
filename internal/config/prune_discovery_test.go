package config

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/pravo-systems/pravo/internal/schema"
)

func pruneDiscoveryPackage(t *testing.T) string {
	t.Helper()
	str := json.RawMessage(`"string"`)
	block := func(attrs ...string) *schema.Schema {
		b := &schema.Block{}
		for _, a := range attrs {
			b.Attributes = append(b.Attributes, &schema.Attribute{Name: a, TypeJSON: str, Optional: true})
		}
		return &schema.Schema{Block: b}
	}
	ps := &schema.ProviderSchema{
		Provider:      block("region"),
		Resources:     map[string]*schema.Schema{"x_alpha": block("name"), "x_beta": block("name")},
		ListResources: map[string]*schema.Schema{"x_alpha": block("region"), "x_beta": block("region")},
	}
	return string(schema.GenerateCUE(ps, "x", schema.GenOptions{Source: "registry.opentofu.org/x/x"}))
}

func TestPruneKeepsTheDiscoveriesThatAreUsed(t *testing.T) {
	src := pruneDiscoveryPackage(t)
	pruned, ok := pruneGenerated([]byte(src), useOf(`
a: $p.#Resource.x_alpha & {}
d: $p.#Discovery.x_beta & {}
`))
	if !ok {
		t.Fatal("nothing was left out")
	}
	text := string(pruned)
	if strings.Count(text, "\n") != strings.Count(src, "\n") {
		t.Error("the lines are not the lines of the file")
	}
	if got := lookupString(t, text, "#Discovery.x_beta.$kind"); got != "discovery" {
		t.Errorf("the discovery that is used is gone: %q", got)
	}
	if got := lookupString(t, text, "#Discovery.x_alpha.$kind"); got != "" {
		t.Errorf("a discovery that is not used is still defined: %q", got)
	}
	if got := lookupString(t, text, "#Resource.x_alpha.$kind"); got != "resource" {
		t.Errorf("the resource that is used is gone: %q", got)
	}
	if got := lookupString(t, text, "#Resource.x_beta.$kind"); got != "" {
		t.Errorf("x_beta the resource is still defined although only its discovery is used: %q", got)
	}
}

func TestPruneReadsAPackageWithoutDiscoveries(t *testing.T) {
	// A package that an earlier pravo wrote has no catalogue of discoveries and is
	// still pruned.
	old := prunePackage(t)
	old = strings.Replace(old, "\n_#Discovery: {\n}\n", "\n", 1)
	if strings.Contains(old, "_#Discovery") && !strings.Contains(old, "#Discovery: _#Discovery") {
		t.Fatal("the fixture is not the older layout")
	}
	if _, ok := pruneGenerated([]byte(old), useOf(`a: $p.#Resource.x_alpha & {}`)); !ok {
		t.Error("a package without discoveries is no longer pruned")
	}
}

func TestScanTypeUseReadsDiscoveries(t *testing.T) {
	u := useOf(`d: $aws.#Discovery.aws_instance & {}
e: $aws.#Discovery."aws-odd" & {}`)
	if !u.names["#Discovery"]["aws_instance"] || !u.names["#Discovery"]["aws-odd"] || u.dynamic {
		t.Errorf("%+v", u)
	}
	if !useOf(`d: $aws.#Discovery`).dynamic {
		t.Error("a catalogue used as a whole is not dynamic")
	}
}
