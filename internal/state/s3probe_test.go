package state

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"pravo.dev/pravo/internal/fakes3"
)

// probeStore serves a fake store that mangles the conditional requests the way
// a real store might: it drops a header, or answers 501 to it.
func probeStore(t *testing.T, mangle func(r *http.Request) (drop bool, status int)) (*fakes3.Server, *S3Backend) {
	t.Helper()
	f := fakes3.New()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if mangle != nil {
			if drop, status := mangle(r); status != 0 {
				code := "NotImplemented"
				if status == http.StatusForbidden {
					code = "AccessDenied"
				}
				w.WriteHeader(status)
				_, _ = w.Write([]byte("<Error><Code>" + code + "</Code></Error>"))
				return
			} else if drop {
				r.Header.Del("If-None-Match")
				r.Header.Del("If-Match")
			}
		}
		f.ServeHTTP(w, r)
	}))
	t.Cleanup(srv.Close)
	b, err := NewS3Backend(context.Background(), S3Config{
		Bucket: "state", Prefix: "team/app", Region: "us-east-1", Endpoint: srv.URL, PathStyle: true, AccessKey: "test", SecretKey: "test",
	})
	if err != nil {
		t.Fatal(err)
	}
	return f, b
}

func TestProbeAcceptsAStoreThatHonoursTheConditions(t *testing.T) {
	f, b := probeStore(t, nil)
	if err := b.Probe(context.Background()); err != nil {
		t.Fatal(err)
	}
	for k := range f.Objects {
		t.Errorf("the probe left %q behind", k)
	}
	if f.Puts == 0 {
		t.Error("the probe wrote nothing")
	}
}

func TestProbeRefusesAStoreThatIgnoresOrRejectsAConditon(t *testing.T) {
	has := func(r *http.Request, h string) bool { return r.Header.Get(h) != "" }
	stale := func(r *http.Request) bool { return r.Header.Get("If-Match") == staleTag }
	cases := []struct {
		name   string
		mangle func(r *http.Request) (bool, int)
		want   string
	}{
		{"ignores create only", func(r *http.Request) (bool, int) {
			return r.Method == http.MethodPut && has(r, "If-None-Match"), 0
		}, "If-None-Match is ignored"},
		{"ignores If-Match on a put", func(r *http.Request) (bool, int) {
			return r.Method == http.MethodPut && stale(r), 0
		}, "If-Match is ignored on a put"},
		{"ignores If-Match on a delete", func(r *http.Request) (bool, int) {
			return r.Method == http.MethodDelete && stale(r), 0
		}, "If-Match is ignored on a delete"},
		{"does not decide concurrent creates", func(r *http.Request) (bool, int) {
			return strings.Contains(r.URL.Path, "-race"), 0
		}, "concurrent creates of one key succeeded"},
		{"rejects If-Match on a delete", func(r *http.Request) (bool, int) {
			if r.Method == http.MethodDelete && has(r, "If-Match") {
				return false, http.StatusNotImplemented
			}
			return false, 0
		}, "If-Match is not implemented"},
		{"rejects create only", func(r *http.Request) (bool, int) {
			if r.Method == http.MethodPut && has(r, "If-None-Match") {
				return false, http.StatusNotImplemented
			}
			return false, 0
		}, "If-None-Match is not implemented"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f, b := probeStore(t, c.mangle)
			err := b.Probe(context.Background())
			var pe *ProbeError
			if !errors.As(err, &pe) || !strings.Contains(pe.Detail, c.want) {
				t.Fatalf("error = %v, want a ProbeError containing %q", err, c.want)
			}
			if !strings.Contains(err.Error(), "s3://state/team/app cannot hold a state") {
				t.Errorf("the message does not name the store: %v", err)
			}
			for k := range f.Objects {
				t.Errorf("the probe left %q behind", k)
			}
		})
	}
}

func TestProbeReportsAFaultOfTheStoreAsAnotherError(t *testing.T) {
	_, b := probeStore(t, func(r *http.Request) (bool, int) {
		return false, http.StatusForbidden
	})
	err := b.Probe(context.Background())
	var pe *ProbeError
	if err == nil || errors.As(err, &pe) {
		t.Fatalf("a store that denies the write gave %v; want an error that is not a ProbeError", err)
	}
}

func TestProbeStepsReportsEachCheckInOrder(t *testing.T) {
	_, b := probeStore(t, nil)
	var got []string
	if err := b.ProbeSteps(context.Background(), func(check string, err error) {
		if err != nil {
			t.Errorf("%s: %v", check, err)
		}
		got = append(got, check)
	}); err != nil {
		t.Fatal(err)
	}
	if strings.Join(got, "|") != strings.Join(ProbeChecks, "|") {
		t.Errorf("reported %q, want %q", got, ProbeChecks)
	}

	// A store that ignores If-Match on a delete passes two checks and fails the
	// third, and the probe stops there.
	_, b = probeStore(t, func(r *http.Request) (bool, int) {
		return r.Method == http.MethodDelete && r.Header.Get("If-Match") == staleTag, 0
	})
	got = nil
	var failed error
	err := b.ProbeSteps(context.Background(), func(check string, err error) {
		got = append(got, check)
		failed = err
	})
	if err == nil || failed == nil || len(got) != 3 || got[2] != ProbeChecks[2] {
		t.Errorf("reports %q, last error %v, probe error %v; want three reports and the third failed", got, failed, err)
	}
}
