package tfstate

import (
	"encoding/json"
	"strings"
	"testing"
)

const rewriteState = `{
  "version": 4, "terraform_version": "1.13.1", "serial": 7, "lineage": "l",
  "outputs": {"o": {"value": 1, "type": "number"}},
  "resources": [
    {"mode": "managed", "type": "demo_bucket", "name": "logs", "provider": "provider[\"registry.opentofu.org/demo/demo\"]",
     "instances": [{"schema_version": 0, "attributes": {"id": "b-1", "bucket": "logs"}}]},
    {"module": "module.edge", "mode": "managed", "type": "demo_server", "name": "this", "provider": "provider[\"registry.opentofu.org/demo/demo\"]",
     "instances": [
       {"index_key": 0, "schema_version": 0, "attributes": {"id": "s-0", "name": "a"}},
       {"index_key": "x", "schema_version": 0, "attributes": {"id": "s-1", "name": "b"}}]},
    {"mode": "data", "type": "demo_zone", "name": "z", "provider": "provider[\"registry.opentofu.org/demo/demo\"]",
     "instances": [{"schema_version": 0, "attributes": {"id": "z"}}]}
  ]
}`

func TestConvertSaysWhereEveryInstanceSat(t *testing.T) {
	res, err := Convert([]byte(rewriteState))
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]struct {
		name  string
		key   string
		keyed bool
		mods  int
	}{
		"demo_bucket.logs": {"logs", "", false, 0},
	}
	seen := 0
	for _, r := range res.Resources {
		o, ok := res.Origins[r.Address()]
		if !ok {
			t.Errorf("%s has no origin", r.Address())
			continue
		}
		seen++
		if w, ok := want[o.Address]; ok && (o.Name != w.name || o.Key != w.key || o.Keyed != w.keyed || len(o.Module) != w.mods) {
			t.Errorf("%s: %+v", o.Address, o)
		}
		if strings.HasPrefix(o.Address, "module.edge.") {
			if len(o.Module) != 1 || o.Module[0].Name != "edge" || !o.Keyed || o.Name != "this" {
				t.Errorf("%s: %+v", o.Address, o)
			}
		}
	}
	if seen != 4 {
		t.Errorf("%d origins for %d records", seen, len(res.Resources))
	}
}

func TestRewriteReplacesWhatItKnowsAndKeepsTheRest(t *testing.T) {
	data := []byte(rewriteState)
	out, stats, err := Rewrite(data, func(addr string) *Replacement {
		switch addr {
		case "demo_bucket.logs":
			return &Replacement{Attributes: json.RawMessage(`{"id":"b-1","bucket":"logs"}`), SchemaVersion: 0}
		case `module.edge.demo_server.this[0]`:
			return &Replacement{Attributes: json.RawMessage(`{"id":"s-0","name":"changed"}`), SchemaVersion: 2, Private: []byte("p")}
		case `module.edge.demo_server.this["x"]`:
			return &Replacement{Gone: true}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if stats.Rewritten != 2 || stats.Changed != 1 || stats.Kept != 1 || stats.Dropped != 1 {
		t.Errorf("%+v", stats)
	}
	var got struct {
		Serial    int64                      `json:"serial"`
		Lineage   string                     `json:"lineage"`
		Outputs   map[string]json.RawMessage `json:"outputs"`
		Resources []struct {
			Type      string `json:"type"`
			Instances []struct {
				Attributes    map[string]any `json:"attributes"`
				SchemaVersion int            `json:"schema_version"`
				Private       string         `json:"private"`
			} `json:"instances"`
		} `json:"resources"`
	}
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatal(err)
	}
	if got.Serial != 8 || got.Lineage != "l" || len(got.Outputs) != 1 || len(got.Resources) != 3 {
		t.Fatalf("serial %d lineage %s: %s", got.Serial, got.Lineage, out)
	}
	srv := got.Resources[1].Instances
	if len(srv) != 1 || srv[0].Attributes["name"] != "changed" || srv[0].SchemaVersion != 2 || srv[0].Private == "" {
		t.Errorf("the instance that was rewritten: %+v", srv)
	}
	if got.Resources[2].Type != "demo_zone" {
		t.Errorf("a data source was not kept: %s", out)
	}
	// Then nothing at all: only the serial moves.
	again, stats, err := Rewrite(data, func(string) *Replacement { return nil })
	if err != nil || stats.Kept != 4 || stats.Rewritten != 0 {
		t.Fatalf("%+v %v", stats, err)
	}
	if !strings.Contains(string(again), `"serial": 8`) {
		t.Errorf("the serial of a copy is one more: %s", again)
	}
}
