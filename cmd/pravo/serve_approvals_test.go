package main

import (
	"encoding/json"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/pravo-systems/pravo/internal/serve"
)

// The viewer says what approvals a stored plan still needs, counted as the apply counts
// them, and who approved it: the key that stored the plan does not count, a writer's does.
func TestServeSaysWhatApprovalsAStoredPlanNeeds(t *testing.T) {
	rule := "{summary: destroy: 0} | {summary: destroy: >0, approvals: {min: 1}}"
	rules := map[string]string{"second_opinion": rule}
	r := newPolicyRoot(t, rules, "a")
	if text, code := r.apply(t); code != 0 {
		t.Fatalf("storing the policy: exit %d\n%s", code, text)
	}
	_, dan := person(t, r.ids, "dan")
	_, eve := person(t, r.ids, "eve")
	ada := r.ada.AsReader().String()
	access := filepath.Join(r.dir, ".pravo", "access")
	writeReaders(t, access, ada, dan, eve)
	if text, code := runPravo(t, r.dir, "protect", "rewrap", "--yes"); code != 0 {
		t.Fatalf("rewrap: exit %d\n%s", code, text)
	}
	writeReaders(t, access, ada+" role:admin", dan+" role:writer", eve+" role:writer")
	r.declare(t, rules) // a is destroyed by the next plan
	text, code := runPravo(t, r.dir, "--identity", "dan", "plan", "--store")
	id := regexp.MustCompile(`plan-[0-9a-f]+`).FindString(text)
	if code != 0 || id == "" {
		t.Fatalf("plan --store: exit %d\n%s", code, text)
	}
	approvals := func(t *testing.T) *serve.PlanApprovals {
		t.Helper()
		info, stop := runServe(t, r.dir, "--identity", "ada")
		defer func() {
			if err := stop(); err != nil {
				t.Error(err)
			}
		}()
		var o serve.PlanOverlay
		if err := json.Unmarshal(fetch(t, info, "/api/plan?id="+id), &o); err != nil {
			t.Fatal(err)
		}
		return o.Plan.Approvals
	}
	ap := approvals(t)
	if ap == nil || len(ap.Items) != 0 || len(ap.Needs) != 1 || ap.Note != "" {
		t.Fatalf("a plan nobody approved: %+v", ap)
	}
	if n := ap.Needs[0]; n.Min != 1 || n.Role != "writer" || n.Self || strings.Join(n.By, ",") != "second_opinion@1" || n.Have != 0 || n.Met {
		t.Errorf("need %+v", n)
	}
	// The key that stored the plan does not count.
	if text, code := runPravo(t, r.dir, "--identity", "dan", "plan", "approve", id); code != 0 {
		t.Fatalf("dan approves: exit %d\n%s", code, text)
	}
	ap = approvals(t)
	if ap == nil || len(ap.Items) != 1 || ap.Items[0].Signer != "dan" || ap.Items[0].Role != "writer" || len(ap.Needs) != 1 || ap.Needs[0].Have != 0 || ap.Needs[0].Met {
		t.Fatalf("approved by its author: %+v", ap)
	}
	if text, code := runPravo(t, r.dir, "--identity", "eve", "plan", "approve", id); code != 0 {
		t.Fatalf("eve approves: exit %d\n%s", code, text)
	}
	ap = approvals(t)
	if ap == nil || len(ap.Items) != 2 || ap.Items[1].Signer != "eve" || len(ap.Needs) != 1 || ap.Needs[0].Have != 1 || !ap.Needs[0].Met {
		t.Fatalf("approved by eve: %+v", ap)
	}
}
