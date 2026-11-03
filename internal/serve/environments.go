package serve

import (
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"time"

	"github.com/pravo-systems/pravo/internal/config"
)

// The environments of a specification that declares $environments. The viewer reads one of them
// at a time: the one that --env NAME named, else the default, else the first of the promotion flow. The
// page can switch to another, which loads the specification for it and opens its state, as a
// command run with --env NAME would; nothing is written. The Environments page shows the
// promotion flow: what each environment runs and how the release of the directory stands in each, which
// the command (Options.Environments) reads from the states without a key.

// Environments is the answer of GET /api/environments.
type Environments struct {
	Version  int    `json:"version"`
	Revision string `json:"revision"`
	// Selected is the environment the page reads, and Flow the environments in order, as
	// "dev → staging → {prod-eu, prod-us}".
	Selected string `json:"selected"`
	Flow     string `json:"flow"`
	// Release is the specification of the directory, by its fingerprint, with the names of the
	// tags it is read with (never their values).
	Release      EnvironmentRelease `json:"release"`
	Environments []EnvironmentEntry `json:"environments"`
	// Next is the command that moves the release one step along, or a sentence when every
	// environment runs it.
	Next string `json:"next"`
	// Waiting counts the environments where someone has something to do for the release: it was
	// promoted and is not applied, or every environment before runs it.
	Waiting int `json:"waiting"`
	// Error says why the environments could not be read; the rest is then empty.
	Error string `json:"error,omitempty"`
}

// EnvironmentRelease names the release of the directory.
type EnvironmentRelease struct {
	Fingerprint string   `json:"fingerprint"`
	Tags        []string `json:"tags"`
}

// EnvironmentEntry is one environment of the promotion flow.
type EnvironmentEntry struct {
	Name    string   `json:"name"`
	After   []string `json:"after"`
	Default bool     `json:"default,omitempty"`
	// Depth is the step of the promotion flow: 0 for an environment that follows none.
	Depth int `json:"depth"`
	// State is where its state is kept; Sealed whether it is sealed.
	State  string `json:"state,omitempty"`
	Sealed bool   `json:"sealed"`
	// Runs is the newest note of what the state runs.
	Runs *EnvironmentRun `json:"runs,omitempty"`
	// Release is how the release of the directory stands here: "runs", "partial" (its apply
	// deferred changes to the next plan), "promoted", "waived", "ready" (every environment
	// before runs it) or empty.
	Release  string                `json:"release,omitempty"`
	Promoted *EnvironmentPromotion `json:"promoted,omitempty"`
	// OtherTags is set when it runs the specification of the directory with other tags.
	OtherTags bool `json:"other_tags,omitempty"`
	// Problem says why the state cannot be read here, or what is wrong with it.
	Problem string `json:"problem,omitempty"`
	// Command is the command that moves the release into it, when it is the next step.
	Command string `json:"command,omitempty"`
}

// EnvironmentRun is the note of what a state runs.
type EnvironmentRun struct {
	Fingerprint string    `json:"fingerprint"`
	GitRevision string    `json:"git_revision,omitempty"`
	At          time.Time `json:"at"`
	By          string    `json:"by,omitempty"`
	Verified    bool      `json:"verified"`
	// Deferred is the number of changes the apply deferred to the next plan.
	Deferred int `json:"deferred,omitempty"`
}

// EnvironmentPromotion is the promotion of the release into an environment.
type EnvironmentPromotion struct {
	Kind string    `json:"kind"`
	By   string    `json:"by"`
	At   time.Time `json:"at"`
	Note string    `json:"note,omitempty"`
}

// environment is the environment the viewer reads, empty for a specification without any.
func (s *Server) environment() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.envName
}

// loadOptions are the options the specification is loaded with: the tags of the command, with
// the environment the page chose.
func (s *Server) loadOptions() Options {
	o := s.opts
	if env := s.environment(); env != "" {
		o.Tags = config.WithEnvironment(o.Tags, env)
		return o
	}
	if _, given := config.EnvironmentOf(o.Tags); given {
		return o
	}
	// No environment was named: the default one, else the first of the promotion flow, so that a
	// specification whose environments have no default still shows.
	list, err := config.ReadEnvironmentSettings(o.Dir, config.LoadOptions{Tags: o.Tags, SysTags: o.SysTags, Env: o.Env})
	if err != nil || len(list) == 0 {
		return o
	}
	name := list[0].Name
	for _, es := range list {
		if es.Default {
			name = es.Name
		}
	}
	o.Tags = config.WithEnvironment(o.Tags, name)
	return o
}

// environmentsHandler answers GET /api/environments, from the states of the environments as the
// command reads them, once for each model.
func (s *Server) environmentsHandler(w http.ResponseWriter, r *http.Request, m *model) {
	writeRaw(w, http.StatusOK, m.environmentsJSON(r.Context()))
}

func (m *model) environmentsJSON(ctx context.Context) []byte {
	m.envOnce.Do(func() {
		doc := &Environments{Version: APIVersion, Revision: m.rev, Environments: []EnvironmentEntry{}, Release: EnvironmentRelease{Tags: []string{}}}
		root := m.spec.root
		switch {
		case root == nil || root.Environment == "":
		case m.srv.opts.Environments == nil:
			doc.Error = "the environments are not read by this viewer"
		default:
			doc.Selected, doc.Flow = root.Environment, config.FlowText(root.Environments)
			m.spec.cueMu.Lock()
			got, err := m.srv.opts.Environments(ctx, root)
			m.spec.cueMu.Unlock()
			if err != nil {
				doc.Error = m.scrubText(cleanReason(err.Error()))
			} else if got != nil {
				got.Version, got.Revision, got.Selected, got.Flow = doc.Version, doc.Revision, doc.Selected, doc.Flow
				got.Next = m.scrubText(got.Next)
				for i := range got.Environments {
					e := &got.Environments[i]
					e.Problem, e.Command = m.scrubText(e.Problem), m.scrubText(e.Command)
					if e.Promoted != nil {
						e.Promoted.Note = m.scrubText(e.Promoted.Note)
					}
				}
				doc = got
			}
		}
		m.envJSON, _ = json.Marshal(doc)
	})
	return m.envJSON
}

// switchEnvironment answers POST /api/environment?name=NAME: the page reads that environment
// from now on. The name must be one the specification declares.
func (s *Server) switchEnvironment(w http.ResponseWriter, r *http.Request, m *model) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "switch the environment with POST")
		return
	}
	name := r.URL.Query().Get("name")
	root := m.spec.root
	if root == nil || !slices.Contains(root.EnvironmentNames(), name) {
		writeError(w, http.StatusBadRequest, "not an environment of this specification")
		return
	}
	s.mu.Lock()
	s.envName = name
	s.mu.Unlock()
	s.watch.setEnvironment(name)
	rev, _ := s.watch.sample()
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "environment": name, "revision": rev})
}
