package state

import (
	"context"
	"crypto/md5"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
)

// taggedBackend is a local backend that lists versions, the way S3 does, and counts what is read.
type taggedBackend struct {
	Backend
	gets atomic.Int64
}

func (b *taggedBackend) Get(ctx context.Context, key string) ([]byte, error) {
	b.gets.Add(1)
	return b.Backend.Get(ctx, key)
}

func (b *taggedBackend) ListVersions(ctx context.Context, prefix string) ([]ObjectVersion, error) {
	keys, err := b.List(ctx, prefix)
	if err != nil {
		return nil, err
	}
	out := make([]ObjectVersion, len(keys))
	for i, k := range keys {
		data, err := b.Backend.Get(ctx, k)
		if err != nil {
			return nil, err
		}
		out[i] = ObjectVersion{Key: k, Tag: fmt.Sprintf("%x", md5.Sum(data))}
	}
	return out, nil
}

func TestASnapshotMakesAnOpenReadFewObjects(t *testing.T) {
	dir := t.TempDir()
	seed, err := OpenWith(dir, Options{NewFormat: FormatImmutable})
	if err != nil {
		t.Fatal(err)
	}
	const n = 120
	for i := range n {
		mustSet(t, seed, node(fmt.Sprintf("r%03d", i)))
	}
	open := func() (*Store, *taggedBackend) {
		lb, err := NewLocalBackend(dir)
		if err != nil {
			t.Fatal(err)
		}
		b := &taggedBackend{Backend: lb}
		s, err := OpenBackend(context.Background(), b)
		if err != nil {
			t.Fatal(err)
		}
		return s, b
	}
	s1, _ := open() // reads every node and keeps a snapshot
	if s1.Len() != n {
		t.Fatalf("first open holds %d nodes, want %d", s1.Len(), n)
	}
	if _, err := seed.b.Get(context.Background(), snapshotKey); err != nil {
		t.Fatalf("the first open kept no snapshot: %v", err)
	}
	// The listing reads every object here, as the local backend has no tags of its own.
	s2, b2 := open()
	if s2.Len() != n {
		t.Fatalf("second open holds %d nodes, want %d", s2.Len(), n)
	}
	if g := b2.gets.Load(); g > 8 {
		t.Fatalf("open with a snapshot read %d objects, want a few (meta, snapshot, outputs)", g)
	}
	// A node that changed since the snapshot is read afresh.
	changed := node("r003")
	changed.Attributes = []byte(`{"changed":true}`)
	mustSet(t, s2, changed)
	s3, _ := open()
	if got := string(s3.Get(changed.Address()).Attributes); !strings.Contains(got, "changed") {
		t.Fatalf("a changed node came from the snapshot: %s", got)
	}
	// A snapshot that is damaged is read around.
	if err := seed.b.Put(context.Background(), snapshotKey, []byte("{not json")); err != nil {
		t.Fatal(err)
	}
	if s4, _ := open(); s4.Len() != n {
		t.Fatalf("an open with a damaged snapshot holds %d nodes, want %d", s4.Len(), n)
	}
}

func TestACommandThatChangesManyNodesLeavesASnapshotThatIsTrue(t *testing.T) {
	dir := t.TempDir()
	seed, err := OpenWith(dir, Options{NewFormat: FormatImmutable})
	if err != nil {
		t.Fatal(err)
	}
	const n = 120
	for i := range n {
		mustSet(t, seed, node(fmt.Sprintf("r%03d", i)))
	}
	open := func() (*Store, *taggedBackend) {
		lb, err := NewLocalBackend(dir)
		if err != nil {
			t.Fatal(err)
		}
		b := &taggedBackend{Backend: lb}
		s, err := OpenBackend(context.Background(), b)
		if err != nil {
			t.Fatal(err)
		}
		return s, b
	}
	s1, _ := open() // no snapshot yet: written at the open
	// One node changes: under the threshold, so the end of the command leaves the snapshot alone.
	one := node("r001")
	one.Attributes = []byte(`{"c":1}`)
	mustSet(t, s1, one)
	before, _ := seed.b.Get(context.Background(), snapshotKey)
	s1.Close()
	if after, _ := seed.b.Get(context.Background(), snapshotKey); string(after) != string(before) {
		t.Fatal("one changed node rewrote the snapshot")
	}
	// Ten change: over one in twenty, so the end of the command makes it true again.
	s2, _ := open()
	for i := range 10 {
		r := node(fmt.Sprintf("r%03d", 10+i))
		r.Attributes = []byte(`{"c":2}`)
		mustSet(t, s2, r)
	}
	s2.Close()
	s3, b3 := open()
	if s3.Len() != n {
		t.Fatalf("holds %d nodes, want %d", s3.Len(), n)
	}
	if s3.snap.missed != 0 {
		t.Fatalf("the open after a big change still read %d nodes afresh", s3.snap.missed)
	}
	if g := b3.gets.Load(); g > 8 {
		t.Fatalf("the open after a big change read %d objects", g)
	}
}

// A reader who plans opens the state through an overlay: it takes the snapshot that a writer
// kept, and keeps none of its own.
func TestAnOverlayReadsTheSnapshotAndKeepsNone(t *testing.T) {
	dir := t.TempDir()
	seed, err := OpenWith(dir, Options{NewFormat: FormatImmutable})
	if err != nil {
		t.Fatal(err)
	}
	const n = 120
	for i := range n {
		mustSet(t, seed, node(fmt.Sprintf("r%03d", i)))
	}
	open := func(overlay bool) (*Store, *taggedBackend) {
		lb, err := NewLocalBackend(dir)
		if err != nil {
			t.Fatal(err)
		}
		b := &taggedBackend{Backend: lb}
		var ob Backend = b
		if overlay {
			ob = OverlayBackend(b)
		}
		s, err := OpenBackend(context.Background(), ob)
		if err != nil {
			t.Fatal(err)
		}
		return s, b
	}
	// Without a snapshot the overlay writes none to the backend.
	if s, _ := open(true); s.Len() != n {
		t.Fatalf("overlay open holds %d nodes, want %d", s.Len(), n)
	}
	if _, err := seed.b.Get(context.Background(), snapshotKey); err == nil {
		t.Fatal("a reader's overlay kept a snapshot in the state")
	}
	open(false) // a writer keeps one
	s, b := open(true)
	if s.Len() != n {
		t.Fatalf("overlay open holds %d nodes, want %d", s.Len(), n)
	}
	if g := b.gets.Load(); g > 8 {
		t.Fatalf("an overlay open with a snapshot read %d objects", g)
	}
}
