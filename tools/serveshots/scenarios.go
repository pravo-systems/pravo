package main

import (
	"embed"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"
)

// The scenarios are CUE roots. They are embedded so that the program does not
// depend on the directory it runs in; the files under scenarios/ are also the
// files that the documentation shows behind the screenshots.
//
//go:embed all:scenarios
var scenarioFS embed.FS

// A scenario is an estate and the changes that make the situations of its
// shots.
type scenario struct {
	Name string
	// Base names a scenario whose files are copied first; the files of this
	// scenario are laid over them.
	Base string
	// Resources is the number of resources that pravo apply creates.
	Resources int
	// Disk are changes to the files that the resources wrote, made after the
	// apply and before pravo drift.
	Disk []diskChange
	// Edits change the specification after the apply. They add a resource that
	// is not applied, remove one that is, and change values, so that a plan has
	// something to do.
	Edits []edit
	// Stored are the plans that the stored situation puts in the state, each
	// made by an actor from an edit of the specification that is undone after.
	Stored []storedPlan
	// Team is the team situation: the state in a bucket, sealed to people with
	// roles, and the work they did.
	Team *teamSetup
}

// A storedPlan is a plan that pravo plan --store puts in the state.
type storedPlan struct {
	// Actor makes the plan (--actor) and is its author; a shot names the plan
	// by it, as "@ana".
	Actor  string
	Reason string
	// Edits are made to the specification for the plan, and undone after it.
	Edits []edit
	// Age is how long ago the plan was made. The program changes the times in
	// the objects of the plan, which a command cannot make in the past. A plan
	// that Expired is older than its time to live.
	Age     time.Duration
	Expired bool
}

// A teamSetup is the team situation of a scenario. The state is kept in an
// S3-compatible server that the program runs, sealed to the people, whose
// roles are pinned; the first person is the admin who seals it and reads as
// the viewer. The steps are the work of the team after the first apply, in
// order, and every command that does work is recorded as a run.
type teamSetup struct {
	People []person
	// Catalog is what the fake provider lists before anything is applied: the
	// objects of the cloud that a discovery finds.
	Catalog []catalogObject
	Steps   []teamStep
	// After are edits of the specification made last, so that the next apply
	// has something to carry: a changed policy.
	After []edit
}

// A person is an identity of the team and its role.
type person struct {
	Name string
	// Role is reader, writer or admin.
	Role string
}

// A catalogObject is an object that the fake provider lists.
type catalogObject struct {
	Type   string `json:"type,omitempty"`
	ID     string `json:"id"`
	Name   string `json:"name"`
	Region string `json:"region"`
}

// A teamStep is one piece of the team's work.
type teamStep struct {
	// Kind is what the step does: "discover" runs the discoveries; "change"
	// stores a plan of the edits, has it approved and applies it by its id, and
	// the edits stay; "propose" stores a plan of the edits and undoes them,
	// and the plan waits in the state.
	Kind string
	// Actor is the person who runs the step.
	Actor  string
	Reason string
	Edits  []edit
	// Label names the stored plan of the step for a fragment, as "@label", and
	// its recorded runs, as "@plan:label" and "@apply:label".
	Label string
	// ApproveBy is the person who approves the plan: the admin, for a change,
	// and anyone, for a proposal that is to show as approved.
	ApproveBy string
}

// diskChange changes a file in the workspace the way a person would outside
// pravo.
type diskChange struct {
	Path string
	// Append is written at the end of the file. Remove deletes the file.
	Append string
	Remove bool
	// Addr is the resource whose object the file is: pravo drift has to report
	// it.
	Addr string
}

// edit replaces Old with New in a file of the specification, and Old must
// occur exactly once. An empty Old appends New to the file.
type edit struct {
	File string
	Old  string
	New  string
}

// The edits that the platform and team scenarios share.
var (
	// A new port for orders changes its configuration, which replaces the
	// file, the deploy step that reads its hash and the route at the edge.
	ordersPort = edit{File: "api.cue", Old: "port:     8082", New: "port:     9082"}
	// The reload step of the balancer, which a plan destroys when it is
	// removed from the specification.
	removeReload = edit{File: "edge.cue", Old: `
		reload: $null.#Resource.null_resource & {
			triggers: config_hash: config.content_sha256
		}
`, New: ""}
)

// scenarios lists the estates by name.
var scenarios = []scenario{
	{
		Name:      "platform",
		Resources: 47,
		Disk: []diskChange{
			{Path: "out/network/dns.zone", Append: "# edited by hand\n", Addr: "network.dns.records"},
			{Path: "out/api/cache.conf", Remove: true, Addr: "api.cache.config"},
		},
		Edits: []edit{
			ordersPort,
			// A resource that the specification declares and no apply created.
			{File: "edge.cue", New: `
edge: waf: rules: $local.#Resource.local_file & {
	filename: "out/edge/waf.rules"
	content:  "block bad-bots\n"
}
`},
			// A resource that the state records and the specification does not
			// declare any more.
			removeReload,
		},
		// Two plans of two people that both replace what writes orders, and a
		// third that nobody applied in time.
		Stored: []storedPlan{
			{Actor: "ana", Reason: "move orders to port 9082", Age: 3 * time.Hour,
				Edits: []edit{ordersPort}},
			{Actor: "ben", Reason: "new ports for orders and search", Age: 40 * time.Minute,
				Edits: []edit{
					{File: "api.cue", Old: "port:     8082", New: "port:     9092"},
					{File: "api.cue", Old: "port:     8083", New: "port:     8084"},
				}},
			{Actor: "ci", Reason: "listen on 8443 at the edge", Age: 9 * 24 * time.Hour, Expired: true,
				Edits: []edit{{File: "edge.cue", Old: `listen 443\n`, New: `listen 8443\n`}}},
		},
	},
	{
		Name:      "fleet",
		Resources: 143,
	},
	{
		// The platform estate as a team runs it: the state in a bucket, sealed
		// to three people with roles, three policies in force, and two buckets
		// of a provider that can list what exists, so that a discovery has
		// something to find.
		Name:      "team",
		Base:      "platform",
		Resources: 49,
		Team: &teamSetup{
			People: []person{{"ada", "admin"}, {"ben", "writer"}, {"cy", "reader"}},
			Catalog: []catalogObject{
				{Type: "demo_bucket", ID: "bkt-platform-logs", Name: "platform-logs", Region: "eu"},
				{Type: "demo_bucket", ID: "bkt-platform-export-2026-10", Name: "platform-export-2026-10", Region: "eu"},
				{Type: "demo_bucket", ID: "bkt-legacy-uploads", Name: "legacy-uploads", Region: "eu"},
				{Type: "demo_bucket", ID: "bkt-tmp-migration", Name: "tmp-migration", Region: "eu"},
				{Type: "demo_bucket", ID: "bkt-us-archive", Name: "us-archive", Region: "us"},
			},
			Steps: []teamStep{
				{Kind: "discover", Actor: "ada"},
				// A change that went through: ben stored it, ada approved it,
				// since every replacement needs an admin, and ben applied it.
				{Kind: "change", Actor: "ben", Reason: "search moves to port 8084", Label: "search", ApproveBy: "ada",
					Edits: []edit{{File: "api.cue", Old: "port:     8083", New: "port:     8084"}}},
				// A proposal that waits for an admin.
				{Kind: "propose", Actor: "ben", Reason: "retire the reload step of the balancer", Label: "reload",
					Edits: []edit{removeReload}},
				// A proposal of a reader, approved and not applied yet.
				{Kind: "propose", Actor: "cy", Reason: "move orders to port 9082", Label: "orders", ApproveBy: "ada",
					Edits: []edit{ordersPort}},
			},
			// A stricter rule, written and not applied: the next apply carries it.
			After: []edit{{File: "policies.cue", Old: `approvals: {min: 1, role: "admin"}`, New: `approvals: {min: 2, role: "admin"}`}},
		},
	},
}

// findScenario returns the scenario with the name.
func findScenario(name string) (scenario, bool) {
	for _, s := range scenarios {
		if s.Name == name {
			return s, true
		}
	}
	return scenario{}, false
}

// hasChanges reports whether the scenario has a changed situation.
func (s scenario) hasChanges() bool { return len(s.Disk) > 0 || len(s.Edits) > 0 }

// hasStored reports whether the scenario has a stored situation.
func (s scenario) hasStored() bool { return len(s.Stored) > 0 }

// hasTeam reports whether the scenario has a team situation.
func (s scenario) hasTeam() bool { return s.Team != nil }

// planLabels are the names that a fragment may give a stored plan of the
// scenario, as "@name": the actors of the stored situation and the labels of
// the team's steps.
func (s scenario) planLabels() []string {
	var out []string
	for _, sp := range s.Stored {
		out = append(out, sp.Actor)
	}
	if s.Team != nil {
		for _, st := range s.Team.Steps {
			if st.Label != "" {
				out = append(out, st.Label)
			}
		}
	}
	return out
}

// traceLabels are the names that a fragment may give a recorded run of the
// team situation, as "@name": "apply" for the first apply, "discover", and
// "plan:LABEL" and "apply:LABEL" for the steps.
func (s scenario) traceLabels() []string {
	if s.Team == nil {
		return nil
	}
	out := []string{"apply"}
	for _, st := range s.Team.Steps {
		switch st.Kind {
		case "discover":
			out = append(out, "discover")
		case "change":
			out = append(out, "plan:"+st.Label, "apply:"+st.Label)
		case "propose":
			out = append(out, "plan:"+st.Label)
		}
	}
	return out
}

// files is where the files of the scenario are, with the base first.
func (s scenario) files() ([]string, error) {
	var roots []string
	if s.Base != "" {
		base, ok := findScenario(s.Base)
		if !ok {
			return nil, fmt.Errorf("scenario %s: base %q is not a scenario", s.Name, s.Base)
		}
		if base.Base != "" {
			return nil, fmt.Errorf("scenario %s: base %q has a base of its own", s.Name, s.Base)
		}
		roots = append(roots, path.Join("scenarios", base.Name))
	}
	return append(roots, path.Join("scenarios", s.Name)), nil
}

// copyScenario writes the files of the scenario into dir, which exists.
func copyScenario(name, dir string) error {
	sc, ok := findScenario(name)
	if !ok {
		return fmt.Errorf("scenario %s: unknown", name)
	}
	roots, err := sc.files()
	if err != nil {
		return err
	}
	for _, root := range roots {
		if _, err := fs.Stat(scenarioFS, root); err != nil {
			return fmt.Errorf("scenario %s: %w", name, err)
		}
		err := fs.WalkDir(scenarioFS, root, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			rel := strings.TrimPrefix(strings.TrimPrefix(p, root), "/")
			target := filepath.Join(dir, filepath.FromSlash(rel))
			if d.IsDir() {
				return os.MkdirAll(target, 0o755)
			}
			data, err := scenarioFS.ReadFile(p)
			if err != nil {
				return err
			}
			return os.WriteFile(target, data, 0o644)
		})
		if err != nil {
			return err
		}
	}
	return nil
}

// applyEdits makes the edits in the files below dir.
func applyEdits(dir string, edits []edit) error {
	for _, e := range edits {
		p := filepath.Join(dir, filepath.FromSlash(e.File))
		data, err := os.ReadFile(p)
		if err != nil {
			return fmt.Errorf("edit of %s: %w", e.File, err)
		}
		out, err := e.apply(string(data))
		if err != nil {
			return err
		}
		if err := os.WriteFile(p, []byte(out), 0o644); err != nil {
			return err
		}
	}
	return nil
}

// apply returns text with the edit made.
func (e edit) apply(text string) (string, error) {
	if e.Old == "" {
		return text + e.New, nil
	}
	if n := strings.Count(text, e.Old); n != 1 {
		return "", fmt.Errorf("edit of %s: the text to replace occurs %d times, not once: %q", e.File, n, firstLine(e.Old))
	}
	return strings.Replace(text, e.Old, e.New, 1), nil
}

func firstLine(s string) string {
	s = strings.TrimLeft(s, "\n")
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

// applyDisk makes the changes to the files that the resources wrote.
func applyDisk(dir string, changes []diskChange) error {
	for _, c := range changes {
		p := filepath.Join(dir, filepath.FromSlash(c.Path))
		if c.Remove {
			if err := os.Remove(p); err != nil {
				return fmt.Errorf("remove %s: %w", c.Path, err)
			}
			continue
		}
		f, err := os.OpenFile(p, os.O_WRONLY|os.O_APPEND, 0)
		if err != nil {
			return fmt.Errorf("change %s: %w", c.Path, err)
		}
		_, werr := f.WriteString(c.Append)
		if cerr := f.Close(); werr == nil {
			werr = cerr
		}
		if werr != nil {
			return fmt.Errorf("change %s: %w", c.Path, werr)
		}
	}
	return nil
}
