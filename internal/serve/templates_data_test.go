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
	spec += "\nzones: $fake.#Data.fake_zones & {state: \"available\"}\nzoneName: {$kind: \"output\", value: zones.names[0]}\nreader: $fake.#Resource.fake_db & {name: zones.names[0]}\n"
	rec := record("zones", "fake_zones", `{"state":"eu-north-1","names":["eu-north-1a","eu-north-1b","eu-north-1c"]}`)
	rec.Mode = "data"
	reader := record("reader", "fake_db", `{"name":"eu-north-1a","pin_code":"eu-north-1","zones":["arn:aws:ssm:eu-north-1:123:parameter/x"]}`, "zones")
	p := templateProjectOf(t, spec, `{"name":"one","pin_code":"eu-north-1","settings":{"region":"eu-north-1"}}`, rec, reader, outputsOf{"zoneName": "eu-north-1a"})
	c := p.choices("zone")
	if c.State != "ok" || len(c.Items) != 3 || c.Items[0].Value != "eu-north-1a" {
		t.Errorf("zones: %+v", c)
	}
	// The output that reads the data source is judged as the data source is.
	found := false
	for _, o := range p.graph().Outputs {
		if o.Name == "zoneName" {
			found = true
			if o.Redacted != "" || o.Value != "eu-north-1a" {
				t.Errorf("zoneName: %+v", o)
			}
		}
	}
	if !found {
		t.Error("no output zoneName")
	}
	// A resource that reads the data source keeps its own typed value out of its own attributes' judgement: the value
	// of an SSM parameter says nothing of its ARN.
	for _, a := range p.detail("reader").Attrs {
		if a.Name == "name" && a.Redacted != "" {
			t.Errorf("name of the reader: %+v", a)
		}
		if a.Name == "zones" && (a.Redacted != "" || len(a.Children) != 1 || a.Children[0].Redacted != "") {
			t.Errorf("zones of the reader: %+v", a)
		}
	}
}
