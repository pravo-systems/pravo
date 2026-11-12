package main

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"maps"
	"net/http/httptest"
	"runtime"
	"slices"
	"strings"
	"testing"

	"cuelabs.dev/go/oci/ociregistry/ociclient"
	"cuelabs.dev/go/oci/ociregistry/ocimem"
	"cuelabs.dev/go/oci/ociregistry/ociserver"
	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

// fakeProviderZip is a provider archive that holds a program of the name pravo looks for.
func fakeProviderZip(t *testing.T, typ, ver string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	name := "terraform-provider-" + typ + "_v" + ver
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	hdr := &zip.FileHeader{Name: name, Method: zip.Deflate}
	hdr.SetMode(0o755)
	w, err := zw.CreateHeader(hdr)
	if err != nil {
		t.Fatal(err)
	}
	w.Write([]byte("#!/bin/sh\n# " + typ + " " + ver + "\n"))
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// ociRegistry serves an in-memory container registry and returns its host.
func ociRegistry(t *testing.T) string {
	t.Helper()
	srv := httptest.NewServer(ociserver.New(ocimem.New(), nil))
	t.Cleanup(srv.Close)
	return strings.TrimPrefix(srv.URL, "http://")
}

// pushFakeProvider publishes a provider version to repo of the registry at host the way
// "pravo providers copy" does, for each platform given as os_arch, and returns the sha256 of
// the archive of each.
func pushFakeProvider(t *testing.T, host, repo, typ, ver string, platforms ...string) map[string]string {
	t.Helper()
	archives := map[string][]byte{}
	for _, p := range platforms {
		// Each platform gets its own bytes, so that its hash differs.
		archives[p] = append(fakeProviderZip(t, typ, ver), []byte(p)...)
	}
	return pushProviderArchives(t, host, repo, ver, archives)
}

// pushProviderArchives publishes the given archives, by os_arch, as repo:ver, and returns the
// sha256 of each as "sha256:HEX".
func pushProviderArchives(t *testing.T, host, repo, ver string, archives map[string][]byte) map[string]string {
	t.Helper()
	ctx := context.Background()
	cl, err := ociclient.New(host, &ociclient.Options{Insecure: true})
	if err != nil {
		t.Fatal(err)
	}
	empty := ocispec.Descriptor{MediaType: "application/vnd.oci.empty.v1+json", Digest: digest.FromBytes([]byte("{}")), Size: 2, Data: []byte("{}")}
	if _, err := cl.PushBlob(ctx, repo, empty, bytes.NewReader([]byte("{}"))); err != nil {
		t.Fatal(err)
	}
	idx := ocispec.Index{MediaType: ocispec.MediaTypeImageIndex, ArtifactType: "application/vnd.pravo.provider.v1"}
	idx.SchemaVersion = 2
	sums := map[string]string{}
	for _, p := range slices.Sorted(maps.Keys(archives)) {
		data := archives[p]
		sum := sha256.Sum256(data)
		layer := ocispec.Descriptor{MediaType: "application/vnd.pravo.provider.layer.v1.zip", Digest: digest.NewDigestFromEncoded(digest.SHA256, hex.EncodeToString(sum[:])), Size: int64(len(data))}
		if _, err := cl.PushBlob(ctx, repo, layer, bytes.NewReader(data)); err != nil {
			t.Fatal(err)
		}
		m := ocispec.Manifest{MediaType: ocispec.MediaTypeImageManifest, ArtifactType: "application/vnd.pravo.provider.v1", Config: empty, Layers: []ocispec.Descriptor{layer}}
		m.SchemaVersion = 2
		mb, _ := json.Marshal(m)
		md, err := cl.PushManifest(ctx, repo, "", mb, ocispec.MediaTypeImageManifest)
		if err != nil {
			t.Fatal(err)
		}
		os, arch, _ := strings.Cut(p, "_")
		md.Platform = &ocispec.Platform{OS: os, Architecture: arch}
		idx.Manifests = append(idx.Manifests, md)
		sums[p] = "sha256:" + hex.EncodeToString(sum[:])
	}
	ib, _ := json.Marshal(idx)
	if _, err := cl.PushManifest(ctx, repo, ver, ib, ocispec.MediaTypeImageIndex); err != nil {
		t.Fatal(err)
	}
	return sums
}
