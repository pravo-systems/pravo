package registry

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"cuelabs.dev/go/oci/ociregistry/ocimem"
	"cuelabs.dev/go/oci/ociregistry/ociserver"
)

func TestParseOCISource(t *testing.T) {
	s, err := ParseSource("oci://ghcr.io/acme/providers/aws")
	if err != nil {
		t.Fatal(err)
	}
	if !s.OCI || s.Host != "ghcr.io" || s.Namespace != "acme/providers" || s.Type != "aws" {
		t.Fatalf("parsed %+v", s)
	}
	if s.String() != "oci://ghcr.io/acme/providers/aws" {
		t.Fatalf("String() = %s", s.String())
	}
	if _, err := ParseSource("oci://ghcr.io/aws"); err == nil {
		t.Fatal("a source without a path should be rejected")
	}
}

// A provider copied from one container registry to another can be resolved
// and installed from the second, and a published version cannot be replaced.
func TestOCICopyResolveInstall(t *testing.T) {
	srv := httptest.NewServer(ociserver.New(ocimem.New(), nil))
	defer srv.Close()
	host := strings.TrimPrefix(srv.URL, "http://")

	// Seed the source repository by hand through Copy's own archive layout:
	// a zip holding the provider executable, pushed for the current platform.
	dir := t.TempDir()
	zp := filepath.Join(dir, "p.zip")
	f, _ := os.Create(zp)
	zw := zip.NewWriter(f)
	name := "terraform-provider-thing_v1.2.3"
	if runtime.GOOS == "windows" {
		name += ".exe" // a provider is a program only by its name there
	}
	hdr := &zip.FileHeader{Name: name, Method: zip.Deflate}
	hdr.SetMode(0o755)
	w, _ := zw.CreateHeader(hdr)
	w.Write([]byte("#!/bin/sh\n"))
	zw.Close()
	f.Close()

	c := &Client{HTTP: srv.Client(), Home: t.TempDir()}
	plat := CurrentPlatform()
	if err := pushTestArchive(t, host, "acme/thing", "1.2.3", zp, plat); err != nil {
		t.Fatal(err)
	}
	src := Source{Host: host, Namespace: "acme", Type: "thing", OCI: true}
	ctx := context.Background()

	ver, err := c.Resolve(ctx, src, "~> 1.2")
	if err != nil || ver != "1.2.3" {
		t.Fatalf("Resolve = %q, %v", ver, err)
	}
	if _, err := c.Resolve(ctx, src, ">= 2"); err == nil {
		t.Fatal("no version satisfies >= 2")
	}
	res, err := c.Install(ctx, src, ver, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(res.Hash, "sha256:") || !res.Downloaded {
		t.Fatalf("install result %+v", res)
	}
	if sum, err := c.Checksum(ctx, src, ver, plat); err != nil || "sha256:"+sum != res.Hash {
		t.Fatalf("Checksum = %q, %v; install hash %s", sum, err, res.Hash)
	}
	if _, err := os.Stat(res.Path); err != nil {
		t.Fatal(err)
	}

	dst := Source{Host: host, Namespace: "mirror/things", Type: "thing", OCI: true}
	if err := c.Copy(ctx, src, dst, "1.2.3", []Platform{plat}, nil); err != nil {
		t.Fatal(err)
	}
	if err := c.Copy(ctx, src, dst, "1.2.3", []Platform{plat}, nil); err == nil || !strings.Contains(err.Error(), "already has version") {
		t.Fatalf("second copy should be refused, got %v", err)
	}
	c2 := &Client{HTTP: srv.Client(), Home: t.TempDir()}
	if _, err := c2.Install(ctx, dst, "1.2.3", nil); err != nil {
		t.Fatal(err)
	}
}

// pushTestArchive publishes zp as repo:ver for one platform.
func pushTestArchive(t *testing.T, host, repo, ver, zp string, p Platform) error {
	t.Helper()
	i := strings.LastIndex(repo, "/")
	dst := Source{Host: host, Namespace: repo[:i], Type: repo[i+1:], OCI: true}
	cl, err := ociClient(host)
	if err != nil {
		return err
	}
	data, _ := os.ReadFile(zp)
	sum := sha256.Sum256(data)
	return pushVersion(context.Background(), cl, dst, ver, []archive{{plat: p, path: zp, sum: hex.EncodeToString(sum[:])}})
}
