package engine

import (
	"encoding/json"
	"sort"
	"time"

	"github.com/hashicorp/terraform-plugin-go/tftypes"

	"pravo.dev/pravo/internal/convert"
)

// ViewFormat is the layout version of the plan document.
const ViewFormat = 1

// View is the machine-readable form of a plan.
type View struct {
	Format      int               `json:"format"`
	Created     time.Time         `json:"created"`
	Actor       string            `json:"actor,omitempty"`
	Reason      string            `json:"reason,omitempty"`
	GitRevision string            `json:"git_revision,omitempty"`
	TraceID     string            `json:"trace_id,omitempty"`
	Destroy     bool              `json:"destroy,omitempty"`
	Summary     Summary           `json:"summary"`
	Stats       Stats             `json:"stats"`
	Changes     []ChangeView      `json:"changes"`
	Outputs     map[string]any    `json:"outputs"`
	Reservation *ReservationView  `json:"reservation,omitempty"`
	Hints       []string          `json:"hints,omitempty"`
	Providers   map[string]string `json:"providers,omitempty"`

	// skipped reports that Changes lists the nodes the plan left alone.
	skipped bool
}

// ViewOptions choose which nodes a plan document lists. The document lists the
// nodes the plan ran: the ones it changes, deletes, moves or imports, and the
// ephemeral resources it opens and the functions it calls.
type ViewOptions struct {
	// Skipped lists the other nodes too, each as an entry that holds what the
	// state records of the node and nothing the plan decided: its address, mode,
	// type and name, the action no-op, and skipped.
	Skipped bool
	// Full gives the entries of the nodes the plan walked and left alone what the
	// plan knows of them: their reasons and the nodes they depend on.
	Full bool
}

// Summary counts the actions of a plan.
type Summary struct {
	Add     int `json:"add"`
	Change  int `json:"change"`
	Destroy int `json:"destroy"`
	Read    int `json:"read"`
	// BlastRadius is the number of distinct nodes a change touches,
	// including the dependents recorded in state of every changed node.
	BlastRadius int `json:"blast_radius"`
}

// ChangeView is one change of a plan as data. Unknown values render as
// {"$unknown": true}; sensitive values are not masked in JSON, since the
// consumer is a machine that already holds the specification.
type ChangeView struct {
	Addr            string   `json:"addr"`
	Mode            string   `json:"mode"`
	Type            string   `json:"type"`
	Name            string   `json:"name"`
	Action          Action   `json:"action"`
	Provider        string   `json:"provider,omitempty"`
	Reasons         []string `json:"reasons,omitempty"`
	RequiresReplace []string `json:"requires_replace,omitempty"`
	Before          any      `json:"before,omitempty"`
	After           any      `json:"after,omitempty"`
	Deferred        bool     `json:"deferred,omitempty"`
	Skipped         bool     `json:"skipped,omitempty"`
	Refreshed       bool     `json:"refreshed,omitempty"`
	Drifted         bool     `json:"drifted,omitempty"`
	Applied         bool     `json:"applied,omitempty"`
	Dependencies    []string `json:"dependencies,omitempty"`
	Dependents      []string `json:"dependents,omitempty"`
	Warnings        []string `json:"warnings,omitempty"`
}

// ReservationView describes the reservation a plan holds on its nodes.
type ReservationView struct {
	// Until is the earliest end among the nodes: a reservation the actor
	// already held for longer keeps its later end.
	Until time.Time `json:"until"`
	Addrs []string  `json:"addrs"`
}

// Unknown is the JSON marker for a value known only after apply.
var Unknown = map[string]any{"$unknown": true}

// ToData converts a value with unknowns to plain data for JSON.
func ToData(v tftypes.Value) any {
	if v.Type() == nil {
		return nil
	}
	if !v.IsKnown() {
		return Unknown
	}
	if v.IsNull() {
		return nil
	}
	t := v.Type()
	switch {
	case t.Is(tftypes.List{}) || t.Is(tftypes.Set{}) || t.Is(tftypes.Tuple{}):
		var elems []tftypes.Value
		_ = v.As(&elems)
		out := make([]any, 0, len(elems))
		for _, e := range elems {
			out = append(out, ToData(e))
		}
		return out
	case t.Is(tftypes.Map{}) || t.Is(tftypes.Object{}):
		var m map[string]tftypes.Value
		_ = v.As(&m)
		out := make(map[string]any, len(m))
		for k, e := range m {
			out[k] = ToData(e)
		}
		return out
	}
	g, err := convert.ToGo(v)
	if err != nil {
		return Unknown
	}
	if n, ok := g.(json.Number); ok {
		return n
	}
	return g
}

// NewView renders the plan as data. The recorded dependents of the changed
// nodes give the blast radius.
func (e *Engine) NewView(plan *Plan, opts ViewOptions) *View {
	v := &View{Format: ViewFormat, Created: time.Now().UTC(), Actor: e.Opts.Actor, Reason: e.Opts.Reason,
		GitRevision: e.Opts.GitRevision, TraceID: e.traceID, Destroy: plan.Destroy, Stats: plan.Stats, Outputs: map[string]any{},
		Providers: map[string]string{}, Hints: plan.Hints, skipped: opts.Skipped}
	for local, rp := range e.Providers.Resolved {
		v.Providers[local] = rp.Version
	}
	v.Changes = []ChangeView{}
	v.Summary.Add, v.Summary.Change, v.Summary.Destroy, v.Summary.Read = plan.Counts()
	touched := map[string]bool{}
	var changed []string
	for _, ch := range plan.Changes {
		if ch.leftAlone() {
			if opts.Skipped && !opts.Full {
				v.Changes = append(v.Changes, skippedEntry(ch.Addr, ch.Mode, ch.Type, ch.Name))
			}
			if !opts.Skipped || !opts.Full {
				continue
			}
		}
		cv := ChangeView{Addr: ch.Addr, Mode: ch.Mode, Type: ch.Type, Name: ch.Name, Action: ch.Action, Provider: ch.ProviderKey,
			Reasons: ch.Reasons, Deferred: ch.Deferred, Skipped: ch.Skipped, Refreshed: ch.Refreshed, Drifted: ch.Drifted,
			Applied: ch.Applied, Dependencies: ch.Deps}
		for _, p := range ch.RequiresReplace {
			if steps := p.Steps(); len(steps) > 0 {
				if n, ok := steps[0].(tftypes.AttributeName); ok {
					cv.RequiresReplace = append(cv.RequiresReplace, string(n))
				}
			}
		}
		for _, w := range ch.Warnings {
			cv.Warnings = append(cv.Warnings, w.Summary)
		}
		switch ch.Action {
		case ActionCreate:
			cv.After = ToData(ch.Planned)
		case ActionUpdate:
			cv.Before, cv.After = ToData(ch.Prior), ToData(ch.Planned)
		case ActionReplace:
			cv.Before = ToData(ch.Prior)
			if ch.PlannedCreate.Type() != nil {
				cv.After = ToData(ch.PlannedCreate)
			} else {
				cv.After = ToData(ch.Planned)
			}
		case ActionDelete:
			cv.Before = ToData(ch.Prior)
		case ActionRead:
			if !ch.Deferred {
				cv.After = ToData(ch.Planned)
			}
		}
		if pending(ch) && !ch.Applied {
			touched[ch.Addr] = true
			changed = append(changed, ch.Addr)
			if e.State != nil {
				cv.Dependents = e.State.Dependents(ch.Addr)
			}
		}
		v.Changes = append(v.Changes, cv)
	}
	if e.State != nil {
		// What all the changes reach is found in one walk of the recorded edges.
		for _, d := range e.State.ReachOf(changed) {
			touched[d] = true
		}
	}
	v.Summary.BlastRadius = len(touched)
	if opts.Skipped {
		v.Changes = e.withSkipped(v.Changes)
	}
	names := make([]string, 0, len(plan.Outputs))
	for n := range plan.Outputs {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		ov := plan.Outputs[n]
		if convert.IsConcrete(ov) {
			if b, err := outputJSON(ov); err == nil {
				v.Outputs[n] = json.RawMessage(b)
				continue
			}
		}
		v.Outputs[n] = Unknown
	}
	for n, raw := range plan.SavedOutputs {
		v.Outputs[n] = json.RawMessage(raw)
	}
	for _, n := range plan.UnknownOutputs {
		v.Outputs[n] = Unknown
	}
	return v
}

// skippedEntry is the entry of a node that the plan left alone.
func skippedEntry(addr, mode, typ, name string) ChangeView {
	return ChangeView{Addr: addr, Mode: mode, Type: typ, Name: name, Action: ActionNoop, Skipped: true}
}

// withSkipped adds an entry for every declared node that has a record and no
// entry, from what the record says of it, so that the document lists each node
// of the specification. The order is the one of a plan: the moves, then the
// declared nodes in the order of the specification, then the deletes of records
// the specification no longer declares.
func (e *Engine) withSkipped(changes []ChangeView) []ChangeView {
	if e.State == nil || e.Root == nil {
		return changes
	}
	have := make(map[string]int, len(changes))
	for i, c := range changes {
		if c.Action != ActionMove {
			have[c.Addr] = i
		}
	}
	missing := false
	for _, r := range e.Root.Resources {
		if _, ok := have[r.Address()]; !ok && e.State.Get(r.Address()) != nil {
			missing = true
			break
		}
	}
	if !missing {
		return changes
	}
	declared := make(map[string]bool, len(e.Root.Resources))
	out := make([]ChangeView, 0, len(changes)+len(e.Root.Resources))
	for _, c := range changes {
		if c.Action == ActionMove {
			out = append(out, c)
		}
	}
	for _, r := range e.Root.Resources {
		addr := r.Address()
		declared[addr] = true
		if i, ok := have[addr]; ok {
			out = append(out, changes[i])
		} else if sr := e.State.Get(addr); sr != nil {
			out = append(out, skippedEntry(addr, sr.Mode, sr.Type, sr.Name))
		}
	}
	for _, c := range changes {
		if c.Action != ActionMove && !declared[c.Addr] {
			out = append(out, c)
		}
	}
	return out
}
