package registry

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func fastClient() *http.Client {
	return &http.Client{Transport: &retryTransport{wait: func(int) time.Duration { return time.Millisecond }}}
}

func TestRetryTransportRepeatsWhatMayPass(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch calls.Add(1) {
		case 1: // the connection is dropped without an answer
			conn, _, _ := w.(http.Hijacker).Hijack()
			_ = conn.Close()
		case 2:
			w.WriteHeader(http.StatusServiceUnavailable)
		default:
			_, _ = io.WriteString(w, "ok")
		}
	}))
	defer srv.Close()
	resp, err := fastClient().Get(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if string(body) != "ok" || calls.Load() != 3 {
		t.Errorf("body %q after %d requests, want ok after 3", body, calls.Load())
	}
}

func TestRetryTransportDoesNotRepeatAnAnswer(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()
	resp, err := fastClient().Get(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound || calls.Load() != 1 {
		t.Errorf("status %d after %d requests, want 404 after 1", resp.StatusCode, calls.Load())
	}
	// A request that changes something is made once.
	calls.Store(0)
	srv2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv2.Close()
	resp, err = fastClient().Post(srv2.URL, "text/plain", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if calls.Load() != 1 {
		t.Errorf("a POST was made %d times, want 1", calls.Load())
	}
}

func TestRetryTransportGivesUpAndStopsWhenCancelled(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer srv.Close()
	resp, err := fastClient().Get(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadGateway || calls.Load() != retryAttempts {
		t.Errorf("status %d after %d requests, want 502 after %d", resp.StatusCode, calls.Load(), retryAttempts)
	}

	ctx, cancel := context.WithCancel(context.Background())
	slow := &http.Client{Transport: &retryTransport{wait: func(int) time.Duration { return time.Minute }}}
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL, nil)
	time.AfterFunc(50*time.Millisecond, cancel)
	start := time.Now()
	if _, err := slow.Do(req); err == nil {
		t.Error("a cancelled request returned no error")
	}
	if time.Since(start) > 5*time.Second {
		t.Errorf("a cancelled request waited %s", time.Since(start))
	}
}
