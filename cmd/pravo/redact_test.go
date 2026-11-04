package main

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/pravo-systems/pravo/internal/schema"
	"github.com/pravo-systems/pravo/internal/state"
)

// An agent that reads the state through pravo mcp must get what the viewer shows: a value
// that the schema marks sensitive, a field built from it, a type without a schema and the
// private blob are withheld, and the rest is kept.
func TestRedactSnapshot(t *testing.T) {
	sch := &schema.Schema{Block: &schema.Block{Attributes: []*schema.Attribute{
		{Name: "result", Sensitive: true, Computed: true},
		{Name: "content"}, {Name: "length"},
	}}}
	rec := func(typ string, attrs string) *state.Resource {
		return &state.Resource{Mode: "managed", Type: typ, Name: typ, ProviderKey: "p", Attributes: json.RawMessage(attrs), Private: []byte("blob")}
	}
	snap := &state.Snapshot{
		Resources: map[string]*state.Resource{
			"pw":      rec("pw", `{"result":"s3cr3t-Value!","length":24}`),
			"note":    rec("pw", `{"content":"derived s3cr3t-Value! and more","length":3}`),
			"plain":   rec("pw", `{"content":"hello world","length":3}`),
			"unknown": rec("mystery", `{"anything":"visible-text"}`),
		},
		Outputs: map[string]json.RawMessage{"o": json.RawMessage(`"has s3cr3t-Value! inside"`), "ok": json.RawMessage(`"fine"`)},
	}
	got, n := redactSnapshot(snap, func(r *state.Resource) *schema.Schema {
		if r.Type == "pw" {
			return sch
		}
		return nil
	})
	all, _ := json.Marshal(got)
	for _, leak := range []string{"s3cr3t-Value!", "visible-text", "blob", "YmxvYg"} {
		if strings.Contains(string(all), leak) {
			t.Errorf("%q survives redaction: %s", leak, all)
		}
	}
	for _, keep := range []string{"hello world", `"fine"`} {
		if !strings.Contains(string(all), keep) {
			t.Errorf("%q was withheld: %s", keep, all)
		}
	}
	if n != 4 {
		t.Errorf("withheld %d values, want 4 (result, note.content, unknown.anything, output o)", n)
	}
	// the input is not changed
	if !strings.Contains(string(snap.Resources["pw"].Attributes), "s3cr3t-Value!") || snap.Resources["pw"].Private == nil {
		t.Error("the snapshot of the store was changed")
	}
}
