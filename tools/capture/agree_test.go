package main

import (
	"encoding/json"
	"html"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// TestAgreesWithSitegen guards the one contract the two tools share: the
// Markdown grammar of AUTHORING.md section 4. Every command that capture runs
// and shows must appear, in the same order, as a terminal line on the page
// that tools/sitegen writes, and sitegen must not show a command that capture
// never runs. The corpus puts run blocks in every container and part, with
// every spelling of the info string.
func TestAgreesWithSitegen(t *testing.T) {
	if testing.Short() {
		t.Skip("runs go run ../sitegen")
	}
	corpus := filepath.Join("testdata", "agree", "docs")
	page := filepath.Join(corpus, "learn", "a01-blocks.md")

	// What capture runs.
	jsonOut := filepath.Join(t.TempDir(), "run.json")
	code, out, errb := capture(t, "-check", "-bin", fakeBin(t), "-json", jsonOut, page)
	if code != 0 {
		t.Fatalf("capture exit %d\n%s%s", code, out, errb)
	}
	raw, err := os.ReadFile(jsonOut)
	if err != nil {
		t.Fatal(err)
	}
	var rec struct {
		Blocks []struct {
			Command string `json:"command"`
			Hidden  bool   `json:"hidden"`
			Check   bool   `json:"check"`
		} `json:"blocks"`
	}
	if err := json.Unmarshal(raw, &rec); err != nil {
		t.Fatal(err)
	}
	var ran []string
	for _, b := range rec.Blocks {
		if b.Hidden || b.Check {
			continue
		}
		ran = append(ran, lines(b.Command)...)
	}

	// What sitegen shows.
	site := t.TempDir()
	if err := os.MkdirAll(filepath.Join(site, "assets"), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := `{"name":"pravo","title":"pravo","description":"x","pravo_version":"0.1","cue_version":"0.17","start_href":"learn/index.html","sections":[{"id":"learn","title":"Learn","blurb":"x"}]}`
	if err := os.WriteFile(filepath.Join(site, "site.json"), []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	assets, err := os.ReadDir(filepath.Join("..", "..", "site", "assets"))
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range assets {
		if a.IsDir() {
			continue
		}
		data, err := os.ReadFile(filepath.Join("..", "..", "site", "assets", a.Name()))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(site, "assets", a.Name()), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	outDir := t.TempDir()
	cmd := exec.Command("go", "run", "../sitegen", "-docs", corpus, "-site", site, "-out", outDir, "-readme", filepath.Join(outDir, "none"), "-lint=false")
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("sitegen: %v\n%s", err, b)
	}
	htmlPage, err := os.ReadFile(filepath.Join(outDir, "learn", "a01-blocks.html"))
	if err != nil {
		t.Fatal(err)
	}
	var shown []string
	for _, m := range regexp.MustCompile(`(?s)<span class="cmd-text">(.*?)</span>`).FindAllStringSubmatch(string(htmlPage), -1) {
		shown = append(shown, lines(html.UnescapeString(m[1]))...)
	}

	if strings.Join(ran, "\n") != strings.Join(shown, "\n") {
		t.Errorf("capture and sitegen disagree about the commands of %s\n--- capture runs and shows\n%s\n--- sitegen shows\n%s",
			page, strings.Join(ran, "\n"), strings.Join(shown, "\n"))
	}
	if len(ran) < 15 {
		t.Errorf("the corpus ran only %d command lines; it should cover every container", len(ran))
	}
}
