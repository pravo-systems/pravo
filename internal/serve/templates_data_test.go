package serve

import (
	"strings"
	"testing"
)

// A data source is a source of values too: what a plan read from it is recorded, and the form offers it. A sensitive
// value that a specification typed into another node (here the region, kept in a sensitive attribute) does not hide
// what the data source holds, since nothing it reads leads to that node.
func TestTemplateChoicesReadADataSource(t *testing.T) {
	spec := strings.Replace(askSpecCUE, "boxes.one.db.zones", "zones.names", 1)
	spec = strings.Replace(spec, "$fake: {", "$fake: {\n\t#Data: fake_zones: {\n\t\t$kind: \"data\", $type: \"fake_zones\", $source: \"example.test/x/fake\"\n\t\tstate?: string\n\t\t...\n\t}", 1)
	spec += "\nzones: $fake.#Data.fake_zones & {state: \"available\"}\n"
	rec := record("zones", "fake_zones", `{"state":"eu-north-1","names":["eu-north-1a","eu-north-1b","eu-north-1c"]}`)
	rec.Mode = "data"
	p := templateProjectOf(t, spec, `{"name":"one","pin_code":"eu-north-1","settings":{"region":"eu-north-1"}}`, rec)
	c := p.choices("zone")
	if c.State != "ok" || len(c.Items) != 3 || c.Items[0].Value != "eu-north-1a" {
		t.Errorf("zones: %+v", c)
	}
}
