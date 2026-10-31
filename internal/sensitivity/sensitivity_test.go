package sensitivity

import (
	"testing"

	"github.com/pravo-systems/pravo/internal/schema"
)

func TestDeriveFollowsReadsToAFixedPoint(t *testing.T) {
	refs := Refs{
		"b": {"content": {{Task: "a", Attr: "pin"}}},
		"c": {"content": {{Task: "b", Attr: "content"}}},
		"d": {"content": {{Task: "a", Attr: "name"}}, "all": {{Task: "c"}}},
	}
	// c comes before b in the order, so a single pass would miss it.
	d := Derive([]string{"d", "c", "b", "a"}, refs, NewDerived(), func(task, attr string, so Derived) bool {
		if task == "a" {
			return attr == "pin"
		}
		if attr == "" {
			return len(so.Fields[task]) > 0
		}
		return so.Fields[task][attr]
	})
	for _, want := range [][2]string{{"b", "content"}, {"c", "content"}, {"d", "all"}} {
		if !d.Fields[want[0]][want[1]] {
			t.Errorf("%s.%s is not derived: %v", want[0], want[1], d.Fields)
		}
	}
	if d.Fields["d"]["content"] {
		t.Errorf("d.content reads a.name, which is not withheld: %v", d.Fields)
	}
	if d.Because["c"]["content"] != "It reads b.content, which is withheld." {
		t.Errorf("because = %q", d.Because["c"]["content"])
	}
}

func TestTopSensitiveFindsWhatTheSchemaAndTheNameWithhold(t *testing.T) {
	str := []byte(`"string"`)
	b := &schema.Block{
		Attributes: []*schema.Attribute{
			{Name: "plain", TypeJSON: str},
			{Name: "marked", TypeJSON: str, Sensitive: true},
			{Name: "api_token", TypeJSON: str},
			{Name: "once", TypeJSON: str, WriteOnly: true},
			{Name: "nested", NestedType: &schema.Object{Attributes: []*schema.Attribute{{Name: "key", TypeJSON: str, Sensitive: true}}}},
		},
		BlockTypes: []*schema.NestedBlock{{TypeName: "rule", Block: &schema.Block{Attributes: []*schema.Attribute{{Name: "pin", TypeJSON: str, Sensitive: true}}}}},
	}
	got := TopSensitive(b)
	for _, name := range []string{"marked", "api_token", "once", "nested", "rule"} {
		if !got[name] {
			t.Errorf("%s is not withheld: %v", name, got)
		}
	}
	if got["plain"] {
		t.Errorf("plain is withheld: %v", got)
	}
}
