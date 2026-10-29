package engine

import (
	"fmt"
	"strings"
	"testing"

	"github.com/pravo-systems/pravo/internal/config"
)

func packDoc(now string, changes ...string) Doc {
	d := fmt.Sprintf(`{"format":1,"created":"2026-01-01T00:00:00Z","now":%q,"summary":{"add":1,"change":0,"destroy":0,"read":0},
"stats":{"total":1,"reached":1,"skipped":0,"refreshed":0,"fresh":0,"not_read":0,"drifted":0,"opened":0,"called":0,"imported":0,"moved":0},
"changes":[%s],"outputs":{},"providers":{}}`, now, strings.Join(changes, ","))
	return Doc{JSON: []byte(d), Addrs: []string{"a"}}
}

func packChange(addr, typ, action, after string) string {
	if after != "" {
		after = `,"after":` + after
	}
	return fmt.Sprintf(`{"addr":%q,"mode":"managed","type":%q,"name":%q,"action":%q%s}`, addr, typ, addr, action, after)
}

// A rule of the package of starter rules is standalone text that a policy can store, and
// it refuses what it says it does.
func TestThePackRules(t *testing.T) {
	tags := func(kv string) string { return `{"id":"i","tags":` + kv + `}` }
	cases := []struct {
		rule, params string
		doc          Doc
		want         string // "" holds, else a part of the refusal
	}{
		{"#RequiredTags", `#keys: ["owner", "env"]`, packDoc("2026-10-29T12:00:00Z", packChange("a", "t", "create", tags(`{"owner":"x","env":"y"}`))), ""},
		{"#RequiredTags", `#keys: ["owner", "env"]`, packDoc("2026-10-29T12:00:00Z", packChange("a", "t", "create", tags(`{"owner":"x"}`))), "a: the tag env is missing"},
		{"#RequiredTags", ``, packDoc("2026-10-29T12:00:00Z", packChange("a", "t", "update", tags(`null`))), "a: the tag owner is missing"},
		{"#RequiredTags", ``, packDoc("2026-10-29T12:00:00Z", packChange("a", "t", "create", tags(`{"owner":""}`))), "a: the tag owner is empty"},
		{"#RequiredTags", ``, packDoc("2026-10-29T12:00:00Z", packChange("a", "t", "create", tags(`{"$unknown":true}`))), "the tag owner is missing"},
		{"#RequiredTags", ``, packDoc("2026-10-29T12:00:00Z", packChange("a", "t", "create", `{"id":"i"}`)), ""}, // takes no tags
		{"#RequiredTags", ``, packDoc("2026-10-29T12:00:00Z", packChange("a", "t", "delete", "")), ""},
		{"#KeepTypes", `#types: ["db"]`, packDoc("2026-10-29T12:00:00Z", packChange("a", "db", "delete", "")), "a: a db may not be deleted or replaced"},
		{"#KeepTypes", `#types: ["db"]`, packDoc("2026-10-29T12:00:00Z", packChange("a", "db", "replace", `{}`)), "a: a db may not be deleted or replaced"},
		{"#KeepTypes", `#types: ["db"]`, packDoc("2026-10-29T12:00:00Z", packChange("a", "db", "update", `{}`), packChange("b", "vm", "delete", "")), ""},
		// 2026-10-29 is a Thursday, 2026-10-31 a Saturday.
		{"#ChangeWindow", `#from: 8, #until: 16`, packDoc("2026-10-29T08:00:00Z", packChange("a", "t", "create", `{}`)), ""},
		{"#ChangeWindow", `#from: 8, #until: 16`, packDoc("2026-10-29T15:59:59Z", packChange("a", "t", "create", `{}`)), ""},
		{"#ChangeWindow", `#from: 8, #until: 16`, packDoc("2026-10-29T16:00:00Z", packChange("a", "t", "create", `{}`)), "a: create outside the change window, Thursday 16:00 UTC"},
		{"#ChangeWindow", `#from: 8, #until: 16`, packDoc("2026-10-31T10:00:00Z", packChange("a", "t", "delete", "")), "a: delete outside the change window, Saturday 10:00 UTC"},
		{"#ChangeWindow", `#days: ["Saturday"]`, packDoc("2026-10-31T10:00:00Z", packChange("a", "t", "update", `{}`)), ""},
		{"#ChangeWindow", ``, packDoc("2026-10-29T07:59:59Z", packChange("a", "t", "read", "")), ""}, // a read is not a change
		{"#Freeze", `#from: "2026-12-24T00:00:00Z", #until: "2027-01-04T00:00:00Z"`, packDoc("2026-12-23T23:59:59Z", packChange("a", "t", "create", `{}`)), ""},
		{"#Freeze", `#from: "2026-12-24T00:00:00Z", #until: "2027-01-04T00:00:00Z"`, packDoc("2026-12-24T00:00:00Z", packChange("a", "t", "create", `{}`)), "a: create during the freeze from 2026-12-24T00:00:00Z to 2027-01-04T00:00:00Z"},
		{"#Freeze", `#from: "2026-12-24T00:00:00Z", #until: "2027-01-04T00:00:00Z"`, packDoc("2027-01-04T00:00:00Z", packChange("a", "t", "create", `{}`)), ""},
	}
	tr := NewTrier()
	for i, c := range cases {
		text, err := config.PackRuleText(c.rule, c.params)
		if err != nil {
			t.Fatalf("%s: %v", c.rule, err)
		}
		if strings.Contains(text, "policies.") || strings.Contains(text, "pravo.") {
			t.Errorf("%s is not standalone:\n%s", c.rule, text)
		}
		if p := tr.Check(c.rule, text); p != nil {
			t.Fatalf("%s is not a rule: %+v\n%s", c.rule, p, text)
		}
		key := fmt.Sprint("doc", i)
		tr.Add(key, c.doc)
		res := tr.Try(c.rule, text, []string{key})[0]
		switch {
		case c.want == "" && res.Outcome != RuleHolds:
			t.Errorf("case %d %s %s: want it to hold, got %+v", i, c.rule, c.params, res)
		case c.want != "" && res.Outcome != RuleRefused:
			t.Errorf("case %d %s %s: want a refusal %q, got %+v", i, c.rule, c.params, c.want, res)
		case c.want != "":
			var msgs []string
			for _, is := range res.Issues {
				msgs = append(msgs, is.Message)
			}
			if !strings.Contains(strings.Join(msgs, "\n"), c.want) {
				t.Errorf("case %d: refusal %q, want %q", i, msgs, c.want)
			}
		}
	}
}
