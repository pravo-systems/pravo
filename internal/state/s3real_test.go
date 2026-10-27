package state

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

// These tests run against a real S3-compatible server when PRAVO_TEST_S3_ENDPOINT
// names one, and are skipped otherwise. They never read the AWS credentials of
// the environment or of ~/.aws: the server's own come from
// PRAVO_TEST_S3_ACCESS_KEY and PRAVO_TEST_S3_SECRET_KEY. The bucket,
// PRAVO_TEST_S3_BUCKET, must exist; every test works under a prefix of its own
// and deletes what it wrote.
//
//	docker run -d --name pravo-minio -p 127.0.0.1:9300:9000 \
//	  -e MINIO_ROOT_USER=minioadmin -e MINIO_ROOT_PASSWORD=minioadmin <a minio image> server /data
//	PRAVO_TEST_S3_ENDPOINT=http://127.0.0.1:9300 PRAVO_TEST_S3_BUCKET=pravo \
//	PRAVO_TEST_S3_ACCESS_KEY=minioadmin PRAVO_TEST_S3_SECRET_KEY=minioadmin \
//	  go test ./internal/state -run TestRealStore -v
func realStore(t *testing.T) *S3Backend {
	t.Helper()
	endpoint := os.Getenv("PRAVO_TEST_S3_ENDPOINT")
	if endpoint == "" {
		t.Skip("PRAVO_TEST_S3_ENDPOINT is not set")
	}
	if os.Getenv("PRAVO_TEST_S3_ACCESS_KEY") == "" || os.Getenv("PRAVO_TEST_S3_SECRET_KEY") == "" {
		// Without both, the client would take the keys of the environment or of
		// ~/.aws, and sign requests to the server with them.
		t.Skip("PRAVO_TEST_S3_ACCESS_KEY and PRAVO_TEST_S3_SECRET_KEY are not both set")
	}
	bucket := os.Getenv("PRAVO_TEST_S3_BUCKET")
	if bucket == "" {
		bucket = "pravo"
	}
	b, err := NewS3Backend(context.Background(), S3Config{
		Bucket: bucket, Prefix: "test-" + newID(6), Region: "us-east-1", Endpoint: endpoint, PathStyle: true,
		AccessKey: os.Getenv("PRAVO_TEST_S3_ACCESS_KEY"), SecretKey: os.Getenv("PRAVO_TEST_S3_SECRET_KEY"),
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx := context.Background()
		for _, dir := range []string{"rec", "resources", "locks", "probe", "page", "keys", "race"} {
			keys, _ := b.List(ctx, dir)
			for _, k := range keys {
				_ = b.Delete(ctx, k)
			}
		}
	})
	return b
}

// recorder counts the statuses of the responses to the requests that go through
// it.
type recorder struct {
	inner aws.HTTPClient
	mu    sync.Mutex
	seen  map[string]int
	first error
}

func (r *recorder) Do(req *http.Request) (*http.Response, error) {
	resp, err := r.inner.Do(req)
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.seen == nil {
		r.seen = map[string]int{}
	}
	if err != nil {
		r.seen[req.Method+" error"]++
		if r.first == nil {
			r.first = err
		}
	} else {
		r.seen[fmt.Sprintf("%s %d", req.Method, resp.StatusCode)]++
	}
	return resp, err
}

func (r *recorder) String() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	var parts []string
	for k, n := range r.seen {
		parts = append(parts, fmt.Sprintf("%s x%d", k, n))
	}
	sort.Strings(parts)
	out := strings.Join(parts, ", ")
	if r.first != nil {
		out += fmt.Sprintf(" (first transport error: %v)", r.first)
	}
	return out
}

// rawOpts makes a call go through rec, with no retries, so that the test sees the
// status of every request.
func (b *S3Backend) rawOpts(rec *recorder) func(*s3.Options) {
	rec.inner = b.http
	return func(o *s3.Options) {
		o.HTTPClient = rec
		o.Retryer = aws.NopRetryer{}
	}
}

// Thirty-two writers (PRAVO_TEST_S3_RACERS sets the number) race to create one key, and then to replace it with the
// entity tag they all read. The server must pick one winner each time. The
// statuses of the losers are logged: 412 PreconditionFailed, or 409
// ConditionalRequestConflict for a writer that met another one in flight.
func TestRealStoreConditionalRaces(t *testing.T) {
	b := realStore(t)
	ctx := context.Background()
	racers := 32
	if n, err := strconv.Atoi(os.Getenv("PRAVO_TEST_S3_RACERS")); err == nil && n > 0 {
		racers = n
	}
	race := func(name string, do func(rec *recorder, i int) error) {
		rec := &recorder{}
		var wg sync.WaitGroup
		var mu sync.Mutex
		wins := 0
		for i := range racers {
			wg.Add(1)
			go func() {
				defer wg.Done()
				err := do(rec, i)
				mu.Lock()
				defer mu.Unlock()
				switch {
				case err == nil:
					wins++
				case !preconditionFailed(err) && !conflicted(err):
					t.Errorf("%s: unexpected error: %v", name, err)
				}
			}()
		}
		wg.Wait()
		t.Logf("%s: %d writers, %d won; %s", name, racers, wins, rec)
		if wins != 1 {
			t.Errorf("%s: %d writers won, want 1", name, wins)
		}
	}
	key := b.key("race/create")
	race("create", func(rec *recorder, i int) error {
		_, err := b.client.PutObject(ctx, &s3.PutObjectInput{Bucket: aws.String(b.bucket), Key: aws.String(key), Body: strings.NewReader(fmt.Sprint(i)), IfNoneMatch: aws.String("*")}, b.rawOpts(rec))
		return err
	})
	_, tag, err := b.getTagged(ctx, "race/create")
	if err != nil {
		t.Fatal(err)
	}
	race("replace", func(rec *recorder, i int) error {
		_, err := b.client.PutObject(ctx, &s3.PutObjectInput{Bucket: aws.String(b.bucket), Key: aws.String(key), Body: strings.NewReader("r" + fmt.Sprint(i)), IfMatch: tag}, b.rawOpts(rec))
		return err
	})
	// Through the backend, where a conflict is sent again, every loser ends in a
	// refusal. The statuses of all the attempts are logged.
	rec := &recorder{inner: b.http}
	b.client = s3.New(b.client.Options(), func(o *s3.Options) { o.HTTPClient = rec })
	started := time.Now()
	var wg sync.WaitGroup
	var mu sync.Mutex
	wins := 0
	for i := range racers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			// Each writer replaces what it read, as ReplaceIf does.
			ok, err := b.ReplaceIf(ctx, "race/create", func(cur []byte) ([]byte, bool) { return []byte(fmt.Sprintf("b%d", i)), true })
			if err != nil {
				t.Errorf("ReplaceIf: %v", err)
			}
			mu.Lock()
			if ok {
				wins++
			}
			mu.Unlock()
		}()
	}
	wg.Wait()
	if wins == 0 {
		t.Error("no ReplaceIf of 32 concurrent ones wrote anything")
	}
	t.Logf("ReplaceIf: %d of %d concurrent calls wrote (the others read a newer object and were refused) in %v; %s", wins, racers, time.Since(started).Round(time.Millisecond), rec)
}

// The probes of the S3 backend against the server. A server that fails Probe
// ignores a condition the locks rest on, and the test fails with it; ProbeStrict
// is run either way.
func TestRealStoreProbes(t *testing.T) {
	b := realStore(t)
	ctx := context.Background()
	err := b.ProbeSteps(ctx, func(check string, err error) {
		t.Logf("probe: %-32s %v", check, err)
	})
	var pe *ProbeError
	switch {
	case err == nil:
	case errors.As(err, &pe):
		t.Errorf("Probe refuses the store: %v", err)
	default:
		t.Fatalf("Probe: %v", err)
	}
	err = b.ProbeStrict(ctx, func(check string, err error) {
		t.Logf("strict: %-32s %v", check, err)
	})
	if err != nil {
		t.Errorf("ProbeStrict: %v", err)
	}
}

// A listing of more than a thousand keys, and keys with characters that a URL
// treats as its own, as they go through the real server.
func TestRealStoreListAndKeys(t *testing.T) {
	b := realStore(t)
	ctx := context.Background()
	const n = 2300
	var want []string
	var wg sync.WaitGroup
	sem := make(chan struct{}, 16)
	for i := range n {
		k := fmt.Sprintf("page/%05d", i)
		want = append(want, k)
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			if err := b.Put(ctx, k, []byte("x")); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	got, err := b.List(ctx, "page")
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(got, want) {
		t.Errorf("listed %d keys, want %d, once each and in order", len(got), n)
	}

	keys := []string{
		"keys/" + encodeAddr(`aws_instance.web["a/b c"]`) + ".json",
		"keys/" + encodeAddr(`module.x[0].aws_s3_bucket.b`) + ".json",
		"keys/" + encodeAddr(`null_resource.ünïcode`) + ".json",
		"keys/plus+and space and %41 and ~ and * and ' and ( and )",
	}
	for _, k := range keys {
		if err := b.Put(ctx, k, []byte(k)); err != nil {
			t.Fatalf("Put %q: %v", k, err)
		}
		if got, err := b.Get(ctx, k); err != nil || string(got) != k {
			t.Errorf("Get %q = %q, %v", k, got, err)
		}
		if ok, err := b.PutIfAbsent(ctx, k, []byte("again")); err != nil || ok {
			t.Errorf("PutIfAbsent %q = %v, %v, want a refusal", k, ok, err)
		}
		if ok, err := b.ReplaceIf(ctx, k, func(cur []byte) ([]byte, bool) { return append(cur, '!'), true }); err != nil || !ok {
			t.Errorf("ReplaceIf %q = %v, %v", k, ok, err)
		}
	}
	listed, err := b.List(ctx, "keys")
	if err != nil {
		t.Fatal(err)
	}
	sorted := slices.Clone(keys)
	sort.Strings(sorted)
	if !slices.Equal(listed, sorted) {
		t.Errorf("List keys = %q, want %q", listed, sorted)
	}
	if _, err := b.Get(ctx, "keys/missing"); !errors.Is(err, ErrNotFound) {
		t.Errorf("Get of a missing key = %v, want ErrNotFound", err)
	}
	if _, err := b.ReplaceIf(ctx, "keys/missing", func([]byte) ([]byte, bool) { return []byte("x"), true }); !errors.Is(err, ErrNotFound) {
		t.Errorf("ReplaceIf of a missing key = %v, want ErrNotFound", err)
	}
}

// An If-Match write to a key that was deleted after it was read must not bring the
// key back: S3 answers 404 NoSuchKey. A server that creates the object makes
// the renewal of a lock that another command has reclaimed write the lock again.
func TestRealStoreReplaceOfADeletedKey(t *testing.T) {
	b := realStore(t)
	ctx := context.Background()
	if err := b.Put(ctx, "race/gone", []byte("one")); err != nil {
		t.Fatal(err)
	}
	_, tag, err := b.getTagged(ctx, "race/gone")
	if err != nil {
		t.Fatal(err)
	}
	if err := b.Delete(ctx, "race/gone"); err != nil {
		t.Fatal(err)
	}
	rec := &recorder{}
	_, err = b.client.PutObject(ctx, &s3.PutObjectInput{Bucket: aws.String(b.bucket), Key: aws.String(b.key("race/gone")), Body: strings.NewReader("two"), IfMatch: tag}, b.rawOpts(rec))
	t.Logf("If-Match put of a deleted key: %s, error %v", rec, err)
	if _, gerr := b.Get(ctx, "race/gone"); gerr == nil {
		t.Errorf("the put with If-Match created the object that had been deleted")
	}
}
