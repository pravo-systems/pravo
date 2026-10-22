package main

import (
	"strings"
	"testing"
)

func TestFragmentString(t *testing.T) {
	tests := []struct {
		name string
		f    Fragment
		want string
	}{
		{"empty", Fragment{}, "/?"},
		{"order", Fragment{Zoom: "fit", Motion: "off", Theme: "dark", Tab: "source", Sel: "a.b"}, "/?sel=a.b&tab=source&theme=dark&motion=off&zoom=fit"},
		{"lists", Fragment{Collapse: []string{"network", "edge"}, Expand: []string{"api"}}, "/?collapse=network,edge&expand=api"},
		{"escapes a space as %20", Fragment{Q: "api key"}, "/?q=api%20key"},
		{"escapes a comma inside a list element", Fragment{Collapse: []string{`a,b`, "c"}}, "/?collapse=a%2Cb,c"},
		{"escapes a quote and a plus", Fragment{Sel: `x["a+b"]`}, "/?sel=x%5B%22a%2Bb%22%5D"},
		{"keeps dots, dashes and underscores", Fragment{Sel: "eu.prod-1.n_04"}, "/?sel=eu.prod-1.n_04"},
		{"palette", Fragment{Palette: true, PQ: "api key", Help: true}, "/?palette=1&pq=api%20key&help=1"},
		{"a cluster", Fragment{Expand: []string{"c:eu.prod.nodes|managed|null_resource"}}, "/?expand=c%3Aeu.prod.nodes%7Cmanaged%7Cnull_resource"},
		{"all", Fragment{
			Sel: "a", Tab: "change", Collapse: []string{"x"}, Expand: []string{"y"}, Theme: "light",
			Q: "k", Filter: "plan", View: "tree", Motion: "off", Zoom: "fit",
		}, "/?sel=a&tab=change&collapse=x&expand=y&theme=light&q=k&filter=plan&view=tree&motion=off&zoom=fit"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.f.String(); got != tt.want {
				t.Errorf("String() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestFragmentValidate(t *testing.T) {
	ok := []Fragment{{}, {Tab: "state", Theme: "dark", Filter: "drifted", View: "tree", Zoom: "fit", Motion: "off"}}
	for _, f := range ok {
		if err := f.validate(); err != nil {
			t.Errorf("%+v: %v", f, err)
		}
	}
	bad := []Fragment{{Tab: "files"}, {Theme: "sepia"}, {Filter: "all"}, {View: "list"}, {Zoom: "100"}, {Motion: "on"}}
	for _, f := range bad {
		if err := f.validate(); err == nil {
			t.Errorf("%+v: no error", f)
		}
	}
}

// wantShots are the files that the documentation links to. docs/img/serve holds
// exactly these.
var wantShots = []string{
	"overview-dark", "overview-light",
	"modules-collapsed-dark", "modules-collapsed-light",
	"modules-expanded-dark",
	"clusters-dark",
	"source-dark", "source-light",
	"plan-dark", "plan-light",
	"drift-dark",
	"sensitive-dark",
	"palette-dark",
	"tree-dark",
	"narrow-dark",
}

func TestShotListIsConsistent(t *testing.T) {
	shots := allShots()
	if err := validateShots(shots); err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, s := range shots {
		names = append(names, s.Name)
	}
	if strings.Join(names, " ") != strings.Join(wantShots, " ") {
		t.Errorf("shots are\n  %s\nwant\n  %s", strings.Join(names, " "), strings.Join(wantShots, " "))
	}
}

func TestShotSizes(t *testing.T) {
	for _, s := range allShots() {
		w, h := 1440, 900
		switch {
		case strings.HasPrefix(s.Name, "source-"):
			h = 1000
		case s.Name == "narrow-dark":
			w, h = 900, 1100
		}
		if s.Width != w || s.Height != h {
			t.Errorf("%s is %dx%d, want %dx%d", s.Name, s.Width, s.Height, w, h)
		}
	}
}

func TestEveryFragmentHasMotionOffAndTheTheme(t *testing.T) {
	for _, s := range allShots() {
		frag := s.URLFragment()
		if !strings.HasPrefix(frag, "#/?") {
			t.Errorf("%s: fragment %q does not start with #/?", s.Name, frag)
		}
		if !strings.Contains(frag, "&motion=off") {
			t.Errorf("%s: no motion=off in %q", s.Name, frag)
		}
		want := "theme=dark"
		if strings.HasSuffix(s.Name, "-light") {
			want = "theme=light"
		}
		if !strings.Contains(frag, want) {
			t.Errorf("%s: no %s in %q", s.Name, want, frag)
		}
	}
}

// A dark and a light shot of one view differ in the theme and nothing else.
func TestThemePairsShowTheSameView(t *testing.T) {
	byName := map[string]Shot{}
	for _, s := range allShots() {
		byName[s.Name] = s
	}
	pairs := 0
	for name, dark := range byName {
		base, ok := strings.CutSuffix(name, "-dark")
		if !ok {
			continue
		}
		light, ok := byName[base+"-light"]
		if !ok {
			continue
		}
		pairs++
		l := light.Fragment
		l.Theme = "dark"
		if l.String() != dark.Fragment.String() || light.Scenario != dark.Scenario || light.Situation != dark.Situation ||
			light.Width != dark.Width || light.Height != dark.Height {
			t.Errorf("%s and %s differ in more than the theme", name, base+"-light")
		}
	}
	if pairs != 4 {
		t.Errorf("%d pairs of dark and light shots, want 4", pairs)
	}
}

func TestShotsUseKnownScenarios(t *testing.T) {
	for _, s := range allShots() {
		sc, ok := findScenario(s.Scenario)
		if !ok {
			t.Errorf("%s: unknown scenario %q", s.Name, s.Scenario)
			continue
		}
		if s.Situation != situationClean && !sc.hasChanges() {
			t.Errorf("%s: %s has no changes for the situation %s", s.Name, s.Scenario, s.Situation)
		}
	}
}

func TestValidateShotsRejects(t *testing.T) {
	good := shot("x-dark", "platform", situationClean, Fragment{})
	tests := map[string]func(s *Shot){
		"no motion":      func(s *Shot) { s.Fragment.Motion = "" },
		"wrong theme":    func(s *Shot) { s.Fragment.Theme = "light" },
		"no theme":       func(s *Shot) { s.Name = "x"; s.Fragment.Theme = "" },
		"unknown scene":  func(s *Shot) { s.Scenario = "nowhere" },
		"unknown sit":    func(s *Shot) { s.Situation = "later" },
		"no size":        func(s *Shot) { s.Width = 0 },
		"bad tab":        func(s *Shot) { s.Fragment.Tab = "files" },
		"changed, fleet": func(s *Shot) { s.Scenario = "fleet"; s.Situation = situationChanged },
	}
	for name, mutate := range tests {
		s := good
		mutate(&s)
		if err := validateShots([]Shot{s}); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
	if err := validateShots([]Shot{good, good}); err == nil {
		t.Error("a duplicate name: no error")
	}
}

func TestSelectShots(t *testing.T) {
	all := allShots()
	got, err := selectShots(all, "")
	if err != nil || len(got) != len(all) {
		t.Fatalf("empty: %d shots, %v", len(got), err)
	}
	got, err = selectShots(all, "overview-dark, plan-light.png")
	if err != nil || len(got) != 2 || got[0].Name != "overview-dark" || got[1].Name != "plan-light" {
		t.Fatalf("names: %v, %v", got, err)
	}
	got, err = selectShots(all, "fleet")
	if err != nil || len(got) != 2 {
		t.Fatalf("scenario: %d shots, %v", len(got), err)
	}
	got, err = selectShots(all, "fleet,clusters-dark")
	if err != nil || len(got) != 2 {
		t.Fatalf("a shot named twice: %d shots, %v", len(got), err)
	}
	if _, err := selectShots(all, "nope"); err == nil {
		t.Error("an unknown name: no error")
	}
}
