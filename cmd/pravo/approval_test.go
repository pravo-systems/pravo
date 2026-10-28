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
	// The approvals are listed in the order they were made, ada's first, whatever the ids of the keys.
	text, code = runPravo(t, r.dir, "--identity", "dan", "plan", "show", id)
	if code != 0 || !strings.Contains(text, "approved: ada (writer), dan (admin)") {
		t.Fatalf("plan show: exit %d\n%s", code, text)
	}
	if text, code := runPravo(t, r.dir, "--identity", "ada", "apply", id, "--auto-approve"); code != 0 {
		t.Fatalf("an approved apply: exit %d\n%s", code, text)
	}
	// An admin needs no approval for a plan that asks for one.
	r.declare(t, map[string]string{"ok_first": rule}, "b")
	if text, code := runPravo(t, r.dir, "--identity", "dan", "apply", "--auto-approve"); code != 0 {
		t.Fatalf("an admin's plain apply: exit %d\n%s", code, text)
	}
}

// A rule that asks for approvals without naming a role asks for a writer, the default of the
// schema, which the judged document does not carry.
func TestApprovalsAskedWithoutARoleCountAWriter(t *testing.T) {
	rule := "{summary: add: 0} | {summary: add: >0, approvals: {min: 1}}"
	r := newPolicyRoot(t, map[string]string{"ok_first": rule})
	if text, code := r.apply(t); code != 0 {
		t.Fatalf("storing the policy: exit %d\n%s", code, text)
	}
	_, dan := person(t, r.ids, "dan")
	ada := r.ada.AsReader().String()
	access := filepath.Join(r.dir, ".pravo", "access")
	writeReaders(t, access, ada, dan)
	if text, code := runPravo(t, r.dir, "protect", "rewrap", "--yes"); code != 0 {
		t.Fatalf("rewrap: exit %d\n%s", code, text)
	}
	writeReaders(t, access, ada+" role:writer", dan+" role:writer")

	r.declare(t, map[string]string{"ok_first": rule}, "a")
	text, code := runPravo(t, r.dir, "--identity", "ada", "plan", "--store")
	id := regexp.MustCompile(`plan-[0-9a-f]+`).FindString(text)
	if code != 0 || id == "" {
		t.Fatalf("plan --store: exit %d\n%s", code, text)
	}
	text, code = runPravo(t, r.dir, "--identity", "ada", "apply", id, "--auto-approve")
	if code != 5 || !strings.Contains(text, "needs 1 approval by a writer or an admin") {
		t.Fatalf("an apply without approvals: exit %d\n%s", code, text)
	}
	if text, code := runPravo(t, r.dir, "--identity", "dan", "plan", "approve", id); code != 0 {
		t.Fatalf("dan approves: exit %d\n%s", code, text)
	}
	if text, code := runPravo(t, r.dir, "--identity", "ada", "apply", id, "--auto-approve"); code != 0 {
		t.Fatalf("an apply approved by a writer: exit %d\n%s", code, text)
	}
}

// With one member, or a readers file that gives no roles, nobody waits for an approval.
func TestAnApprovalNeverBlocksALoneMember(t *testing.T) {
	rule := "{summary: add: 0} | {summary: add: >0, approvals: {min: 2, role: \"admin\"}}"
	r := newPolicyRoot(t, map[string]string{"ok_first": rule})
	if text, code := r.apply(t); code != 0 {
		t.Fatalf("storing the policy: exit %d\n%s", code, text)
	}
	r.declare(t, map[string]string{"ok_first": rule}, "a")
	if text, code := r.apply(t); code != 0 {
		t.Fatalf("no roles, one member: exit %d\n%s", code, text)
	}
	// One member with the admin role.
	access := filepath.Join(r.dir, ".pravo", "access")
	writeReaders(t, access, r.ada.AsReader().String()+" role:admin")
	r.declare(t, map[string]string{"ok_first": rule}, "a", "b")
	if text, code := r.apply(t); code != 0 {
		t.Fatalf("one admin: exit %d\n%s", code, text)
	}
}

// A plan that edits or removes a policy that asks for approvals needs an admin's approval itself, so
// a writer cannot end the requirement with a plan that no rule judges.
func TestChangingAPolicyThatAsksForApprovalsNeedsAnAdmin(t *testing.T) {
	rule := "{summary: add: 0} | {summary: add: >0, approvals: {min: 1}}"
	r := newPolicyRoot(t, map[string]string{"ok_first": rule})
	if text, code := r.apply(t); code != 0 {
		t.Fatalf("storing the policy: exit %d\n%s", code, text)
	}
	_, dan := person(t, r.ids, "dan")
	ada := r.ada.AsReader().String()
	access := filepath.Join(r.dir, ".pravo", "access")
	writeReaders(t, access, ada, dan)
	if text, code := runPravo(t, r.dir, "protect", "rewrap", "--yes"); code != 0 {
		t.Fatalf("rewrap: exit %d\n%s", code, text)
	}
	writeReaders(t, access, ada+" role:writer", dan+" role:admin")

	// The writer drops the requirement: refused until an admin approves the stored plan.
	r.declare(t, map[string]string{"ok_first": "summary: add: >=0"})
	text, code := runPravo(t, r.dir, "--identity", "ada", "apply", "--auto-approve")
	if code != 5 || !strings.Contains(text, "a change to ok_first@1") {
		t.Fatalf("a plain apply: exit %d\n%s", code, text)
	}
	text, _ = runPravo(t, r.dir, "--identity", "ada", "plan", "--store")
	id := regexp.MustCompile(`plan-[0-9a-f]+`).FindString(text)
	if id == "" {
		t.Fatalf("plan --store:\n%s", text)
	}
	if text, code := runPravo(t, r.dir, "--identity", "ada", "apply", id, "--auto-approve"); code != 5 {
		t.Fatalf("an apply without approval: exit %d\n%s", code, text)
	}
	if text, code := runPravo(t, r.dir, "--identity", "dan", "plan", "approve", id); code != 0 {
		t.Fatalf("dan approves: exit %d\n%s", code, text)
	}
	if text, code := runPravo(t, r.dir, "--identity", "ada", "apply", id, "--auto-approve"); code != 0 {
		t.Fatalf("an approved apply: exit %d\n%s", code, text)
	}
}
