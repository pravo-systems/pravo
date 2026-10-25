package serve

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pravo-systems/pravo/internal/state"
)

const policySpecCUE = `package demo

no_destroy: {
	$kind: "policy"
	rule:  "changes: [...]\n"
}
`

func TestPolicyNameDisplay(t *testing.T) {
	for in, want := range map[string]string{"no_destroy": "no_destroy", "a%002Eb": "a.b", "rules%002Fx%0020y": "rules/x y", "%zzzz": "%zzzz"} {
		if got := policyDisplay(in); got != want {
			t.Errorf("%q: %q, want %q", in, got, want)
		}
	}
}

// A declared policy that the state does not hold is new: the document says what the next
// apply writes, and the text the specification gives.
func TestPoliciesDocumentSetsTheSpecificationAgainstTheState(t *testing.T) {
	dir := t.TempDir()
	writeSpec(t, dir, policySpecCUE)
	st, err := state.Open(filepath.Join(dir, "pravo.state"))
	if err != nil {
		t.Fatal(err)
	}
	p := start(t, dir, st, Options{})
	var d Policies
	if err := json.Unmarshal(p.get("/api/policies"), &d); err != nil {
		t.Fatal(err)
	}
	g := p.graph()
	if d.Version != APIVersion || d.Revision != g.Revision || !d.SpecOK || d.Checked {
		t.Errorf("document: %+v", d)
	}
	if len(d.Policies) != 1 || d.Pending != 1 || d.InForce != 0 || d.Command != "pravo apply" {
		t.Fatalf("policies: %+v", d)
	}
	pi := d.Policies[0]
	if pi.Name != "no_destroy" || pi.Status != "new" || !pi.Declared || pi.Head != 0 || len(pi.Versions) != 0 || !strings.Contains(pi.Rule, "changes") {
		t.Errorf("policy: %+v", pi)
	}
	if pi.Source == nil || !strings.HasSuffix(pi.Source.File, ".cue") {
		t.Errorf("source: %+v", pi.Source)
	}
}

// A project with no policy has an empty document, and the page asks for nothing else.
func TestPoliciesDocumentIsEmptyWithoutPolicies(t *testing.T) {
	p := newProject(t)
	var d Policies
	if err := json.Unmarshal(p.get("/api/policies"), &d); err != nil {
		t.Fatal(err)
	}
	if len(d.Policies) != 0 || d.Pending != 0 || d.InForce != 0 || d.Problem != "" {
		t.Errorf("document: %+v", d)
	}
}
