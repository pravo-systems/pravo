package main

import (
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestScenariosAreEmbedded(t *testing.T) {
	for _, sc := range scenarios {
		root := path.Join("scenarios", sc.Name)
		for _, f := range []string{"cue.mod/module.cue", "main.cue"} {
			if _, err := fs.Stat(scenarioFS, path.Join(root, f)); err != nil {
				t.Errorf("%s: %v", sc.Name, err)
			}
		}
		err := fs.WalkDir(scenarioFS, root, func(p string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return err
			}
			rel := strings.TrimPrefix(p, root+"/")
			switch {
			case strings.HasPrefix(rel, "cue.mod/gen/"), rel == "pravo.lock.cue", strings.HasPrefix(rel, "out/"), strings.HasPrefix(rel, ".pravo"):
				t.Errorf("%s: %s is generated and does not belong in the scenario", sc.Name, rel)
			}
			return nil
		})
		if err != nil {
			t.Error(err)
		}
	}
}

func TestEveryScenarioDirectoryIsListed(t *testing.T) {
	entries, err := fs.ReadDir(scenarioFS, "scenarios")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if _, ok := findScenario(e.Name()); !ok {
			t.Errorf("scenarios/%s is not in the list of scenarios", e.Name())
		}
	}
	if len(entries) != len(scenarios) {
		t.Errorf("%d directories, %d scenarios", len(entries), len(scenarios))
	}
}

func TestCopyScenario(t *testing.T) {
	dir := t.TempDir()
	if err := copyScenario("platform", dir); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"cue.mod/module.cue", "main.cue", "api.cue", "edge.cue", "network.cue", "kit/kit.cue"} {
		if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(f))); err != nil {
			t.Errorf("platform: %v", err)
		}
	}
	if err := copyScenario("nowhere", t.TempDir()); err == nil {
		t.Error("an unknown scenario: no error")
	}
}

// The edits of every scenario apply to its files: each text to replace occurs
// exactly once, and the result still has the resources that the shots select.
func TestScenarioEditsApply(t *testing.T) {
	for _, sc := range scenarios {
		if len(sc.Edits) == 0 {
			continue
		}
		dir := t.TempDir()
		if err := copyScenario(sc.Name, dir); err != nil {
			t.Fatal(err)
		}
		if err := applyEdits(dir, sc.Edits); err != nil {
			t.Errorf("%s: %v", sc.Name, err)
		}
	}
}

// The edits of each stored plan apply to a fresh copy of the scenario, the actors
// are distinct, and two of the plans that can be applied change the same thing
// (the port of orders), which is what the picture of an overlap needs.
func TestStoredPlanEditsApply(t *testing.T) {
	for _, sc := range scenarios {
		actors := map[string]bool{}
		for _, sp := range sc.Stored {
			if actors[sp.Actor] || sp.Actor == "" || sp.Reason == "" || len(sp.Edits) == 0 {
				t.Errorf("%s: the stored plan of %q is not a plan: %+v", sc.Name, sp.Actor, sp)
			}
			actors[sp.Actor] = true
			dir := t.TempDir()
			if err := copyScenario(sc.Name, dir); err != nil {
				t.Fatal(err)
			}
			if err := applyEdits(dir, sp.Edits); err != nil {
				t.Errorf("%s, %s: %v", sc.Name, sp.Actor, err)
			}
			if (sp.Age > 7*24*time.Hour) != sp.Expired {
				t.Errorf("%s, %s: age %s and expired %v disagree with a time to live of 7 days", sc.Name, sp.Actor, sp.Age, sp.Expired)
			}
		}
	}
	sc, _ := findScenario("platform")
	ports := 0
	for _, sp := range sc.Stored {
		if !sp.Expired {
			for _, e := range sp.Edits {
				if e.Old == "port:     8082" {
					ports++
				}
			}
		}
	}
	if ports != 2 {
		t.Errorf("%d plans that can be applied change the port of orders, want 2", ports)
	}
}

func TestPlatformEditsMakeTheSituation(t *testing.T) {
	sc, _ := findScenario("platform")
	dir := t.TempDir()
	if err := copyScenario("platform", dir); err != nil {
		t.Fatal(err)
	}
	if err := applyEdits(dir, sc.Edits); err != nil {
		t.Fatal(err)
	}
	api := readFile(t, filepath.Join(dir, "api.cue"))
	edge := readFile(t, filepath.Join(dir, "edge.cue"))
	if !strings.Contains(api, "port:     9082") || strings.Contains(api, "8082") {
		t.Error("the port of orders did not change")
	}
	if !strings.Contains(edge, "waf: rules:") {
		t.Error("the resource that is only in the specification is not added")
	}
	if strings.Contains(edge, "reload:") {
		t.Error("the resource that is only in the state is not removed")
	}
	if strings.Contains(edge, "\n\n\n") {
		t.Error("the removal left a gap")
	}
}

func TestEditApply(t *testing.T) {
	got, err := edit{File: "f", Old: "b", New: "B"}.apply("abc")
	if err != nil || got != "aBc" {
		t.Errorf("replace: %q, %v", got, err)
	}
	got, err = edit{File: "f", New: "!"}.apply("abc")
	if err != nil || got != "abc!" {
		t.Errorf("append: %q, %v", got, err)
	}
	if _, err := (edit{File: "f", Old: "x", New: "y"}).apply("abc"); err == nil {
		t.Error("a missing text: no error")
	}
	if _, err := (edit{File: "f", Old: "a", New: "y"}).apply("aba"); err == nil {
		t.Error("a text that occurs twice: no error")
	}
}

func TestApplyDisk(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "a.txt"), "one\n")
	write(t, filepath.Join(dir, "b.txt"), "two\n")
	err := applyDisk(dir, []diskChange{{Path: "a.txt", Append: "more\n"}, {Path: "b.txt", Remove: true}})
	if err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, filepath.Join(dir, "a.txt")); got != "one\nmore\n" {
		t.Errorf("a.txt = %q", got)
	}
	if _, err := os.Stat(filepath.Join(dir, "b.txt")); !os.IsNotExist(err) {
		t.Error("b.txt still exists")
	}
	if err := applyDisk(dir, []diskChange{{Path: "missing.txt", Append: "x"}}); err == nil {
		t.Error("a missing file: no error")
	}
	if err := applyDisk(dir, []diskChange{{Path: "missing.txt", Remove: true}}); err == nil {
		t.Error("removing a missing file: no error")
	}
}

func write(t *testing.T, p, s string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(s), 0o644); err != nil {
		t.Fatal(err)
	}
}

func readFile(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
