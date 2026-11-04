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
	out := redactData(in, blk).(map[string]any)
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
