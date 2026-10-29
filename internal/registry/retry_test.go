package registry

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"
)

func fastClient() *http.Client {
	return &http.Client{Transport: &retryTransport{wait: func(int) time.Duration { return time.Millisecond }}}
}

type retryRoundTripper func(*http.Request) (*http.Response, error)

func (f retryRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func TestRetryTransportHonoursRetryAfter(t *testing.T) {
	for _, tc := range []struct {
		name   string
		header string
		date   bool
		want   time.Duration
	}{
		{name: "seconds", header: "3", want: 3 * time.Second},
		{name: "HTTP date", date: true, want: 3 * time.Second},
		{name: "whitespace", header: " 3 ", want: 3 * time.Second},
		{name: "shorter than backoff", header: "1", want: 2 * time.Second},
		{name: "zero", header: "0", want: 2 * time.Second},
		{name: "missing", want: 2 * time.Second},
		{name: "invalid", header: "soon", want: 2 * time.Second},
		{name: "past date", header: "Fri, 31 Dec 1999 00:00:00 GMT", want: 2 * time.Second},
		{name: "negative", header: "-3", want: 2 * time.Second},
		{name: "signed", header: "+3", want: 2 * time.Second},
		{name: "fractional", header: "3.5", want: 2 * time.Second},
		{name: "duration overflow", header: "9223372037", want: 2 * time.Second},
		{name: "overflow", header: "9223372036854775807", want: 2 * time.Second},
	} {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				start := time.Now()
				header := tc.header
				if tc.date {
					header = start.Add(tc.want).Format(http.TimeFormat)
				}
				calls := 0
				tr := &retryTransport{
					wait: func(int) time.Duration { return 2 * time.Second },
					base: retryRoundTripper(func(*http.Request) (*http.Response, error) {
						calls++
						status := http.StatusOK
						if calls == 1 {
							status = http.StatusServiceUnavailable
						}
						return &http.Response{
							StatusCode: status,
							Header:     http.Header{"Retry-After": []string{header}},
							Body:       io.NopCloser(strings.NewReader("")),
						}, nil
					}),
				}
				req, err := http.NewRequest(http.MethodGet, "https://registry.example.test", nil)
				if err != nil {
					t.Fatal(err)
				}
				resp, err := tr.RoundTrip(req)
				if err != nil {
					t.Fatal(err)
				}
				resp.Body.Close()
				if elapsed := time.Since(start); elapsed != tc.want {
					t.Errorf("retried after %s, want %s", elapsed, tc.want)
				}
				if calls != 2 || resp.StatusCode != http.StatusOK {
					t.Errorf("status %d after %d requests, want 200 after 2", resp.StatusCode, calls)
				}
			})
		})
	}
}

func TestRetryTransportRetryAfterCanBeCancelled(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		calls := 0
		tr := &retryTransport{
			wait: func(int) time.Duration { return time.Second },
			base: retryRoundTripper(func(*http.Request) (*http.Response, error) {
				calls++
				return &http.Response{
					StatusCode: http.StatusTooManyRequests,
					Header:     http.Header{"Retry-After": []string{"3600"}},
					Body:       io.NopCloser(strings.NewReader("")),
				}, nil
			}),
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://registry.example.test", nil)
		if err != nil {
			t.Fatal(err)
		}
		go func() {
			time.Sleep(time.Second)
			cancel()
		}()
		start := time.Now()
		if _, err := tr.RoundTrip(req); !errors.Is(err, context.Canceled) {
			t.Errorf("error %v, want context.Canceled", err)
		}
		if elapsed := time.Since(start); elapsed != time.Second {
			t.Errorf("cancelled after %s, want 1s", elapsed)
		}
		if calls != 1 {
			t.Errorf("made %d requests, want 1", calls)
		}
	})
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
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL, http.NoBody)
	time.AfterFunc(50*time.Millisecond, cancel)
	start := time.Now()
	if _, err := slow.Do(req); err == nil {
		t.Error("a cancelled request returned no error")
	}
	if time.Since(start) > 5*time.Second {
		t.Errorf("a cancelled request waited %s", time.Since(start))
	}
}
