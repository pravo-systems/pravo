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
