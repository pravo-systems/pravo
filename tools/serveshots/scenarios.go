package main

import (
	"embed"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
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
	// Resources is the number of resources that pravo apply creates.
	Resources int
	// Disk are changes to the files that the resources wrote, made after the
	// apply and before pravo drift.
	Disk []diskChange
	// Edits change the specification after the apply. They add a resource that
	// is not applied, remove one that is, and change values, so that a plan has
	// something to do.
	Edits []edit
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
			// A new port for orders changes its configuration, which replaces the
			// file, the deploy step that reads its hash and the route at the edge.
			{File: "api.cue", Old: "port:     8082", New: "port:     9082"},
			// A resource that the specification declares and no apply created.
			{File: "edge.cue", New: `
edge: waf: rules: $local.#Resource.local_file & {
	filename: "out/edge/waf.rules"
	content:  "block bad-bots\n"
}
`},
			// A resource that the state records and the specification no longer
			// declares.
			{File: "edge.cue", Old: `
		reload: $null.#Resource.null_resource & {
			triggers: config_hash: config.content_sha256
		}
`, New: ""},
		},
	},
	{
		Name:      "fleet",
		Resources: 143,
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

// copyScenario writes the files of the scenario into dir, which exists.
func copyScenario(name, dir string) error {
	root := path.Join("scenarios", name)
	if _, err := fs.Stat(scenarioFS, root); err != nil {
		return fmt.Errorf("scenario %s: %w", name, err)
	}
	return fs.WalkDir(scenarioFS, root, func(p string, d fs.DirEntry, err error) error {
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
