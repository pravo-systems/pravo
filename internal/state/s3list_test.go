package state

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"sort"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pravo-systems/pravo/internal/fakes3"
)

// A listing of more than 1,000 keys comes in pages, and every key is returned
// once, in order.
func TestS3ListFollowsEveryPage(t *testing.T) {
	f, b := newFakeS3(t)
	ctx := context.Background()
	const n = 2500
	var want []string
	for i := 0; i < n; i++ {
		k := fmt.Sprintf("rec/%04d", i)
		f.Objects["team/app/"+k] = []byte("x")
		want = append(want, k)
	}
	f.Objects["team/app/resources/other.json"] = []byte("x")
	f.Objects["team/app2/rec/elsewhere"] = []byte("x")
	var pages int
	f.Fault = func(r *http.Request) (int, string) {
		if r.URL.Query().Get("list-type") == "2" {
			pages++
		}
		return 0, ""
	}
	got, err := b.List(ctx, "rec")
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(got, want) {
		t.Fatalf("listed %d keys, want the %d under rec/ once each and in order", len(got), n)
	}
	if pages != 3 {
		t.Errorf("%d requests for %d keys, want 3 pages of at most %d", pages, n, fakes3.MaxKeys)
	}
}

// A store that says the listing goes on and gives no token to go on with is
// asked again after the last key it returned; one that gives the same token
// again is an error and not a loop.
func TestS3ListDoesNotLoop(t *testing.T) {
	f, b := newFakeS3(t)
	ctx := context.Background()
	for i := 0; i < 7; i++ {
		f.Objects[fmt.Sprintf("team/app/rec/%d", i)] = []byte("x")
	}
	f.PageSize = 3
	f.DropTokens = true
	got, err := b.List(ctx, "rec")
	if err != nil || len(got) != 7 || !sort.StringsAreSorted(got) {
		t.Fatalf("a listing without continuation tokens = %v, %v, want the 7 keys", got, err)
	}
	f.DropTokens = false
	f.RepeatToken = true
	if _, err := b.List(ctx, "rec"); err == nil || !strings.Contains(err.Error(), "does not advance") {
		t.Errorf("a listing that repeats its token = %v, want an error that says so", err)
	}
}

// The keys of a state hold characters that a URL treats as its own: an address
// with an index or a quote is written as %005B and %0022, and a prefix may hold
// a space or a plus. They go to the store and come back from a listing as they
// went.
func TestS3KeysWithSpecialCharacters(t *testing.T) {
	f, b := newFakeS3(t)
	ctx := context.Background()
	keys := []string{
		"resources/" + encodeAddr(`aws_instance.web["a/b c"]`) + ".json",
		"resources/" + encodeAddr(`module.x[0].aws_s3_bucket.b`) + ".json",
		"resources/" + encodeAddr(`null_resource.ünïcode`) + ".json",
		"locks/" + encodeAddr(`a.b["x+y&z=1?#"]`) + ".lock",
		"rec/plus+and space and %41 and ~",
	}
	for _, k := range keys {
		if err := b.Put(ctx, k, []byte(k)); err != nil {
			t.Fatalf("Put %q: %v", k, err)
		}
		if _, ok := f.Objects["team/app/"+k]; !ok {
			t.Fatalf("%q was stored under another name: %v", k, mapKeys(f.Objects))
		}
		if got, err := b.Get(ctx, k); err != nil || string(got) != k {
			t.Errorf("Get %q = %q, %v", k, got, err)
		}
		if ok, err := b.PutIfAbsent(ctx, k, []byte("again")); err != nil || ok {
			t.Errorf("PutIfAbsent %q = %v, %v, want a refusal", k, ok, err)
		}
	}
	for _, dir := range []string{"resources", "locks", "rec"} {
		got, err := b.List(ctx, dir)
		if err != nil {
			t.Fatal(err)
		}
		var want []string
		for _, k := range keys {
			if strings.HasPrefix(k, dir+"/") {
				want = append(want, k)
			}
		}
		sort.Strings(want)
		if !slices.Equal(got, want) {
			t.Errorf("List %s = %q, want %q", dir, got, want)
		}
	}
	for _, k := range keys {
		ok, err := b.DeleteIf(ctx, k, func(cur []byte) bool { return string(cur) == k })
		if err != nil || !ok {
			t.Errorf("DeleteIf %q = %v, %v", k, ok, err)
		}
	}
	if len(f.Objects) != 0 {
		t.Errorf("objects left: %v", mapKeys(f.Objects))
	}
}

func mapKeys(m map[string][]byte) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// A store may send entity tags without quotes. The tag is sent back as the store
// gave it, and the conditions work.
func TestS3EntityTagsWithoutQuotes(t *testing.T) {
	f, b := newFakeS3(t)
	f.UnquotedTags = true
	ctx := context.Background()
	if err := b.Put(ctx, "k", []byte("one")); err != nil {
		t.Fatal(err)
	}
	ok, err := b.ReplaceIf(ctx, "k", func([]byte) ([]byte, bool) { return []byte("two"), true })
	if err != nil || !ok {
		t.Fatalf("ReplaceIf with an unquoted tag = %v, %v", ok, err)
	}
	ok, err = b.DeleteIf(ctx, "k", func([]byte) bool { return true })
	if err != nil || !ok {
		t.Fatalf("DeleteIf with an unquoted tag = %v, %v", ok, err)
	}
}

// A store that ignores the continuation token, and numbers its pages, sends the
// first page again and again. The listing stops at the first key it has seen, and
// says so, and does not run until the command is cancelled.
func TestS3ListStopsOnAPageThatIsSentAgain(t *testing.T) {
	var requests atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/xml")
		fmt.Fprintf(w, `<ListBucketResult><IsTruncated>true</IsTruncated><NextContinuationToken>page-%d</NextContinuationToken>`+
			`<Contents><Key>team/app/rec/a</Key></Contents><Contents><Key>team/app/rec/b</Key></Contents></ListBucketResult>`, requests.Add(1))
	}))
	t.Cleanup(srv.Close)
	b, err := NewS3Backend(context.Background(), S3Config{Bucket: "state", Prefix: "team/app", Region: "us-east-1", Endpoint: srv.URL, PathStyle: true, AccessKey: "test", SecretKey: "test"})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	keys, err := b.List(ctx, "rec")
	if err == nil || !strings.Contains(err.Error(), "does not advance") || ctx.Err() != nil {
		t.Fatalf("List = %v, %v after %d requests, want an error that says that the listing does not advance", keys, err, requests.Load())
	}
	if requests.Load() > 3 {
		t.Errorf("%d requests, want the listing to stop at the second page", requests.Load())
	}
}
