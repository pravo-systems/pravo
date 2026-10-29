package engine

import (
	"strings"
	"testing"
)

const tryDoc = `{"format":1,"created":"2026-01-01T00:00:00Z","now":"2026-01-01T00:00:00Z","summary":{"add":1,"change":0,"destroy":1,"read":0},
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

func TestTryFileJudgesWholePolicies(t *testing.T) {
	tr := NewTrier()
	tr.Add("p", Doc{JSON: []byte(tryDoc), Addrs: []string{"a", "b"}})
	file := func(body string) string { return "import \"pravo.dev/pravo\"\n\n" + body }

	r := tr.TryFile(file("few: pravo.#Policy & {\n\trule: summary: destroy: 0\n}\n\nmany: pravo.#Policy & {\n\trule: summary: add: <=5\n}\n"), []string{"p"})
	if r.Problem != nil || len(r.Policies) != 2 || r.Policies[0].Name != "few" || r.Policies[0].Line != 3 {
		t.Fatalf("two policies: %+v", r)
	}
	if res := r.Plans[0]; res.Outcome != RuleRefused || len(res.Issues) != 1 || res.Issues[0].Policy != "few" || res.Issues[0].Line != 4 || res.Issues[0].Path != "summary.destroy" {
		t.Errorf("the refusal names the policy and the line of the file: %+v", res)
	}

	r = tr.TryFile(file("cost: max: pravo.#Policy & {\n\trule: summary: destroy: 0\n}\n"), []string{"p"})
	if r.Problem != nil || len(r.Policies) != 1 || r.Policies[0].Name != "cost.max" || r.Plans[0].Outcome != RuleRefused || r.Plans[0].Issues[0].Path != "summary.destroy" {
		t.Errorf("a policy at a path: %+v", r)
	}

	r = tr.TryFile(file("ok: pravo.#Policy & {\n\trule: summary: destroy: <=1\n}\n"), []string{"p"})
	if r.Problem != nil || r.Plans[0].Outcome != RuleHolds {
		t.Errorf("holds: %+v", r)
	}

	// The file is type-checked against the pravo package, at the line that holds the mistake.
	for body, want := range map[string]struct {
		stage string
		line  int
	}{
		"p: pravo.#Policy & {\n\trule: sumary: destroy: 0\n}\n":      {"compile", 4},
		"p: pravo.#Policy & {\n\trule: summary: destroy: \"x\"\n}\n": {"compile", 4},
		"p: pravo.#Policy & {\n\trul: {}\n}\n":                       {"compile", 0},
		"x: 1\n":                                                     {"policies", 0},
	} {
		r = tr.TryFile(file(body), []string{"p"})
		if r.Problem == nil || r.Problem.Stage != want.stage {
			t.Errorf("%q: %+v", body, r.Problem)
			continue
		}
		if want.line > 0 {
			ok := false
			for _, is := range r.Problem.Issues {
				ok = ok || is.Line == want.line
			}
			if !ok {
				t.Errorf("%q: no issue at line %d: %+v", body, want.line, r.Problem.Issues)
			}
		}
	}
	if r = tr.TryFile("import \"example.com/x\"\np: pravo.#Policy & {rule: {}}\n", []string{"p"}); r.Problem == nil || r.Problem.Stage != "imports" {
		t.Errorf("another import: %+v", r.Problem)
	}
	if r = tr.TryFile("p: pravo.#Policy & {rule: summary: destroy: <=\n", []string{"p"}); r.Problem == nil || r.Problem.Stage != "parse" || r.Plans[0].Outcome != RuleInvalid {
		t.Errorf("parse: %+v", r)
	}
}
