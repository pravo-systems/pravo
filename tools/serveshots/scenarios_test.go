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

// A scenario without resources is initialised and not applied: it has stored
// plans or a team, which is what the viewer shows of it.
func TestScenariosWithoutResourcesHaveSomethingToShow(t *testing.T) {
	for _, sc := range scenarios {
		if sc.Resources == 0 && !sc.hasStored() {
			t.Errorf("%s has no resources and no stored plans", sc.Name)
		}
		if sc.Resources == 0 && (sc.hasTeam() || sc.hasChanges()) {
			t.Errorf("%s has no resources, and the team and the changed situations apply the scenario", sc.Name)
		}
	}
}

// Every scenario is a CUE root, or lays its files over one, or is the files
// of its base alone: the module and main.cue are in the first of its roots,
// and nothing generated is embedded.
func TestScenariosAreEmbedded(t *testing.T) {
	for _, sc := range scenarios {
		roots, err := sc.files()
		if err != nil {
			t.Errorf("%s: %v", sc.Name, err)
			continue
		}
		for _, f := range []string{"cue.mod/module.cue", "main.cue"} {
			if _, err := fs.Stat(scenarioFS, path.Join(roots[0], f)); err != nil {
				t.Errorf("%s: %v", sc.Name, err)
			}
		}
		own := path.Join("scenarios", sc.Name)
		if _, err := fs.Stat(scenarioFS, own); err != nil {
			if sc.Base == "" {
				t.Errorf("%s: no directory, and no base", sc.Name)
			}
			continue
		}
		if roots[len(roots)-1] != own {
			t.Errorf("%s: the roots %v do not end with its own directory", sc.Name, roots)
		}
		err = fs.WalkDir(scenarioFS, own, func(p string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return err
			}
			rel := strings.TrimPrefix(p, own+"/")
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
	withFiles := 0
	for _, sc := range scenarios {
		if _, err := fs.Stat(scenarioFS, path.Join("scenarios", sc.Name)); err == nil {
			withFiles++
		}
	}
	if len(entries) != withFiles {
		t.Errorf("%d directories, %d scenarios with files of their own", len(entries), withFiles)
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
	// The team lays its files over the platform: both sets are copied, and
	// the files of the team name the store and the provider by placeholders
	// that the program fills in.
	dir = t.TempDir()
	if err := copyScenario("team", dir); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"cue.mod/module.cue", "main.cue", "api.cue", "kit/kit.cue", "settings.cue", "policies.cue", "storage.cue"} {
		if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(f))); err != nil {
			t.Errorf("team: %v", err)
		}
	}
	if !strings.Contains(readFile(t, filepath.Join(dir, "settings.cue")), storePlaceholder) {
		t.Error("team: settings.cue does not name the store by its placeholder")
	}
	if !strings.Contains(readFile(t, filepath.Join(dir, "storage.cue")), providerPlaceholder) {
		t.Error("team: storage.cue does not name the provider by its placeholder")
	}
}

// Every catalog is objects with an id, a name and a region, and the files of
// a scenario with a catalog name the fake provider by its placeholder.
func TestCatalogsAreComplete(t *testing.T) {
	for _, sc := range scenarios {
		if !sc.hasCatalog() {
			continue
		}
		for _, o := range sc.Catalog {
			if o.ID == "" || o.Name == "" || o.Region == "" {
				t.Errorf("%s: the object %+v is incomplete", sc.Name, o)
			}
		}
		dir := t.TempDir()
		if err := copyScenario(sc.Name, dir); err != nil {
			t.Fatal(err)
		}
		lacks, err := lacksPlaceholder(dir, providerPlaceholder)
		if err != nil {
			t.Fatal(err)
		}
		if lacks {
			t.Errorf("%s: no file names the provider by %s", sc.Name, providerPlaceholder)
		}
	}
}

// Every team is people with roles, the first of them the admin; each step is
// run by one of them and approved by one of them, its edits apply to a fresh
// copy of the scenario, its label or its file is unique, and the edits made
// last apply after every step that stays.
func TestTeamSetupsAreConsistent(t *testing.T) {
	for _, sc := range scenarios {
		if sc.Team == nil {
			continue
		}
		t.Run(sc.Name, func(t *testing.T) { checkTeam(t, sc) })
	}
}

func checkTeam(t *testing.T, sc scenario) {
	t.Helper()
	team := sc.Team
	people := map[string]string{}
	for _, p := range team.People {
		if people[p.Name] != "" || p.Name == "" {
			t.Errorf("the person %q is listed twice or has no name", p.Name)
		}
		switch p.Role {
		case "admin", "writer", "reader":
		default:
			t.Errorf("%s has the role %q", p.Name, p.Role)
		}
		people[p.Name] = p.Role
	}
	if len(team.People) == 0 || team.People[0].Role != "admin" {
		t.Errorf("the people of the team: %+v", team.People)
	}
	labels := map[string]bool{}
	files := map[string]bool{}
	base := t.TempDir()
	if err := copyScenario(sc.Name, base); err != nil {
		t.Fatal(err)
	}
	for _, st := range team.Steps {
		if people[st.Actor] == "" {
			t.Errorf("the step %s of %q: not a person of the team", st.Kind, st.Actor)
		}
		for _, by := range st.ApproveBy {
			if people[by] == "" {
				t.Errorf("the step %s of %s is approved by %q, who is not in the team", st.Kind, st.Actor, by)
			}
		}
		switch st.Kind {
		case "discover":
			if st.Label != "" || len(st.Edits) > 0 || st.File != "" {
				t.Errorf("a discovery has no label, no edits and no file: %+v", st)
			}
		case "change", "propose":
			if st.Label == "" || labels[st.Label] || st.Reason == "" || len(st.Edits) == 0 || st.File != "" {
				t.Errorf("the step %s of %s is not a plan: %+v", st.Kind, st.Actor, st)
			}
			labels[st.Label] = true
			if st.Kind == "change" && len(st.ApproveBy) > 0 && people[st.ApproveBy[len(st.ApproveBy)-1]] != "admin" {
				t.Errorf("the change %s is approved last by %s, who is not an admin", st.Label, st.ApproveBy[len(st.ApproveBy)-1])
			}
		case "save":
			if st.File == "" || files[st.File] || len(st.Edits) == 0 || st.Label != "" || len(st.ApproveBy) > 0 || !strings.HasSuffix(st.File, ".plan") {
				t.Errorf("the save of %s is not a plan file: %+v", st.Actor, st)
			}
			files[st.File] = true
		default:
			t.Errorf("the step of %s has the kind %q", st.Actor, st.Kind)
		}
		if st.Kind == "discover" {
			continue
		}
		dir := t.TempDir()
		if err := copyScenario(sc.Name, dir); err != nil {
			t.Fatal(err)
		}
		if err := applyEdits(dir, st.Edits); err != nil {
			t.Errorf("the step %s of %s: %v", st.Kind, st.Actor, err)
		}
		// A change stays: the steps after it, and the edits made last,
		// apply on top of it.
		if st.Kind == "change" {
			if err := applyEdits(base, st.Edits); err != nil {
				t.Errorf("the change %s on top of the changes before it: %v", st.Label, err)
			}
		}
	}
	if err := applyEdits(base, team.After); err != nil {
		t.Errorf("the edits made last: %v", err)
	}
}

// The team of the platform has every role, a catalog for the discovery, every
// kind of step that is recorded, and the plans and runs that its shots name.
func TestTeamOfThePlatform(t *testing.T) {
	sc, _ := findScenario("team")
	team := sc.Team
	if team == nil {
		t.Fatal("the team scenario has no team")
	}
	roles := map[string]bool{}
	for _, p := range team.People {
		roles[p.Role] = true
	}
	if len(team.People) < 3 || !roles["admin"] || !roles["writer"] || !roles["reader"] {
		t.Errorf("the team does not have one of each role: %+v", team.People)
	}
	if len(sc.Catalog) == 0 {
		t.Error("the fake provider lists nothing")
	}
	kinds := map[string]int{}
	for _, st := range team.Steps {
		kinds[st.Kind]++
	}
	if kinds["discover"] == 0 || kinds["change"] == 0 || kinds["propose"] == 0 {
		t.Errorf("the team does not discover, change and propose: %v", kinds)
	}
	if got := sc.planLabels(); strings.Join(got, " ") != "search reload orders" {
		t.Errorf("the plans of the team are %v", got)
	}
	if got := sc.traceLabels(); strings.Join(got, " ") != "apply discover plan:search apply:search plan:reload plan:orders" {
		t.Errorf("the runs of the team are %v", got)
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
// (the port of orders), which is what the picture of an overlap needs. A plan
// without edits is a plan of the specification as it stands.
func TestStoredPlanEditsApply(t *testing.T) {
	for _, sc := range scenarios {
		actors := map[string]bool{}
		for _, sp := range sc.Stored {
			if actors[sp.Actor] || sp.Actor == "" || sp.Reason == "" {
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
