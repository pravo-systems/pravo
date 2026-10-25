package main

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pravo-systems/pravo/internal/config"
	"github.com/pravo-systems/pravo/internal/fakehttp"
	"github.com/pravo-systems/pravo/internal/fakes3"
	"github.com/pravo-systems/pravo/internal/state"
	"github.com/pravo-systems/pravo/internal/ui"
)

func probeRoot(t *testing.T, ignore bool) (*config.Root, state.Backend) {
	t.Helper()
	f := fakes3.New()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if ignore {
			r.Header.Del("If-None-Match")
			r.Header.Del("If-Match")
		}
		f.ServeHTTP(w, r)
	}))
	t.Cleanup(srv.Close)
	root := &config.Root{}
	root.State.Backend, root.State.Bucket, root.State.Endpoint, root.State.Region = "s3", "state", srv.URL, "us-east-1"
	root.State.AccessKey, root.State.SecretKey = "test", "test"
	b, err := remoteBackend(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	return root, b
}

func TestCheckS3Store(t *testing.T) {
	home := t.TempDir()
	t.Setenv("PRAVO_HOME", home)
	t.Setenv("PRAVO_S3_PROBE", "")
	ctx := context.Background()

	root, b := probeRoot(t, false)
	if err := checkStoreConditions(ctx, root, b); err != nil {
		t.Fatalf("a store that honours the conditions: %v", err)
	}
	marks, _ := filepath.Glob(filepath.Join(home, "s3-probe", "*"))
	if len(marks) != 1 {
		t.Fatalf("the verdict is remembered in %d files, want 1", len(marks))
	}

	root, b = probeRoot(t, true)
	err := checkStoreConditions(ctx, root, b)
	if err == nil || !strings.Contains(err.Error(), "If-None-Match is ignored") || !strings.Contains(err.Error(), "PRAVO_S3_PROBE=off") {
		t.Fatalf("a store that ignores a condition: %v", err)
	}
	marks, _ = filepath.Glob(filepath.Join(home, "s3-probe", "*"))
	if len(marks) != 1 {
		t.Errorf("a refused store was remembered: %d files", len(marks))
	}

	t.Setenv("PRAVO_S3_PROBE", "off")
	if err := checkStoreConditions(ctx, root, b); err != nil {
		t.Errorf("PRAVO_S3_PROBE=off still probes: %v", err)
	}
	if _, err := os.Stat(home); err != nil {
		t.Fatal(err)
	}
}

func TestCheckHTTPStore(t *testing.T) {
	t.Setenv("PRAVO_HOME", t.TempDir())
	t.Setenv("PRAVO_S3_PROBE", "")
	t.Setenv("PRAVO_STATE_TOKEN", "from-env")
	ctx := context.Background()
	f := fakehttp.New()
	f.Token = "from-env"
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	root := &config.Root{}
	root.State.Backend, root.State.URL = "http", srv.URL+"/team"
	b, err := remoteBackend(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	if err := checkStoreConditions(ctx, root, b); err != nil {
		t.Fatalf("a server that honours the conditions: %v", err)
	}
	f.IgnoreConditions = true
	t.Setenv("PRAVO_HOME", t.TempDir())
	err = checkStoreConditions(ctx, root, b)
	if err == nil || !strings.Contains(err.Error(), "ignored") {
		t.Fatalf("a server that ignores a condition: %v", err)
	}
}

func TestProbeS3PrintsEachCheck(t *testing.T) {
	t.Setenv("PRAVO_HOME", t.TempDir())
	var buf bytes.Buffer
	prev := out
	out = ui.NewPrinter(&buf, true)
	t.Cleanup(func() { out = prev })

	root, _ := probeRoot(t, false)
	if err := probeS3(context.Background(), root); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"create only (If-None-Match)", "ok", "concurrent creates", "honours the conditional requests"} {
		if !strings.Contains(buf.String(), want) {
			t.Errorf("output lacks %q:\n%s", want, buf.String())
		}
	}
	if _, err := os.Stat(s3ProbeMark(root)); err != nil {
		t.Errorf("a store that passed is not remembered: %v", err)
	}

	buf.Reset()
	root, _ = probeRoot(t, true)
	if err := probeS3(context.Background(), root); err == nil || !strings.Contains(buf.String(), "FAILED") {
		t.Errorf("a store that ignores a condition: error %v, output:\n%s", err, buf.String())
	}
	local := &config.Root{}
	if err := probeS3(context.Background(), local); err == nil {
		t.Error("a local state was probed")
	}
}
