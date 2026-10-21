package config

import (
	"fmt"
	"testing"
	"time"

	"cuelang.org/go/cue"
)

func declared(names ...string) *Root {
	root := &Root{}
	for _, n := range names {
		root.Resources = append(root.Resources, &Resource{Mode: "managed", Type: "t", Name: n, Path: cue.ParsePath(n)})
	}
	return root
}

// A lookup by address finds the resource that has it, the first of two that
// share one, and nothing for an address that no resource has. The index follows
// resources added after the first lookup.
func TestRootResourceLookup(t *testing.T) {
	root := declared("a", "b.c", `"d-e"`)
	for _, addr := range []string{"a", "b.c", `"d-e"`} {
		if got := root.Resource(addr); got == nil || got.Address() != addr {
			t.Errorf("Resource(%q) = %v", addr, got)
		}
	}
	if got := root.Resource("nope"); got != nil {
		t.Errorf("Resource(nope) = %v, want nil", got)
	}
	dup := &Resource{Mode: "data", Type: "t", Name: "a", Path: cue.ParsePath("a")}
	root.Resources = append(root.Resources, dup, &Resource{Mode: "managed", Type: "t", Name: "late", Path: cue.ParsePath("late")})
	if got := root.Resource("a"); got == dup || got == nil || got.Mode != "managed" {
		t.Errorf("Resource(a) = %v, want the first of the two", got)
	}
	if got := root.Resource("late"); got == nil {
		t.Error("a resource added after a lookup is not found")
	}
}

// The address of a resource found by discovery is kept; one built by hand has
// the address of its path.
func TestResourceAddressIsThePath(t *testing.T) {
	r := &Resource{Path: cue.ParsePath(`files."f-1".x`)}
	if got := r.Address(); got != `files."f-1".x` {
		t.Errorf("Address() = %q", got)
	}
	r.addr = "kept"
	if got := r.Address(); got != "kept" {
		t.Errorf("Address() = %q, want the kept address", got)
	}
}

// Looking up every resource of a root costs the size of the root, not its
// square: destroy asks for the resource of every record in the state.
func TestRootResourceLookupIsIndexed(t *testing.T) {
	const n = 5000
	names := make([]string, n)
	for i := range names {
		names[i] = fmt.Sprintf("files.f%d", i)
	}
	root := declared(names...)
	start := time.Now()
	for _, name := range names {
		if root.Resource(name) == nil {
			t.Fatalf("%s is not found", name)
		}
	}
	for i := 0; i < n; i++ {
		if root.Resource(fmt.Sprintf("orphan.o%d", i)) != nil {
			t.Fatal("an orphan is found")
		}
	}
	// Scanning the resources for each of these 10000 lookups takes tens of seconds.
	if took := time.Since(start); took > 2*time.Second {
		t.Errorf("%d lookups took %s", 2*n, took)
	}
}
