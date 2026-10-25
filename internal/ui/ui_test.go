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
		EvalOutputs: []string{"link", "list"},
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

func TestRenderPlanShowsOnlyTheOutputsThatChange(t *testing.T) {
	plan := &engine.Plan{
		Changes: []*engine.Change{{Addr: "home", Type: "local_file", Action: engine.ActionCreate, Schema: pageBlock(), Planned: page("x", "y")}},
		SavedOutputs: map[string]json.RawMessage{
			"url":    json.RawMessage(`"https://new"`),
			"steady": json.RawMessage(`"unchanging"`),
			"token":  json.RawMessage(`"s3cret"`),
		},
		UnknownOutputs:   []string{"port"},
		SensitiveOutputs: map[string]bool{"token": true},
		EvalOutputs:      []string{"url", "port", "token"},
		DropOutputs:      []string{"gone"},
		OldOutputs: map[string]json.RawMessage{
			"url":  json.RawMessage(`"https://old"`),
			"gone": json.RawMessage(`7`),
		},
	}
	got := render(plan)
	for _, want := range []string{`url: "https://old" → "https://new"`, `port: (known after apply)`, `token: (sensitive)`, `gone: 7 → (removed)`, `1 output unchanged.`} {
		if !strings.Contains(got, want) {
			t.Errorf("the plan lacks %s:\n%s", want, got)
		}
	}
	for _, not := range []string{"unchanging", "s3cret"} {
		if strings.Contains(got, not) {
			t.Errorf("the plan shows %s:\n%s", not, got)
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
			"Graph: planned 0 · skipped 4 · refreshed 1 · within refresh age 3",
			"No changes. Your infrastructure matches the configuration.", "not read"},
		{"one record not read",
			engine.Plan{Stats: engine.Stats{Total: 4, Skipped: 4, Refreshed: 1, Fresh: 2, NotRead: 1}},
			"Graph: planned 0 · skipped 4 · refreshed 1 · within refresh age 2 · not read 1",
			"No changes. The specification matches the state. 1 record was not read; run pravo drift to compare it with the infrastructure.", "Your infrastructure"},
		{"many records not read",
			engine.Plan{Stats: engine.Stats{Total: 2001, Skipped: 2001, NotRead: 2001}},
			"Graph: planned 0 · skipped 2001 · not read 2001",
			"No changes. The specification matches the state. 2001 records were not read; run pravo drift to compare them with the infrastructure.", "Your infrastructure"},
		{"nothing read at all",
			engine.Plan{NoRefresh: true, Stats: engine.Stats{Total: 4, Skipped: 4}},
			"Graph: planned 0 · skipped 4",
			"No changes. The specification matches the state. Nothing was read from the infrastructure.", "Your infrastructure"},
		{"records of removed resources",
			engine.Plan{Changes: []*engine.Change{{Addr: "gone", Type: "local_file", Action: engine.ActionDelete, Schema: pageBlock(), Prior: page("x", "y")}},
				Stats: engine.Stats{Total: 5, Skipped: 5, Removed: 2, Fresh: 7}},
			"Graph: planned 0 · skipped 5 · removed 2 · within refresh age 7",
			"Plan: 0 to add", "No changes"},
		{"no resource, one function called",
			engine.Plan{Stats: engine.Stats{Called: 1}},
			"Graph: called 1",
			"No changes. Your infrastructure matches the configuration.", "planned"},
		{"no resource, one ephemeral opened",
			engine.Plan{Stats: engine.Stats{Opened: 1}},
			"Graph: opened 1",
			"No changes. Your infrastructure matches the configuration.", "planned"},
		{"drift and several parts",
			engine.Plan{Changes: []*engine.Change{{Addr: "home", Type: "local_file", Action: engine.ActionCreate, Schema: pageBlock(), Planned: page("x", "y")}},
				Stats: engine.Stats{Total: 2001, Reached: 2, Skipped: 1999, Refreshed: 3, Drifted: 1, NotRead: 1998}},
			"Graph: planned 2 · skipped 1999 · refreshed 3 · drifted 1 · not read 1998",
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

// A root with nothing to plan against a record, only variables of the
// environment, say, has no graph line to show.
func TestNoGraphLineWithoutResources(t *testing.T) {
	if got := render(&engine.Plan{}); strings.Contains(got, "Graph:") {
		t.Errorf("a root without resources has a graph line:\n%s", got)
	}
}
