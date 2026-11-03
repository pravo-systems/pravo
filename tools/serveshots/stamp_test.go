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

var tourShotRe = regexp.MustCompile(`(?m)^\s*shot (\S+) "\$theme" - - - - 2160 1350 (\w+)$`)

// The five pages of the tour are one session: the same estate, so the same
// identity, badges and records on every page. The script takes each from a
// shot, and the five are shots of one scenario, and of a situation of it.
func TestTourIsOneSession(t *testing.T) {
	root := repoRoot(t)
	b, err := os.ReadFile(filepath.Join(root, landingDir, "build.sh"))
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]Shot{}
	for _, s := range allShots() {
		byName[s.Name] = s
	}
	seen := 0
	scenarios := map[string]bool{}
	for _, m := range tourShotRe.FindAllStringSubmatch(string(b), -1) {
		s, ok := byName[m[1]+"-light"]
		if !ok {
			t.Errorf("build.sh makes the picture %s from the shot %s, which does not exist", m[2], m[1])
			continue
		}
		seen++
		scenarios[s.Scenario+"/"+s.Situation] = true
	}
	if seen != 5 {
		t.Errorf("build.sh makes %d pictures of the tour, want 5", seen)
	}
	if len(scenarios) != 1 {
		t.Errorf("the tour shows more than one estate: %v; every page must be of one scenario and situation", scenarios)
	}
}
