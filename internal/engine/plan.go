package engine

import (
	"encoding/json"

	"cuelang.org/go/cue"
	"github.com/hashicorp/terraform-plugin-go/tftypes"

	"github.com/pravo-systems/pravo/internal/plugin"
	"github.com/pravo-systems/pravo/internal/schema"
)

// Action is what will happen to a resource.
type Action string

const (
	ActionNoop    Action = "no-op"
	ActionCreate  Action = "create"
	ActionUpdate  Action = "update"
	ActionReplace Action = "replace"
	ActionDelete  Action = "delete"
	ActionRead    Action = "read"
)

// RefreshMode controls how prior state is refreshed before planning.
type RefreshMode int

const (
	// RefreshAge refreshes a resource that the plan looks at when its record is
	// older than its refresh age. A plan looks at the resources it reaches and
	// at the ones they read; a record outside that is not read, whatever its age.
	RefreshAge RefreshMode = iota
	// RefreshNone never refreshes.
	RefreshNone
	// RefreshAll refreshes every resource regardless of its refresh age.
	RefreshAll
	// RefreshExpired refreshes every resource whose record is older than its
	// refresh age, whether or not the plan reaches it.
	RefreshExpired
)

// Change is the planned change for one resource or data source.
type Change struct {
	Addr        string
	Mode        string
	Type        string
	Name        string
	Action      Action
	ProviderKey string
	Schema      *schema.Schema

	Config          tftypes.Value
	ConfigHash      string
	Prior           tftypes.Value
	Planned         tftypes.Value
	PlannedCreate   tftypes.Value // for replace: planned state of the new instance
	PriorPrivate    []byte
	PlannedPrivate  []byte
	CreatePrivate   []byte
	RequiresReplace []*tftypes.AttributePath
	Deps            []string

	CreateBeforeDestroy bool
	PreventDestroy      bool
	// Deferred marks a data source whose configuration is not yet known.
	Deferred bool
	// Skipped marks a node that was not planned because nothing about it
	// changed; Prior and Planned hold its recorded state.
	Skipped bool
	// Refreshed reports that the provider was asked for the current state.
	Refreshed bool
	// Kept marks a data source that was read and returned what its record holds,
	// whatever the time of the read: there is nothing to write. The apply leaves
	// the record alone, takes no lock for it and reads it as a record.
	Kept bool
	// Drifted reports that the refresh returned something new.
	Drifted bool
	// Forced marks a replacement that was requested rather than required
	// by a changed attribute.
	Forced bool
	// Fresh reports that a refresh was skipped because the record is within its refresh age.
	Fresh bool
	// Unread marks a node that the plan did not look at: it is skipped, and the
	// provider was not asked about it. Fresh says whether its record is within
	// its refresh age.
	Unread   bool
	Warnings plugin.Diagnostics
	// Applied marks a change of a saved plan that was applied earlier.
	Applied    bool
	AppliedTxn string
	// Imported marks a resource whose object an $import declaration names: the plan
	// read it and stages the record, and the apply writes it. MovedFrom names the previous
	// address of a record that a $from declaration renames; the apply moves it.
	Imported bool
	// ImportID is what the import named: an id, or the identity written as
	// name=value pairs, when ImportIdentity is set.
	ImportID       string
	ImportIdentity bool
	MovedFrom      string
	// Reasons explains why the change is planned, for "pravo why".
	Reasons []string

	// PriorRevision is the revision of the node's record when the plan was made,
	// after the plan wrote what its own reads found: 0 when the node has no
	// record. A saved plan expects the record at this revision, and in this
	// generation: a record that was removed and created again is not the one the
	// plan was made against, whatever its revision.
	PriorRevision   int
	PriorGeneration string

	// removedRevision is the revision of a record that a replacement removed
	// before the new one was written.
	removedRevision int
	// age is the refresh age the node set for itself, "" for the default.
	age string
	// forced is why the plan read the node whatever its refresh age: an apply
	// stopped while it held the node, or its provider points elsewhere now.
	forced string
}

// leftAlone reports whether the plan did nothing with the node: it is skipped,
// and nothing about it shows in a plan.
func (c *Change) leftAlone() bool {
	return c.Skipped && !c.Imported && (c.Action == ActionNoop || c.Action == ActionRead)
}

// Plan is the result of Engine.Plan.
type Plan struct {
	Changes []*Change
	// Policies are the changes the plan makes to the policies in force.
	Policies []PolicyEntry
	// Outputs holds the evaluated output values after planning; non-concrete
	// values are "known after apply".
	Outputs map[string]cue.Value
	// SensitiveOutputs names the outputs that are not shown in listings.
	SensitiveOutputs map[string]bool
	Destroy          bool
	// Partial marks a selection of a saved plan that leaves changes of the
	// plan unapplied.
	Partial bool
	// Stats describe how much of the graph the plan had to walk.
	Stats Stats
	// NoRefresh reports that the plan was made with -refresh=false: nothing was
	// read from the providers.
	NoRefresh bool
	// SavedOutputs and UnknownOutputs stand in for Outputs when the plan was
	// loaded from a file.
	SavedOutputs   map[string]json.RawMessage
	UnknownOutputs []string
	// OldOutputs holds the recorded value, as the state wraps it, of each
	// output that EvalOutputs or DropOutputs names and that has one. A plan
	// shows these outputs, and no others.
	OldOutputs map[string]json.RawMessage
	// Actor that made the plan (from the file, when loaded).
	Actor string
	// Reservation taken by the plan on its pending nodes, if any.
	Reservation *ReservationView
	// Hints are suggestions that do not change the plan, such as a create
	// and a delete that look like one resource under a new address.
	Hints []string
	// Relabels are the records whose provider a provider $from renames: the plan holds the
	// new label in memory, and the apply writes it.
	Relabels []Relabel

	// The apply of a plan walks the nodes the plan changes and the nodes they
	// read, and no others. Write lists the addresses it writes or removes. Inputs
	// maps the address of every record that a node of Write, an output to
	// evaluate or a provider configuration of theirs reads to the revision the
	// plan saw, and InputGenerations to its generation. Support lists the ephemeral resources, function calls and built-in
	// values that the walk runs again. EvalOutputs names the outputs that the
	// apply evaluates; the others keep the value that is recorded. DropOutputs
	// names the recorded outputs that the specification no longer declares; the
	// apply removes them.
	Write            []string
	Inputs           map[string]int
	InputGenerations map[string]string
	Support          []string
	EvalOutputs      []string
	DropOutputs      []string
}

// Stats summarise the work a plan did.
type Stats struct {
	// Total is the number of resources and data sources in configuration.
	Total int `json:"total"`
	// Reached is the number of resources that were planned with the provider. A
	// data source is read, not planned, and counts in Refreshed.
	Reached int `json:"reached"`
	// Skipped is the number of nodes reused from state without planning.
	Skipped int `json:"skipped"`
	// Removed is the number of records of resources that the specification no
	// longer declares and that the plan deletes. Total does not include them;
	// Refreshed, Fresh and NotRead do.
	Removed int `json:"removed,omitempty"`
	// Refreshed is the number of nodes whose state was read from the provider.
	Refreshed int `json:"refreshed"`
	// Fresh is the number of nodes that were not read because the record was within its refresh age.
	Fresh int `json:"fresh"`
	// NotRead is the number of nodes that were not read and whose record was older than its refresh age.
	NotRead int `json:"not_read"`
	// Drifted is the number of refreshed nodes whose state had changed.
	Drifted int `json:"drifted"`
	// Opened is the number of ephemeral resources opened during the run.
	Opened int `json:"opened"`
	// Called is the number of provider function calls made during the run.
	Called int `json:"called"`
	// Imported and Moved count import and moved declarations carried out.
	Imported int `json:"imported"`
	Moved    int `json:"moved"`
}

// Counts summarises a plan.
func (p *Plan) Counts() (add, change, destroy, read int) {
	for _, c := range p.Changes {
		switch c.Action {
		case ActionCreate:
			add++
		case ActionUpdate:
			change++
		case ActionReplace:
			add++
			destroy++
		case ActionDelete:
			destroy++
		case ActionRead:
			if !c.Skipped {
				read++
			}
		}
	}
	return
}

// HasChanges reports whether applying the plan would do anything.
// PendingCount is the number of changes that would act on a provider: those
// not yet applied, excluding no-ops, reads, opens, calls and moves.
func (p *Plan) PendingCount() int {
	n := 0
	for _, c := range p.Changes {
		if c.Applied {
			continue
		}
		if c.Action != ActionNoop && c.Action != ActionRead && c.Action != ActionOpen && c.Action != ActionCall && c.Action != ActionMove {
			n++
		}
	}
	return n
}

// PendingAddrs returns the addresses of the changes that PendingCount counts.
func (p *Plan) PendingAddrs() []string {
	var out []string
	for _, c := range p.Changes {
		if c.Applied {
			continue
		}
		if c.Action != ActionNoop && c.Action != ActionRead && c.Action != ActionOpen && c.Action != ActionCall && c.Action != ActionMove {
			out = append(out, c.Addr)
		}
	}
	return out
}

// HasChanges reports whether applying the plan would change an object. A move and an
// import are not changes of an object: HasWrites counts them.
func (p *Plan) HasChanges() bool {
	return len(p.Policies) > 0 || p.hasProviderChanges()
}

// hasProviderChanges reports whether applying the plan would act on a provider.
func (p *Plan) hasProviderChanges() bool {
	for _, c := range p.Changes {
		if c.Applied {
			continue
		}
		if c.Action != ActionNoop && c.Action != ActionRead && c.Action != ActionOpen && c.Action != ActionCall && c.Action != ActionMove {
			return true
		}
	}
	return false
}
