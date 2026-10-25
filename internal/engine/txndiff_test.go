package engine

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/pravo-systems/pravo/internal/schema"
	"github.com/pravo-systems/pravo/internal/state"
)

func diffSchema() *schema.Schema {
	return &schema.Schema{Block: &schema.Block{
		Attributes: []*schema.Attribute{
			{Name: "name"}, {Name: "port"}, {Name: "blob", Sensitive: true}, {Name: "payload", WriteOnly: true},
			{Name: "api_token"}, {Name: "tags"}, {Name: "url"},
			{Name: "settings", NestedType: &schema.Object{Attributes: []*schema.Attribute{{Name: "region"}, {Name: "key", Sensitive: true}}}},
		},
		BlockTypes: []*schema.NestedBlock{{TypeName: "rule", Block: &schema.Block{Attributes: []*schema.Attribute{{Name: "cidr"}, {Name: "pin", Sensitive: true}}}}},
	}}
}

func TestDiffWithholdsWhatMayHoldASecret(t *testing.T) {
	before := json.RawMessage(`{"name":"a","port":1,"blob":"old-blob-value","payload":"old-payload","api_token":"old-token-1","tags":{"owner":"x","db_password":"old-map-pass"},` +
		`"url":"db://u:old-blob-value@h","settings":{"region":"eu","key":"old-nested"},"rule":[{"cidr":"10/8","pin":"old-pin-1"}]}`)
	after := json.RawMessage(`{"name":"b","port":2,"blob":"new-blob-value","payload":"new-payload","api_token":"new-token-1","tags":{"owner":"y","db_password":"new-map-pass"},` +
		`"url":"db://u:new-blob-value@h","settings":{"region":"us","key":"new-nested"},"rule":[{"cidr":"10/9","pin":"new-pin-1"}]}`)
	d := state.DiffOf(before, after, diffWithhold(diffSchema(), before, after))
	if d == nil {
		t.Fatal("no diff")
	}
	if len(d.Before) != 2 || string(d.After["name"]) != `"b"` || string(d.After["port"]) != "2" {
		t.Errorf("kept: %+v", d)
	}
	for _, name := range []string{"blob", "payload", "api_token", "tags", "url", "settings", "rule"} {
		if d.Withheld[name] != "both" {
			t.Errorf("%s is not withheld: %v", name, d.Withheld)
		}
	}
	raw, _ := json.Marshal(d)
	for _, v := range []string{"blob-value", "-payload", "token-1", "map-pass", "nested", "pin-1"} {
		if strings.Contains(string(raw), v) {
			t.Errorf("the diff keeps %q: %s", v, raw)
		}
	}
	// With no schema nothing says what is safe.
	d = state.DiffOf(before, after, diffWithhold(nil, before, after))
	if len(d.Before) != 0 || len(d.After) != 0 || len(d.Withheld) != 9 {
		t.Errorf("without a schema: %+v", d)
	}
}
