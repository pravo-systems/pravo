package serve

import (
	"strings"
	"testing"
)

// A data source is a source of values too: what a plan read from it is recorded, and the form offers it.
func TestTemplateChoicesReadADataSource(t *testing.T) {
	spec := strings.Replace(askSpecCUE, "boxes.one.db.zones", "zones.names", 1)
	spec = strings.Replace(spec, "$fake: {", "$fake: {\n\t#Data: fake_zones: {\n\t\t$kind: \"data\", $type: \"fake_zones\", $source: \"example.test/x/fake\"\n\t\tstate?: string\n\t\t...\n\t}", 1)
	spec += "\nzones: $fake.#Data.fake_zones & {state: \"available\"}\n"
	rec := record("zones", "fake_zones", `{"state":"eu-north-1","names":["eu-north-1a","eu-north-1b","eu-north-1c"]}`)
	rec.Mode = "data"
	p := templateProjectOf(t, spec, `{"name":"one","blob":"eu-north-1","settings":{"region":"eu-north-1"}}`, rec)
	c := p.choices("zone")
	if c.State != "ok" || len(c.Items) != 3 || c.Items[0].Value != "eu-north-1a" {
		t.Errorf("zones: %+v", c)
	}
	// What no attribute shows is still withheld from a data source: the value of the sensitive attribute is kept out
	// of it when it is not shown anywhere else.
	rec2 := record("zones", "fake_zones", `{"names":["hunter22-secret"]}`)
	rec2.Mode = "data"
	p = templateProjectOf(t, spec, `{"name":"one","blob":"hunter22-secret"}`, rec2)
	if c := p.choices("zone"); c.State == "ok" {
		t.Errorf("a secret is offered: %+v", c)
	}
}
