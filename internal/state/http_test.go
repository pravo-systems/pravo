package state

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pravo-systems/pravo/internal/fakehttp"
)

func newFakeHTTP(t *testing.T) (*fakehttp.Server, *HTTPBackend) {
	t.Helper()
	httpBackoffBase = time.Millisecond
	t.Cleanup(func() { httpBackoffBase = 100 * time.Millisecond })
	f := fakehttp.New()
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	b, err := NewHTTPBackend(HTTPConfig{URL: srv.URL + "/team/app/"})
	if err != nil {
		t.Fatal(err)
	}
	return f, b
}

func TestHTTPBackendStore(t *testing.T) {
	f, b := newFakeHTTP(t)
	ctx := context.Background()
	s, err := OpenBackend(ctx, b)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := f.Objects["team/app/meta.json"]; !ok {
		t.Fatalf("meta.json not written under the path: %v", f.Objects)
	}
	r := &Resource{Mode: "managed", Type: "random_pet", Name: "a", Attributes: []byte(`{"id":"x"}`)}
	if err := s.Set(r, ""); err != nil {
		t.Fatal(err)
	}
	if _, ok := f.Objects["team/app/resources/random_pet.a.json"]; !ok {
		t.Fatalf("record not written: %v", f.Objects)
	}
	again, err := OpenBackend(ctx, b)
	if err != nil {
		t.Fatal(err)
	}
	if again.Get("random_pet.a") == nil {
		t.Fatal("a second store does not see the record")
	}
	if err := s.Lock("random_pet.a", "apply", ""); err != nil {
		t.Fatal(err)
	}
	var locked *ErrLocked
	if err := again.Lock("random_pet.a", "apply", ""); !errors.As(err, &locked) {
		t.Fatalf("second lock: %v, want ErrLocked", err)
	}
}

func TestHTTPBackendNamesTheURLWithoutCredentials(t *testing.T) {
	b, err := NewHTTPBackend(HTTPConfig{URL: "https://state.example.com/team/app/", Token: "secret"})
	if err != nil {
		t.Fatal(err)
	}
	if b.Name() != "https://state.example.com/team/app" || strings.Contains(b.Name(), "secret") {
		t.Fatalf("name %q", b.Name())
	}
	for _, bad := range []string{"", "ftp://x/y", "state.example.com", "https://u:p@state.example.com/", "https://x/y?a=b"} {
		if _, err := NewHTTPBackend(HTTPConfig{URL: bad}); err == nil {
			t.Errorf("URL %q accepted", bad)
		}
	}
}

func TestHTTPBackendSendsCredentials(t *testing.T) {
	f := fakehttp.New()
	f.Token = "s3cret"
	srv := httptest.NewServer(f)
	defer srv.Close()
	ctx := context.Background()
	good, _ := NewHTTPBackend(HTTPConfig{URL: srv.URL, Token: "s3cret"})
	if err := good.Put(ctx, "a", []byte("x")); err != nil {
		t.Fatal(err)
	}
	bad, _ := NewHTTPBackend(HTTPConfig{URL: srv.URL, Token: "nope"})
	err := bad.Put(ctx, "a", []byte("y"))
	if err == nil || httpStatus(err) != http.StatusUnauthorized {
		t.Fatalf("put with a wrong token: %v, want 401", err)
	}
	var seen string
	basic := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u, p, _ := r.BasicAuth()
		seen = u + ":" + p
		w.WriteHeader(http.StatusNotFound)
	}))
	defer basic.Close()
	bb, _ := NewHTTPBackend(HTTPConfig{URL: basic.URL, Username: "ada", Password: "pw"})
	if _, err := bb.Get(ctx, "a"); !errors.Is(err, ErrNotFound) || seen != "ada:pw" {
		t.Fatalf("basic get: %v, seen %q", err, seen)
	}
}

func TestHTTPBackendRetriesABusyServer(t *testing.T) {
	f, b := newFakeHTTP(t)
	ctx := context.Background()
	f.Fault = fakehttp.FailFirst(3, http.StatusServiceUnavailable, nil)
	if err := b.Put(ctx, "a", []byte("x")); err != nil {
		t.Fatal(err)
	}
	f.Fault = fakehttp.FailFirst(2, http.StatusTooManyRequests, nil)
	if got, err := b.Get(ctx, "a"); err != nil || string(got) != "x" {
		t.Fatalf("get: %q, %v", got, err)
	}
	f.Fault = fakehttp.FailFirst(1000, http.StatusBadGateway, nil)
	if _, err := b.Get(ctx, "a"); httpStatus(err) != http.StatusBadGateway {
		t.Fatalf("get from a server that stays busy: %v", err)
	}
}

// A create whose answer was lost is sent again and refused because the first
// attempt made it: the create is this call's.
func TestHTTPPutIfAbsentThatLandedBeforeTheAnswerWasLost(t *testing.T) {
	f, b := newFakeHTTP(t)
	var lost atomic.Bool
	f.Lost = func(r *http.Request) int {
		if r.Method == http.MethodPut && lost.CompareAndSwap(false, true) {
			return http.StatusBadGateway
		}
		return 0
	}
	ok, err := b.PutIfAbsent(context.Background(), "locks/a.lock", []byte("mine"))
	if err != nil || !ok {
		t.Fatalf("PutIfAbsent = %v, %v; want true", ok, err)
	}
	// Another writer's object is a refusal.
	ok, err = b.PutIfAbsent(context.Background(), "locks/a.lock", []byte("yours"))
	if err != nil || ok {
		t.Fatalf("PutIfAbsent over an object = %v, %v; want false", ok, err)
	}
}

// When the server stays busy after a conditional write, nothing says whether it
// was made, and the error says so.
func TestHTTPConditionalWriteThatStaysBusyIsOfUnknownOutcome(t *testing.T) {
	f, b := newFakeHTTP(t)
	ctx := context.Background()
	if err := b.Put(ctx, "k", []byte("one")); err != nil {
		t.Fatal(err)
	}
	f.Fault = func(r *http.Request) int {
		if r.Method == http.MethodPut {
			return http.StatusServiceUnavailable
		}
		return 0
	}
	if _, err := b.PutIfAbsent(ctx, "new", []byte("x")); !errors.Is(err, ErrUnknownOutcome) {
		t.Fatalf("PutIfAbsent: %v, want ErrUnknownOutcome", err)
	}
	if _, err := b.ReplaceIf(ctx, "k", func([]byte) ([]byte, bool) { return []byte("two"), true }); !errors.Is(err, ErrUnknownOutcome) {
		t.Fatalf("ReplaceIf: %v, want ErrUnknownOutcome", err)
	}
}

func TestHTTPListIsRelativeAndSorted(t *testing.T) {
	_, b := newFakeHTTP(t)
	ctx := context.Background()
	for _, k := range []string{"resources/b.json", "resources/a.json", "txn/x.json"} {
		if err := b.Put(ctx, k, []byte("{}")); err != nil {
			t.Fatal(err)
		}
	}
	got, err := b.List(ctx, "resources")
	if err != nil || strings.Join(got, ",") != "resources/a.json,resources/b.json" {
		t.Fatalf("List = %v, %v", got, err)
	}
	if got, err := b.List(ctx, "nothing"); err != nil || len(got) != 0 {
		t.Fatalf("List of nothing = %v, %v", got, err)
	}
}

func TestHTTPKeysWithPercentSurviveTheRoundTrip(t *testing.T) {
	_, b := newFakeHTTP(t)
	ctx := context.Background()
	key := "resources/" + encodeAddr("a b/c") + ".json"
	if err := b.Put(ctx, key, []byte("x")); err != nil {
		t.Fatal(err)
	}
	if got, err := b.Get(ctx, key); err != nil || string(got) != "x" {
		t.Fatalf("Get = %q, %v", got, err)
	}
	if keys, _ := b.List(ctx, "resources"); len(keys) != 1 || keys[0] != key {
		t.Fatalf("List = %v, want %s", keys, key)
	}
}

func TestHTTPProbe(t *testing.T) {
	f, b := newFakeHTTP(t)
	ctx := context.Background()
	var checks []string
	if err := b.ProbeSteps(ctx, func(c string, err error) {
		if err != nil {
			t.Errorf("%s: %v", c, err)
		}
		checks = append(checks, c)
	}); err != nil {
		t.Fatal(err)
	}
	if strings.Join(checks, "|") != strings.Join(ProbeChecks, "|") {
		t.Fatalf("checks %v", checks)
	}
	if err := b.ProbeStrict(ctx, nil); err != nil {
		t.Fatal(err)
	}
	if n := len(f.Objects); n != 0 {
		t.Fatalf("%d scratch objects left: %v", n, f.Objects)
	}
	f.IgnoreConditions = true
	var pe *ProbeError
	if err := b.Probe(ctx); !errors.As(err, &pe) {
		t.Fatalf("Probe of a server that ignores conditions: %v, want *ProbeError", err)
	}
	f.IgnoreConditions = false
	f.Fault = func(*http.Request) int { return http.StatusForbidden }
	if err := b.Probe(ctx); err == nil || errors.As(err, &pe) {
		t.Fatalf("Probe of a server that refuses us: %v, want an error that is not a verdict", err)
	}
}
