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
		{"a stored plan", Fragment{Sel: "a", Plan: "plan-2f9c1a4e", Motion: "off"}, "/?sel=a&plan=plan-2f9c1a4e&motion=off"},
		{"the plan of an actor", Fragment{Plan: "@ana"}, "/?plan=%40ana"},
		{"all", Fragment{
			Sel: "a", Tab: "change", Collapse: []string{"x"}, Expand: []string{"y"}, Theme: "light",
			Q: "k", Filter: "plan", Plan: "plan-2f9c1a4e", View: "table", Motion: "off", Zoom: "fit",
		}, "/?sel=a&tab=change&collapse=x&expand=y&theme=light&q=k&filter=plan&plan=plan-2f9c1a4e&view=table&motion=off&zoom=fit"},
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
	ok := []Fragment{{}, {Tab: "state", Theme: "dark", Filter: "drifted", View: "table", Zoom: "fit", Motion: "off"},
		{Plan: "plan-2f9c1a4e"}, {Plan: "none"}, {Plan: "@ana"}}
	for _, f := range ok {
		if err := f.validate(); err != nil {
			t.Errorf("%+v: %v", f, err)
		}
	}
	bad := []Fragment{{Tab: "files"}, {Theme: "sepia"}, {Filter: "all"}, {View: "list"}, {Zoom: "100"}, {Motion: "on"},
		{Plan: "2f9c1a4e"}, {Plan: "plan-xyz"}, {Plan: "@"}, {Plan: "@Ana"}}
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
	"plans-dark", "plans-light",
	"plan-expired-dark",
	"drift-dark",
	"sensitive-dark",
	"palette-dark",
	"table-dark",
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
	if pairs != 5 {
		t.Errorf("%d pairs of dark and light shots, want 5", pairs)
	}
}

func TestShotsUseKnownScenarios(t *testing.T) {
	for _, s := range allShots() {
		sc, ok := findScenario(s.Scenario)
		if !ok {
			t.Errorf("%s: unknown scenario %q", s.Name, s.Scenario)
			continue
		}
		switch s.Situation {
		case situationClean:
		case situationStored:
			if !sc.hasStored() {
				t.Errorf("%s: %s has no stored plans for the situation %s", s.Name, s.Scenario, s.Situation)
			}
		default:
			if !sc.hasChanges() {
				t.Errorf("%s: %s has no changes for the situation %s", s.Name, s.Scenario, s.Situation)
			}
		}
	}
}

// The plan of an actor is named in the fragment as @ACTOR, and the id of the plan
// that the actor stored takes its place when the address is made.
func TestResolvePlans(t *testing.T) {
	ids := map[string]string{"ana": "plan-2f9c1a4e", "ben": "plan-7b04d1c9"}
	for in, want := range map[string]string{
		"#/?plan=%40ana&motion=off":            "#/?plan=plan-2f9c1a4e&motion=off",
		"#/?sel=a&plan=%40ben":                 "#/?sel=a&plan=plan-7b04d1c9",
		"#/?sel=a&plan=plan-2f9c1a4e&zoom=fit": "#/?sel=a&plan=plan-2f9c1a4e&zoom=fit",
		"#/?sel=a":                             "#/?sel=a",
	} {
		got, err := resolvePlans(in, ids)
		if err != nil || got != want {
			t.Errorf("resolvePlans(%q) = %q, %v, want %q", in, got, err, want)
		}
	}
	if _, err := resolvePlans("#/?plan=%40eve", ids); err == nil {
		t.Error("an actor that made no plan: no error")
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
