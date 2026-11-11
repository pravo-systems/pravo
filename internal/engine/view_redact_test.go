package engine

import (
	"testing"

	"github.com/pravo-systems/pravo/internal/schema"
)

func TestRedactDataWithholdsSensitiveAndWriteOnlyAttributes(t *testing.T) {
	blk := &schema.Block{
		Attributes: []*schema.Attribute{
			{Name: "name"}, {Name: "password", Sensitive: true}, {Name: "token", WriteOnly: true}, {Name: "unset", Sensitive: true},
		},
		BlockTypes: []*schema.NestedBlock{{TypeName: "auth", Block: &schema.Block{Attributes: []*schema.Attribute{{Name: "key", Sensitive: true}, {Name: "user"}}}}},
	}
	in := map[string]any{"name": "db", "password": "hunter2", "token": "t", "unset": nil,
		"auth": []any{map[string]any{"key": "k", "user": "u"}}}
	cv := ChangeView{Before: in, After: in}
	redactChange(&cv, &schema.Schema{Block: blk})
	out := cv.After.(map[string]any)
	if out["name"] != "db" || out["unset"] != nil {
		t.Errorf("a plain or null attribute changed: %v", out)
	}
	for _, k := range []string{"password", "token"} {
		if m, ok := out[k].(map[string]any); !ok || m["$sensitive"] != true {
			t.Errorf("%s is not withheld: %v", k, out[k])
		}
	}
	a := out["auth"].([]any)[0].(map[string]any)
	if a["user"] != "u" || a["key"].(map[string]any)["$sensitive"] != true {
		t.Errorf("the nested block is not redacted: %v", a)
	}
	if in["password"] != "hunter2" {
		t.Errorf("the input was changed")
	}
}

// Plan JSON withholds what the printed plan and the viewer withhold: an attribute whose name says
// it is a secret, and a sensitive attribute inside a nested attribute type.
func TestRedactChangeWithholdsNestedAndNamedSecrets(t *testing.T) {
	blk := &schema.Block{Attributes: []*schema.Attribute{
		{Name: "db_password"},
		{Name: "cfg", NestedType: &schema.Object{Nesting: schema.NestingSingle, Attributes: []*schema.Attribute{{Name: "pw", Sensitive: true}, {Name: "host"}}}},
		{Name: "list", NestedType: &schema.Object{Nesting: schema.NestingList, Attributes: []*schema.Attribute{{Name: "pw", Sensitive: true}}}},
	}}
	cv := ChangeView{After: map[string]any{"db_password": "p4ssw0rd", "cfg": map[string]any{"pw": "hunter2", "host": "h"},
		"list": []any{map[string]any{"pw": "x1"}}}}
	redactChange(&cv, &schema.Schema{Block: blk})
	out := cv.After.(map[string]any)
	marked := func(v any) bool { m, ok := v.(map[string]any); return ok && m["$sensitive"] == true }
	if !marked(out["db_password"]) {
		t.Errorf("a name-guarded attribute is not withheld: %v", out["db_password"])
	}
	cfg := out["cfg"].(map[string]any)
	if !marked(cfg["pw"]) || cfg["host"] != "h" {
		t.Errorf("a sensitive attribute of a nested type is not withheld, or the rest is: %v", cfg)
	}
	if l := out["list"].([]any)[0].(map[string]any); !marked(l["pw"]) {
		t.Errorf("a sensitive attribute in a list of nested objects is not withheld: %v", l)
	}
}
