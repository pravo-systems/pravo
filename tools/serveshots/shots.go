package main

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/pravo-systems/pravo/internal/telemetry"
)

// A situation is the state of a scenario's workspace that a shot is made from.
const (
	// situationClean is the scenario applied and left alone: nothing changed,
	// nothing planned, nothing drifted.
	situationClean = "clean"
	// situationChanged is the clean estate after a file was edited and another
	// deleted on disk and pravo drift ran, after one resource was added to the
	// specification and one removed from it, and after values were edited.
	situationChanged = "changed"
	// situationPlan is situationChanged served with the plan that
	// pravo plan --out saved, so the viewer overlays its actions.
	situationPlan = "plan"
	// situationStored is the clean estate with the plans that people stored in
	// the state with pravo plan --store, served without --plan: the viewer lists
	// them, and the fragment of a shot lays one over the graph.
	situationStored = "stored"
	// situationTeam is the estate as a team runs it: the state in a bucket,
	// sealed to people with roles, policies in force, plans stored, approved
	// and applied by different people, a discovery run and the runs recorded.
	// The viewer reads as the admin.
	situationTeam = "team"
)

// Fragment is the view that the interface keeps in the URL fragment, so that a
// link reproduces a view. The fields are the parameters of the deep link; an
// empty field is left out.
type Fragment struct {
	Sel      string
	Tab      string
	Collapse []string
	Expand   []string
	Theme    string
	Q        string
	// DQ, PLQ and TQ are the filters of the Discovery section, the Plans
	// finder and the Traces section.
	DQ, PLQ, TQ string
	// Plan is a stored plan to lay over the graph: its id, "none", or "@NAME"
	// for the plan that an actor of the scenario made, or that a step of the
	// team labelled, whose id is not known until the plans are stored.
	Plan string
	View string
	// Pol is the policy that the Policies section opens, and Studio opens
	// the policy studio of the section.
	Pol    string
	Studio bool
	// Trace is the recorded run that the Traces section opens: its id, or
	// "@LABEL" for a run of the team. Span is the span it selects: its id, or
	// "@NAME" for the first span of that name. TV is "table" for the table
	// of the slowest spans.
	Trace, Span, TV string
	Zoom            string
	// Motion is "off" in every shot, so that a capture does not depend on the
	// moment it is taken.
	Motion string
	// Palette opens the search palette, with the text PQ typed into it, and
	// Help opens the sheet of shortcuts.
	Palette bool
	PQ      string
	Help    bool
}

var (
	fragmentTabs   = []string{"state", "source", "schema", "history", "change"}
	fragmentThemes = []string{"light", "dark"}
	fragmentViews  = []string{"graph", "list", "state", "plans", "discovery", "policies", "traces"}
)

// String is the fragment without the leading #, in the order of the deep-link
// table: sel, tab, collapse, expand, theme, q, dq, plq, plan, view, pol, ps,
// trace, span, tv, tq, motion, zoom, and then palette, pq and help.
// Values are URL-encoded, and a list is written comma separated with each
// element encoded.
func (f Fragment) String() string {
	var parts []string
	add := func(k, v string) {
		if v != "" {
			parts = append(parts, k+"="+escape(v))
		}
	}
	list := func(k string, vs []string) {
		if len(vs) == 0 {
			return
		}
		enc := make([]string, len(vs))
		for i, v := range vs {
			enc[i] = escape(v)
		}
		parts = append(parts, k+"="+strings.Join(enc, ","))
	}
	add("sel", f.Sel)
	add("tab", f.Tab)
	list("collapse", f.Collapse)
	list("expand", f.Expand)
	add("theme", f.Theme)
	add("q", f.Q)
	add("dq", f.DQ)
	add("plq", f.PLQ)
	add("plan", f.Plan)
	add("view", f.View)
	add("pol", f.Pol)
	if f.Studio {
		add("ps", "1")
	}
	add("trace", f.Trace)
	add("span", f.Span)
	add("tv", f.TV)
	add("tq", f.TQ)
	add("motion", f.Motion)
	add("zoom", f.Zoom)
	if f.Palette {
		add("palette", "1")
	}
	add("pq", f.PQ)
	if f.Help {
		add("help", "1")
	}
	return "/?" + strings.Join(parts, "&")
}

// escape encodes a fragment value. A space is %20, since the page decodes with
// decodeURIComponent, which leaves a + alone.
func escape(s string) string {
	return strings.ReplaceAll(url.QueryEscape(s), "+", "%20")
}

var (
	planIDRe  = regexp.MustCompile(`^plan-[0-9a-f]{6,64}$`)
	labelRe   = regexp.MustCompile(`^@[a-z][a-z0-9_:-]*$`)
	traceIDRe = regexp.MustCompile(`^[0-9a-f]{32}-[0-9a-f]{16}$`)
)

// resolveLabels puts the ids of the stored plans and the recorded runs in
// the place of "@NAME" in a fragment, and reports a name that nothing has.
// A span named "@NAME" is the first span of that name in the run.
func resolveLabels(fragment string, w *workspace) (string, error) {
	var err error
	fragment, err = resolveParam(fragment, "plan", func(label string) (string, error) {
		id, ok := w.Plans[label]
		if !ok {
			return "", fmt.Errorf("the fragment names the plan of %q, which the workspace does not have", label)
		}
		return id, nil
	})
	if err != nil {
		return "", err
	}
	var trace string
	fragment, err = resolveParam(fragment, "trace", func(label string) (string, error) {
		id, ok := w.Traces[label]
		if !ok {
			return "", fmt.Errorf("the fragment names the run %q, which the workspace did not record", label)
		}
		trace = id
		return id, nil
	})
	if err != nil {
		return "", err
	}
	if trace == "" {
		trace = param(fragment, "trace")
	}
	return resolveParam(fragment, "span", func(name string) (string, error) {
		if trace == "" {
			return "", fmt.Errorf("the fragment names the span %q and no run", name)
		}
		return spanID(filepath.Join(w.Dir, ".pravo", "traces", trace+".json"), name)
	})
}

// resolvePlans puts the id of the stored plan that an actor made in the place of
// "@ACTOR" in a fragment, and reports an actor that made none.
func resolvePlans(fragment string, ids map[string]string) (string, error) {
	return resolveLabels(fragment, &workspace{Plans: ids})
}

// resolveParam replaces the value of the parameter, when it starts with @,
// with what resolve returns for the name after it.
func resolveParam(fragment, key string, resolve func(string) (string, error)) (string, error) {
	prefix := key + "=" + escape("@")
	i := strings.Index(fragment, prefix)
	if i < 0 || (i > 0 && fragment[i-1] != '&' && fragment[i-1] != '?') {
		return fragment, nil
	}
	start := i + len(key) + 1
	end := start + len(escape("@"))
	for end < len(fragment) && fragment[end] != '&' {
		end++
	}
	name, err := url.QueryUnescape(fragment[start+len(escape("@")) : end])
	if err != nil {
		return "", err
	}
	id, err := resolve(name)
	if err != nil {
		return "", err
	}
	return fragment[:start] + escape(id) + fragment[end:], nil
}

// param is the value of a parameter of the fragment, decoded.
func param(fragment, key string) string {
	for _, part := range strings.Split(strings.TrimPrefix(fragment, "#/?"), "&") {
		if k, v, ok := strings.Cut(part, "="); ok && k == key {
			s, _ := url.QueryUnescape(v)
			return s
		}
	}
	return ""
}

// spanID reads the recorded run at path and returns the id of the first span
// with the name.
func spanID(path, name string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	var rt telemetry.RunTrace
	if err := json.Unmarshal(data, &rt); err != nil {
		return "", fmt.Errorf("%s: %w", filepath.Base(path), err)
	}
	for _, s := range rt.Spans {
		if s.Name == name {
			return s.ID, nil
		}
	}
	return "", fmt.Errorf("the run %s has no span named %q", filepath.Base(path), name)
}

// validate reports a parameter that the interface does not know.
func (f Fragment) validate() error {
	if p := f.Plan; p != "" && p != "none" && !planIDRe.MatchString(p) && !labelRe.MatchString(p) {
		return fmt.Errorf("plan=%q is not an id, none or @NAME", p)
	}
	if t := f.Trace; t != "" && !traceIDRe.MatchString(t) && !labelRe.MatchString(t) {
		return fmt.Errorf("trace=%q is not an id or @LABEL", t)
	}
	if f.Span != "" && f.Trace == "" {
		return fmt.Errorf("span=%q without a trace", f.Span)
	}
	if (f.Pol != "" || f.Studio) && f.View != "policies" {
		return fmt.Errorf("pol and ps need view=policies")
	}
	if (f.Trace != "" || f.TV != "" || f.TQ != "") && f.View != "traces" {
		return fmt.Errorf("trace, tv and tq need view=traces")
	}
	check := func(name, v string, ok []string) error {
		if v == "" {
			return nil
		}
		for _, o := range ok {
			if v == o {
				return nil
			}
		}
		return fmt.Errorf("%s=%q is not one of %s", name, v, strings.Join(ok, ", "))
	}
	for _, e := range []error{
		check("tab", f.Tab, fragmentTabs),
		check("theme", f.Theme, fragmentThemes),
		check("view", f.View, fragmentViews),
		check("tv", f.TV, []string{"table"}),
		check("zoom", f.Zoom, []string{"fit"}),
		check("motion", f.Motion, []string{"off"}),
	} {
		if e != nil {
			return e
		}
	}
	return nil
}

// A Shot is one picture of the documentation: the file docs/img/serve/NAME.png
// made from a view of a scenario.
type Shot struct {
	// Name is the file name without .png. A name that ends in -dark or -light
	// states the theme of the fragment.
	Name      string
	Scenario  string
	Situation string
	Fragment  Fragment
	// Width and Height are the viewport in CSS pixels. The PNG is twice as large
	// in each direction.
	Width, Height int
}

// scale is the device scale factor of every shot.
const scale = 2

// File is the name of the PNG.
func (s Shot) File() string { return s.Name + ".png" }

// URLFragment is the fragment of the address that the shot opens, with the #.
func (s Shot) URLFragment() string { return "#" + s.Fragment.String() }

// shot builds a shot of 1440 by 900 in the theme that its name ends with.
func shot(name, scenario, situation string, f Fragment) Shot {
	f.Motion = "off"
	if f.Zoom == "" {
		f.Zoom = "fit"
	}
	switch {
	case strings.HasSuffix(name, "-dark"):
		f.Theme = "dark"
	case strings.HasSuffix(name, "-light"):
		f.Theme = "light"
	}
	return Shot{Name: name, Scenario: scenario, Situation: situation, Fragment: f, Width: 1440, Height: 900}
}

func (s Shot) size(w, h int) Shot {
	s.Width, s.Height = w, h
	return s
}

// The nodes and groups that the shots select. They are addresses of the
// platform, team and fleet scenarios; the tests check that each exists.
const (
	// billingConfig comes from the definition #Service in kit/kit.cue and is
	// written as an instance in api.cue, so its Source tab shows both files.
	billingConfig = "api.services.billing.config"
	// ordersConfig is replaced by the saved plan: the port of orders changes.
	ordersConfig = "api.services.orders.config"
	// searchConfig is replaced by the change that the team applied, so its
	// history has two transactions by two people.
	searchConfig = "api.services.search.config"
	// dnsRecords is the resource whose file is edited by hand.
	dnsRecords = "network.dns.records"
	// dbDSN is a local_sensitive_file that embeds the password of the
	// database: the provider marks its content sensitive, so the State tab
	// lists the attribute and withholds the value.
	dbDSN = "api.database.dsn"
	// euProd is a group two levels deep in the fleet, the production
	// environment of the region eu.
	euProd = "eu.prod"
)

// allShots lists every picture, in the order that they are made. Every view
// is made in both themes, so that a page shows the one of its own theme.
func allShots() []Shot {
	both := func(base, scenario, situation string, f Fragment) []Shot {
		return []Shot{
			shot(base+"-dark", scenario, situation, f),
			shot(base+"-light", scenario, situation, f),
		}
	}
	sized := func(w, h int, shots []Shot) []Shot {
		for i := range shots {
			shots[i] = shots[i].size(w, h)
		}
		return shots
	}
	var s []Shot
	// Nothing is selected, so the inspector shows the project: this is the page
	// as it opens.
	s = append(s, both("overview", "platform", situationClean, Fragment{})...)
	s = append(s, both("groups-collapsed", "platform", situationClean, Fragment{Sel: billingConfig, Tab: "state", Collapse: []string{"network", "edge"}})...)
	s = append(s, both("groups-expanded", "fleet", situationClean, Fragment{
		Sel:      euProd,
		Collapse: []string{"us", "ap", "eu.stage"},
		// The three services of the production environment are siblings
		// of one shape: they open together. The pool of nodes stays one
		// box, and the group is selected so that the inspector shows it.
		Expand: []string{"eu", "eu.prod", "eu.prod.api", "eu.prod.jobs", "eu.prod.web"},
	})...)
	s = append(s, both("clusters", "fleet", situationClean, Fragment{
		Sel:      "eu.prod.nodes.n04",
		Tab:      "state",
		Collapse: []string{"us", "ap", "eu.stage", "eu.prod.api", "eu.prod.web", "eu.prod.jobs"},
		// c:GROUP|MODE|TYPE is the cluster of the resources of one type in a
		// group.
		Expand: []string{"eu", "eu.prod", "c:eu.prod.nodes|managed|null_resource"},
	})...)
	s = append(s, sized(1440, 1000, both("source", "platform", situationClean, Fragment{Sel: billingConfig, Tab: "source", Collapse: []string{"network", "edge"}}))...)
	s = append(s, both("sensitive", "platform", situationClean, Fragment{Sel: dbDSN, Tab: "state", Collapse: []string{"network", "edge"}})...)
	s = append(s, both("palette", "platform", situationClean, Fragment{Palette: true, PQ: "billing"})...)
	s = append(s, sized(900, 1100, both("narrow", "platform", situationClean, Fragment{Sel: billingConfig, Tab: "state", Collapse: []string{"network", "edge"}}))...)
	s = append(s, both("list", "platform", situationChanged, Fragment{Sel: billingConfig, Tab: "state", View: "list"})...)
	s = append(s, both("drift", "platform", situationChanged, Fragment{Sel: dnsRecords, Tab: "state", Q: "status:drifted"})...)
	s = append(s, both("plan", "platform", situationPlan, Fragment{Sel: ordersConfig, Tab: "change", Collapse: []string{"network", "edge"}})...)
	// Three people stored a plan each. Those of ana and ben can be applied and
	// both replace what writes the configuration of orders, which carries both
	// badges and the ring of an overlap; the plan of ana is laid over the graph.
	// The third expired, and is laid over the graph in its own picture.
	s = append(s, both("plans", "platform", situationStored, Fragment{Sel: ordersConfig, Tab: "change", Plan: "@ana", Collapse: []string{"network", "edge"}})...)
	// The Plans section, with the row of the plan of ana open, and the finder
	// narrowed to the replacements that ben's plan makes.
	s = append(s, both("plans-list", "platform", situationStored, Fragment{View: "plans", Plan: "@ana"})...)
	s = append(s, both("plans-finder", "platform", situationStored, Fragment{View: "plans", PLQ: "by:ben action:replace"})...)
	s = append(s, both("plan-expired", "platform", situationStored, Fragment{Plan: "@ci", Collapse: []string{"network", "api"}})...)
	// The State section of a state that is not sealed: what it suggests.
	s = append(s, both("state-unsealed", "platform", situationClean, Fragment{View: "state"})...)

	// The team: the search service was moved by ben, after ada approved the
	// plan, so its configuration has two transactions; the plan of cy waits,
	// approved; the plan of ben waits for an admin.
	s = append(s, both("history", "team", situationTeam, Fragment{Sel: searchConfig, Tab: "history", Collapse: []string{"network", "edge"}})...)
	s = append(s, both("plans-team", "team", situationTeam, Fragment{View: "plans", Plan: "@orders"})...)
	s = append(s, both("policies", "team", situationTeam, Fragment{View: "policies", Pol: "files_under_out"})...)
	s = append(s, both("policy-pending", "team", situationTeam, Fragment{View: "policies", Pol: "approve_deletes"})...)
	s = append(s, both("studio", "team", situationTeam, Fragment{View: "policies", Studio: true})...)
	s = append(s, both("discovery", "team", situationTeam, Fragment{View: "discovery"})...)
	s = append(s, both("traces", "team", situationTeam, Fragment{View: "traces", Trace: "@apply"})...)
	// The apply of the plan of ben touched seven resources. The picture is
	// tall enough for the detail of the selected span, a request to the state
	// store, under the timeline.
	s = append(s, sized(1440, 1500, both("trace-span", "team", situationTeam, Fragment{View: "traces", Trace: "@apply:search", Span: "@network PUT 127.0.0.1"}))...)
	s = append(s, both("traces-slowest", "team", situationTeam, Fragment{View: "traces", Trace: "@apply", TV: "table"})...)
	s = append(s, both("members", "team", situationTeam, Fragment{View: "state"})...)
	return s
}

// selectShots keeps the shots that the -only value names: a comma separated
// list of shot names, with or without .png, and of scenario names. An empty
// value keeps every shot. A name that matches nothing is an error.
func selectShots(all []Shot, only string) ([]Shot, error) {
	only = strings.TrimSpace(only)
	if only == "" {
		return all, nil
	}
	var out []Shot
	seen := map[string]bool{}
	for _, name := range strings.Split(only, ",") {
		name = strings.TrimSuffix(strings.TrimSpace(name), ".png")
		if name == "" {
			continue
		}
		found := false
		for _, s := range all {
			if s.Name == name || s.Scenario == name {
				found = true
				if !seen[s.Name] {
					seen[s.Name] = true
					out = append(out, s)
				}
			}
		}
		if !found {
			return nil, fmt.Errorf("-only %q names no shot or scenario", name)
		}
	}
	return out, nil
}
