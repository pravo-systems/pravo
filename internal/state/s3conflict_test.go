package state

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pravo-systems/pravo/internal/fakes3"
)

// quickRetries lets a test wait for the retries of the client in milliseconds.
func quickRetries(t *testing.T) {
	t.Helper()
	old := s3BackoffBase
	s3BackoffBase = time.Millisecond
	t.Cleanup(func() { s3BackoffBase = old })
}

// counted wraps a fault so that the test can tell how many requests the server
// saw that the fault looked at.
func counted(n *atomic.Int64, fault func(*http.Request) (int, string)) func(*http.Request) (int, string) {
	return func(r *http.Request) (int, string) {
		n.Add(1)
		return fault(r)
	}
}

// S3 answers 409 ConditionalRequestConflict to a conditional write while another
// conditional write to the key runs. The request is sent again, and ends as it
// would have: accepted, or refused with 412. It is neither a refusal that tells
// the caller that the lock is held, nor an error.
func TestS3ARetriedConflictEndsAsTheRequestWould(t *testing.T) {
	quickRetries(t)
	conflict := func(n int) func(*http.Request) (int, string) {
		return fakes3.FailFirst(n, http.StatusConflict, "ConditionalRequestConflict", fakes3.Conditional)
	}
	ctx := context.Background()

	t.Run("create only", func(t *testing.T) {
		f, b := newFakeS3(t)
		var seen atomic.Int64
		f.Fault = counted(&seen, conflict(3))
		ok, err := b.PutIfAbsent(ctx, "locks/a.lock", []byte("one"))
		if err != nil || !ok {
			t.Fatalf("PutIfAbsent = %v, %v after three conflicts, want it created", ok, err)
		}
		if seen.Load() != 4 {
			t.Errorf("the store saw %d requests, want the three that conflicted and the one that was accepted", seen.Load())
		}
		// The conflict that comes before a refusal ends in the refusal.
		f.Fault = conflict(2)
		ok, err = b.PutIfAbsent(ctx, "locks/a.lock", []byte("two"))
		if err != nil || ok {
			t.Errorf("PutIfAbsent of a key that exists = %v, %v after two conflicts, want a clean refusal", ok, err)
		}
	})
	t.Run("replace", func(t *testing.T) {
		f, b := newFakeS3(t)
		if err := b.Put(ctx, "k", []byte("one")); err != nil {
			t.Fatal(err)
		}
		f.Fault = conflict(2)
		ok, err := b.ReplaceIf(ctx, "k", func([]byte) ([]byte, bool) { return []byte("two"), true })
		if err != nil || !ok {
			t.Fatalf("ReplaceIf = %v, %v after two conflicts, want it written", ok, err)
		}
		if string(f.Objects["team/app/k"]) != "two" {
			t.Errorf("the object holds %q", f.Objects["team/app/k"])
		}
	})
	t.Run("delete", func(t *testing.T) {
		f, b := newFakeS3(t)
		if err := b.Put(ctx, "k", []byte("one")); err != nil {
			t.Fatal(err)
		}
		f.Fault = conflict(2)
		ok, err := b.DeleteIf(ctx, "k", func([]byte) bool { return true })
		if err != nil || !ok {
			t.Fatalf("DeleteIf = %v, %v after two conflicts, want it deleted", ok, err)
		}
		if _, there := f.Objects["team/app/k"]; there {
			t.Error("the object is still there")
		}
	})
	t.Run("a conflict that the store sends bare", func(t *testing.T) {
		f, b := newFakeS3(t)
		f.Fault = fakes3.FailFirst(2, http.StatusConflict, "", fakes3.Conditional)
		if ok, err := b.PutIfAbsent(ctx, "k", []byte("one")); err != nil || !ok {
			t.Fatalf("PutIfAbsent = %v, %v after two bare 409s", ok, err)
		}
	})
}

// A store that goes on answering with a conflict is an error that says so. The
// caller must not read it as "the lock is held" or "the record changed".
func TestS3AConflictThatNeverEndsIsAnError(t *testing.T) {
	quickRetries(t)
	f, b := newFakeS3(t)
	var seen atomic.Int64
	f.Fault = counted(&seen, fakes3.FailFirst(1000, http.StatusConflict, "ConditionalRequestConflict", fakes3.Conditional))
	ok, err := b.PutIfAbsent(context.Background(), "locks/a.lock", []byte("one"))
	if err == nil || ok {
		t.Fatalf("PutIfAbsent = %v, %v, want an error", ok, err)
	}
	if !strings.Contains(err.Error(), "ConditionalRequestConflict") {
		t.Errorf("the error does not say what the store answered: %v", err)
	}
	if seen.Load() != s3Attempts {
		t.Errorf("the request was sent %d times, want %d", seen.Load(), s3Attempts)
	}
	if _, there := f.Objects["team/app/locks/a.lock"]; there {
		t.Error("the object was written")
	}
}

// 503 SlowDown and 500 are sent again with a backoff, for any number of writers:
// the retry quota of the standard retryer would end the upgrade of a bucket that
// throttles its requests with "retry quota exceeded".
func TestS3SlowDownAndServerErrorsAreRetriedForManyWriters(t *testing.T) {
	quickRetries(t)
	f, b := newFakeS3(t)
	// Every request is refused twice, once with each error, before it is served.
	// The store holds its lock while the fault runs, so the map needs none.
	tries := map[string]int{}
	f.Fault = func(r *http.Request) (int, string) {
		id := r.Method + " " + r.URL.Path
		tries[id]++
		switch tries[id] {
		case 1:
			return http.StatusServiceUnavailable, "SlowDown"
		case 2:
			return http.StatusInternalServerError, "InternalError"
		}
		return 0, ""
	}
	const writers, each = 16, 40
	var wg sync.WaitGroup
	errs := make(chan error, writers*each)
	for w := 0; w < writers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < each; i++ {
				key := fmt.Sprintf("rec/%d-%d", w, i)
				ok, err := b.PutIfAbsent(context.Background(), key, []byte(key))
				if err != nil || !ok {
					errs <- fmt.Errorf("%s: %v, %v", key, ok, err)
				}
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
	if len(f.Objects) != writers*each {
		t.Errorf("%d objects, want %d", len(f.Objects), writers*each)
	}
}

// 429, which the stores that rate limit by the second answer, is sent again too.
func TestS3TooManyRequestsIsRetried(t *testing.T) {
	quickRetries(t)
	f, b := newFakeS3(t)
	f.Fault = fakes3.FailFirst(3, http.StatusTooManyRequests, "TooManyRequests", nil)
	if err := b.Put(context.Background(), "k", []byte("v")); err != nil {
		t.Fatal(err)
	}
}

// A request that the store keeps refusing with 503 is an error, and the error
// says so.
func TestS3PersistentSlowDownIsAnError(t *testing.T) {
	quickRetries(t)
	f, b := newFakeS3(t)
	f.Fault = fakes3.FailFirst(1000, http.StatusServiceUnavailable, "SlowDown", nil)
	ok, err := b.PutIfAbsent(context.Background(), "k", []byte("v"))
	if err == nil || ok || !strings.Contains(err.Error(), "SlowDown") {
		t.Fatalf("PutIfAbsent = %v, %v, want an error that names SlowDown", ok, err)
	}
}

// The probe and the check of a sealed store race eight writers on one key. A
// store that answers some of them with 409 gets the same verdict as one that
// answers 412.
func TestProbesSeeOneWinnerWhenTheLosersAreToldToRetry(t *testing.T) {
	quickRetries(t)
	f, b := newFakeS3(t)
	f.Fault = fakes3.FailFirst(40, http.StatusConflict, "ConditionalRequestConflict", func(r *http.Request) bool {
		p := r.URL.Path
		return fakes3.Conditional(r) && r.Method == http.MethodPut && (strings.HasSuffix(p, "-race") || strings.HasSuffix(p, "-swap")) && r.Header.Get("If-Match") != staleTag
	})
	if err := b.Probe(context.Background()); err != nil {
		t.Fatalf("Probe: %v", err)
	}
	if err := b.ProbeStrict(context.Background(), nil); err != nil {
		t.Fatalf("ProbeStrict: %v", err)
	}
}

// The status of the response decides, not the text of the error: a connection
// error that holds "412" in an address is not a refused condition.
func TestPreconditionFailedReadsTheStatus(t *testing.T) {
	for _, err := range []error{
		errors.New("dial tcp 127.0.0.1:4120: connect: connection refused"),
		errors.New("operation error S3: PutObject, request id 4128AB: timeout"),
		errors.New("operation error S3: PutObject, https response error StatusCode: 500, RequestID: 412"),
	} {
		if preconditionFailed(err) {
			t.Errorf("%q is read as a refused condition", err)
		}
	}
	f, b := newFakeS3(t)
	ctx := context.Background()
	// A 412 with no body, as some stores send it, is a refusal.
	f.Fault = fakes3.FailFirst(1, http.StatusPreconditionFailed, "", fakes3.Conditional)
	if ok, err := b.PutIfAbsent(ctx, "k", []byte("v")); err != nil || ok {
		t.Errorf("PutIfAbsent answered by a bare 412 = %v, %v, want a refusal", ok, err)
	}
}

// A 404 with no error code is a missing object. A missing bucket is not.
func TestMissingObjectAndMissingBucket(t *testing.T) {
	f, b := newFakeS3(t)
	ctx := context.Background()
	f.Fault = fakes3.FailFirst(1, http.StatusNotFound, "", nil)
	if _, err := b.Get(ctx, "k"); !errors.Is(err, ErrNotFound) {
		t.Errorf("Get answered by a bare 404 = %v, want ErrNotFound", err)
	}
	f.Fault = fakes3.FailFirst(1, http.StatusNotFound, "NoSuchBucket", nil)
	if _, err := b.Get(ctx, "k"); err == nil || errors.Is(err, ErrNotFound) {
		t.Errorf("Get of a missing bucket = %v, want an error that is not ErrNotFound", err)
	}
}

// A write that the store applied and then failed to answer is sent again by the
// client, and the second request is refused because the object is there: the
// object that this very call wrote. That is a success. Without the check, a
// create of a node ends in "changed by another command" and a replace in the same,
// for a change that was made.
func TestS3AWriteWhoseAnswerWasLostIsNotRefused(t *testing.T) {
	quickRetries(t)
	ctx := context.Background()
	lost := func() func(*http.Request) (int, string) {
		return fakes3.FailFirst(1, http.StatusInternalServerError, "InternalError", fakes3.Conditional)
	}

	t.Run("create only", func(t *testing.T) {
		f, b := newFakeS3(t)
		f.Lost = lost()
		ok, err := b.PutIfAbsent(ctx, "resources/a.json", []byte("mine"))
		if err != nil || !ok {
			t.Fatalf("PutIfAbsent = %v, %v, want it created", ok, err)
		}
		// Another command's object at the key is a refusal, also after a retry.
		f.Lost = nil
		f.Fault = fakes3.FailFirst(1, http.StatusInternalServerError, "InternalError", fakes3.Conditional)
		if ok, err := b.PutIfAbsent(ctx, "resources/a.json", []byte("theirs")); err != nil || ok {
			t.Errorf("PutIfAbsent of another object after a retry = %v, %v, want a refusal", ok, err)
		}
	})
	t.Run("replace", func(t *testing.T) {
		f, b := newFakeS3(t)
		if err := b.Put(ctx, "k", []byte("one")); err != nil {
			t.Fatal(err)
		}
		f.Lost = lost()
		ok, err := b.ReplaceIf(ctx, "k", func([]byte) ([]byte, bool) { return []byte("two"), true })
		if err != nil || !ok {
			t.Fatalf("ReplaceIf = %v, %v, want it written", ok, err)
		}
		if string(f.Objects["team/app/k"]) != "two" {
			t.Errorf("the object holds %q", f.Objects["team/app/k"])
		}
	})
	t.Run("a refusal without a retry is not checked", func(t *testing.T) {
		f, b := newFakeS3(t)
		if err := b.Put(ctx, "k", []byte("same")); err != nil {
			t.Fatal(err)
		}
		var gets int
		f.Fault = func(r *http.Request) (int, string) {
			if r.Method == http.MethodGet {
				gets++
			}
			return 0, ""
		}
		// The object holds these very bytes, but nothing was sent twice, so
		// somebody else wrote them.
		if ok, err := b.PutIfAbsent(ctx, "k", []byte("same")); err != nil || ok {
			t.Errorf("PutIfAbsent = %v, %v, want a refusal", ok, err)
		}
		if gets != 0 {
			t.Errorf("%d reads to check a refusal that had no retry", gets)
		}
	})
}

// A store that sends no entity tag cannot make a write conditional. The request
// would carry no If-Match and replace or delete whatever the object has become, so
// the call is an error.
func TestS3AStoreWithoutEntityTagsIsAnErrorForConditionalWrites(t *testing.T) {
	f := fakes3.New()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.ServeHTTP(&noTags{w}, r)
	}))
	t.Cleanup(srv.Close)
	b, err := NewS3Backend(context.Background(), S3Config{Bucket: "state", Prefix: "team/app", Region: "us-east-1", Endpoint: srv.URL, PathStyle: true, AccessKey: "test", SecretKey: "test"})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := b.Put(ctx, "k", []byte("one")); err != nil {
		t.Fatal(err)
	}
	ok, err := b.ReplaceIf(ctx, "k", func([]byte) ([]byte, bool) { return []byte("two"), true })
	if err == nil || ok || string(f.Objects["team/app/k"]) != "one" {
		t.Errorf("ReplaceIf = %v, %v and the object holds %q, want an error and no write", ok, err, f.Objects["team/app/k"])
	}
	ok, err = b.DeleteIf(ctx, "k", func([]byte) bool { return true })
	if err == nil || ok || f.Objects["team/app/k"] == nil {
		t.Errorf("DeleteIf = %v, %v, want an error and no delete", ok, err)
	}
}

// noTags is a response writer that leaves the entity tag out.
type noTags struct{ http.ResponseWriter }

func (w *noTags) WriteHeader(status int) {
	w.Header().Del("ETag")
	w.ResponseWriter.WriteHeader(status)
}

func (w *noTags) Write(p []byte) (int, error) {
	w.Header().Del("ETag")
	return w.ResponseWriter.Write(p)
}

// A store that refuses to create the lock of outputs.json (412) and then says that
// there is no such object (404) is waited for as long as a lock that is held, and
// then reported. Without a deadline in that branch the update asks the store
// again, with no pause, for as long as the command runs.
func TestS3OutputsLockThatIsRefusedAndNotThereIsReported(t *testing.T) {
	quickRetries(t)
	f, b := newFakeS3(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s, err := OpenBackend(ctx, b)
	if err != nil {
		t.Fatal(err)
	}
	wait := outputsLockWait
	outputsLockWait = 200 * time.Millisecond
	t.Cleanup(func() { outputsLockWait = wait })
	var requests atomic.Int64
	f.Fault = func(r *http.Request) (int, string) {
		if strings.HasSuffix(r.URL.Path, outputsLockKey) {
			requests.Add(1)
			if fakes3.Conditional(r) {
				return http.StatusPreconditionFailed, "PreconditionFailed"
			}
		}
		return 0, ""
	}
	done := make(chan error, 1)
	go func() {
		_, err := s.lockOutputs()
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil || !strings.Contains(err.Error(), "outputs.json stayed locked") {
			t.Fatalf("lockOutputs = %v, want the error that the lock was not taken", err)
		}
	case <-time.After(5 * time.Second):
		cancel()
		t.Fatalf("lockOutputs still asking after 5 s and %d requests, with a wait of %s", requests.Load(), outputsLockWait)
	}
	if requests.Load() > 200 {
		t.Errorf("%d requests for a wait of %s: the loop does not pause", requests.Load(), outputsLockWait)
	}
}
