package registry

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"

	"cuelabs.dev/go/oci/ociregistry/ocimem"
	"cuelabs.dev/go/oci/ociregistry/ociserver"

	"github.com/pravo-systems/pravo/internal/env"
)

func TestParseMirror(t *testing.T) {
	m, err := ParseMirror(" oci://123456789012.dkr.ecr.us-east-2.amazonaws.com/pravo-providers/ , hashicorp/aws=oci://localhost:5000/aws-copy/aws,registry.example.com/acme/x=oci://h/p/x")
	if err != nil {
		t.Fatal(err)
	}
	got, ok := m.For(Source{Host: DefaultHost, Namespace: "hashicorp", Type: "random"})
	if want := "oci://123456789012.dkr.ecr.us-east-2.amazonaws.com/pravo-providers/hashicorp/random"; !ok || got.String() != want {
		t.Errorf("prefix: %s, %v; want %s", got, ok, want)
	}
	if got, ok := m.For(Source{Host: DefaultHost, Namespace: "hashicorp", Type: "aws"}); !ok || got.String() != "oci://localhost:5000/aws-copy/aws" {
		t.Errorf("override: %s, %v", got, ok)
	}
	if got, ok := m.For(Source{Host: "registry.example.com", Namespace: "acme", Type: "x"}); !ok || got.String() != "oci://h/p/x" {
		t.Errorf("override of another host: %s, %v", got, ok)
	}
	// A provider that is in a container registry already is not moved.
	if _, ok := m.For(Source{Host: "ghcr.io", Namespace: "acme/providers", Type: "random", OCI: true}); ok {
		t.Error("a source in a container registry was moved")
	}
	if m.String() != "oci://123456789012.dkr.ecr.us-east-2.amazonaws.com/pravo-providers,registry.example.com/acme/x=oci://h/p/x,registry.opentofu.org/hashicorp/aws=oci://localhost:5000/aws-copy/aws" {
		t.Errorf("String = %s", m)
	}
	if m, err := ParseMirror(" , "); m != nil || err != nil {
		t.Errorf("an empty setting: %v, %v", m, err)
	}
	if m, err := ParseMirror("hashicorp/aws=oci://h/p/aws"); err != nil || m == nil {
		t.Errorf("an exception alone: %v, %v", m, err)
	} else if _, ok := m.For(Source{Host: DefaultHost, Namespace: "hashicorp", Type: "random"}); ok {
		t.Error("a source with no exception and no prefix was mirrored")
	}
	for _, bad := range []string{"registry.example.com/x", "oci://host", "oci://h/a,oci://h/b", "oci://h/p/x=oci://h/p/x", "hashicorp/aws=hashicorp/aws", "hashicorp/aws=oci://h/p", "hashicorp/aws=oci://h/p/aws,hashicorp/aws=oci://h/q/aws"} {
		if m, err := ParseMirror(bad); err == nil {
			t.Errorf("%q parsed as %v", bad, m)
		}
	}
}

// A client with a bad mirror says so when it is used, not when it is made.
func TestBadMirrorSettingIsReportedByTheFirstUse(t *testing.T) {
	c := NewClient(env.New([]string{"PRAVO_PROVIDER_MIRROR=not-an-address", "PRAVO_HOME=" + t.TempDir()}))
	_, err := c.Resolve(context.Background(), Source{Host: DefaultHost, Namespace: "hashicorp", Type: "random"}, "")
	if err == nil || !strings.Contains(err.Error(), "PRAVO_PROVIDER_MIRROR") {
		t.Fatalf("Resolve = %v", err)
	}
}

// refuseAll is a transport that fails the test for any request.
type refuseAll struct{ t *testing.T }

func (r refuseAll) RoundTrip(req *http.Request) (*http.Response, error) {
	r.t.Errorf("a request to the source: %s", req.URL)
	return nil, fmt.Errorf("the source is not reachable")
}

func TestMirrorFillsFromTheSourceAndServesWithoutIt(t *testing.T) {
	plats := []Platform{{OS: "linux", Arch: "amd64"}, {OS: "linux", Arch: "arm64"}}
	zips := map[string][]byte{}
	sums := map[Platform]string{}
	for _, p := range plats {
		var buf bytes.Buffer
		zw := zip.NewWriter(&buf)
		w, _ := zw.CreateHeader(&zip.FileHeader{Name: "terraform-provider-thing_v1.0.0", Method: zip.Deflate})
		w.Write([]byte("#!/bin/sh\n# " + p.String() + "\n"))
		zw.Close()
		zips[p.String()] = buf.Bytes()
		s := sha256.Sum256(buf.Bytes())
		sums[p] = hex.EncodeToString(s[:])
	}
	// The source: a provider registry.
	var hits atomic.Int32
	var base string
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/terraform.json", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"providers.v1": "/v1/providers/"}`))
	})
	mux.HandleFunc("/v1/providers/", func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		path := strings.TrimPrefix(r.URL.Path, "/v1/providers/")
		switch {
		case strings.HasSuffix(path, "/versions"):
			fmt.Fprint(w, `{"versions":[{"version":"1.0.0","protocols":["5.0"],"platforms":[{"os":"linux","arch":"amd64"},{"os":"linux","arch":"arm64"}]},{"version":"1.0.1","protocols":["5.0"],"platforms":[{"os":"linux","arch":"amd64"}]}]}`)
		case strings.Contains(path, "/download/"):
			parts := strings.Split(path, "/")
			p := parts[len(parts)-2] + "_" + parts[len(parts)-1]
			if parts[len(parts)-4] == "1.0.1" {
				zips[p] = append(append([]byte{}, zips[p]...), []byte("changed")...)
			}
			s := sha256.Sum256(zips[p])
			fmt.Fprintf(w, `{"filename":"p.zip","download_url":%q,"shasum":%q}`, base+"/v1/providers/"+p+".zip", hex.EncodeToString(s[:]))
		default:
			w.Write(zips[strings.TrimSuffix(path, ".zip")])
		}
	})
	pub := httptest.NewTLSServer(mux)
	defer pub.Close()
	base = pub.URL
	src := Source{Host: strings.TrimPrefix(pub.URL, "https://"), Namespace: "hashicorp", Type: "thing"}

	// The mirror: a container registry.
	oci := httptest.NewServer(ociserver.New(ocimem.New(), nil))
	defer oci.Close()
	ociHost := strings.TrimPrefix(oci.URL, "http://")
	prefix, err := ParseMirrorPrefix("oci://" + ociHost + "/pravo-providers")
	if err != nil {
		t.Fatal(err)
	}
	dst := prefix.Under(src)
	if dst.String() != "oci://"+ociHost+"/pravo-providers/hashicorp/thing" {
		t.Fatalf("the layout is %s", dst)
	}
	ctx := context.Background()
	filler := &Client{HTTP: pub.Client(), Home: t.TempDir(), Mirror: &Mirror{Prefix: prefix}}

	// The pins of a lock file.
	pins := map[Platform]string{plats[0]: sums[plats[0]], plats[1]: sums[plats[1]]}
	published, err := filler.MirrorVersion(ctx, src, dst, "1.0.0", pins, nil)
	if err != nil || !published {
		t.Fatalf("MirrorVersion = %v, %v", published, err)
	}
	// It reads the source, whatever mirror the client has.
	if hits.Load() == 0 {
		t.Fatal("the source was not read")
	}
	if published, err = filler.MirrorVersion(ctx, src, dst, "1.0.0", pins, nil); err != nil || published {
		t.Fatalf("a second run = %v, %v; want it to leave the published version", published, err)
	}
	// A published version that is not the pinned one is an error.
	bad := map[Platform]string{plats[0]: sums[plats[0]], plats[1]: strings.Repeat("0", 64)}
	if _, err := filler.MirrorVersion(ctx, src, dst, "1.0.0", bad, nil); err == nil || !strings.Contains(err.Error(), "the lock file pins") {
		t.Fatalf("a different pin for a published version = %v", err)
	}
	// An archive of the source that is not the pinned one is refused before anything is published.
	if _, err := filler.MirrorVersion(ctx, src, dst, "1.0.1", map[Platform]string{plats[0]: sums[plats[0]]}, nil); err == nil || !strings.Contains(err.Error(), "the lock file pins") {
		t.Fatalf("a changed archive = %v", err)
	}
	cl, err := ociClient(ociHost)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := cl.ResolveTag(ctx, dst.repo(), "1.0.1"); err == nil {
		t.Error("a version that was refused was published")
	}
	// A platform that the source does not offer, though the lock pins it, is an error too.
	if _, err := filler.MirrorVersion(ctx, src, dst, "1.0.1", map[Platform]string{plats[0]: sums[plats[0]], plats[1]: sums[plats[1]]}, nil); err == nil || !strings.Contains(err.Error(), "no build for linux_arm64") {
		t.Fatalf("a platform the source lacks = %v", err)
	}

	// A client with the mirror reads from the mirror and never asks the source.
	if runtime.GOOS != "linux" || (runtime.GOARCH != "amd64" && runtime.GOARCH != "arm64") {
		return
	}
	hits.Store(0)
	c := &Client{HTTP: &http.Client{Transport: refuseAll{t}}, Home: t.TempDir(), Mirror: &Mirror{Prefix: prefix}}
	ver, err := c.Resolve(ctx, src, "~> 1.0")
	if err != nil || ver != "1.0.0" {
		t.Fatalf("Resolve = %q, %v", ver, err)
	}
	res, err := c.Install(ctx, src, ver, nil)
	if err != nil {
		t.Fatal(err)
	}
	cur := CurrentPlatform()
	if res.Hash != "sha256:"+sums[cur] || res.Signer != "" || !res.Downloaded {
		t.Errorf("install result %+v, want the pinned hash %s and no signer", res, sums[cur])
	}
	if sum, err := c.Checksum(ctx, src, ver, cur); err != nil || sum != sums[cur] {
		t.Errorf("Checksum = %q, %v", sum, err)
	}
	// It is installed under the source's address, so that nothing else changes.
	if want := c.ProviderDir(src, ver); !strings.Contains(res.Path, want) {
		t.Errorf("installed at %s, want under %s", res.Path, want)
	}
	if h := hits.Load(); h != 0 {
		t.Errorf("the source was asked %d times", h)
	}
}
