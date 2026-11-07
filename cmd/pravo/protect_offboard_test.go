package main

import (
	"strings"
	"testing"

	"github.com/pravo-systems/pravo/internal/protect"
)

func TestRemoveReaderLineKeepsTheRest(t *testing.T) {
	in := "# team\nada ssh-ed25519 A x25519:a role:admin\ndan ssh-ed25519 B x25519:b role:writer\nroles-genesis abc\n"
	got, found := removeReaderLine(in, "dan")
	if !found || strings.Contains(got, "dan") || !strings.Contains(got, "ada ") || !strings.Contains(got, "roles-genesis abc\n") || !strings.Contains(got, "# team") {
		t.Fatalf("found=%v\n%s", found, got)
	}
	if _, found := removeReaderLine(in, "eve"); found {
		t.Fatal("eve is not in the file")
	}
}

func TestRoleHintForAReader(t *testing.T) {
	h := roleHint(protect.RoleReader, protect.RoleWriter, "change the state", "f.txt")
	if !strings.Contains(h, "pravo plan --store") || !strings.Contains(h, "role:writer") {
		t.Fatal(h)
	}
	if h := roleHint(protect.RoleReader, protect.RoleWriter, "approve a plan", "f.txt"); !strings.Contains(h, "cannot approve") {
		t.Fatal(h)
	}
	if roleHint(protect.RoleWriter, protect.RoleAdmin, "x", "f") != "" {
		t.Fatal("no hint for an admin-only action")
	}
}
