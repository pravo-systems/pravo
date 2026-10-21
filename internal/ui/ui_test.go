package ui

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-go/tftypes"

	"github.com/pravo-systems/pravo/internal/engine"
	"github.com/pravo-systems/pravo/internal/schema"
)

var pageType = tftypes.Object{AttributeTypes: map[string]tftypes.Type{
	"content": tftypes.String,
	"tags":    tftypes.List{ElementType: tftypes.String},
}}

func pageBlock() *schema.Schema {
	return &schema.Schema{Block: &schema.Block{Attributes: []*schema.Attribute{
		{Name: "content", TypeJSON: []byte(`"string"`), Optional: true},
		{Name: "tags", TypeJSON: []byte(`["list","string"]`), Optional: true},
	}}}
}

func page(content, tag string) tftypes.Value {
	return tftypes.NewValue(pageType, map[string]tftypes.Value{
		"content": tftypes.NewValue(tftypes.String, content),
		"tags":    tftypes.NewValue(tftypes.List{ElementType: tftypes.String}, []tftypes.Value{tftypes.NewValue(tftypes.String, tag)}),
	})
}

func render(plan *engine.Plan) string {
	var buf bytes.Buffer
	NewPrinter(&buf, true).RenderPlan(plan)
	return buf.String()
}

func TestRenderPlanKeepsMarkupCharacters(t *testing.T) {
	plan := &engine.Plan{Changes: []*engine.Change{
		{Addr: "home", Type: "local_file", Action: engine.ActionCreate, Schema: pageBlock(), Planned: page("<h1>Tom & Jerry</h1>", "<b>")},
		{Addr: "about", Type: "local_file", Action: engine.ActionUpdate, Schema: pageBlock(),
			Prior: page("<p>old & grey</p>", "<i>"), Planned: page("<p>new & bright</p>", "<i>")},
		{Addr: "gone", Type: "local_file", Action: engine.ActionDelete, Schema: pageBlock(), Prior: page("<hr>", "a&b")},
	}}
	got := render(plan)
	if strings.Contains(got, `\u00`) {
		t.Errorf("the plan escapes markup characters:\n%s", got)
	}
	for _, want := range []string{
		`content: "<h1>Tom & Jerry</h1>"`,
		`"<b>"`,
		`"<p>old & grey</p>" -> "<p>new & bright</p>"`,
		`content: "<hr>"`,
		`"a&b"`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("the plan lacks %s:\n%s", want, got)
		}
	}
}

func TestRenderPlanOutputsKeepMarkupCharacters(t *testing.T) {
	plan := &engine.Plan{
		Changes: []*engine.Change{{Addr: "home", Type: "local_file", Action: engine.ActionCreate, Schema: pageBlock(), Planned: page("x", "y")}},
		SavedOutputs: map[string]json.RawMessage{
			"link": json.RawMessage(`"<a href=\"/\">home</a> & more"`),
			"list": json.RawMessage(`["<b>", {"k": "a&b"}]`),
		},
	}
	got := render(plan)
	if strings.Contains(got, `\u00`) {
		t.Errorf("the outputs escape markup characters:\n%s", got)
	}
	for _, want := range []string{`link: "<a href=\"/\">home</a> & more"`, `"<b>"`, `"a&b"`} {
		if !strings.Contains(got, want) {
			t.Errorf("the outputs lack %s:\n%s", want, got)
		}
	}
}

func TestJSONToCUEKeepsMarkupCharacters(t *testing.T) {
	got := JSONToCUE([]byte(`{"html":"\u003cb\u003e Tom \u0026 Jerry","list":["\u003c/b\u003e"]}`), "")
	if strings.Contains(got, `\u00`) || !strings.Contains(got, `"<b> Tom & Jerry"`) || !strings.Contains(got, `"</b>"`) {
		t.Errorf("JSONToCUE = %s", got)
	}
}

func TestGraphLineAndClosingSentenceSayWhatWasNotRead(t *testing.T) {
	cases := []struct {
		name       string
		plan       engine.Plan
		graph      string
		closing    string
		notClosing string
	}{
		{"everything read or young",
			engine.Plan{Stats: engine.Stats{Total: 4, Skipped: 4, Refreshed: 1, Fresh: 3}},
			"Graph: planned 0 of 4, 4 unchanged and skipped, 1 refreshed, 3 within refresh age.",
			"No changes. Your infrastructure matches the configuration.", "not read"},
		{"one record not read",
			engine.Plan{Stats: engine.Stats{Total: 4, Skipped: 4, Refreshed: 1, Fresh: 2, NotRead: 1}},
			"Graph: planned 0 of 4, 4 unchanged and skipped, 1 refreshed, 2 within refresh age, 1 not read.",
			"No changes. The specification matches the state. 1 record was not read; run pravo drift to compare it with the infrastructure.", "Your infrastructure"},
		{"many records not read",
			engine.Plan{Stats: engine.Stats{Total: 2001, Skipped: 2001, NotRead: 2001}},
			"Graph: planned 0 of 2001, 2001 unchanged and skipped, 2001 not read.",
			"No changes. The specification matches the state. 2001 records were not read; run pravo drift to compare them with the infrastructure.", "Your infrastructure"},
		{"nothing read at all",
			engine.Plan{NoRefresh: true, Stats: engine.Stats{Total: 4, Skipped: 4}},
			"Graph: planned 0 of 4, 4 unchanged and skipped.",
			"No changes. The specification matches the state. Nothing was read from the infrastructure.", "Your infrastructure"},
		{"records of removed resources",
			engine.Plan{Changes: []*engine.Change{{Addr: "gone", Type: "local_file", Action: engine.ActionDelete, Schema: pageBlock(), Prior: page("x", "y")}},
				Stats: engine.Stats{Total: 5, Skipped: 5, Removed: 2, Fresh: 7}},
			"Graph: planned 0 of 5, 5 unchanged and skipped, 2 removed from the specification, 7 within refresh age.",
			"Plan: 0 to add", "No changes"},
		{"drift and several parts",
			engine.Plan{Changes: []*engine.Change{{Addr: "home", Type: "local_file", Action: engine.ActionCreate, Schema: pageBlock(), Planned: page("x", "y")}},
				Stats: engine.Stats{Total: 2001, Reached: 2, Skipped: 1999, Refreshed: 3, Drifted: 1, NotRead: 1998}},
			"Graph: planned 2 of 2001, 1999 unchanged and skipped, 3 refreshed, 1 drifted, 1998 not read.",
			"Plan: 1 to add", "No changes"},
	}
	for _, c := range cases {
		plan := c.plan
		got := render(&plan)
		for _, want := range []string{c.graph, c.closing} {
			if !strings.Contains(got, want) {
				t.Errorf("%s: the output lacks %q:\n%s", c.name, want, got)
			}
		}
		if strings.Contains(got, c.notClosing) {
			t.Errorf("%s: the output has %q:\n%s", c.name, c.notClosing, got)
		}
	}
}
