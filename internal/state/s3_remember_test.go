package state

import (
	"context"
	"fmt"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/pravo-systems/pravo/internal/fakes3"
)

// otherS3 is another client of the bucket that f serves: a backend that remembers nothing of
// what the first one wrote.
func otherS3(t *testing.T, f *fakes3.Server) *S3Backend {
	t.Helper()
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	b, err := NewS3Backend(context.Background(), S3Config{
		Bucket: "state", Prefix: "team/app", Region: "us-east-1", Endpoint: srv.URL, PathStyle: true,
		AccessKey: "test", SecretKey: "test",
	})
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// A lock or a pointer that this process has just written or read is replaced and deleted
// with one request: the condition names the entity tag that the store gave, and no read is
// made first. Any other object is read, as before.
func TestBucketWritesWhatItRemembersWithoutReading(t *testing.T) {
	f, b := newFakeS3(t)
	ctx := context.Background()
	var log []string
	requests := func(do func()) []string {
		log = nil
		f.Fault = fakes3.Requests(&log)
		do()
		f.Fault = nil
		return slices.Clone(log)
	}
	want := func(what string, got []string, want ...string) {
		t.Helper()
		if !slices.Equal(got, want) {
			t.Fatalf("%s made the requests %q, want %q", what, got, want)
		}
	}
	const lock, object = "locks/a.lock", "/state/team/app/locks/a.lock"
	if ok, err := b.PutIfAbsent(ctx, lock, []byte("one")); !ok || err != nil {
		t.Fatal(ok, err)
	}
	seen := ""
	want("a replace of a lock just taken", requests(func() {
		ok, err := b.ReplaceIf(ctx, lock, func(cur []byte) ([]byte, bool) { seen = string(cur); return []byte("two"), true })
		if !ok || err != nil {
			t.Fatal(ok, err)
		}
	}), "PUT "+object)
	if seen != "one" || string(f.Objects["team/app/locks/a.lock"]) != "two" {
		t.Fatalf("the function saw %q and the object is %q", seen, f.Objects["team/app/locks/a.lock"])
	}
	want("a delete of a lock just replaced", requests(func() {
		ok, err := b.DeleteIf(ctx, lock, func(cur []byte) bool { seen = string(cur); return true })
		if !ok || err != nil {
			t.Fatal(ok, err)
		}
	}), "DELETE "+object)
	if _, ok := f.Objects["team/app/locks/a.lock"]; ok || seen != "two" {
		t.Fatalf("the function saw %q and the object is there: %v", seen, ok)
	}
	// What was deleted is forgotten: the next call reads, and finds nothing.
	want("a replace of a deleted lock", requests(func() {
		if ok, err := b.ReplaceIf(ctx, lock, func([]byte) ([]byte, bool) { return []byte("x"), true }); ok || err == nil {
			t.Fatal(ok, err)
		}
	}), "GET "+object)
	// A version that was read is remembered as one that was written is.
	f.Set("team/app/resources/r.json", []byte("p1"))
	if _, err := b.Get(ctx, "resources/r.json"); err != nil {
		t.Fatal(err)
	}
	want("a replace of a pointer just read", requests(func() {
		if ok, err := b.ReplaceIf(ctx, "resources/r.json", func([]byte) ([]byte, bool) { return []byte("p2"), true }); !ok || err != nil {
			t.Fatal(ok, err)
		}
	}), "PUT /state/team/app/resources/r.json")
	// Objects that are neither locks nor pointers are read first.
	if err := b.Put(ctx, "plans/p.json", []byte("one")); err != nil {
		t.Fatal(err)
	}
	want("a replace of another object", requests(func() {
		if ok, err := b.ReplaceIf(ctx, "plans/p.json", func([]byte) ([]byte, bool) { return []byte("two"), true }); !ok || err != nil {
			t.Fatal(ok, err)
		}
	}), "GET /state/team/app/plans/p.json", "PUT /state/team/app/plans/p.json")
}

// The store decides, and not what the process remembers: when another writer has replaced the
// object, the write that names the remembered tag is refused, the object is read, and the
// function judges what the store holds.
func TestBucketReadsWhenTheRememberedVersionIsNotTheStores(t *testing.T) {
	f, b := newFakeS3(t)
	ctx := context.Background()
	const lock, object = "locks/a.lock", "team/app/locks/a.lock"
	put := func() {
		t.Helper()
		if err := b.Put(ctx, lock, []byte("mine")); err != nil {
			t.Fatal(err)
		}
		f.Set(object, []byte("theirs")) // another command took the lock over
	}
	// A function that accepts only its own lock: the remembered version passes, the store
	// refuses, and the version that is read does not pass.
	put()
	var saw []string
	ok, err := b.ReplaceIf(ctx, lock, func(cur []byte) ([]byte, bool) {
		saw = append(saw, string(cur))
		return []byte("mine again"), string(cur) == "mine"
	})
	if ok || err != nil || !slices.Equal(saw, []string{"mine", "theirs"}) || string(f.Objects[object]) != "theirs" {
		t.Fatalf("ReplaceIf of a lock that was taken over: %v, %v, saw %q, object %q", ok, err, saw, f.Objects[object])
	}
	put()
	saw = nil
	ok, err = b.DeleteIf(ctx, lock, func(cur []byte) bool { saw = append(saw, string(cur)); return string(cur) == "mine" })
	if ok || err != nil || !slices.Equal(saw, []string{"mine", "theirs"}) || string(f.Objects[object]) != "theirs" {
		t.Fatalf("DeleteIf of a lock that was taken over: %v, %v, saw %q, object %q", ok, err, saw, f.Objects[object])
	}
	// A function that refuses what is remembered and accepts what the store holds.
	put()
	ok, err = b.ReplaceIf(ctx, lock, func(cur []byte) ([]byte, bool) { return []byte("taken"), string(cur) == "theirs" })
	if !ok || err != nil || string(f.Objects[object]) != "taken" {
		t.Fatalf("ReplaceIf that accepts only the store's version: %v, %v, object %q", ok, err, f.Objects[object])
	}
	put()
	ok, err = b.DeleteIf(ctx, lock, func(cur []byte) bool { return string(cur) == "theirs" })
	if _, there := f.Objects[object]; !ok || err != nil || there {
		t.Fatalf("DeleteIf that accepts only the store's version: %v, %v, object there: %v", ok, err, there)
	}
	// The object is gone: not found, as a read would have said.
	if err := b.Put(ctx, lock, []byte("mine")); err != nil {
		t.Fatal(err)
	}
	f.Set(object, nil)
	if ok, err := b.DeleteIf(ctx, lock, func([]byte) bool { return true }); ok || err == nil {
		t.Fatalf("DeleteIf of an object that is gone: %v, %v", ok, err)
	}
}

// The nodes that a command wrote are not read back to bring the snapshot up to date: the
// store gave the tag of each pointer when it was written, and the listing shows that tag.
func TestTheSnapshotTakesWrittenNodesWithoutReadingThem(t *testing.T) {
	for _, format := range []int{1, FormatImmutable} {
		t.Run(fmt.Sprintf("format %d", format), func(t *testing.T) {
			f, b := newFakeS3(t)
			ctx := context.Background()
			s, err := openBackend(ctx, b, Options{NewFormat: format})
			if err != nil {
				t.Fatal(err)
			}
			const n = 60
			for i := range n {
				mustSet(t, s, node(fmt.Sprintf("r%03d", i)))
			}
			var log []string
			f.Fault = fakes3.Requests(&log)
			s.settleSnapshot()
			f.Fault = nil
			if want := []string{"GET /state ?list=team/app/resources/", "PUT /state/team/app/snapshot.json"}; !slices.Equal(log, want) {
				t.Fatalf("the snapshot of nodes that were just written took the requests %q, want %q", log, want)
			}
			// It is true: an open takes every node from it.
			log = nil
			f.Fault = fakes3.Requests(&log)
			s2, err := OpenBackend(ctx, b)
			f.Fault = nil
			if err != nil || s2.Len() != n || s2.snap.missed != 0 {
				t.Fatalf("open: %v, %d nodes, %d read afresh", err, s2.Len(), s2.snap.missed)
			}
			for _, r := range log {
				if strings.HasPrefix(r, "GET ") && !strings.Contains(r, "?list") && (strings.Contains(r, "/resources/") || strings.Contains(r, "/rec/")) {
					t.Errorf("an open with a true snapshot read %s", r)
				}
			}
			// A node that another writer changed after the command wrote it is read, and
			// not taken from what the command remembers.
			changed := node("r007")
			changed.Attributes = []byte(`{"by":"another"}`)
			other, err := OpenBackend(ctx, otherS3(t, f))
			if err != nil {
				t.Fatal(err)
			}
			mustSet(t, other, changed)
			mustSet(t, s, node("extra"))
			s.snap.written.Store(n)
			s.settleSnapshot()
			s3, err := OpenBackend(ctx, b)
			if err != nil {
				t.Fatal(err)
			}
			if got := string(s3.Get(changed.Address()).Attributes); !strings.Contains(got, "another") {
				t.Fatalf("the node that another writer changed is %s", got)
			}
		})
	}
}
