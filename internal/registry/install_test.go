package registry

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func providerZip(t *testing.T, body string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, err := zw.Create("terraform-provider-ramdom_v1.0.0")
	if err != nil {
		t.Fatal(err)
	}
	w.Write([]byte(body))
	zw.Close()
	return buf.Bytes()
}

func installServer(t *testing.T, z []byte) (*Client, Source) {
	t.Helper()
	sum := sha256.Sum256(z)
	var base string
	cl, src := registryServer(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, ".zip") {
			w.Write(z)
			return
		}
		fmt.Fprintf(w, `{"filename":"p.zip","download_url":%q,"shasum":%q}`, base+"/v1/providers/p.zip", hex.EncodeToString(sum[:]))
	})
	base = "https://" + src.Host
	return cl, src
}

func TestInstallLeavesAFinishedDirectory(t *testing.T) {
	cl, src := installServer(t, providerZip(t, "binary-content"))
	res, err := cl.Install(context.Background(), src, "1.0.0", nil)
	if err != nil {
		t.Fatal(err)
	}
	if p, ok := cl.FindInstalled(src, "1.0.0"); !ok || p != res.Path {
		t.Fatalf("FindInstalled = %q, %v; want %q", p, ok, res.Path)
	}
	entries, _ := os.ReadDir(filepath.Dir(cl.ProviderDir(src, "1.0.0")))
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".install-") {
			t.Errorf("staging directory %s left behind", e.Name())
		}
	}
}

// A binary cut short by a killed unpack (or one from before installs were
// atomic) is not accepted; Install puts a whole one in its place.
func TestInstallReplacesACutBinary(t *testing.T) {
	cl, src := installServer(t, providerZip(t, "binary-content"))
	res, err := cl.Install(context.Background(), src, "1.0.0", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(res.Path, []byte("binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, ok := cl.FindInstalled(src, "1.0.0"); ok {
		t.Fatal("a cut binary was accepted")
	}
	// No marker at all: the leftover of an old, non-atomic unpack.
	os.Remove(filepath.Join(filepath.Dir(res.Path), completeMarker))
	if _, ok := cl.FindInstalled(src, "1.0.0"); ok {
		t.Fatal("a binary without the marker was accepted")
	}
	res, err = cl.Install(context.Background(), src, "1.0.0", nil)
	if err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(res.Path); string(b) != "binary-content" {
		t.Fatalf("binary after reinstall = %q", b)
	}
	if _, ok := cl.FindInstalled(src, "1.0.0"); !ok {
		t.Fatal("reinstalled binary not accepted")
	}
}

func TestConcurrentInstallsAllSucceed(t *testing.T) {
	cl, src := installServer(t, providerZip(t, strings.Repeat("x", 1<<16)))
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			res, err := cl.Install(context.Background(), src, "1.0.0", nil)
			if err == nil {
				if _, ok := cl.FindInstalled(src, "1.0.0"); !ok || res.Path == "" {
					err = fmt.Errorf("installed but not found")
				}
			}
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Error(err)
		}
	}
}
