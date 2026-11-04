package registry

import (
	"archive/zip"
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func zipOf(t *testing.T, entries map[string]os.FileMode, order []string) string {
	t.Helper()
	var b bytes.Buffer
	zw := zip.NewWriter(&b)
	for _, n := range order {
		h := &zip.FileHeader{Name: n, Method: zip.Deflate}
		h.SetMode(entries[n])
		w, err := zw.CreateHeader(h)
		if err != nil {
			t.Fatal(err)
		}
		w.Write([]byte("data"))
	}
	zw.Close()
	p := filepath.Join(t.TempDir(), "a.zip")
	os.WriteFile(p, b.Bytes(), 0o644)
	return p
}

func TestUnzipRefusesWhatIsNotAFile(t *testing.T) {
	for name, mode := range map[string]os.FileMode{"link": os.ModeSymlink | 0o777, "pipe": os.ModeNamedPipe | 0o644} {
		dir := t.TempDir()
		p := zipOf(t, map[string]os.FileMode{name: mode}, []string{name})
		err := unzip(p, dir)
		if err == nil || !strings.Contains(err.Error(), "refusing") {
			t.Errorf("%s entry: err = %v, want a refusal", name, err)
		}
	}
}

func TestUnzipRefusesNamesOutsideTheDirectory(t *testing.T) {
	for _, n := range []string{"../x", "a/../../x", "/tmp/x", ""} {
		dir := t.TempDir()
		p := zipOf(t, map[string]os.FileMode{n: 0o644}, []string{n})
		if err := unzip(p, dir); err == nil {
			t.Errorf("%q accepted", n)
		}
	}
}

func TestUnzipLimits(t *testing.T) {
	defer func(a, b int64, c int) { maxUnzippedFile, maxUnzippedTotal, maxZipEntries = a, b, c }(maxUnzippedFile, maxUnzippedTotal, maxZipEntries)
	maxUnzippedFile, maxUnzippedTotal, maxZipEntries = 3, 6, 2
	// one file over the cap is an error, not a silently shortened file
	p := zipOf(t, map[string]os.FileMode{"a": 0o644}, []string{"a"})
	if err := unzip(p, t.TempDir()); err == nil || !strings.Contains(err.Error(), "unpacks to more than") {
		t.Errorf("file cap: err = %v", err)
	}
	maxUnzippedFile = 4
	p = zipOf(t, map[string]os.FileMode{"a": 0o644, "b": 0o644}, []string{"a", "b"})
	if err := unzip(p, t.TempDir()); err == nil || !strings.Contains(err.Error(), "the archive unpacks to more than") {
		t.Errorf("total cap: err = %v", err)
	}
	p = zipOf(t, map[string]os.FileMode{"a": 0o644, "b": 0o644, "c": 0o644}, []string{"a", "b", "c"})
	if err := unzip(p, t.TempDir()); err == nil || !strings.Contains(err.Error(), "entries") {
		t.Errorf("entry cap: err = %v", err)
	}
}

func TestCleanRemovesControlCharacters(t *testing.T) {
	got := clean("a\x1b[2Jb\n::error::x\r\x00\u0085z")
	if strings.ContainsAny(got, "\x1b\n\r\x00\u0085") {
		t.Errorf("clean = %q", got)
	}
}

// archiveServer serves a registry whose answers a test shapes.
func archiveServer(t *testing.T, info func(dl map[string]any), archive http.HandlerFunc) (*Client, Source) {
	t.Helper()
	var srv *httptest.Server
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/terraform.json", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"providers.v1": "/v1/providers/"}`)
	})
	mux.HandleFunc("/v1/providers/", func(w http.ResponseWriter, r *http.Request) {
		dl := map[string]any{"protocols": []string{"6.0"}, "os": CurrentPlatform().OS, "arch": CurrentPlatform().Arch, "filename": "f.zip", "download_url": srv.URL + "/f.zip", "shasum": strings.Repeat("0", 64)}
		info(dl)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, jsonOf(dl))
	})
	mux.HandleFunc("/f.zip", archive)
	srv = httptest.NewTLSServer(mux)
	t.Cleanup(srv.Close)
	return &Client{HTTP: srv.Client(), Home: t.TempDir()}, Source{Host: strings.TrimPrefix(srv.URL, "https://"), Namespace: "a", Type: "b"}
}

func jsonOf(m map[string]any) string {
	var parts []string
	for k, v := range m {
		switch x := v.(type) {
		case string:
			parts = append(parts, fmt.Sprintf("%q:%q", k, x))
		case []string:
			parts = append(parts, fmt.Sprintf("%q:[%q]", k, strings.Join(x, `","`)))
		}
	}
	return "{" + strings.Join(parts, ",") + "}"
}

func TestArchiveRefusesAnotherPlatform(t *testing.T) {
	c, src := archiveServer(t, func(dl map[string]any) { dl["os"] = "plan9" }, func(w http.ResponseWriter, r *http.Request) { t.Error("downloaded") })
	_, _, err := c.Archive(context.Background(), src, "1.0.0", CurrentPlatform(), t.TempDir(), nil)
	if err == nil || !strings.Contains(err.Error(), "plan9") {
		t.Errorf("err = %v", err)
	}
}

func TestArchiveRefusesAnotherProtocol(t *testing.T) {
	c, src := archiveServer(t, func(dl map[string]any) { dl["protocols"] = []string{"4.0"} }, func(w http.ResponseWriter, r *http.Request) { t.Error("downloaded") })
	_, _, err := c.Archive(context.Background(), src, "1.0.0", CurrentPlatform(), t.TempDir(), nil)
	if err == nil || !strings.Contains(err.Error(), "protocol") {
		t.Errorf("err = %v", err)
	}
}

func TestArchiveStopsAtTheSizeCap(t *testing.T) {
	defer func(a int64) { maxArchive = a }(maxArchive)
	maxArchive = 1 << 20
	c, src := archiveServer(t, func(dl map[string]any) {}, func(w http.ResponseWriter, r *http.Request) {
		for i := 0; i < 64; i++ {
			if _, err := w.Write(make([]byte, 1<<20)); err != nil {
				return
			}
		}
	})
	dir := t.TempDir()
	_, _, err := c.Archive(context.Background(), src, "1.0.0", CurrentPlatform(), dir, nil)
	if err == nil || !strings.Contains(err.Error(), "larger than") {
		t.Errorf("err = %v", err)
	}
	if left, _ := filepath.Glob(filepath.Join(dir, ".download-*")); len(left) > 0 {
		t.Errorf("left %v", left)
	}
}

func TestArchiveGivesUpOnASilentRegistry(t *testing.T) {
	defer func(d time.Duration) { idleTimeout = d }(idleTimeout)
	idleTimeout = 300 * time.Millisecond
	c, src := archiveServer(t, func(dl map[string]any) {}, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("x"))
		w.(http.Flusher).Flush()
		select {
		case <-r.Context().Done():
		case <-time.After(10 * time.Second):
		}
	})
	start := time.Now()
	_, _, err := c.Archive(context.Background(), src, "1.0.0", CurrentPlatform(), t.TempDir(), nil)
	if err == nil || time.Since(start) > 5*time.Second {
		t.Errorf("err = %v after %s", err, time.Since(start))
	}
}

func TestArchiveCleansTheFilename(t *testing.T) {
	c, src := archiveServer(t, func(dl map[string]any) { dl["filename"] = "evil\x1b[2J\n::error::x" }, func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("zip")) })
	var shown []string
	_, _, err := c.Archive(context.Background(), src, "1.0.0", CurrentPlatform(), t.TempDir(), func(m string) { shown = append(shown, m) })
	all := strings.Join(shown, "\n")
	if err != nil {
		all += err.Error()
	}
	if strings.ContainsAny(all, "\x1b") || strings.Contains(all, "\n::error") {
		t.Errorf("registry text reached the output: %q", all)
	}
}

func TestArchiveRefusesPlainHTTPDownload(t *testing.T) {
	c, src := archiveServer(t, func(dl map[string]any) { dl["download_url"] = "http://registry.example/f.zip" }, func(w http.ResponseWriter, r *http.Request) {})
	_, _, err := c.Archive(context.Background(), src, "1.0.0", CurrentPlatform(), t.TempDir(), nil)
	if err == nil || !strings.Contains(err.Error(), "https") {
		t.Errorf("err = %v", err)
	}
}

func TestDiscoveryRefusesPlainHTTPService(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/terraform.json", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"providers.v1": "http://registry.example/v1/providers/"}`)
	})
	srv := httptest.NewTLSServer(mux)
	defer srv.Close()
	c := &Client{HTTP: srv.Client(), Home: t.TempDir()}
	_, err := c.Versions(context.Background(), Source{Host: strings.TrimPrefix(srv.URL, "https://"), Namespace: "a", Type: "b"})
	if err == nil || !strings.Contains(err.Error(), "https") {
		t.Errorf("err = %v", err)
	}
}

func TestRedirectFromHTTPSToHTTPIsRefused(t *testing.T) {
	plain := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("{}")) }))
	defer plain.Close()
	// a name that is not loopback, so the exemption does not apply
	target := strings.Replace(plain.URL, "127.0.0.1", "localhost.example", 1)
	tls := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, target, http.StatusFound) }))
	defer tls.Close()
	cl := newHTTPClient()
	cl.Transport = tls.Client().Transport
	_, err := cl.Get(tls.URL)
	if err == nil || !strings.Contains(err.Error(), "refusing a redirect") {
		t.Errorf("err = %v", err)
	}
}

func TestJSONAnswerIsBounded(t *testing.T) {
	defer func(a int64) { maxJSON = a }(maxJSON)
	maxJSON = 1 << 10
	c, src := registryServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"versions":[`))
		for i := 0; i < 1000; i++ {
			w.Write([]byte(`{"version":"1.0.0"},`))
		}
	})
	_, err := c.Versions(context.Background(), src)
	if err == nil || !strings.Contains(err.Error(), "larger than") {
		t.Errorf("err = %v", err)
	}
}

func TestRetryAfterWaitIsCapped(t *testing.T) {
	if maxRetryWait > 2*time.Minute {
		t.Errorf("maxRetryWait = %s", maxRetryWait)
	}
}
