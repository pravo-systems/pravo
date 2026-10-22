// Package engine plans and applies pravo configurations by driving provider
// plugins according to a dependency graph derived from CUE references.
//
// Plans are scoped: a resource whose configuration hash, schema and provider
// version are unchanged in state, and whose record carries no mark, is not
// planned at all. Its recorded state is used as-is. A plan reads the records of
// the resources it reaches and of the resources they read, when they are older
// than their refresh age, and no others. The work of a plan with the providers,
// the locks and the records follows the change, not the size of the state;
// evaluating the specification and opening the state still follow the size of
// the estate. The apply of a plan walks the nodes the plan changes and the nodes
// they read.
package engine

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"cuelang.org/go/cue"
	"github.com/hashicorp/terraform-plugin-go/tftypes"

	"github.com/pravo-systems/pravo/internal/config"
	"github.com/pravo-systems/pravo/internal/convert"
	"github.com/pravo-systems/pravo/internal/plog"
	"github.com/pravo-systems/pravo/internal/plugin"
	"github.com/pravo-systems/pravo/internal/schema"
	"github.com/pravo-systems/pravo/internal/state"
	"github.com/pravo-systems/pravo/internal/telemetry"
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
	// Imported marks a resource whose record was created from an "import"
	// declaration during this plan; MovedFrom names the previous address of
	// a record renamed by a $from declaration.
	Imported  bool
	ImportID  string
	MovedFrom string
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
	// Actor that made the plan (from the file, when loaded).
	Actor string
	// Reservation taken by the plan on its pending nodes, if any.
	Reservation *ReservationView
	// Hints are suggestions that do not change the plan, such as a create
	// and a delete that look like one resource under a new address.
	Hints []string

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
	// Reached is the number of nodes that were planned with the provider.
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

func (p *Plan) HasChanges() bool {
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

// Options tune a run.
type Options struct {
	Refresh RefreshMode
	// NoReadBack stops an apply from reading back the nodes it wrote. The tests that
	// count the reads of an apply use it; no command sets it.
	NoReadBack bool
	// DefaultRefresh is the refresh age for resources that set none
	// ("always", "never" or a duration such as "30m"); empty means always.
	DefaultRefresh string
	// Full plans every resource with the provider, as OpenTofu does, instead
	// of skipping unchanged nodes.
	Full bool
	// Replace lists resource addresses to destroy and create again even
	// though the specification did not change.
	Replace  []string
	Destroy  bool
	Command  string
	Progress func(format string, args ...any)
	// Event receives structured progress; nil disables it.
	Event func(Event)
	// Actor, Reason and GitRevision are recorded on transactions, locks and
	// plan files for attribution.
	Actor       string
	Reason      string
	GitRevision string
	// Reserve, when positive, makes Plan take reservations of this duration
	// on every node with a pending change.
	Reserve time.Duration
}

// Engine runs plans and applies.
type Engine struct {
	Root      *config.Root
	State     *state.Store
	Providers *Providers
	Schemas   map[string]*schema.ProviderSchema
	Value     cue.Value
	Opts      Options

	// policies are the plan policies named by the invoker.
	policies []*Policy

	mu      sync.Mutex
	changes map[string]*Change
	deps    map[string][]string
	byPath  map[string]*config.Resource
	// reads holds what this plan read from the providers, by address, so that a
	// node is read once: the next walk of the graph and the apply that follows
	// use the answer.
	reads map[string]*readResult
	// dataReads holds what this plan read from the data sources, by address, with
	// the hash of the configuration that was read.
	dataReads map[string]dataRead
	// imported holds the id of each import made by this plan, by address.
	imported map[string]string
	// ages holds the refresh ages that were parsed, by their text.
	ages map[string]parsedAge
	// now is the clock that records are aged against; nil is the wall clock.
	now func() time.Time
	// belowIdx lists, for every path that holds tasks without being one, the
	// tasks below it; it is built on first use.
	belowOnce sync.Once
	belowIdx  map[string][]cue.Path

	// cueMu serialises all CUE evaluation; current is the configuration
	// with the settled fills applied. pending holds the fills of finished
	// tasks that are not unified into current yet, by address, in the order
	// they were made: see fill and settle.
	cueMu        sync.Mutex
	current      cue.Value
	pending      map[string]pendingFill
	pendingOrder []string
	// seeded reports that the first batch of fills of the run carried the seed
	// fills; see seedFills. iterated holds the containers of tasks that something
	// reads, by path.
	seeded   bool
	iterMu   sync.Mutex
	iterated map[string]bool
	// settles counts the times pending fills were unified into current.
	settles int
	// providerDeps lists, per provider key, the tasks its configuration
	// references.
	providerDeps map[string][]string
	// readers holds the tasks that something reads the values of: another task,
	// the configuration of a provider or an output. The graph sets it.
	readers map[string]bool
	// graph is the dependency graph, built on first use: the specification does
	// not change during a run, so every walk of the graph uses it. outputDeps
	// lists, per output, the tasks its value reads.
	graph      map[string]*node
	outputDeps map[string][]string
	// outputReads lists, per output, the tasks its value reads; see
	// outputReadsLocked. graphBuilds counts the times the graph was worked out.
	outputReads map[string][]string
	graphBuilds int

	txn   *state.Txn
	stats Stats
	// saved holds the changes of a plan being applied, by address; only lists the
	// resources to apply, the rest are taken from state. approved reports that
	// the plan was made by this engine and not read from a file.
	saved    map[string]*Change
	only     map[string]bool
	approved bool
	// scope limits the apply walk to the nodes the plan changes and the nodes
	// they read.
	scope *applyScope
	// held is the set of locks the apply holds on the nodes it writes.
	held *heldLocks
	// onTask, when set, is told the address of each task a walk starts, onConvert
	// the address of each configuration that is converted for its provider and
	// onFill the address of each task whose values are queued for the
	// configuration; adjustScope, when set, may change the scope before the apply
	// walks it. Tests set them.
	onTask      func(addr string, apply bool)
	onConvert   func(addr string, apply bool)
	onFill      func(addr string)
	adjustScope func(*applyScope)
	// readOnce, when set, stands in for the read of the inputs of the reached
	// nodes that readInputs makes in each round. Tests set it.
	readOnce func(ctx context.Context) ([]string, error)
	// suspects holds the stale locks the plan found, by address: the node was
	// locked by an apply that stopped. targets holds, by provider key, the digest
	// of the configuration of the provider instance for the walk that is running.
	suspects map[string]state.Lock
	targets  map[string]*target
	// flying holds the nodes whose provider is changing the object right now, by
	// address, with what it is doing; unsettled holds what is said of the ones
	// whose result is not known; interrupt is the handle of the signals of the
	// command, if it has one.
	flying    map[string]string
	unsettled map[string]string
	interrupt *Interrupt
	// completed holds the nodes whose change the current apply finished and
	// recorded, by address.
	completed map[string]bool
	// open lists the ephemeral resources opened during the current run. keepOpen
	// makes a run leave them open for its caller to close.
	open     []*openEphemeral
	keepOpen bool
	// traceID is the OpenTelemetry trace of the current run, if any.
	traceID string
}

// New creates an engine. value must already have provider schemas unified in.
func New(root *config.Root, st *state.Store, providers *Providers, schemas map[string]*schema.ProviderSchema, value cue.Value, opts Options) *Engine {
	if opts.Progress == nil {
		opts.Progress = func(string, ...any) {}
	}
	if opts.Command == "" {
		opts.Command = "pravo"
	}
	e := &Engine{Root: root, State: st, Providers: providers, Schemas: schemas, Value: value, Opts: opts,
		changes: map[string]*Change{}, deps: map[string][]string{}, byPath: map[string]*config.Resource{},
		current: value}
	for _, r := range root.Resources {
		e.byPath[r.Path.String()] = r
	}
	st.SetActor(opts.Actor, opts.Reason, opts.GitRevision)
	providers.ConfigValue = value
	providers.ConfigConvert = func(key string, blk *schema.Block) (tftypes.Value, error) {
		cfg, _, _, err := e.providerConfig(key, blk)
		return cfg, err
	}
	return e
}

func (e *Engine) resourceSchema(r *config.Resource) (*schema.Schema, error) {
	ps, ok := e.Schemas[r.LocalProvider()]
	if !ok {
		return nil, fmt.Errorf("%s: no schema for provider %s", r.Address(), r.LocalProvider())
	}
	var s *schema.Schema
	switch r.Mode {
	case "data":
		s = ps.DataSources[r.Type]
	case "ephemeral":
		s = ps.Ephemerals[r.Type]
	default:
		s = ps.Resources[r.Type]
	}
	if s == nil {
		return nil, fmt.Errorf("%s: provider %s has no %s type %q", r.Address(), r.LocalProvider(), r.Mode, r.Type)
	}
	return s, nil
}

func (e *Engine) providerVersion(key string) string {
	if rp, ok := e.Providers.Resolved[config.LocalName(key)]; ok {
		return rp.Version
	}
	return ""
}

func (e *Engine) txnID() string {
	if e.txn == nil {
		return ""
	}
	return e.txn.ID()
}

// checkReplace refuses -replace addresses that cannot name a replacement: the
// resource must be a managed resource of the specification and exist in the
// state.
func (e *Engine) checkReplace() error {
	if len(e.Opts.Replace) == 0 {
		return nil
	}
	if e.Opts.Destroy {
		return fmt.Errorf("-replace cannot be combined with -destroy")
	}
	for _, addr := range e.Opts.Replace {
		r := e.Root.Resource(addr)
		switch {
		case r == nil:
			return fmt.Errorf("-replace %s: the specification declares no such resource", addr)
		case r.Mode != "managed":
			return fmt.Errorf("-replace %s: only managed resources can be replaced", addr)
		case e.State.Get(addr) == nil:
			return fmt.Errorf("-replace %s: it is not in the state, so there is nothing to replace; apply creates it", addr)
		}
	}
	return nil
}

// checkDestroy refuses a destroy of a specification that cannot be walked or
// that protects a resource: a dependency cycle, or $lifecycle.prevent_destroy
// on a resource that is still declared and recorded in the state. A record
// whose resource was removed from the specification has no lifecycle to read
// and is destroyed.
func (e *Engine) checkDestroy() error {
	e.current = e.Value
	if _, err := e.buildGraph(); err != nil {
		return err
	}
	return e.checkPreventDestroy()
}

func (e *Engine) checkPreventDestroy() error {
	var held []string
	e.cueMu.Lock()
	for _, sr := range e.State.List() {
		r := e.Root.Resource(sr.Address())
		if r == nil || r.Mode != "managed" || sr.Mode != "managed" {
			continue
		}
		if _, prevent, _, _ := lifecycle(e.Value.LookupPath(r.Path)); prevent {
			held = append(held, sr.Address())
		}
	}
	e.cueMu.Unlock()
	if len(held) > 0 {
		return fmt.Errorf("destroy refused: $lifecycle.prevent_destroy is set on %s; remove the setting from the specification to destroy the resource", strings.Join(held, ", "))
	}
	return nil
}

// replaceRequested reports whether the resource must be replaced because the
// user asked for it, now or in the saved plan being applied.
func (e *Engine) replaceRequested(addr string) bool {
	for _, a := range e.Opts.Replace {
		if a == addr {
			return true
		}
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if sc, ok := e.saved[addr]; ok && sc.Forced && !sc.Applied {
		return true
	}
	return false
}

// Plan computes the changes needed to reach the configuration.
func (e *Engine) planRun(ctx context.Context) (*Plan, error) {
	if err := e.checkOutputs(); err != nil {
		return nil, err
	}
	if err := e.checkReplace(); err != nil {
		return nil, err
	}
	if e.Opts.Destroy {
		if err := e.checkDestroy(); err != nil {
			return nil, err
		}
	}
	e.txn = e.State.Begin("plan", e.Opts.Command)
	e.resetRun(false)
	e.forgetReads()
	e.loadSuspects()
	e.warnSuspects()
	plan := &Plan{Outputs: map[string]cue.Value{}, Destroy: e.Opts.Destroy, NoRefresh: e.Opts.Refresh == RefreshNone}
	if e.Opts.Destroy {
		changes, err := e.planOrphans(ctx, nil)
		if err != nil {
			_ = e.txn.Finish("failed", err.Error(), false)
			return nil, err
		}
		plan.Changes = changes
		plan.Stats = e.statsNow()
		return plan, e.txn.Finish("planned", e.summary(plan), false)
	}
	if err := e.relabelProviders(); err != nil {
		_ = e.txn.Finish("failed", err.Error(), true)
		return nil, err
	}
	moves, err := e.applyMoves(ctx)
	if err != nil {
		_ = e.txn.Finish("failed", err.Error(), true)
		return nil, err
	}
	plan.Changes = append(plan.Changes, moves...)
	final, err := e.run(ctx, false)
	if err == nil && e.Opts.Refresh == RefreshAge {
		final, err = e.readInputs(ctx, final)
	}
	if err != nil {
		_ = e.txn.Finish("failed", err.Error(), false)
		return nil, err
	}
	e.countUnread()
	for _, r := range e.Root.Resources {
		if ch := e.changeFor(r.Address()); ch != nil {
			plan.Changes = append(plan.Changes, ch)
		}
	}
	present := map[string]bool{}
	for _, r := range e.Root.Resources {
		present[r.Address()] = true
	}
	orphans, err := e.planOrphans(ctx, present)
	if err != nil {
		_ = e.txn.Finish("failed", err.Error(), false)
		return nil, err
	}
	plan.Changes = append(plan.Changes, orphans...)
	plan.Hints = e.renameHints(plan.Changes)
	for _, o := range e.Root.Outputs {
		plan.Outputs[o.Name] = canonicalOutput(final.LookupPath(o.ValuePath()))
		if o.Sensitive {
			if plan.SensitiveOutputs == nil {
				plan.SensitiveOutputs = map[string]bool{}
			}
			plan.SensitiveOutputs[o.Name] = true
		}
	}
	if err := e.frontier(plan); err != nil {
		_ = e.txn.Finish("failed", err.Error(), false)
		return nil, err
	}
	plan.Stats = e.statsNow()
	if err := e.checkReservations(plan); err != nil {
		_ = e.txn.Finish("rejected", err.Error(), true)
		return nil, err
	}
	if err := e.reserve(plan); err != nil {
		_ = e.txn.Finish("rejected", err.Error(), true)
		return nil, err
	}
	return plan, e.txn.Finish("planned", e.summary(plan), false)
}

func (e *Engine) summary(plan *Plan) string {
	add, change, destroy, _ := plan.Counts()
	return fmt.Sprintf("%d to add, %d to change, %d to destroy; %d of %d reached, %d skipped, %d refreshed, %d within refresh age, %d not read, %d ephemeral opened, %d functions called, %d imported, %d moved",
		add, change, destroy, plan.Stats.Reached, plan.Stats.Total, plan.Stats.Skipped, plan.Stats.Refreshed, plan.Stats.Fresh, plan.Stats.NotRead, plan.Stats.Opened, plan.Stats.Called, plan.Stats.Imported, plan.Stats.Moved)
}

func (e *Engine) planDelete(ctx context.Context, sr *state.Resource, reason string) (*Change, error) {
	local := config.LocalName(sr.ProviderKey)
	ps, ok := e.Schemas[local]
	if !ok {
		hint := ""
		if sr.ProviderProtocol != "" {
			hint = fmt.Sprintf(" (it was last written through protocol %q: declare a provider struct with the same $protocol and $address)", sr.ProviderProtocol)
		}
		return nil, fmt.Errorf("%s: provider %s is needed to destroy it but is not available%s", sr.Address(), local, hint)
	}
	s := ps.Resources[sr.Type]
	if s == nil {
		return nil, fmt.Errorf("%s: provider %s no longer has resource type %q", sr.Address(), local, sr.Type)
	}
	prov, err := e.Providers.Provider(ctx, sr.ProviderKey)
	if err != nil {
		return nil, err
	}
	ch := &Change{Addr: sr.Address(), Mode: sr.Mode, Type: sr.Type, Name: sr.Name, ProviderKey: sr.ProviderKey, Schema: s, Deps: sr.Dependencies, Reasons: []string{reason}}
	prior, private, err := e.priorState(ctx, prov, sr, s, ch, "")
	if err != nil {
		return nil, err
	}
	ch.Prior, ch.PriorPrivate = prior, private
	ch.PriorRevision, ch.PriorGeneration = e.versionOf(ch.Addr)
	objT := s.Block.Type()
	ch.Config = tftypes.NewValue(objT, nil)
	if prior.IsNull() {
		ch.Action = ActionNoop
		return ch, nil
	}
	res, diags, err := prov.PlanResourceChange(ctx, sr.Type, prior, tftypes.NewValue(objT, nil), ch.Config, private)
	if err != nil {
		return nil, fmt.Errorf("%s: planning destroy: %w", sr.Address(), err)
	}
	if err := diags.Err(); err != nil {
		return nil, fmt.Errorf("%s: planning destroy: %w", sr.Address(), err)
	}
	ch.Warnings = diags.Warnings()
	ch.Planned = res.PlannedState
	ch.PlannedPrivate = res.PlannedPrivate
	ch.Action = ActionDelete
	return ch, nil
}

// ParseAge validates a refresh age and returns the maximum age; never is true
// for "never". "always" and "" return zero.
func ParseAge(p string) (maxAge time.Duration, never bool, err error) {
	switch strings.TrimSpace(p) {
	case "", "always":
		return 0, false, nil
	case "never":
		return 0, true, nil
	}
	s := strings.TrimSpace(p)
	if strings.HasSuffix(s, "d") {
		n, err := strconv.Atoi(strings.TrimSuffix(s, "d"))
		if err != nil {
			return 0, false, fmt.Errorf("invalid refresh age %q", p)
		}
		return time.Duration(n) * 24 * time.Hour, false, nil
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		return 0, false, fmt.Errorf("invalid refresh age %q: want \"always\", \"never\" or a duration such as \"30m\", \"6h\", \"7d\"", p)
	}
	return d, false, nil
}

// parsedAge is a refresh age that was parsed.
type parsedAge struct {
	max   time.Duration
	never bool
	err   error
}

// parseAge is ParseAge with the result kept: an estate has a few distinct
// ages and many records.
func (e *Engine) parseAge(text string) (time.Duration, bool, error) {
	e.mu.Lock()
	a, ok := e.ages[text]
	e.mu.Unlock()
	if !ok {
		a.max, a.never, a.err = ParseAge(text)
		e.mu.Lock()
		if e.ages == nil {
			e.ages = map[string]parsedAge{}
		}
		e.ages[text] = a
		e.mu.Unlock()
	}
	return a.max, a.never, a.err
}

// refreshDue decides whether sr must be read given its refresh age. A mode
// that reads by age reads a record that is older than that; whether the plan
// looks at the record at all is decided by the caller.
func (e *Engine) refreshDue(sr *state.Resource, age string) (bool, error) {
	switch e.Opts.Refresh {
	case RefreshNone:
		return false, nil
	case RefreshAll:
		return true, nil
	}
	return e.pastAge(sr, age)
}

// pastAge reports whether the record is older than the refresh age, which is
// the default one when age is empty.
func (e *Engine) pastAge(sr *state.Resource, age string) (bool, error) {
	if age == "" {
		age = e.Opts.DefaultRefresh
	}
	maxAge, never, err := e.parseAge(age)
	if err != nil {
		return false, err
	}
	if never {
		return false, nil
	}
	if maxAge == 0 || sr.RefreshedAt == nil {
		return true, nil
	}
	return olderThan(*sr.RefreshedAt, e.clock(), maxAge), nil
}

// clock returns the time that records are aged against.
func (e *Engine) clock() time.Time {
	if e.now != nil {
		return e.now()
	}
	return time.Now()
}

// olderThan reports whether a record that was read at the given time is past
// the refresh age at now. A record exactly as old as the age is within it.
func olderThan(read, now time.Time, age time.Duration) bool {
	return now.Sub(read) > age
}

// readResult is what the provider reported when a plan read a record.
type readResult struct {
	// state is null when the object does not exist.
	state   tftypes.Value
	private []byte
	// drifted reports that the object differs from the record.
	drifted bool
	// forced is why the read was made whatever the refresh age, or "" when the
	// age asked for it.
	forced string
}

func (e *Engine) cachedRead(addr string) *readResult {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.reads[addr]
}

func (e *Engine) rememberRead(addr string, rd *readResult) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.reads == nil {
		e.reads = map[string]*readResult{}
	}
	e.reads[addr] = rd
}

// dataRead is what the provider returned when a plan read a data source.
type dataRead struct {
	// hash is the digest of the configuration that was read.
	hash  string
	state tftypes.Value
}

// cachedData returns what the plan read of the data source when its
// configuration had the hash.
func (e *Engine) cachedData(addr, hash string) (tftypes.Value, bool) {
	if hash == "" {
		return tftypes.Value{}, false
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	rd, ok := e.dataReads[addr]
	return rd.state, ok && rd.hash == hash
}

func (e *Engine) rememberData(addr, hash string, st tftypes.Value) {
	if hash == "" {
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.dataReads == nil {
		e.dataReads = map[string]dataRead{}
	}
	e.dataReads[addr] = dataRead{hash: hash, state: st}
}

// forgetReads starts a plan with nothing read and nothing imported.
func (e *Engine) forgetReads() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.reads, e.dataReads, e.imported = nil, nil, nil
}

// upgrade asks the provider to bring the recorded state to the current schema.
func (e *Engine) upgrade(ctx context.Context, prov plugin.Provider, sr *state.Resource, s *schema.Schema, addr string) (tftypes.Value, error) {
	prior, diags, err := prov.UpgradeResourceState(ctx, sr.Type, sr.SchemaVersion, sr.Attributes, s.Block.Type())
	if err != nil {
		return tftypes.Value{}, fmt.Errorf("%s: upgrading state: %w", addr, err)
	}
	if err := diags.Err(); err != nil {
		return tftypes.Value{}, fmt.Errorf("%s: upgrading state: %w", addr, err)
	}
	return prior, nil
}

// priorState upgrades the recorded state and, when the refresh age
// requires it, refreshes it from the provider, persisting the result. A record
// that an earlier read found gone has no prior state until a read finds the
// object again.
func (e *Engine) priorState(ctx context.Context, prov plugin.Provider, sr *state.Resource, s *schema.Schema, ch *Change, age string) (tftypes.Value, []byte, error) {
	addr := ch.Addr
	if rd := e.cachedRead(addr); rd != nil {
		ch.Refreshed, ch.Drifted, ch.forced = true, rd.drifted, rd.forced
		e.count(func(st *Stats) {
			st.Refreshed++
			if rd.drifted {
				st.Drifted++
			}
		})
		return rd.state, rd.private, nil
	}
	prior, err := e.upgrade(ctx, prov, sr, s, addr)
	if err != nil {
		return tftypes.Value{}, nil, err
	}
	private := sr.Private
	if prior.IsNull() {
		return prior, private, nil
	}
	due, err := e.refreshDue(sr, age)
	if err != nil {
		return tftypes.Value{}, nil, fmt.Errorf("%s: %w", addr, err)
	}
	// What makes the plan read the record whatever its age is what the plan says,
	// also when the age has run out as well.
	if ch.forced = e.readReason(sr); ch.forced != "" {
		due = true
	}
	if !due {
		if e.Opts.Refresh == RefreshNone {
			// Not a matter of age: the caller asked for no refresh at all.
			ch.Reasons = append(ch.Reasons, "refresh skipped: -refresh=false was given")
		} else {
			ch.Fresh = true
			e.count(func(st *Stats) { st.Fresh++ })
		}
		ch.Drifted = sr.Marked()
		if sr.Gone {
			return tftypes.NewValue(s.Block.Type(), nil), nil, nil
		}
		return prior, private, nil
	}
	return e.readRecord(ctx, prov, sr, ch, prior, private)
}

// readRecord asks the provider for the real state of a record, writes what it
// found to the state and keeps it for the rest of the plan.
func (e *Engine) readRecord(ctx context.Context, prov plugin.Provider, sr *state.Resource, ch *Change, prior tftypes.Value, private []byte) (tftypes.Value, []byte, error) {
	addr := ch.Addr
	announce(ctx, func() {
		e.step("refresh", addr, "start", idOf(prior), time.Time{}, "%s: Refreshing state... %s", addr, idSuffix(prior))
	})
	newState, newPrivate, diags, err := prov.ReadResource(ctx, sr.Type, prior, private)
	if err != nil {
		return tftypes.Value{}, nil, fmt.Errorf("%s: refreshing: %w", addr, err)
	}
	if err := diags.Err(); err != nil {
		return tftypes.Value{}, nil, fmt.Errorf("%s: refreshing: %w", addr, err)
	}
	ch.Refreshed = true
	e.count(func(st *Stats) { st.Refreshed++ })
	rd := &readResult{state: newState, private: newPrivate, forced: ch.forced}
	defer func() { e.rememberRead(addr, rd) }()
	if newState.IsNull() {
		rd.drifted, ch.Drifted = true, true
		e.count(func(st *Stats) { st.Drifted++ })
		e.Opts.Progress("%s: no longer exists; it will be created again", addr)
		e.persistGone(addr, sr)
		return newState, newPrivate, nil
	}
	attrs, err := convert.ToJSON(newState)
	if err == nil {
		changed := !sameAttributes(attrs, sr.Attributes)
		// An object that is back after a read did not find it differs from the
		// record, which says it is gone.
		rd.drifted = changed || sr.Gone
		ch.Drifted = rd.drifted
		if rd.drifted {
			e.count(func(st *Stats) { st.Drifted++ })
		}
		e.persistRefreshed(addr, sr, attrs, newPrivate)
	}
	return newState, newPrivate, nil
}

// persistRefreshed writes the record that a refresh read, so that the next
// run within the refresh age can skip the provider. A read that differs from
// the record leaves a mark on the new record, which keeps the node in every
// plan until an apply writes it. Another process applying the node holds its
// lock; the record is left alone in that case. The reservation that the actor
// holds on the node is not touched: only an apply of the node, or expiry, ends
// it.
func (e *Engine) persistRefreshed(addr string, sr *state.Resource, attrs, private []byte) {
	e.persistRead(addr, sr, attrs, private, true)
}

// persistRead writes the record that a read returned. With markDrift, a read that
// differs from the record leaves the mark; the read that follows an apply does not.
func (e *Engine) persistRead(addr string, sr *state.Resource, attrs, private []byte, markDrift bool) {
	release, ok, _ := e.State.TryHold(addr, e.Opts.Command+" refresh", e.txnID())
	if !ok {
		return
	}
	defer release()
	n := *sr
	n.Attributes = attrs
	n.Private = private
	n.RefreshedAt = state.Now()
	n.ProviderConfig = e.stamp(sr.ProviderKey, sr.ProviderConfig)
	n.Gone = false
	action := "refresh"
	if markDrift && !sameAttributes(attrs, sr.Attributes) {
		n.DriftedAt = state.Now()
		action = "drift"
	}
	before := n.Revision
	e.recordRefresh(addr, action, before, &n)
}

// recordRefresh writes the record of a refresh and notes it in the transaction. A
// record that another command wrote, removed or created since this command read
// it is left as it is: what the refresh read belongs to the record this command
// holds, not to the one in the state.
func (e *Engine) recordRefresh(addr, action string, before int, n *state.Resource) {
	err := e.State.Refresh(n, e.txnID())
	switch {
	case errors.Is(err, state.ErrChanged):
		e.Opts.Progress("Warning: %s changed in the state since this command read it; what the refresh read is not recorded", addr)
	case err == nil && e.txn != nil:
		e.txn.Record(addr, action, before, n.Revision)
	}
}

// persistGone writes the record of a node whose object a read did not find.
// The record keeps the attributes it had and carries the mark, so that the
// next plan, saved plan and apply create the object again.
func (e *Engine) persistGone(addr string, sr *state.Resource) {
	release, ok, _ := e.State.TryHold(addr, e.Opts.Command+" refresh", e.txnID())
	if !ok {
		return
	}
	defer release()
	n := *sr
	n.RefreshedAt = state.Now()
	n.ProviderConfig = e.stamp(sr.ProviderKey, sr.ProviderConfig)
	action := "refresh"
	if !sr.Gone {
		n.Gone, n.DriftedAt = true, state.Now()
		action = "drift"
	}
	before := n.Revision
	e.recordRefresh(addr, action, before, &n)
}

// readInputs reads the records that the nodes reached by the walk depend on and
// that the walk did not read. The plan of a node holds only while the nodes it
// reads are as the state records them. A record that differs, or whose object
// is gone, is written with its mark and the plan walks again from the records
// as they are now, using every read already made. Each walk reaches the nodes
// that the changed records make reachable, one level of the records they read
// further up, so a chain of changed records takes one round for each. A record
// that changes in a second round after it was found changed in an earlier one
// keeps changing, and the plan fails.
func (e *Engine) readInputs(ctx context.Context, final cue.Value) (cue.Value, error) {
	found := map[string]bool{}
	once := e.readInputsOnce
	if e.readOnce != nil {
		once = e.readOnce
	}
	for {
		changed, err := once(ctx)
		if err != nil || len(changed) == 0 {
			return final, err
		}
		var again []string
		for _, addr := range changed {
			if found[addr] {
				again = append(again, addr)
			}
			found[addr] = true
		}
		if len(again) > 0 {
			return final, fmt.Errorf("%s changed while the plan read it; run pravo plan again", strings.Join(again, ", "))
		}
		e.rewalk()
		if final, err = e.run(ctx, false); err != nil {
			return final, err
		}
	}
}

// readInputsOnce reads the unread records that a reached node reads and that
// are older than their refresh age, and returns the addresses whose read found
// drift or no object.
func (e *Engine) readInputsOnce(ctx context.Context) ([]string, error) {
	inputs := map[string]bool{}
	for _, ch := range e.recorded() {
		if ch.Skipped || (ch.Mode != "managed" && ch.Mode != "data") {
			continue
		}
		for _, d := range ch.Deps {
			inputs[d] = true
		}
	}
	var due []*Change
	for addr := range inputs {
		ch := e.changeFor(addr)
		sr := e.State.Get(addr)
		if ch == nil || !ch.Unread || sr == nil {
			continue
		}
		past, err := e.refreshDue(sr, ch.age)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", addr, err)
		}
		if past {
			due = append(due, ch)
		}
	}
	sort.Slice(due, func(i, j int) bool { return due[i].Addr < due[j].Addr })
	var (
		mu       sync.Mutex
		wg       sync.WaitGroup
		firstErr error
		changed  []string
		sem      = make(chan struct{}, Parallelism)
	)
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	for _, ch := range due {
		sem <- struct{}{}
		wg.Add(1)
		go func(ch *Change) {
			defer wg.Done()
			defer func() { <-sem }()
			drifted, err := e.readInput(ctx, ch)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				if firstErr == nil {
					firstErr = err
					cancel()
				}
				return
			}
			if drifted {
				changed = append(changed, ch.Addr)
			}
		}(ch)
	}
	wg.Wait()
	sort.Strings(changed)
	return changed, firstErr
}

// readInput reads the record of a node that the walk skipped without reading.
// It reports whether the object differs from the record or is gone.
func (e *Engine) readInput(ctx context.Context, ch *Change) (bool, error) {
	sr := e.State.Get(ch.Addr)
	prov, err := e.Providers.Provider(ctx, ch.ProviderKey)
	if err != nil {
		return false, err
	}
	prior, err := e.upgrade(ctx, prov, sr, ch.Schema, ch.Addr)
	if err != nil || prior.IsNull() {
		return false, err
	}
	if _, _, err := e.readRecord(ctx, prov, sr, ch, prior, sr.Private); err != nil {
		return false, err
	}
	ch.Unread, ch.Fresh = false, false
	ch.Reasons = []string{"refreshed because a node that the plan reaches reads it; no drift", "skipped: nothing the provider saw has changed"}
	return ch.Drifted, nil
}

// rewalk clears the changes and the counters of the walk that is over and keeps
// the counts of the work done before it.
func (e *Engine) rewalk() {
	done := e.statsNow()
	e.resetRun(true)
	e.count(func(st *Stats) { st.Moved, st.Imported = done.Moved, done.Imported })
}

// countUnread counts the nodes the plan did not read: within their refresh age,
// or past it. The walk does not count them, because a node that a later read
// of an input reached is read after all.
func (e *Engine) countUnread() {
	if e.Opts.Refresh == RefreshNone {
		return // nothing is read, so no record is late or early for it
	}
	var fresh, notRead int
	for _, ch := range e.recorded() {
		switch {
		case !ch.Unread:
		case ch.Fresh:
			fresh++
		default:
			notRead++
		}
	}
	e.count(func(st *Stats) { st.Fresh += fresh; st.NotRead += notRead })
}

// quiet turns ch into a skipped no-op for a node that nothing about has
// changed and that the plan has no reason to look at: the provider is not
// asked, and nothing is written. The record stands in for the planned state when
// something reads the node; for any other node it is not even decoded. A node
// that sets its own refresh age is read once its record is past it, so it is not
// quiet then. It reports false when the node must be planned.
func (e *Engine) quiet(ch *Change, sr *state.Resource, s *schema.Schema, age string) (bool, error) {
	due, err := e.refreshDue(sr, age)
	if err != nil {
		return false, fmt.Errorf("%s: %w", ch.Addr, err)
	}
	if due && age != "" {
		return false, nil
	}
	if e.readReason(sr) != "" {
		return false, nil
	}
	if e.readers[ch.Addr] {
		stored, err := storedValue(sr, s)
		if err != nil {
			return false, nil
		}
		ch.Prior, ch.Planned = stored, stored
	}
	ch.PriorPrivate = sr.Private
	ch.Action, ch.Skipped, ch.Unread, ch.age = ActionNoop, true, true, age
	if e.Opts.Refresh == RefreshNone {
		ch.Reasons = append(ch.Reasons, "refresh skipped: -refresh=false was given", "skipped: nothing the provider saw has changed")
	} else {
		ch.Fresh = !due
		ch.Reasons = append(ch.Reasons, "skipped: nothing the provider saw has changed", e.unreadReason(sr, age))
	}
	e.count(func(st *Stats) { st.Skipped++ })
	return true, nil
}

// unreadReason says that a node was not read, and how old its record is next
// to the age it may have.
func (e *Engine) unreadReason(sr *state.Resource, age string) string {
	if age == "" {
		age = e.Opts.DefaultRefresh
	}
	if age == "" {
		age = "always"
	}
	if sr.RefreshedAt == nil {
		return "not read: the plan did not reach it; the record was never read, its refresh age is " + age
	}
	return fmt.Sprintf("not read: the plan did not reach it; the record is %s old, its refresh age is %s", shortAge(time.Since(*sr.RefreshedAt)), age)
}

// shortAge writes a duration in its largest whole unit: 12s, 45m, 3h, 5d.
func shortAge(d time.Duration) string {
	switch {
	case d >= 48*time.Hour:
		return fmt.Sprintf("%dd", int(d/(24*time.Hour)))
	case d >= time.Hour:
		return fmt.Sprintf("%dh", int(d/time.Hour))
	case d >= time.Minute:
		return fmt.Sprintf("%dm", int(d/time.Minute))
	}
	return fmt.Sprintf("%ds", int(d/time.Second))
}

func jsonEqual(a, b []byte) bool {
	var x, y any
	if json.Unmarshal(a, &x) != nil || json.Unmarshal(b, &y) != nil {
		return string(a) == string(b)
	}
	ax, _ := json.Marshal(x)
	by, _ := json.Marshal(y)
	return string(ax) == string(by)
}

// sameAttributes reports whether two records of a resource say the same for the
// purpose of drift. It differs from jsonEqual in one way: a list or a map that
// one leaves null and the other has empty is the same. Providers write a null where
// the configuration has none and read back an empty list or map, so that a record
// would differ from the read of the object that it was made from.
func sameAttributes(a, b []byte) bool {
	var x, y any
	if json.Unmarshal(a, &x) != nil || json.Unmarshal(b, &y) != nil {
		return string(a) == string(b)
	}
	ax, _ := json.Marshal(emptyAsNull(x))
	by, _ := json.Marshal(emptyAsNull(y))
	return string(ax) == string(by)
}

// emptyAsNull replaces every empty list and empty map in a decoded value with null.
func emptyAsNull(v any) any {
	switch t := v.(type) {
	case []any:
		if len(t) == 0 {
			return nil
		}
		out := make([]any, len(t))
		for i, e := range t {
			out[i] = emptyAsNull(e)
		}
		return out
	case map[string]any:
		if len(t) == 0 {
			return nil
		}
		out := make(map[string]any, len(t))
		for k, e := range t {
			out[k] = emptyAsNull(e)
		}
		return out
	}
	return v
}

// revisionOf returns the revision of the record at addr, 0 when there is none.
func (e *Engine) revisionOf(addr string) int {
	rev, _ := e.versionOf(addr)
	return rev
}

// versionOf returns the revision and the generation of the record at addr, 0 and
// "" when there is none.
func (e *Engine) versionOf(addr string) (int, string) {
	if sr := e.State.Get(addr); sr != nil {
		return sr.Revision, sr.Generation
	}
	return 0, ""
}

func (e *Engine) count(f func(*Stats)) {
	e.mu.Lock()
	f(&e.stats)
	e.mu.Unlock()
}

// configHash digests a fully known configuration value; it is empty when
// the value contains unknowns.
func configHash(cfg tftypes.Value) string {
	data, err := convert.ToJSON(cfg)
	if err != nil {
		return ""
	}
	return configHashBytes(data)
}

func configHashBytes(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:8])
}

func lifecycle(v cue.Value) (cbd, prevent bool, ignore []string, refresh string) {
	lc := v.LookupPath(cue.MakePath(cue.Str("$lifecycle")))
	if !lc.Exists() {
		return
	}
	cbd, _ = lc.LookupPath(cue.ParsePath("create_before_destroy")).Bool()
	prevent, _ = lc.LookupPath(cue.ParsePath("prevent_destroy")).Bool()
	refresh, _ = lc.LookupPath(cue.ParsePath("refresh")).String()
	if it, err := lc.LookupPath(cue.ParsePath("ignore_changes")).List(); err == nil {
		for it.Next() {
			if s, err := it.Value().String(); err == nil {
				ignore = append(ignore, s)
			}
		}
	}
	return
}

// storedValue decodes a node's attributes with the type of its schema.
func storedValue(sr *state.Resource, s *schema.Schema) (tftypes.Value, error) {
	return tftypes.ValueFromJSON(sr.Attributes, s.Block.Type())
}

// planResource runs the plan steps for one resource with the current task
// value. strict makes unknown values an error (apply time).
func (e *Engine) planResource(ctx context.Context, r *config.Resource, v cue.Value, strict bool) (*Change, error) {
	s, err := e.resourceSchema(r)
	if err != nil {
		return nil, err
	}
	addr := r.Address()
	if e.onConvert != nil {
		e.onConvert(addr, strict)
	}
	e.cueMu.Lock()
	conv := &convert.Converter{Strict: strict, Self: r.Path, Resolve: e.resolver(), IsEphemeral: e.isEphemeral}
	cfg := conv.Block(addr, s.Block, v)
	cbd, prevent, ignore, age := lifecycle(v)
	e.cueMu.Unlock()
	if err := conv.Err(); err != nil {
		return nil, err
	}
	if _, _, err := ParseAge(age); err != nil {
		return nil, fmt.Errorf("%s: $lifecycle.refresh: %w", addr, err)
	}
	ch := &Change{Addr: addr, Mode: r.Mode, Type: r.Type, Name: r.Name, ProviderKey: r.ProviderKey, Schema: s, Config: cfg, Deps: e.deps[addr]}
	ch.ConfigHash = configHash(cfg)
	ch.CreateBeforeDestroy, ch.PreventDestroy = cbd, prevent
	objT := s.Block.Type()
	sr := e.State.Get(addr)
	forced := sr != nil && r.Mode == "managed" && e.replaceRequested(addr)
	// A node that a read left a mark on is planned until an apply writes it.
	unchanged := sr != nil && !forced && !sr.Marked() && !e.Opts.Full && ch.ConfigHash != "" && ch.ConfigHash == sr.ConfigHash &&
		sr.SchemaVersion == s.Version && sr.ProviderVersion == e.providerVersion(r.ProviderKey) && sr.ProviderKey == r.ProviderKey
	switch {
	case sr == nil:
		ch.Reasons = append(ch.Reasons, "not in the state: a new resource")
	case ch.ConfigHash == "":
		ch.Reasons = append(ch.Reasons, "inputs depend on values known only after apply: "+strings.Join(conv.DepAddrs(), ", "))
	case sr.ConfigHash == "" && sr.AppliedAt != nil:
		// An apply that stopped halfway recorded the state it had reached and
		// no hash: the provider has not seen this configuration through.
		ch.Reasons = append(ch.Reasons, "the last apply of it did not finish")
	case ch.ConfigHash != sr.ConfigHash:
		ch.Reasons = append(ch.Reasons, "the specification changed since the last apply (hash "+sr.ConfigHash+" -> "+ch.ConfigHash+")")
	}
	if sr != nil && sr.ProviderVersion != e.providerVersion(r.ProviderKey) {
		ch.Reasons = append(ch.Reasons, "provider version changed ("+sr.ProviderVersion+" -> "+e.providerVersion(r.ProviderKey)+")")
	}
	if sr != nil && sr.ProviderKey != r.ProviderKey {
		// The key is not part of the configuration the provider sees, so
		// nothing is planned for the object. The apply records the new key.
		ch.Reasons = append(ch.Reasons, "provider instance changed ("+sr.ProviderKey+" -> "+r.ProviderKey+")")
	}
	if sr != nil && sr.SchemaVersion != s.Version {
		ch.Reasons = append(ch.Reasons, fmt.Sprintf("schema version changed (%d -> %d)", sr.SchemaVersion, s.Version))
	}
	if e.Opts.Full {
		ch.Reasons = append(ch.Reasons, "-full: every resource is planned")
	}
	if forced {
		ch.Reasons = append(ch.Reasons, "replacement requested with -replace")
	}

	if r.Mode == "data" {
		ch.Action = ActionRead
		// A data source that the plan read is not read again: the walk that follows
		// it, and the apply, use the answer.
		if st, ok := e.cachedData(addr, ch.ConfigHash); ok && (strict || !e.dependsOnPendingChange(addr)) {
			ch.Planned, ch.Refreshed = st, true
			ch.Kept = unchanged && e.recordHolds(sr, ch, st)
			e.count(func(st *Stats) { st.Reached++; st.Refreshed++ })
			return ch, nil
		}
		if unchanged && !e.dependsOnPendingChange(addr) {
			due, err := e.refreshDue(sr, age)
			if err != nil {
				return nil, fmt.Errorf("%s: %w", addr, err)
			}
			if !due {
				if ch.forced = e.readReason(sr); ch.forced != "" {
					ch.Reasons = append(ch.Reasons, ch.forced)
				}
			}
			if !due && ch.forced == "" {
				stored, err := storedValue(sr, s)
				if err == nil {
					ch.Planned, ch.Skipped = stored, true
					e.count(func(st *Stats) { st.Skipped++ })
					if e.Opts.Refresh != RefreshNone {
						ch.Fresh = true
						e.count(func(st *Stats) { st.Fresh++ })
					}
					return ch, nil
				}
			}
		}
		prov, err := e.Providers.Provider(ctx, r.ProviderKey)
		if err != nil {
			return nil, err
		}
		diags, err := prov.ValidateDataSourceConfig(ctx, r.Type, cfg)
		if err != nil {
			return nil, fmt.Errorf("%s: validating: %w", addr, err)
		}
		if err := diags.Err(); err != nil {
			return nil, fmt.Errorf("%s: %w", addr, err)
		}
		if conv.Unknown || (!strict && e.dependsOnPendingChange(addr)) {
			ch.Deferred = true
			ch.Planned = tftypes.NewValue(objT, tftypes.UnknownValue)
			e.count(func(st *Stats) { st.Reached++ })
			return ch, nil
		}
		e.step("read", addr, "start", "", time.Time{}, "%s: Reading...", addr)
		st, diags, err := prov.ReadDataSource(ctx, r.Type, cfg)
		if err != nil {
			return nil, fmt.Errorf("%s: reading: %w", addr, err)
		}
		if err := diags.Err(); err != nil {
			return nil, fmt.Errorf("%s: %w", addr, err)
		}
		ch.Warnings = diags.Warnings()
		ch.Planned, ch.Refreshed = st, true
		if ch.Kept = unchanged && e.recordHolds(sr, ch, st); ch.Kept && !strict {
			e.renewData(addr, sr, age)
		}
		e.rememberData(addr, ch.ConfigHash, st)
		e.count(func(st *Stats) { st.Reached++; st.Refreshed++ })
		return ch, nil
	}

	// A plan in the default mode looks at the nodes it reaches and at the nodes
	// they read. The rest is not read, whatever its refresh age. A plan that
	// reads nothing does not start a provider for a node that did not change.
	if unchanged && (e.Opts.Refresh == RefreshAge || (e.Opts.Refresh == RefreshNone && !strict)) && e.cachedRead(addr) == nil {
		quiet, err := e.quiet(ch, sr, s, age)
		if err != nil || quiet {
			return ch, err
		}
	}

	prov, err := e.Providers.Provider(ctx, r.ProviderKey)
	if err != nil {
		return nil, err
	}
	prior := tftypes.NewValue(objT, nil)
	var private []byte
	if sr != nil {
		prior, private, err = e.priorState(ctx, prov, sr, s, ch, age)
		if err != nil {
			return nil, err
		}
		if id, ok := e.importedID(addr); ok && !strict {
			// The record was written by the import of an earlier walk of this plan.
			ch.Imported, ch.ImportID = true, id
		}
	} else if imp, ok := e.Root.Imports[addr]; ok && !strict {
		prior, private, err = e.importResource(ctx, prov, r, s, imp, ch)
		if err != nil {
			return nil, err
		}
	}
	ch.Prior, ch.PriorPrivate = prior, private
	if ch.Refreshed {
		if ch.forced != "" {
			ch.Reasons = append(ch.Reasons, ch.forced)
		}
		if ch.Drifted {
			ch.Reasons = append(ch.Reasons, "refresh found the real state differs from the state (drift)")
		} else if e.Opts.Refresh == RefreshAll {
			ch.Reasons = append(ch.Reasons, "refreshed because -refresh=all was given; no drift")
		} else if ch.forced == "" {
			ch.Reasons = append(ch.Reasons, "refreshed because the record is older than its refresh age; no drift")
		}
	} else if ch.Fresh {
		ch.Reasons = append(ch.Reasons, "refresh skipped: the record is within its refresh age")
	}
	if sr != nil && sr.Marked() && !(ch.Refreshed && ch.Drifted) {
		// An earlier read left a mark that this run did not find again.
		if sr.Gone {
			ch.Reasons = append(ch.Reasons, "an earlier read did not find the object; it is planned until an apply writes the record")
		} else {
			ch.Reasons = append(ch.Reasons, "an earlier read found the real state differs from the state (drift); it is planned until an apply writes the record")
		}
	}
	if unchanged && !prior.IsNull() && (!ch.Refreshed || !ch.Drifted) {
		ch.Reasons = append(ch.Reasons, "skipped: nothing the provider saw has changed")
		// Nothing the provider saw has changed: reuse the recorded state.
		ch.Action, ch.Planned, ch.Skipped = ActionNoop, prior, true
		e.count(func(st *Stats) { st.Skipped++ })
		return ch, nil
	}
	e.count(func(st *Stats) { st.Reached++ })

	diags, err := prov.ValidateResourceConfig(ctx, r.Type, cfg)
	if err != nil {
		return nil, fmt.Errorf("%s: validating: %w", addr, err)
	}
	if err := diags.Err(); err != nil {
		return nil, fmt.Errorf("%s: %w", addr, err)
	}
	ch.Warnings = append(ch.Warnings, diags.Warnings()...)

	cfg = applyIgnoreChanges(s.Block, prior, cfg, ignore)
	ch.Config = cfg

	proposed := ProposedNew(s.Block, prior, cfg)
	res, diags, err := prov.PlanResourceChange(ctx, r.Type, prior, proposed, cfg, private)
	if err != nil {
		return nil, fmt.Errorf("%s: planning: %w", addr, err)
	}
	if err := diags.Err(); err != nil {
		return nil, fmt.Errorf("%s: %w", addr, err)
	}
	ch.Warnings = append(ch.Warnings, diags.Warnings()...)
	ch.Planned, ch.PlannedPrivate, ch.RequiresReplace = res.PlannedState, res.PlannedPrivate, res.RequiresReplace

	switch {
	case prior.IsNull():
		ch.Action = ActionCreate
	case forced || (len(res.RequiresReplace) > 0 && !prior.Equal(res.PlannedState)):
		ch.Action = ActionReplace
		ch.Forced = forced
		for _, p := range res.RequiresReplace {
			if steps := p.Steps(); len(steps) > 0 {
				if n, ok := steps[0].(tftypes.AttributeName); ok {
					ch.Reasons = append(ch.Reasons, "the provider cannot change "+string(n)+" in place; replacement required")
				}
			}
		}
		nullPrior := tftypes.NewValue(objT, nil)
		res2, diags, err := prov.PlanResourceChange(ctx, r.Type, nullPrior, ProposedNew(s.Block, nullPrior, cfg), cfg, nil)
		if err != nil {
			return nil, fmt.Errorf("%s: planning replacement: %w", addr, err)
		}
		if err := diags.Err(); err != nil {
			return nil, fmt.Errorf("%s: planning replacement: %w", addr, err)
		}
		ch.PlannedCreate, ch.CreatePrivate = res2.PlannedState, res2.PlannedPrivate
		if ch.PreventDestroy {
			return nil, fmt.Errorf("%s: replacement requires destroying the resource, but $lifecycle.prevent_destroy is set", addr)
		}
	case prior.Equal(res.PlannedState):
		ch.Action = ActionNoop
	default:
		ch.Action = ActionUpdate
	}
	return ch, nil
}

// recordHolds reports whether the read of a data source returned what its
// record holds: the same attributes, edges and provider. Only the time of the
// read differs, and a write of the record would change nothing a plan or an
// apply looks at.
func (e *Engine) recordHolds(sr *state.Resource, ch *Change, read tftypes.Value) bool {
	if sr == nil || len(sr.Private) > 0 || !slices.Equal(sr.Dependencies, ch.Deps) ||
		sr.Provider != e.providerSource(ch.ProviderKey) || sr.ProviderProtocol != e.providerProtocol(ch.ProviderKey) {
		return false
	}
	attrs, err := convert.ToJSON(read)
	return err == nil && jsonEqual(attrs, sr.Attributes)
}

// renewData records the time of a read that found the record of a data source
// as it is. The revision stays, so a saved plan that reads the record is still
// current. Only a refresh age that is a duration looks at the time: with
// "always" or "never" the write would change nothing, and it is left out, unless
// the record has to take the digest of its provider's configuration. A record
// that another process holds the lock of is left alone.
func (e *Engine) renewData(addr string, sr *state.Resource, age string) {
	if age == "" {
		age = e.Opts.DefaultRefresh
	}
	stamp := e.stamp(sr.ProviderKey, sr.ProviderConfig)
	if maxAge, never, err := e.parseAge(age); err != nil || ((never || maxAge == 0) && stamp == sr.ProviderConfig) {
		return
	}
	release, ok, _ := e.State.TryHold(addr, e.Opts.Command+" refresh", e.txnID())
	if !ok {
		return
	}
	defer release()
	n := *sr
	n.RefreshedAt = state.Now()
	n.ProviderConfig = stamp
	before := n.Revision
	e.recordRefresh(addr, "refresh", before, &n)
}

// dependsOnPendingChange reports whether any dependency of the task has a
// planned change, in which case reading a data source is deferred to apply.
func (e *Engine) dependsOnPendingChange(addr string) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, d := range e.deps[addr] {
		ch, ok := e.changes[d]
		if !ok {
			continue
		}
		switch ch.Action {
		case ActionCreate, ActionUpdate, ActionReplace, ActionDelete:
			return true
		case ActionRead:
			if ch.Deferred {
				return true
			}
		}
	}
	return false
}

func (e *Engine) record(ch *Change) {
	e.mu.Lock()
	e.changes[ch.Addr] = ch
	e.mu.Unlock()
}

// changeFor returns the change recorded for addr, or nil. Tasks run in
// parallel and record their changes as they finish, so every read of the
// changes goes through here or recorded.
func (e *Engine) changeFor(addr string) *Change {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.changes[addr]
}

// recorded returns the changes recorded so far.
func (e *Engine) recorded() []*Change {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make([]*Change, 0, len(e.changes))
	for _, ch := range e.changes {
		out = append(out, ch)
	}
	return out
}

// statsNow returns a copy of the counters of the current run.
func (e *Engine) statsNow() Stats {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.stats
}

// resetRun clears the changes (for an apply, which records its own) and the
// counters of the run about to start.
func (e *Engine) resetRun(changes bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if changes {
		e.changes = map[string]*Change{}
	}
	e.targets = nil
	e.stats = Stats{Total: len(e.Root.Resources)}
}

// runTask plans (or applies) one task against the current configuration and
// fills the resulting values back in.
func (e *Engine) runTaskInner(ctx context.Context, r *config.Resource, apply bool) error {
	v, err := e.snapshot(r)
	if err != nil {
		return err
	}
	var final tftypes.Value
	if r.Builtin {
		return e.builtinTask(ctx, r, v)
	}
	if r.Mode == "call" {
		fill, err := e.callTask(ctx, r, v, apply)
		if err != nil || fill == nil {
			return err
		}
		return e.fill(r, fill)
	}
	if r.Mode == "ephemeral" {
		final, err = e.ephemeralTask(ctx, r, v, apply)
		if err != nil || final.Type() == nil {
			return err
		}
		ch := e.changeFor(r.Address())
		e.cueMu.Lock()
		fill := convert.Fill(v, ch.Schema.Block, final)
		e.cueMu.Unlock()
		return e.fill(r, fill)
	}
	if apply {
		final, err = e.applyTask(ctx, r, v)
	} else {
		final, err = e.planTask(ctx, r, v)
	}
	if err != nil {
		return err
	}
	if final.Type() == nil {
		// A node that the plan did not look at and that nothing reads has no value
		// to unify into the configuration.
		return nil
	}
	ch := e.changeFor(r.Address())
	e.cueMu.Lock()
	fill := convert.Fill(v, ch.Schema.Block, final)
	e.cueMu.Unlock()
	return e.fill(r, fill)
}

func (e *Engine) planTask(ctx context.Context, r *config.Resource, v cue.Value) (tftypes.Value, error) {
	ch, err := e.planResource(ctx, r, v, false)
	if err != nil {
		return tftypes.Value{}, err
	}
	// What the plan read of the node is written by now: a saved plan expects the
	// record as it is.
	ch.PriorRevision, ch.PriorGeneration = e.versionOf(ch.Addr)
	e.record(ch)
	if ch.Deferred {
		return tftypes.Value{}, nil
	}
	if ch.Action == ActionReplace {
		return ch.PlannedCreate, nil
	}
	return ch.Planned, nil
}

// providerProtocol returns the protocol to record on a node: empty for the
// default, so existing records and new ones look alike.
func (e *Engine) providerProtocol(key string) string {
	if rp, ok := e.Providers.Resolved[config.LocalName(key)]; ok && rp.Protocol != "" && rp.Protocol != config.ProtocolTfplugin {
		return rp.Protocol
	}
	return ""
}

func (e *Engine) providerSource(key string) string {
	if rp, ok := e.Providers.Resolved[config.LocalName(key)]; ok {
		return rp.Req.Source.String()
	}
	return ""
}

// writeState records the node after an apply or read.
func (e *Engine) writeState(ch *Change, v tftypes.Value, private []byte, action string) error {
	attrs, err := convert.ToJSON(v)
	if err != nil {
		return fmt.Errorf("%s: new state is not fully known: %w", ch.Addr, err)
	}
	now := state.Now()
	n := &state.Resource{
		Addr: ch.Addr, Mode: ch.Mode, Type: ch.Type, Name: ch.Name, Provider: e.providerSource(ch.ProviderKey), ProviderKey: ch.ProviderKey,
		ProviderProtocol: e.providerProtocol(ch.ProviderKey),
		ProviderVersion:  e.providerVersion(ch.ProviderKey), SchemaVersion: ch.Schema.Version,
		Attributes: attrs, Private: private, Dependencies: ch.Deps, ConfigHash: ch.ConfigHash,
		ProviderConfig: e.stamp(ch.ProviderKey, ""), RefreshedAt: now, AppliedAt: now,
	}
	if action == "partial" {
		// The provider returned the state it had reached before it failed. Without
		// a hash, the next plan reaches the node.
		n.ConfigHash = ""
	}
	before := ch.removedRevision
	n.Revision = ch.removedRevision
	rekeyed := false
	if prev := e.State.Get(ch.Addr); prev != nil {
		before = prev.Revision
		n.Revision = prev.Revision
		rekeyed = prev.ProviderKey != n.ProviderKey
		if ch.Mode == "managed" && action == "refresh" {
			n.AppliedAt = prev.AppliedAt
		}
	}
	write := e.State.Set
	if (action == "refresh" || action == "read") && !rekeyed {
		// A record that names another provider instance is written, not
		// refreshed: Refresh keeps a record whose attributes did not change. A
		// read of a data source is the same: the revision moves only when the
		// answer or the edges differ from the record.
		write = e.State.Refresh
	}
	if err := write(n, e.txnID()); err != nil {
		if errors.Is(err, state.ErrChanged) {
			return err // it names the node
		}
		return fmt.Errorf("%s: the record could not be written: %w", ch.Addr, err)
	}
	if e.txn != nil {
		e.txn.Record(ch.Addr, action, before, n.Revision)
	}
	return nil
}

// recordApplied writes the record of a node whose provider has just finished the
// change that what names ("creation", "update" or "destruction"). When the state
// cannot be written the object is changed and the record does not say so: the
// node is noted as one whose change is not recorded, which marks its record, if
// it has one, for a read and names the node in the error of the apply.
func (e *Engine) recordApplied(ch *Change, what string, v tftypes.Value, private []byte, action string) error {
	err := e.writeState(ch, v, private, action)
	if err != nil {
		e.unrecorded(ch.Addr, what)
	}
	return err
}

func (e *Engine) applyTask(ctx context.Context, r *config.Resource, v cue.Value) (tftypes.Value, error) {
	if e.only != nil && !e.only[r.Address()] {
		return e.fromState(r)
	}
	ch, err := e.planResource(ctx, r, v, true)
	if err != nil {
		return tftypes.Value{}, err
	}
	if e.saved != nil {
		if err := e.checkSaved(ch); err != nil {
			return tftypes.Value{}, err
		}
	}
	e.record(ch)
	var final tftypes.Value
	var private []byte
	start := time.Now()
	switch ch.Action {
	case ActionRead:
		final = ch.Planned
		if !ch.Skipped && !ch.Kept {
			if err := e.writeState(ch, final, nil, "read"); err != nil {
				return tftypes.Value{}, err
			}
			e.complete(ch.Addr)
			e.step("read", ch.Addr, "done", "", start, "%s: Read complete after %s", ch.Addr, since(start))
		}
		return final, nil
	case ActionNoop:
		final, private = ch.Prior, ch.PriorPrivate
		if !ch.Skipped {
			if err := e.writeState(ch, final, private, "refresh"); err != nil {
				return tftypes.Value{}, err
			}
			e.complete(ch.Addr)
		}
		return final, nil
	}
	prov, err := e.Providers.Provider(ctx, r.ProviderKey)
	if err != nil {
		return tftypes.Value{}, err
	}
	switch ch.Action {
	case ActionCreate, ActionUpdate:
		verb, done := "Creating", "Creation"
		if ch.Action == ActionUpdate {
			verb, done = "Modifying", "Modifications"
		}
		e.step(string(ch.Action), ch.Addr, "start", idOf(ch.Prior), time.Time{}, "%s: %s... %s", ch.Addr, verb, idSuffix(ch.Prior))
		res, err := e.applyChange(ctx, prov, ch, ch.Prior, ch.Planned, ch.Config, ch.PlannedPrivate)
		if err != nil {
			return tftypes.Value{}, err
		}
		final, private = res.NewState, res.Private
		what := "creation"
		if ch.Action == ActionUpdate {
			what = "update"
		}
		if err := e.recordApplied(ch, what, final, private, string(ch.Action)); err != nil {
			return tftypes.Value{}, err
		}
		e.step(string(ch.Action), ch.Addr, "done", idOf(final), start, "%s: %s complete after %s %s", ch.Addr, done, since(start), idSuffix(final))
	case ActionReplace:
		nullV := tftypes.NewValue(ch.Schema.Block.Type(), nil)
		if ch.CreateBeforeDestroy {
			e.Opts.Progress("%s: Creating replacement...", ch.Addr)
			res, err := e.applyChange(ctx, prov, ch, nullV, ch.PlannedCreate, ch.Config, ch.CreatePrivate)
			if err != nil {
				return tftypes.Value{}, err
			}
			final, private = res.NewState, res.Private
			if err := e.recordApplied(ch, "creation", final, private, "replace"); err != nil {
				return tftypes.Value{}, err
			}
			e.Opts.Progress("%s: Destroying previous instance... %s", ch.Addr, idSuffix(ch.Prior))
			if _, err := e.applyChange(ctx, prov, ch, ch.Prior, nullV, nullV, ch.PriorPrivate); err != nil {
				return tftypes.Value{}, err
			}
		} else {
			e.Opts.Progress("%s: Destroying... %s", ch.Addr, idSuffix(ch.Prior))
			if _, err := e.applyChange(ctx, prov, ch, ch.Prior, nullV, nullV, ch.PriorPrivate); err != nil {
				return tftypes.Value{}, err
			}
			// The record goes and comes back: its revision continues, so a plan
			// made against the old record is stale.
			if prev := e.State.Get(ch.Addr); prev != nil {
				ch.removedRevision = prev.Revision
			}
			if _, err := e.State.Remove(ch.Addr); err != nil {
				e.unrecorded(ch.Addr, "destruction")
				return tftypes.Value{}, fmt.Errorf("%s: the record could not be removed: %w", ch.Addr, err)
			}
			e.Opts.Progress("%s: Creating replacement...", ch.Addr)
			res, err := e.applyChange(ctx, prov, ch, nullV, ch.PlannedCreate, ch.Config, ch.CreatePrivate)
			if err != nil {
				return tftypes.Value{}, err
			}
			final, private = res.NewState, res.Private
			if err := e.recordApplied(ch, "creation", final, private, "replace"); err != nil {
				return tftypes.Value{}, err
			}
		}
		e.step("replace", ch.Addr, "done", idOf(final), start, "%s: Replacement complete after %s %s", ch.Addr, since(start), idSuffix(final))
	default:
		return tftypes.Value{}, fmt.Errorf("%s: unexpected action %s during apply", ch.Addr, ch.Action)
	}
	e.complete(ch.Addr)
	return final, nil
}

func (e *Engine) applyChange(ctx context.Context, prov plugin.Provider, ch *Change, prior, planned, cfg tftypes.Value, private []byte) (*plugin.ApplyResult, error) {
	if e.held != nil {
		if err := e.held.fence(); err != nil {
			return nil, err
		}
	}
	what := mutation(prior, planned)
	e.fly(ch.Addr, what)
	// The provider finishes what it began. A run that stops starting tasks, because
	// another node failed or the command was interrupted, does not cancel a call
	// that is changing an object: the object would be left half changed, with no
	// record. The state records what the call returns.
	res, diags, err := prov.ApplyResourceChange(context.WithoutCancel(ctx), ch.Type, prior, planned, cfg, private)
	e.land(ch.Addr)
	if err != nil {
		e.unsettle(ch.Addr, what)
		return nil, fmt.Errorf("%s: applying: %w", ch.Addr, err)
	}
	recorded := false
	var unwritten error
	if res != nil && !res.NewState.IsNull() && !planned.IsNull() {
		// Even on error, providers may return a partial new state that must
		// be recorded so the resource is not leaked.
		if diags.HasErrors() {
			if unwritten = e.recordApplied(ch, what, res.NewState, res.Private, "partial"); unwritten == nil {
				recorded = true
			}
		}
	}
	if err := diags.Err(); err != nil {
		err = fmt.Errorf("%s: %w", ch.Addr, err)
		if unwritten != nil {
			// The object is changed and the state that the provider returned for it is
			// not recorded: the error names both failures.
			return nil, fmt.Errorf("%w; the state it returned: %w", err, unwritten)
		}
		if !recorded && (e.interrupt.Stopped() || (what != "creation" && e.State.Get(ch.Addr) != nil)) {
			// The provider answered with an error and no state for an object whose
			// record exists, or it was asked to stop and said nothing of the object:
			// the change may have been made in part, and the record does not say so.
			e.unsettle(ch.Addr, what)
		}
		return nil, err
	}
	return res, nil
}

// ApplyResult summarises an apply. It is returned with the error of an apply that
// failed or was interrupted, so that the caller knows what that apply did.
type ApplyResult struct {
	Added, Changed, Destroyed int
	Outputs                   map[string]json.RawMessage
	Txn                       string
	// Applied lists, sorted, the addresses of the plan's changes that the apply
	// finished and recorded in the state: the creates, updates, replacements and
	// deletes whose provider call returned and whose record was written, and the
	// records and data sources the plan only wrote. A change that failed, that
	// returned part of its state, whose result is not known or whose record
	// could not be written is not in it, nor is a change that was not started.
	// Outputs are not changes: an apply that fails before it records them
	// leaves them to the next one, whatever Applied holds.
	Applied []string
}

// complete notes that the apply finished and recorded the change at addr.
func (e *Engine) complete(addr string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.completed == nil {
		e.completed = map[string]bool{}
	}
	e.completed[addr] = true
}

// completedAddrs returns the addresses of the changes the apply has finished and
// recorded, sorted. A node whose change is noted as one with an unknown result is
// not among them.
func (e *Engine) completedAddrs() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	var out []string
	for addr := range e.completed {
		if _, unsettled := e.unsettled[addr]; !unsettled {
			out = append(out, addr)
		}
	}
	sort.Strings(out)
	return out
}

// lockAll takes the lock of every node the plan will write, and of no other,
// or none of them: if any lock is held by another process the whole apply is
// rejected before anything changes. A node that the apply only reads, such as a
// data source whose read found its record, is not locked, so two applies that
// write different nodes run at the same time. The locks are renewed until the
// returned set is released.
func (e *Engine) lockAll(plan *Plan) (*heldLocks, error) {
	h := newHeldLocks(e.State, e.Opts.Progress, state.HeartbeatInterval)
	if err := h.acquire(plan.writeAddrs(), e.Opts.Command, e.txnID()); err != nil {
		h.release()
		return nil, err
	}
	return h, nil
}

// ApplySaved applies a plan loaded from a file. Every change computed during
// the apply is checked against the saved one, and the apply stops at the
// first inconsistency. The apply reads nothing from the providers: a data source
// that the plan read is applied with the answer the file holds, as the apply of
// the plan that was just printed uses the answer it remembers.
func (e *Engine) ApplySaved(ctx context.Context, plan *Plan) (*ApplyResult, error) {
	e.saved = map[string]*Change{}
	e.only = map[string]bool{}
	e.forgetReads()
	for _, ch := range plan.Changes {
		if ch.writes() && e.byPath[ch.Addr] != nil {
			e.saved[ch.Addr] = ch
			e.only[ch.Addr] = true
			if ch.Mode == "data" && ch.Action == ActionRead && !ch.Deferred && ch.Planned.Type() != nil {
				e.rememberData(ch.Addr, ch.ConfigHash, ch.Planned)
			}
		}
	}
	e.Opts.Destroy = plan.Destroy
	return e.Apply(ctx, plan)
}

// approve takes the changes of a plan that this engine made as the ones to
// apply, when no saved plan was loaded. The apply compares each node it plans
// again with the change that was approved, and reads nothing from the
// providers: the plan read what it needed, and the engine remembers it. It
// returns the function that undoes this.
func (e *Engine) approve(plan *Plan) func() {
	if e.saved != nil {
		return func() {}
	}
	e.saved, e.only, e.approved = map[string]*Change{}, map[string]bool{}, true
	for _, ch := range plan.Changes {
		if ch.writes() && e.byPath[ch.Addr] != nil {
			e.saved[ch.Addr] = ch
			e.only[ch.Addr] = true
		}
	}
	refresh := e.Opts.Refresh
	e.Opts.Refresh = RefreshNone
	return func() { e.saved, e.only, e.approved, e.Opts.Refresh = nil, nil, false, refresh }
}

// Apply executes the plan: locks the nodes it writes, walks the nodes it
// changes and the nodes they read (planning each again with known values),
// destroys orphans, and records outputs, all as one recorded transaction. The
// plan is one that Plan or LoadPlan returned.
func (e *Engine) applyRun(ctx context.Context, plan *Plan) (*ApplyResult, error) {
	kind := "apply"
	if e.Opts.Destroy {
		kind = "destroy"
	}
	if e.Opts.Destroy {
		if err := e.checkPreventDestroy(); err != nil {
			return &ApplyResult{Outputs: map[string]json.RawMessage{}}, err
		}
	}
	var sc *applyScope
	if !e.Opts.Destroy {
		var err error
		if sc, err = e.scopeFor(plan); err != nil {
			return &ApplyResult{Outputs: map[string]json.RawMessage{}}, err
		}
		if e.adjustScope != nil {
			e.adjustScope(sc)
		}
		if !plan.NeedsApply() {
			// Nothing to write and no output to record or remove: no lock, no record, no transaction.
			return &ApplyResult{Outputs: map[string]json.RawMessage{}}, nil
		}
	}
	e.txn = e.State.Begin(kind, e.Opts.Command)
	e.mu.Lock()
	e.completed = nil
	e.mu.Unlock()
	result := &ApplyResult{Outputs: map[string]json.RawMessage{}, Txn: e.txn.ID()}
	// The outputs as they were when this apply began. Another apply that runs
	// at the same time may record outputs of its own; only the outputs this
	// apply changes are written over them.
	baseline := e.State.Outputs()
	fail := func(err error) (*ApplyResult, error) {
		err = e.withUnsettled(err)
		result.Applied = e.completedAddrs()
		_ = e.txn.Finish("failed", err.Error(), true)
		return result, err
	}
	lockStart := time.Now()
	held, err := e.lockAll(plan)
	if err != nil {
		_ = e.txn.Finish("rejected", err.Error(), true)
		return result, err
	}
	defer held.release()
	locked := len(held.held())
	telemetry.LockWait(ctx, time.Since(lockStart), locked)
	plog.Debug(ctx, fmt.Sprintf("locked %d resources", locked))
	// With the locks held, the records are read from the backend again: a node
	// that another process wrote after the plan was made is not applied over.
	if err := e.checkRevisions(plan, sc.inputsOf(plan)); err != nil {
		_ = e.txn.Finish("rejected", err.Error(), true)
		return result, err
	}
	if locked > 0 {
		// An apply that is killed from here on leaves a transaction that is still
		// running, and locks that go stale.
		if err := e.txn.Start(); err != nil {
			return fail(err)
		}
	}
	e.held = held
	defer func() { e.held = nil }()
	defer e.register(ctx, held)()
	var skipped []string
	if !e.Opts.Destroy {
		defer e.approve(plan)()
		e.scope = sc
		defer func() { e.scope = nil }()
		e.resetRun(true)
		final, err := e.run(ctx, true)
		if err != nil {
			return fail(err)
		}
		for _, ch := range e.recorded() {
			switch ch.Action {
			case ActionCreate:
				result.Added++
			case ActionUpdate:
				result.Changed++
			case ActionReplace:
				result.Added++
				result.Destroyed++
			}
		}
		byName := make(map[string]*config.Output, len(e.Root.Outputs))
		for _, o := range e.Root.Outputs {
			byName[o.Name] = o
		}
		for _, name := range plan.EvalOutputs {
			o := byName[name]
			if o == nil {
				continue
			}
			ov := final.LookupPath(o.ValuePath())
			if !convert.IsConcrete(ov) {
				if plan.Partial {
					// Part of the plan is still to be applied: an output that reads
					// a resource outside the selection waits for it.
					skipped = append(skipped, o.Name)
					continue
				}
				return fail(fmt.Errorf("output %s is not concrete after apply. A name inside its value may refer to a field of the output itself, as in value: {pet: pet.id}; rename the field", o.Name))
			}
			b, err := outputJSON(ov)
			if err != nil {
				return fail(fmt.Errorf("output %s: %w", o.Name, err))
			}
			result.Outputs[o.Name] = WrapOutput(b, o.Sensitive)
		}
	}
	// Orphans were planned already; destroy them, dependents first.
	destroyed, err := e.destroyOrphans(ctx, plan)
	result.Destroyed += destroyed
	if err != nil {
		return fail(err)
	}
	if !e.Opts.Destroy {
		e.readBack(ctx)
	}
	// The outputs are recorded by a read, a merge and a write while their lock is
	// held: an apply that finished meanwhile has recorded outputs of its own.
	if e.Opts.Destroy {
		err = e.State.UpdateOutputs(func(recorded map[string]json.RawMessage, _ bool) (map[string]json.RawMessage, bool, error) {
			outputs, err := e.outputsAfterDestroy(plan, recorded)
			return outputs, err == nil, err
		})
	} else {
		declared := make([]string, 0, len(e.Root.Outputs))
		for _, o := range e.Root.Outputs {
			declared = append(declared, o.Name)
		}
		merge := func(recorded map[string]json.RawMessage, present bool) (map[string]json.RawMessage, bool) {
			outputs := mergeOutputs(recorded, baseline, result.Outputs, declared)
			// An output that this apply did not evaluate keeps the value that is
			// recorded; the apply reports it with the others.
			evaluated := stringSet(plan.EvalOutputs)
			for _, name := range declared {
				if _, ok := result.Outputs[name]; !ok && !evaluated[name] {
					if raw, ok := outputs[name]; ok {
						result.Outputs[name] = raw
					}
				}
			}
			// Outputs are written when they changed, and when the state has no
			// outputs object yet: a root that reads these outputs looks for it. An
			// object that holds none is left as it is, so two applies that write
			// different nodes do not write the same object.
			return outputs, !present || !sameOutputs(recorded, outputs)
		}
		var recorded map[string]json.RawMessage
		var present bool
		if recorded, present, err = e.State.LoadOutputs(); err == nil {
			if _, write := merge(recorded, present); write {
				err = e.State.UpdateOutputs(func(recorded map[string]json.RawMessage, present bool) (map[string]json.RawMessage, bool, error) {
					outputs, write := merge(recorded, present)
					return outputs, write, nil
				})
			}
		}
	}
	if err != nil {
		return fail(err)
	}
	result.Applied = e.completedAddrs()
	summary := fmt.Sprintf("%d added, %d changed, %d destroyed", result.Added, result.Changed, result.Destroyed)
	return result, e.txn.Finish("applied", summary, true)
}

// sameOutputs reports whether two sets of recorded outputs hold the same values.
func sameOutputs(a, b map[string]json.RawMessage) bool {
	if len(a) != len(b) {
		return false
	}
	for name, raw := range a {
		other, ok := b[name]
		if !ok || !jsonEqual(raw, other) {
			return false
		}
	}
	return true
}

// checkReservations refuses a plan whose pending nodes another actor has
// reserved: the reservation is a declaration of intent that others honour. A
// node that the plan only reads, and that another actor has reserved, is
// reported and does not stop the plan.
func (e *Engine) checkReservations(plan *Plan) error {
	locks, err := e.State.Locks()
	if err != nil || len(locks) == 0 {
		return nil
	}
	held := map[string]state.Lock{}
	for _, l := range locks {
		if l.Kind == "reservation" && (l.Expires == nil || time.Now().Before(*l.Expires)) && !e.State.OwnsReservation(l) {
			held[l.Addr] = l
		}
	}
	for _, ch := range plan.Changes {
		if !pending(ch) || ch.Applied {
			continue
		}
		if l, ok := held[ch.Addr]; ok {
			return WithCode(ExitLocked, fmt.Errorf("plan rejected: %w", &state.ErrLocked{Lock: l}))
		}
	}
	read := make([]string, 0, len(plan.Inputs))
	for addr := range plan.Inputs {
		if _, ok := held[addr]; ok {
			read = append(read, addr)
		}
	}
	sort.Strings(read)
	for _, addr := range read {
		e.step("note", addr, "warn", "", time.Time{}, "Warning: %v; this plan reads it", &state.ErrLocked{Lock: held[addr]})
	}
	return nil
}

// reserve takes reservations on the plan's pending nodes when requested: all
// of them or, when one is held by a running command of another actor, none
// of them, and what the actor held before the plan stays as it was. A
// reservation the actor already holds is extended and not shortened, so the
// plan reports the earliest end among its nodes.
func (e *Engine) reserve(plan *Plan) error {
	if e.Opts.Reserve <= 0 {
		return nil
	}
	until := time.Now().UTC().Add(e.Opts.Reserve)
	var addrs []string
	for _, ch := range plan.Changes {
		if pending(ch) && !ch.Applied {
			addrs = append(addrs, ch.Addr)
		}
	}
	sort.Strings(addrs)
	ends, err := e.State.ReserveAll(addrs, e.Opts.Command, until)
	if err != nil {
		var locked *state.ErrLocked
		if errors.As(err, &locked) {
			return WithCode(ExitLocked, fmt.Errorf("cannot reserve: %w", err))
		}
		return err
	}
	var earliest time.Time
	for _, a := range addrs {
		end := ends[a]
		if earliest.IsZero() || end.Before(earliest) {
			earliest = end
		}
		e.emit(Event{Kind: "reserve", Addr: a, Phase: "done", Message: "reserved until " + end.Format(time.RFC3339)})
	}
	if len(addrs) > 0 {
		plan.Reservation = &ReservationView{Until: earliest, Addrs: addrs}
	}
	return nil
}

// orderForDestroy sorts delete changes so that dependents go first.
func orderForDestroy(changes []*Change) []*Change {
	byAddr := map[string]*Change{}
	for _, c := range changes {
		byAddr[c.Addr] = c
	}
	// dependents[x] = resources that depend on x
	dependents := map[string]int{}
	for _, c := range changes {
		for _, d := range c.Deps {
			if _, ok := byAddr[d]; ok {
				dependents[d]++
			}
		}
	}
	var out []*Change
	remaining := append([]*Change(nil), changes...)
	sort.Slice(remaining, func(i, j int) bool { return remaining[i].Addr < remaining[j].Addr })
	for len(remaining) > 0 {
		progress := false
		var next []*Change
		for _, c := range remaining {
			if dependents[c.Addr] == 0 {
				out = append(out, c)
				for _, d := range c.Deps {
					dependents[d]--
				}
				progress = true
			} else {
				next = append(next, c)
			}
		}
		if !progress {
			// Cycle in recorded dependencies; fall back to alphabetical.
			out = append(out, next...)
			break
		}
		remaining = next
	}
	return out
}

func idSuffix(v tftypes.Value) string {
	if v.IsNull() || !v.IsKnown() {
		return ""
	}
	var m map[string]tftypes.Value
	if err := v.As(&m); err != nil {
		return ""
	}
	id, ok := m["id"]
	if !ok || id.IsNull() || !id.IsKnown() || !id.Type().Is(tftypes.String) {
		return ""
	}
	var s string
	_ = id.As(&s)
	return "[id=" + s + "]"
}

// idOf returns the provider id of an object, or "".
func idOf(v tftypes.Value) string {
	s := idSuffix(v)
	if s == "" {
		return ""
	}
	return strings.TrimSuffix(strings.TrimPrefix(s, "[id="), "]")
}

func since(t time.Time) string {
	d := time.Since(t).Round(time.Second)
	if d < time.Second {
		return "0s"
	}
	return d.String()
}

// Addresses returns the addresses of all changes, for messages.
func Addresses(chs []*Change) string {
	var a []string
	for _, c := range chs {
		a = append(a, c.Addr)
	}
	return strings.Join(a, ", ")
}

// renameHints finds a resource to be created and a record to be deleted that
// are the same thing under a new address: same type, same configuration. A
// rename that nobody declared would otherwise destroy and create it.
func (e *Engine) renameHints(changes []*Change) []string {
	var deletes []*Change
	for _, d := range changes {
		if d.Action == ActionDelete {
			deletes = append(deletes, d)
		}
	}
	if len(deletes) == 0 {
		return nil
	}
	used := map[string]bool{}
	var hints []string
	for _, c := range changes {
		if c.Action != ActionCreate || c.Mode != "managed" || c.ConfigHash == "" {
			continue
		}
		for _, d := range deletes {
			if d.Mode != c.Mode || d.Type != c.Type || used[d.Addr] {
				continue
			}
			if sr := e.State.Get(d.Addr); sr != nil && sr.ConfigHash == c.ConfigHash {
				used[d.Addr] = true
				hints = append(hints, fmt.Sprintf("%s looks like %s under a new address; to keep the existing object, add $from: %q to %s", c.Addr, d.Addr, d.Addr, c.Addr))
				break
			}
		}
	}
	return hints
}

// mergeOutputs combines the outputs recorded now with the ones an apply
// computed. Another apply that ran at the same time may have recorded outputs
// since this one began (baseline holds what this one started with), so a
// computed output replaces the recorded one only when it differs from the
// baseline or nothing is recorded; an output that is no longer declared is
// dropped. An output left out of a partial apply is not computed and keeps the
// value recorded.
func mergeOutputs(recorded, baseline, computed map[string]json.RawMessage, declared []string) map[string]json.RawMessage {
	keep := map[string]bool{}
	for _, name := range declared {
		keep[name] = true
	}
	out := make(map[string]json.RawMessage, len(recorded))
	for name, raw := range recorded {
		if keep[name] {
			out[name] = raw
		}
	}
	for name, raw := range computed {
		if old, ok := baseline[name]; !ok || !jsonEqual(old, raw) || out[name] == nil {
			out[name] = raw
		}
	}
	return out
}
