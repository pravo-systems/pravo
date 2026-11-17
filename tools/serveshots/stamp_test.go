package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

var landingShotRe = regexp.MustCompile(`src="(assets/shots/[A-Za-z0-9._-]+\.webp)"`)

// The digest does not depend on where the checkout is and changes with the
// files that the viewer serves.
func TestViewerDigestFollowsTheFiles(t *testing.T) {
	dir := t.TempDir()
	web := filepath.Join(dir, viewerDir)
	if err := os.MkdirAll(filepath.Join(web, "assets"), 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(name, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(web, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("index.html", "a")
	write("assets/app.js", "b")
	one, err := viewerDigest(dir)
	if err != nil {
		t.Fatal(err)
	}
	again, _ := viewerDigest(dir)
	if one != again {
		t.Errorf("the digest changed with nothing: %s, %s", one, again)
	}
	write("assets/app.js", "c")
	if two, _ := viewerDigest(dir); two == one {
		t.Error("the digest did not change with a file of the viewer")
	}
}

// Every picture that a landing band names is made by build.sh, since the
// pictures are not stored.
func TestLandingPicturesAreMade(t *testing.T) {
	root := repoRoot(t)
	pages, err := filepath.Glob(filepath.Join(root, "site", "landing", "*.html"))
	if err != nil {
		t.Fatal(err)
	}
	script, err := os.ReadFile(filepath.Join(root, landingDir, "build.sh"))
	if err != nil {
		t.Fatal(err)
	}
	for _, page := range pages {
		b, err := os.ReadFile(page)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range landingShotRe.FindAllStringSubmatch(string(b), -1) {
			// build.sh names a picture by its name and the theme: "graph" and
			// "$theme" make graph-dark.webp and graph-light.webp.
			name := strings.TrimSuffix(strings.TrimPrefix(m[1], "assets/shots/"), ".webp")
			base := strings.TrimSuffix(strings.TrimSuffix(name, "-dark"), "-light")
			if !strings.Contains(string(script), " "+base+"\n") {
				t.Errorf("%s: no picture %s that build.sh makes", filepath.Base(page), m[1])
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
