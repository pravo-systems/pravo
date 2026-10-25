package engine

import (
	"strings"
	"testing"
)

const tryDoc = `{"format":1,"created":"2026-01-01T00:00:00Z","summary":{"add":1,"change":0,"destroy":1,"read":0},
"stats":{"total":2,"reached":2,"skipped":0,"refreshed":0,"fresh":0,"not_read":0,"drifted":0,"opened":0,"called":0,"imported":0,"moved":0},
"changes":[{"addr":"a","mode":"managed","type":"t","name":"a","action":"create","after":{"n":1}},
{"addr":"b","mode":"managed","type":"t","name":"b","action":"delete","before":{"n":2}}],
"outputs":{},"providers":{}}`

func TestTrierJudgesDocumentsAsAnApplyDoes(t *testing.T) {
	tr := NewTrier()
	tr.Add("p", Doc{JSON: []byte(tryDoc), Addrs: []string{"a", "b"}})
	res := tr.Try("r", "summary: destroy: 0\n", []string{"p", "missing"})
	if res[0].Outcome != RuleRefused || len(res[0].Issues) == 0 || res[0].Issues[0].Path != "summary.destroy" || res[0].Issues[0].Got != "1" {
		t.Errorf("refused: %+v", res[0])
	}
	if res[1].Outcome != RuleInvalid {
		t.Errorf("unknown document: %+v", res[1])
	}
	res = tr.Try("r", "changes: [...{action: \"create\"}]\n", []string{"p"})
	if is := res[0].Issues[0]; res[0].Outcome != RuleRefused || is.Change != "b" || !strings.HasPrefix(is.Path, "changes.1.") {
		t.Errorf("a change is named: %+v", res[0])
	}
	if res = tr.Try("r", "summary: destroy: <=1\n", []string{"p"}); res[0].Outcome != RuleHolds {
		t.Errorf("holds: %+v", res[0])
	}
	if res = tr.Try("r", "summary: destroy: <=\n", []string{"p"}); res[0].Outcome != RuleInvalid {
		t.Errorf("broken: %+v", res[0])
	}
	// A guard on a value the document lacks is strict, as at apply.
	res = tr.Try("r", "changes: [...{action: string, if action == \"create\" {after: n: 2}}]\n", []string{"p"})
	if res[0].Outcome != RuleRefused {
		t.Errorf("strict: %+v", res[0])
	}
}

func TestCheckRuleStages(t *testing.T) {
	tr := NewTrier()
	for text, stage := range map[string]string{
		"summary: destroy: <=":                      "parse",
		"import \"example.com/x\"\nsummary: add: 1": "imports",
		"summary: destroy: foo":                     "compile",
		"summary: add: 5":                           "empty",
		"chnges: []":                                "shape",
	} {
		p := tr.Check("r", text)
		if p == nil || p.Stage != stage {
			t.Errorf("%q: %+v, want stage %s", text, p, stage)
		}
	}
	if p := tr.Check("r", "summary: destroy: <=5\n"); p != nil {
		t.Errorf("a good rule: %+v", p)
	}
}
