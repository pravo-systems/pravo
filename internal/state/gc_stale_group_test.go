package state

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"
)

// A writer that opened the store before a rewrap goes on sealing in the group it
// opened with for up to sealRecheck. If a collection dropped that group inside the
// window, the writer's record would be sealed in a group that meta.json no longer
// lists, and the state would not open for anyone. GC drops a group only once no
// such writer can be left.
func TestGCKeepsTheGroupThatAStaleWriterMaySealIn(t *testing.T) {
	skipWhenSealed(t)
	ctx := context.Background()
	b, _ := NewLocalBackend(t.TempDir())
	ada, fay := testIdentity(t, "ada"), testIdentity(t, "fay")
	s := sealedStore(t, b, ada, fay)
	stale := openIdentity(t, b, ada) // opened before the rewrap, keeps sealing in g1
	if _, err := s.Rewrap(ctx, RewrapOptions{Readers: readersOf(ada), All: true}); err != nil {
		t.Fatal(err)
	}
	none := func(context.Context) (map[string]int, error) { return nil, nil }

	// Right after the rewrap nothing is dropped, and says why.
	res, err := s.GC(ctx, GCOptions{Groups: true, Plans: none})
	if err != nil || len(res.Groups) != 0 || !strings.Contains(res.GroupsRefused, "may still seal in the old group") {
		t.Fatalf("GC right after the rewrap = %+v, %v", res, err)
	}
	if got := fmt.Sprint(groupsOf(t, b)); got != "[g1 g2]" {
		t.Fatalf("groups = %s", got)
	}
	// The writer from before the rewrap writes: the state still opens.
	if err := stale.Set(node("late"), ""); err != nil {
		t.Fatalf("the writer from before the rewrap: %v", err)
	}
	if _, err := OpenBackendWith(ctx, b, Options{Identity: ada}); err != nil {
		t.Fatalf("the state does not open after the write of a writer from before the rewrap: %v", err)
	}

	// Once the window is over, the group goes if nothing is sealed in it, and not
	// before the record of the late writer is sealed again.
	later := ahead(sweepGrace() + time.Second)
	res, err = s.GC(ctx, GCOptions{Groups: true, Plans: none, Now: later})
	if err != nil || len(res.Groups) != 1 || res.Groups[0].Dropped || res.Groups[0].Records != 1 {
		t.Fatalf("GC after the window, with a record in g1 = %+v, %v", res, err)
	}
	if _, err := s.Rewrap(ctx, RewrapOptions{Readers: readersOf(ada), All: true}); err != nil {
		t.Fatal(err)
	}
	res, err = s.GC(ctx, GCOptions{Groups: true, Plans: none, Now: ahead(2*sweepGrace() + time.Minute)})
	if err != nil || len(res.Groups) != 1 || !res.Groups[0].Dropped || fmt.Sprint(groupsOf(t, b)) != "[g2]" {
		t.Fatalf("GC after a second rewrap --all = %+v, %v", res, err)
	}
	if _, err := OpenBackendWith(ctx, b, Options{Identity: ada}); err != nil {
		t.Fatalf("the state does not open after the group went: %v", err)
	}
}
