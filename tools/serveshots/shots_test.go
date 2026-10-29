package main

import (
	"path/filepath"
	"strconv"
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
		{"the plans finder", Fragment{View: "plans", PLQ: "by:ben action:replace"}, "/?plq=by%3Aben%20action%3Areplace&view=plans"},
		{"a policy", Fragment{View: "policies", Pol: "files_under_out"}, "/?view=policies&pol=files_under_out"},
		{"the policy studio", Fragment{View: "policies", Studio: true}, "/?view=policies&ps=1"},
		{"a run and a span", Fragment{View: "traces", Trace: "@apply", Span: "@network PUT 127.0.0.1", TV: "table", TQ: "provider"},
			"/?view=traces&trace=%40apply&span=%40network%20PUT%20127.0.0.1&tv=table&tq=provider"},
		{"the discovery filter", Fragment{View: "discovery", DQ: "status:unmanaged"}, "/?dq=status%3Aunmanaged&view=discovery"},
		{"all", Fragment{
			Sel: "a", Tab: "change", Collapse: []string{"x"}, Expand: []string{"y"}, Theme: "light",
			Q: "k", Plan: "plan-2f9c1a4e", View: "list", Motion: "off", Zoom: "fit",
		}, "/?sel=a&tab=change&collapse=x&expand=y&theme=light&q=k&plan=plan-2f9c1a4e&view=list&motion=off&zoom=fit"},
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
	ok := []Fragment{{}, {Tab: "state", Theme: "dark", View: "list", Zoom: "fit", Motion: "off"},
		{Plan: "plan-2f9c1a4e"}, {Plan: "none"}, {Plan: "@ana"}, {Plan: "@plan:search"},
		{View: "policies", Pol: "x"}, {View: "policies", Studio: true},
		{View: "traces", Trace: "@apply"}, {View: "traces", Trace: strings.Repeat("ab", 16) + "-" + strings.Repeat("cd", 8), Span: "@setup"},
		{View: "traces", Trace: "@apply", TV: "table", TQ: "provider"},
		{View: "discovery", DQ: "x"}, {View: "plans", PLQ: "x"}}
	for _, f := range ok {
		if err := f.validate(); err != nil {
			t.Errorf("%+v: %v", f, err)
		}
	}
	bad := []Fragment{{Tab: "files"}, {Theme: "sepia"}, {View: "tree"}, {Zoom: "100"}, {Motion: "on"},
		{Plan: "2f9c1a4e"}, {Plan: "plan-xyz"}, {Plan: "@"}, {Plan: "@Ana"},
		{Pol: "x"}, {Studio: true}, {View: "graph", Pol: "x"},
		{Trace: "@apply"}, {View: "traces", Trace: "apply"}, {View: "traces", Span: "@x"}, {TV: "table"}, {TQ: "x"},
		{View: "traces", Trace: "@apply", TV: "list"}}
	for _, f := range bad {
		if err := f.validate(); err == nil {
			t.Errorf("%+v: no error", f)
		}
	}
}

// wantViews are the views of the documentation, in the order they are made.
// Each comes as a dark and a light picture, and docs/img/serve holds exactly
// those.
var wantViews = []string{
	"overview", "groups-collapsed", "groups-expanded", "clusters", "source", "sensitive", "palette", "narrow",
	"list", "drift", "plan",
	"plans", "plans-list", "plans-finder", "plan-expired", "state-unsealed",
	"history", "plans-team", "policies", "policy-pending", "studio", "discovery", "traces", "trace-span", "traces-slowest", "members",
	"b02-plan", "b03-state", "b05-groups", "b06-source", "b08-move", "b09-drift", "b10-discovery", "b11-policies", "c02-plans",
	"c05-studio-shape", "c05-studio-template", "c05-studio-resources", "c05-studio-typo", "c06-waits", "c06-approved",
	"ask-notice", "ask-answer", "ask-sent",
}

func TestShotListIsConsistent(t *testing.T) {
	shots := allShots()
	if err := validateShots(shots); err != nil {
		t.Fatal(err)
	}
	var names, want []string
	for _, s := range shots {
		names = append(names, s.Name)
	}
	for _, v := range wantViews {
		want = append(want, v+"-dark", v+"-light")
	}
	if strings.Join(names, " ") != strings.Join(want, " ") {
		t.Errorf("shots are\n  %s\nwant\n  %s", strings.Join(names, " "), strings.Join(want, " "))
	}
}

func TestShotSizes(t *testing.T) {
	for _, s := range allShots() {
		w, h := 1440, 900
		switch {
		case strings.HasPrefix(s.Name, "source-"):
			h = 1000
		case strings.HasPrefix(s.Name, "trace-span-"):
			h = 1500
		case strings.HasPrefix(s.Name, "narrow-"):
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

// Every view comes as a dark and a light picture, and the two differ in the
// theme and nothing else.
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
			t.Errorf("%s has no light picture", name)
			continue
		}
		pairs++
		l := light.Fragment
		l.Theme = "dark"
		if l.String() != dark.Fragment.String() || light.Scenario != dark.Scenario || light.Situation != dark.Situation ||
			light.Width != dark.Width || light.Height != dark.Height || light.Script != dark.Script {
			t.Errorf("%s and %s differ in more than the theme", name, base+"-light")
		}
	}
	if pairs*2 != len(byName) {
		t.Errorf("%d pairs of dark and light shots among %d shots", pairs, len(byName))
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
		case situationTeam:
			if !sc.hasTeam() {
				t.Errorf("%s: %s has no team for the situation %s", s.Name, s.Scenario, s.Situation)
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

// A recorded run is named by its label, and a span by its name: the ids of the
// run and of the first span of that name take their place.
func TestResolveLabelsFindsARunAndASpan(t *testing.T) {
	dir := t.TempDir()
	id := strings.Repeat("ab", 16) + "-" + strings.Repeat("01", 8)
	write(t, filepath.Join(dir, ".pravo", "traces", id+".json"), `{"version":1,"trace_id":"`+strings.Repeat("ab", 16)+`","service":"pravo","spans":[
		{"id":"`+strings.Repeat("01", 8)+`","name":"pravo apply","start_us":0,"dur_us":10},
		{"id":"`+strings.Repeat("02", 8)+`","parent":"`+strings.Repeat("01", 8)+`","name":"network PUT 127.0.0.1","start_us":1,"dur_us":2},
		{"id":"`+strings.Repeat("03", 8)+`","parent":"`+strings.Repeat("01", 8)+`","name":"network PUT 127.0.0.1","start_us":4,"dur_us":2}]}`)
	w := &workspace{Dir: dir, Plans: map[string]string{"orders": "plan-2f9c1a4e"}, Traces: map[string]string{"apply": id}}
	for in, want := range map[string]string{
		"#/?view=traces&trace=%40apply&motion=off":                                 "#/?view=traces&trace=" + id + "&motion=off",
		"#/?view=traces&trace=%40apply&span=%40network%20PUT%20127.0.0.1&tv=table": "#/?view=traces&trace=" + id + "&span=" + strings.Repeat("02", 8) + "&tv=table",
		"#/?view=traces&trace=" + id + "&span=%40pravo%20apply":                    "#/?view=traces&trace=" + id + "&span=" + strings.Repeat("01", 8),
		"#/?view=plans&plan=%40orders":                                             "#/?view=plans&plan=plan-2f9c1a4e",
		"#/?view=traces&trace=" + id:                                               "#/?view=traces&trace=" + id,
	} {
		got, err := resolveLabels(in, w)
		if err != nil || got != want {
			t.Errorf("resolveLabels(%q) = %q, %v, want %q", in, got, err, want)
		}
	}
	for _, in := range []string{
		"#/?view=traces&trace=%40discover",
		"#/?view=traces&trace=%40apply&span=%40seal",
		"#/?view=traces&span=%40pravo%20apply",
		"#/?view=plans&plan=%40reload",
	} {
		if _, err := resolveLabels(in, w); err == nil {
			t.Errorf("resolveLabels(%q): no error", in)
		}
	}
}

func TestValidateShotsRejects(t *testing.T) {
	good := shot("x-dark", "platform", situationClean, Fragment{})
	tests := map[string]func(s *Shot){
		"no motion":           func(s *Shot) { s.Fragment.Motion = "" },
		"wrong theme":         func(s *Shot) { s.Fragment.Theme = "light" },
		"no theme":            func(s *Shot) { s.Name = "x"; s.Fragment.Theme = "" },
		"unknown scene":       func(s *Shot) { s.Scenario = "nowhere" },
		"unknown sit":         func(s *Shot) { s.Situation = "later" },
		"no size":             func(s *Shot) { s.Width = 0 },
		"bad tab":             func(s *Shot) { s.Fragment.Tab = "files" },
		"changed, fleet":      func(s *Shot) { s.Scenario = "fleet"; s.Situation = situationChanged },
		"team, platform":      func(s *Shot) { s.Situation = situationTeam },
		"a plan label, clean": func(s *Shot) { s.Fragment.Plan = "@ana" },
		"a plan of nobody":    func(s *Shot) { s.Situation = situationStored; s.Fragment.Plan = "@eve" },
		"a step of the team in the stored situation": func(s *Shot) {
			s.Situation = situationStored
			s.Fragment.Plan = "@orders"
		},
		"a run outside the team": func(s *Shot) {
			s.Situation = situationStored
			s.Fragment.View = "traces"
			s.Fragment.Trace = "@apply"
		},
		"a run that nobody recorded": func(s *Shot) {
			s.Scenario = "team"
			s.Situation = situationTeam
			s.Fragment.View = "traces"
			s.Fragment.Trace = "@apply:reload"
		},
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
	team := shot("y-dark", "team", situationTeam, Fragment{View: "traces", Trace: "@apply:search", Span: "@engine apply"})
	if err := validateShots([]Shot{good, team}); err != nil {
		t.Error(err)
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
	if err != nil || len(got) != 4 {
		t.Fatalf("scenario: %d shots, %v", len(got), err)
	}
	got, err = selectShots(all, "fleet,clusters-dark")
	if err != nil || len(got) != 4 {
		t.Fatalf("a shot named twice: %d shots, %v", len(got), err)
	}
	if _, err := selectShots(all, "nope"); err == nil {
		t.Error("an unknown name: no error")
	}
}

// Every shot of the team opens a view that its workspace can show: a plan or
// a run that a step of the team labelled, and a span that the recorded run
// has. The names of the spans are those of the recorder.
func TestTeamShotsNameStepsOfTheTeam(t *testing.T) {
	sc, _ := findScenario("team")
	for _, s := range allShots() {
		if s.Scenario != "team" {
			continue
		}
		if s.Situation != situationTeam {
			t.Errorf("%s: the team scenario is only made in the team situation", s.Name)
		}
		if strings.HasPrefix(s.Fragment.Span, "@") && !strings.HasPrefix(s.Fragment.Span, "@network ") && !strings.HasPrefix(s.Fragment.Span, "@pravo ") {
			t.Errorf("%s: the span %q is not one that every recorded run has", s.Name, s.Fragment.Span)
		}
	}
	if len(sc.traceLabels()) < 2 || sc.traceLabels()[0] != "apply" {
		t.Errorf("the runs of the team: %v", sc.traceLabels())
	}
}

// A shot of the studio types the rule of a file of its scenario and turns the
// switch: the script names the file's text, the target and, for a plan, the
// plan file that a save step of the team wrote.
func TestStudioShotsTypeTheRulesOfTheScenario(t *testing.T) {
	n := 0
	for _, s := range allShots() {
		if s.Script == "" || s.Chat || strings.Contains(s.Script, "const reason =") {
			continue
		}
		n++
		if !s.Fragment.Studio || s.Fragment.View != "policies" {
			t.Errorf("%s has a script and does not open the studio", s.Name)
		}
		sc, _ := findScenario(s.Scenario)
		if sc.Team == nil {
			t.Errorf("%s: a shot of the studio needs the team situation", s.Name)
			continue
		}
		for _, st := range sc.Team.Steps {
			if st.Kind == "save" && strings.Contains(s.Script, strconv.Quote(st.File)) {
				goto ok
			}
		}
		if strings.Contains(s.Script, `target = "plan"`) {
			t.Errorf("%s looks at a plan that no save step of %s wrote", s.Name, s.Scenario)
		}
	ok:
		if !strings.Contains(s.Script, `pravo.#Policy`) {
			t.Errorf("%s does not type a policy", s.Name)
		}
	}
	if n == 0 {
		t.Error("no shot of the studio")
	}
	if got := studioScript("x", "plan", "a.plan", ""); !strings.Contains(got, `plan = "a.plan"`) || !strings.Contains(got, `target = "plan"`) {
		t.Errorf("studioScript = %s", got)
	}
	for _, bad := range [][3]string{{"plan", "", ""}, {"resources", "a.plan", ""}, {"state", "", ""}} {
		func() {
			defer func() {
				if recover() == nil {
					t.Errorf("studioScript(%q, %q, %q): no panic", bad[0], bad[1], bad[2])
				}
			}()
			studioScript("x", bad[0], bad[1], bad[2])
		}()
	}
	if got := withBound("a >=16 b", ">=16", ">=20"); got != "a >=20 b" {
		t.Errorf("withBound = %q", got)
	}
	if got := rule("c05", "no_destroy"); !strings.HasPrefix(got, `import "pravo.dev/pravo"`) {
		t.Errorf("rule = %q", got)
	}
}

// A script that opens the row of a plan names a plan that a propose step of
// the team of its scenario stored, in the Plans section.
func TestPlanShotsOpenAPlanOfTheScenario(t *testing.T) {
	n := 0
	for _, s := range allShots() {
		if !strings.Contains(s.Script, "const reason =") {
			continue
		}
		n++
		if s.Fragment.View != "plans" {
			t.Errorf("%s opens the row of a plan outside the Plans section", s.Name)
		}
		sc, _ := findScenario(s.Scenario)
		if sc.Team == nil {
			t.Errorf("%s: a shot that opens a plan needs the team situation", s.Name)
			continue
		}
		found := false
		for _, st := range sc.Team.Steps {
			if st.Kind == "propose" && strings.Contains(s.Script, "const reason = "+strconv.Quote(st.Reason)+";") {
				found = true
			}
		}
		if !found {
			t.Errorf("%s opens a plan that no propose step of %s stored", s.Name, s.Scenario)
		}
	}
	if n == 0 {
		t.Error("no shot opens the row of a plan")
	}
	if got := openPlanScript("ask for nothing"); !strings.Contains(got, `const reason = "ask for nothing";`) || !strings.Contains(got, "const until =") {
		t.Errorf("openPlanScript = %s", got)
	}
}
