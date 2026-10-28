package main

import (
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// A policy that asks for an approval makes a plan wait for a key other than the one that
// stored it, and the approval counts only from a role that suffices.
func TestAPolicyAsksForApprovalsBeforeAPlanIsApplied(t *testing.T) {
	rule := "{summary: add: 0} | {summary: add: >0, approvals: {min: 1, role: \"admin\"}}"
	r := newPolicyRoot(t, map[string]string{"ok_first": rule})
	if text, code := r.apply(t); code != 0 {
		t.Fatalf("storing the policy: exit %d\n%s", code, text)
	}
	// dan joins as an admin; ada stays a writer.
	_, dan := person(t, r.ids, "dan")
	ada := r.ada.AsReader().String()
	access := filepath.Join(r.dir, ".pravo", "access")
	writeReaders(t, access, ada, dan)
	if text, code := runPravo(t, r.dir, "protect", "rewrap", "--yes"); code != 0 {
		t.Fatalf("rewrap: exit %d\n%s", code, text)
	}
	writeReaders(t, access, ada+" role:writer", dan+" role:admin")

	r.declare(t, map[string]string{"ok_first": rule}, "a")
	// A plan that is not stored has nowhere to keep approvals.
	text, code := runPravo(t, r.dir, "--identity", "ada", "apply", "--auto-approve")
	if code != 5 || !strings.Contains(text, "needs 1 approval by an admin") || !strings.Contains(text, "plan --store") {
		t.Fatalf("a plain apply: exit %d\n%s", code, text)
	}
	text, code = runPravo(t, r.dir, "--identity", "ada", "plan", "--store")
	id := regexp.MustCompile(`plan-[0-9a-f]+`).FindString(text)
	if code != 0 || id == "" {
		t.Fatalf("plan --store: exit %d\n%s", code, text)
	}
	text, code = runPravo(t, r.dir, "--identity", "ada", "apply", id, "--auto-approve")
	if code != 5 || !strings.Contains(text, "has 0") {
		t.Fatalf("an apply without approvals: exit %d\n%s", code, text)
	}
	// The key that stored the plan does not count, and neither would a writer.
	if text, code := runPravo(t, r.dir, "--identity", "ada", "plan", "approve", id); code != 0 {
		t.Fatalf("ada approves: exit %d\n%s", code, text)
	}
	text, code = runPravo(t, r.dir, "--identity", "ada", "apply", id, "--auto-approve")
	if code != 5 || !strings.Contains(text, "has 0") {
		t.Fatalf("an apply approved by its writer: exit %d\n%s", code, text)
	}
	if text, code := runPravo(t, r.dir, "--identity", "dan", "plan", "approve", id); code != 0 {
		t.Fatalf("dan approves: exit %d\n%s", code, text)
	}
	text, code = runPravo(t, r.dir, "--identity", "dan", "plan", "show", id)
	if code != 0 || !strings.Contains(text, "approved:") || !strings.Contains(text, "dan (admin)") {
		t.Fatalf("plan show: exit %d\n%s", code, text)
	}
	if text, code := runPravo(t, r.dir, "--identity", "ada", "apply", id, "--auto-approve"); code != 0 {
		t.Fatalf("an approved apply: exit %d\n%s", code, text)
	}
}
