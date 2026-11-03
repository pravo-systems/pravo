package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

var landingShotRe = regexp.MustCompile(`src="(assets/shots/[A-Za-z0-9._-]+\.webp)"`)

const remake = `

The interface of the viewer (internal/serve/web) changed after the pictures were made. Make them again:
	go build -o bin/pravo ./cmd/pravo
	go run ./tools/serveshots -bin bin/pravo -out docs/img/serve
	sh site/demos/views/build.sh
and look at the callouts of site/landing/12-views.html, which point at places in the pictures.`

func checkStamp(t *testing.T, stamp, what string) {
	t.Helper()
	root := repoRoot(t)
	want, err := viewerDigest(root)
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(root, stamp))
	if err != nil {
		t.Fatalf("%v%s", err, remake)
	}
	if got := strings.TrimSpace(string(b)); got != want {
		t.Errorf("%s is stale: %s records %.12s, the viewer is %.12s.%s", what, stamp, got, want, remake)
	}
}

// The documentation pictures were made from the interface in the checkout.
func TestDocumentationPicturesAgreeWithTheViewer(t *testing.T) {
	checkStamp(t, stampFile, "docs/img/serve")
}

// The pictures of the landing page, and the callouts on them, were made from
// the same interface: the script that writes them copies the stamp.
func TestLandingPicturesAgreeWithTheViewer(t *testing.T) {
	checkStamp(t, landingDir+"/viewer.stamp", "site/assets/shots")
}

// Every picture that a landing band names exists.
func TestLandingPicturesExist(t *testing.T) {
	root := repoRoot(t)
	pages, err := filepath.Glob(filepath.Join(root, "site", "landing", "*.html"))
	if err != nil {
		t.Fatal(err)
	}
	for _, page := range pages {
		b, err := os.ReadFile(page)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range landingShotRe.FindAllStringSubmatch(string(b), -1) {
			if _, err := os.Stat(filepath.Join(root, "site", m[1])); err != nil {
				t.Errorf("%s: %v", filepath.Base(page), err)
			}
		}
	}
}
