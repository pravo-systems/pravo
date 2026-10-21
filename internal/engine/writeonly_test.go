package engine

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-go/tftypes"

	"github.com/pravo-systems/pravo/internal/schema"
)

func strAttr(name string, writeOnly bool) *schema.Attribute {
	return &schema.Attribute{Name: name, TypeJSON: json.RawMessage(`"string"`), Optional: true, WriteOnly: writeOnly}
}

func TestWithoutWriteOnlyNullsEveryLevel(t *testing.T) {
	inner := &schema.Block{Attributes: []*schema.Attribute{strAttr("keep", false), strAttr("secret", true)}}
	nested := &schema.Object{Nesting: schema.NestingList, Attributes: []*schema.Attribute{strAttr("keep", false), strAttr("secret", true)}}
	blk := &schema.Block{
		Attributes: []*schema.Attribute{
			strAttr("name", false), strAttr("token", true),
			{Name: "creds", NestedType: nested, Optional: true},
		},
		BlockTypes: []*schema.NestedBlock{
			{TypeName: "one", Block: inner, Nesting: schema.NestingSingle},
			{TypeName: "many", Block: inner, Nesting: schema.NestingList},
			{TypeName: "uniq", Block: inner, Nesting: schema.NestingSet},
			{TypeName: "named", Block: inner, Nesting: schema.NestingMap},
		},
	}
	objT := inner.Type()
	obj := func(keep, secret string) tftypes.Value {
		return tftypes.NewValue(objT, map[string]tftypes.Value{"keep": str(keep), "secret": str(secret)})
	}
	credsT := tftypes.List{ElementType: nested.ObjectType()}
	v := tftypes.NewValue(blk.Type(), map[string]tftypes.Value{
		"name":  str("n"),
		"token": str("PEM-TOKEN"),
		"creds": tftypes.NewValue(credsT, []tftypes.Value{
			tftypes.NewValue(nested.ObjectType(), map[string]tftypes.Value{"keep": str("k"), "secret": str("PEM-CREDS")}),
		}),
		"one":   obj("k", "PEM-ONE"),
		"many":  tftypes.NewValue(tftypes.List{ElementType: objT}, []tftypes.Value{obj("k", "PEM-MANY")}),
		"uniq":  tftypes.NewValue(tftypes.Set{ElementType: objT}, []tftypes.Value{obj("k", "PEM-SET")}),
		"named": tftypes.NewValue(tftypes.Map{ElementType: objT}, map[string]tftypes.Value{"a": obj("k", "PEM-MAP")}),
	})

	got := withoutWriteOnly(blk, v)
	if text := got.String(); strings.Contains(text, "PEM-") {
		t.Errorf("a write-only value is left: %s", text)
	}
	var m map[string]tftypes.Value
	if err := got.As(&m); err != nil {
		t.Fatal(err)
	}
	var s string
	if err := m["name"].As(&s); err != nil || s != "n" {
		t.Errorf("name = %q, %v; want n", s, err)
	}
	var one map[string]tftypes.Value
	if err := m["one"].As(&one); err != nil {
		t.Fatal(err)
	}
	if err := one["keep"].As(&s); err != nil || s != "k" {
		t.Errorf("one.keep = %q, %v; want k", s, err)
	}
	if !one["secret"].IsNull() || !one["secret"].Type().Is(tftypes.String) {
		t.Errorf("one.secret = %s; want a null string", one["secret"])
	}
	if !got.Type().Equal(v.Type()) {
		t.Errorf("type changed: %s", got.Type())
	}
}

func TestWithoutWriteOnlyLeavesNullAndUnknownBlocks(t *testing.T) {
	inner := &schema.Block{Attributes: []*schema.Attribute{strAttr("secret", true)}}
	blk := &schema.Block{BlockTypes: []*schema.NestedBlock{{TypeName: "many", Block: inner, Nesting: schema.NestingList}}}
	listT := tftypes.List{ElementType: inner.Type()}
	for name, many := range map[string]tftypes.Value{
		"null":    tftypes.NewValue(listT, nil),
		"unknown": tftypes.NewValue(listT, tftypes.UnknownValue),
	} {
		v := tftypes.NewValue(blk.Type(), map[string]tftypes.Value{"many": many})
		if got := withoutWriteOnly(blk, v); !got.Equal(v) {
			t.Errorf("%s: %s, want %s", name, got, v)
		}
	}
	if got := withoutWriteOnly(blk, tftypes.NewValue(blk.Type(), nil)); !got.IsNull() {
		t.Errorf("a null block became %s", got)
	}
}

// A saved plan holds the configuration of a change in a form that anyone who
// can read the file can decode, so it carries no write-only value.
func TestSavedPlanHoldsNoWriteOnlyValue(t *testing.T) {
	h := newHarness(t)
	s := spec{cue: "a: " + thing("a", "content: \"PEM-SECRET\"\n"), resources: []string{"a"}}
	e := h.engine(s, Options{})
	plan, err := e.Plan(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Changes) != 1 {
		t.Fatalf("%d changes", len(plan.Changes))
	}
	ch := plan.Changes[0]
	if !strings.Contains(ch.Config.String(), "PEM-SECRET") {
		t.Fatalf("the planned configuration does not hold the value: %s", ch.Config)
	}
	// content is write-only for this test.
	marked := &schema.Block{}
	for _, a := range ch.Schema.Block.Attributes {
		c := *a
		c.WriteOnly = a.Name == "content"
		marked.Attributes = append(marked.Attributes, &c)
	}
	ch.Schema = &schema.Schema{Block: marked}

	file := filepath.Join(t.TempDir(), "p.plan")
	if err := e.SavePlan(file, plan, nil, false); err != nil {
		t.Fatal(err)
	}
	pf, err := ReadPlanFile(file)
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := unpackValue(pf.Changes[0].Config, marked.Type())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(cfg.String(), "PEM-SECRET") {
		t.Errorf("the saved configuration holds the value: %s", cfg)
	}
	var m map[string]tftypes.Value
	if err := cfg.As(&m); err != nil {
		t.Fatal(err)
	}
	var name string
	if err := m["name"].As(&name); err != nil || name != "a" {
		t.Errorf("name = %q, %v; want a", name, err)
	}
	if !m["content"].IsNull() {
		t.Errorf("content = %s; want null", m["content"])
	}
}
