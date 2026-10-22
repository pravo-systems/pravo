package main

import (
	"fmt"
	"net/url"
	"regexp"
	"strings"
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
	Filter   string
	// Plan is a stored plan to lay over the graph: its id, "none", or "@ACTOR"
	// for the plan that an actor of the scenario made, whose id is not known
	// until the plans are stored.
	Plan string
	View string
	Zoom string
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
	fragmentTabs    = []string{"state", "source", "schema", "history", "change"}
	fragmentThemes  = []string{"light", "dark"}
	fragmentFilters = []string{"plan", "drifted", "sensitive", "orphans", "declared"}
	fragmentViews   = []string{"graph", "table"}
)

// String is the fragment without the leading #, in the order of the deep-link
// table: sel, tab, collapse, expand, theme, q, filter, plan, view, motion, zoom,
// and then palette, pq and help.
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
	add("filter", f.Filter)
	add("plan", f.Plan)
	add("view", f.View)
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
	planIDRe    = regexp.MustCompile(`^plan-[0-9a-f]{6,64}$`)
	planActorRe = regexp.MustCompile(`^@[a-z][a-z0-9_-]*$`)
)

// resolvePlans puts the id of the stored plan that an actor made in the place of
// "@ACTOR" in a fragment, and reports an actor that made none.
func resolvePlans(fragment string, ids map[string]string) (string, error) {
	i := strings.Index(fragment, "plan="+escape("@"))
	if i < 0 {
		return fragment, nil
	}
	start := i + len("plan=")
	end := start + len(escape("@"))
	for end < len(fragment) && fragment[end] != '&' {
		end++
	}
	actor := fragment[start+len(escape("@")) : end]
	id, ok := ids[actor]
	if !ok {
		return "", fmt.Errorf("the fragment names the plan of %q, which the workspace does not have", actor)
	}
	return fragment[:start] + id + fragment[end:], nil
}

// validate reports a parameter that the interface does not know.
func (f Fragment) validate() error {
	if p := f.Plan; p != "" && p != "none" && !planIDRe.MatchString(p) && !planActorRe.MatchString(p) {
		return fmt.Errorf("plan=%q is not an id, none or @ACTOR", p)
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
		check("filter", f.Filter, fragmentFilters),
		check("view", f.View, fragmentViews),
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

// The nodes and modules that the shots select. They are addresses of the
// platform and fleet scenarios; the tests check that each exists.
const (
	// billingConfig comes from the definition #Service in kit/kit.cue and is
	// written as an instance in api.cue, so its Source tab shows both files.
	billingConfig = "api.services.billing.config"
	// ordersConfig is replaced by the saved plan: the port of orders changes.
	ordersConfig = "api.services.orders.config"
	// dnsRecords is the resource whose file is edited by hand.
	dnsRecords = "network.dns.records"
	// dbDSN is a local_sensitive_file that embeds the password of the
	// database: the provider marks its content sensitive, so the State tab
	// lists the attribute and withholds the value.
	dbDSN = "api.database.dsn"
	// euProd is a module two levels deep in the fleet, the production
	// environment of the region eu.
	euProd = "eu.prod"
)

// allShots lists every picture, in the order that they are made.
func allShots() []Shot {
	both := func(base, scenario, situation string, f Fragment) []Shot {
		return []Shot{
			shot(base+"-dark", scenario, situation, f),
			shot(base+"-light", scenario, situation, f),
		}
	}
	var s []Shot
	// Nothing is selected, so the inspector shows the project: this is the page
	// as it opens.
	s = append(s, both("overview", "platform", situationClean, Fragment{})...)
	s = append(s, both("modules-collapsed", "platform", situationClean, Fragment{Sel: billingConfig, Tab: "state", Collapse: []string{"network", "edge"}})...)
	s = append(s,
		shot("modules-expanded-dark", "fleet", situationClean, Fragment{
			Sel:      euProd,
			Collapse: []string{"us", "ap", "eu.stage"},
			// The three services of the production environment are siblings
			// of one shape: they open together. The pool of nodes stays one
			// box, and the module is selected so that the inspector shows it.
			Expand: []string{"eu", "eu.prod", "eu.prod.api", "eu.prod.jobs", "eu.prod.web"},
		}),
		shot("clusters-dark", "fleet", situationClean, Fragment{
			Sel:      "eu.prod.nodes.n04",
			Tab:      "state",
			Collapse: []string{"us", "ap", "eu.stage", "eu.prod.api", "eu.prod.web", "eu.prod.jobs"},
			// c:MODULE|MODE|TYPE is the cluster of the resources of one type in a
			// module.
			Expand: []string{"eu", "eu.prod", "c:eu.prod.nodes|managed|null_resource"},
		}),
	)
	s = append(s, both("source", "platform", situationClean, Fragment{Sel: billingConfig, Tab: "source", Collapse: []string{"network", "edge"}})...)
	for i := range s {
		if strings.HasPrefix(s[i].Name, "source-") {
			s[i] = s[i].size(1440, 1000)
		}
	}
	s = append(s, both("plan", "platform", situationPlan, Fragment{Sel: ordersConfig, Tab: "change", Collapse: []string{"network", "edge"}})...)
	// Three people stored a plan each. Those of ana and ben can be applied and
	// both replace what writes the configuration of orders, which carries both
	// badges and the ring of an overlap; the plan of ana is laid over the graph.
	// The third expired, and is laid over the graph in its own picture.
	s = append(s, both("plans", "platform", situationStored, Fragment{Sel: ordersConfig, Tab: "change", Plan: "@ana", Collapse: []string{"network", "edge"}})...)
	s = append(s, shot("plan-expired-dark", "platform", situationStored, Fragment{Plan: "@ci", Collapse: []string{"network", "api"}}))
	s = append(s,
		shot("drift-dark", "platform", situationChanged, Fragment{Sel: dnsRecords, Tab: "state", Filter: "drifted"}),
		shot("sensitive-dark", "platform", situationClean, Fragment{Sel: dbDSN, Tab: "state", Collapse: []string{"network", "edge"}}),
		shot("palette-dark", "platform", situationClean, Fragment{Palette: true, PQ: "billing"}),
		shot("table-dark", "platform", situationChanged, Fragment{Sel: billingConfig, Tab: "state", View: "table"}),
		shot("narrow-dark", "platform", situationClean, Fragment{Sel: billingConfig, Tab: "state", Collapse: []string{"network", "edge"}}).size(900, 1100),
	)
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
