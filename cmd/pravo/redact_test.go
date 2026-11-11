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

// A block that is withheld for the one secret in it does not make its settings secret: the words that
// sit beside the secret ("enabled", "optional") are not made values, so the attributes that hold them
// stay, while the secret itself still taints what echoes it.
func TestRedactKeepsWhatOnlySharesASettingWithASecret(t *testing.T) {
	str := func(n string, o ...func(*schema.Attribute)) *schema.Attribute {
		a := &schema.Attribute{Name: n, Optional: true}
		for _, f := range o {
			f(a)
		}
		return a
	}
	sch := &schema.Schema{Block: &schema.Block{
		Attributes: []*schema.Attribute{str("versioning"), str("note")},
		BlockTypes: []*schema.NestedBlock{{TypeName: "metadata_options", Nesting: schema.NestingList, Block: &schema.Block{Attributes: []*schema.Attribute{
			str("http_endpoint"), str("http_tokens"),
		}}}},
	}}
	rec := &state.Resource{Mode: "managed", Type: "t", Name: "a", ProviderKey: "p", Attributes: json.RawMessage(
		`{"metadata_options":[{"http_endpoint":"enabled","http_tokens":"optional-secret-1"}],"versioning":[{"enabled":false,"status":"enabled"}],"note":"copy optional-secret-1 here"}`)}
	got, _ := redactSnapshot(&state.Snapshot{Resources: map[string]*state.Resource{"a": rec}}, func(*state.Resource) *schema.Schema { return sch })
	var attrs map[string]json.RawMessage
	_ = json.Unmarshal(got.Resources["a"].Attributes, &attrs)
	if !strings.Contains(string(attrs["versioning"]), "enabled") {
		t.Errorf("versioning was withheld for a word that the settings share: %s", attrs["versioning"])
	}
	if strings.Contains(string(attrs["note"]), "optional-secret-1") {
		t.Errorf("the echo of the secret survives: %s", attrs["note"])
	}
}

// The value of a setting that only its name marks as secret (http_tokens: "optional") is a word of the
// enum and not a made value: a tag with the same word stays.
func TestRedactKeepsAnEnumWordUnderANamedKey(t *testing.T) {
	sch := &schema.Schema{Block: &schema.Block{
		Attributes: []*schema.Attribute{{Name: "tags", Optional: true}},
		BlockTypes: []*schema.NestedBlock{{TypeName: "metadata_options", Nesting: schema.NestingList, Block: &schema.Block{Attributes: []*schema.Attribute{
			{Name: "http_tokens", Optional: true},
		}}}},
	}}
	rec := &state.Resource{Mode: "managed", Type: "t", Name: "a", ProviderKey: "p", Attributes: json.RawMessage(
		`{"metadata_options":[{"http_tokens":"optional"}],"tags":{"Backup":"optional"}}`)}
	got, _ := redactSnapshot(&state.Snapshot{Resources: map[string]*state.Resource{"a": rec}}, func(*state.Resource) *schema.Schema { return sch })
	var attrs map[string]json.RawMessage
	_ = json.Unmarshal(got.Resources["a"].Attributes, &attrs)
	if !strings.Contains(string(attrs["tags"]), "optional") {
		t.Errorf("tags was withheld for an enum word: %s", attrs["tags"])
	}
}
