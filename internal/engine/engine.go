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
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"cuelang.org/go/cue"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
	"go.opentelemetry.io/otel/attribute"

	"github.com/pravo-systems/pravo/internal/config"
	"github.com/pravo-systems/pravo/internal/convert"
	"github.com/pravo-systems/pravo/internal/env"
	"github.com/pravo-systems/pravo/internal/protect"
	"github.com/pravo-systems/pravo/internal/schema"
	"github.com/pravo-systems/pravo/internal/state"
)

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
	Replace []string
	Destroy bool
	// Command is the command line as the state shows it in transactions, locks and
	// the summaries of stored plans. In a sealed state it holds the tags of the
	// values that are sealed: see state.Store.ShowCommand.
	Command string
	// Args, when set, are the arguments Command was made from (os.Args without the
	// program name). The plan files keep the command line they give, as it was typed.
	Args []string
	// Identity, if set, returns the key of this process to the states that are
	// sealed. It is called only when remote_outputs finds that the state of another
	// root is sealed, so that a command that never needs a key never asks for a
	// passphrase. readers are the reader keys with a lock in the current group of
	// that state, as its meta.json lists them, for a caller that has several
	// identities and chooses the one that can read it; they are nil when meta.json
	// could not be read.
	Identity func(readers []protect.ReaderPublic) (*protect.Identity, error)
	// Readers are the readers of the readers file, against which the signatures of the
	// policy versions are checked.
	Readers []protect.Reader
	// Vouched are the transactions an admin vouched for, and StrictKeys refuses a transaction
	// signed by a key that is not in the roles and was not vouched for, once the roles are
	// pinned in the state. See state.RecordRules.
	Vouched    map[string]protect.Vouch
	StrictKeys bool
	// Judged is called when an apply judges its plan, before the verdict is known.
	Judged   func()
	Progress func(format string, args ...any)
	// Env is the environment the specification reads: the variables its
	// pravo.#Env and pravo.#SecretEnv nodes name, and the credentials of the state of
	// another root (AWS_SESSION_TOKEN, PRAVO_STATE_TOKEN). It is empty unless set.
	Env env.Env
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

// Engine runs plans and applies. Its state is grouped by what uses it: the
// evaluation of the specification (cueState), the dependency graph (graphState),
// what a plan has read (readState), the apply that runs (applyState) and the
// hooks that tests set (testHooks). The groups are embedded, so a field is
// reached as e.name.
type Engine struct {
	Root      *config.Root
	State     *state.Store
	Providers *Providers
	Schemas   map[string]*schema.ProviderSchema
	Value     cue.Value
	Opts      Options

	cueState
	graphState
	readState
	applyState
	testHooks

	mu      sync.Mutex
	changes map[string]*Change
	deps    map[string][]string
	byPath  map[string]*config.Resource
	// now is the clock that records are aged against; nil is the wall clock.
	now   func() time.Time
	stats Stats
	// open lists the ephemeral resources opened during the current run. keepOpen
	// makes a run leave them open for its caller to close.
	open     []*openEphemeral
	keepOpen bool
	// secretPins holds, by name, the version of each secret of the sealed state that the current
	// run reads, so that the value of a pravo.#Secret and the number of a pravo.#SecretVersion
	// belong together; secretMu guards it. See secretVersion.
	secretMu   sync.Mutex
	secretPins map[string]int
	// traceID is the OpenTelemetry trace of the current run, if any.
	traceID string
	// runCtx is the context a plan or an apply was started with, so that what
	// follows it (the policy, the saved plan) is part of the same trace.
	runCtx context.Context
}

// cueState is the evaluation of the specification.
type cueState struct {
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
}

// graphState is the dependency graph.
type graphState struct {
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
}

// readState is what a plan found out about the records and the providers.
type readState struct {
	// reads holds what this plan read from the providers, by address, so that a
	// node is read once: the next walk of the graph and the apply that follows
	// use the answer.
	reads map[string]*readResult
	// dataReads holds what this plan read from the data sources, by address, with
	// the hash of the configuration that was read.
	dataReads map[string]dataRead
	// imported holds what each import made by this plan named, by address.
	imported map[string]importMark
	// touchedAddrs are the records that the apply of the declarations wrote (see declared.go).
	touchedAddrs map[string]bool
	// ages holds the refresh ages that were parsed, by their text.
	ages map[string]parsedAge
	// suspects holds the stale locks the plan found, by address: the node was
	// locked by an apply that stopped. targets holds, by provider key, the digest
	// of the configuration of the provider instance for the walk that is running.
	suspects map[string]state.Lock
	targets  map[string]*target
}

// applyState is the apply that runs, or the plan it runs from.
type applyState struct {
	// planID and planDigest name the plan an apply runs from (see SetPlan).
	planID, planDigest string

	// planFile is the whole plan file an apply runs from, when it runs from one.
	planFile *PlanFile

	txn *state.Txn
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
}

// testHooks are set by tests only.
type testHooks struct {
	// onTask, when set, is told the address of each task a walk starts, onConvert
	// the address of each configuration that is converted for its provider and
	// onFill the address of each task whose values are queued for the
	// configuration; adjustScope, when set, may change the scope before the apply
	// walks it.
	onTask      func(addr string, apply bool)
	onConvert   func(addr string, apply bool)
	onFill      func(addr string)
	adjustScope func(*applyScope)
	// readOnce, when set, stands in for the read of the inputs of the reached
	// nodes that readInputs makes in each round.
	readOnce func(ctx context.Context) ([]string, error)
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
		cueState: cueState{current: value}}
	// With roles, the transactions sign the records they write.
	st.BindRecords(protect.RolesOn(opts.Readers))
	for _, r := range root.Resources {
		e.byPath[r.Path.String()] = r
	}
	st.SetActor(opts.Actor, opts.Reason, opts.GitRevision)
	// In a sealed state the actor is the signing identity, whatever label was given.
	e.Opts.Actor = st.Actor()
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
		s = ps.DataSource(r.Type)
	case "ephemeral":
		s = ps.Ephemeral(r.Type)
	default:
		s = ps.Resource(r.Type)
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
		return errors.New("-replace cannot be combined with -destroy")
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
	_, done := e.phase(ctx, "check specification")
	err := e.checkSpec()
	done(err)
	if err != nil {
		return nil, err
	}
	_, done = e.phase(ctx, "begin transaction")
	e.txn = e.State.Begin("plan", e.Opts.Command)
	e.resetRun(false)
	e.forgetReads()
	e.loadSuspects()
	e.warnSuspects()
	done(nil)
	plan := &Plan{Outputs: map[string]cue.Value{}, Destroy: e.Opts.Destroy, NoRefresh: e.Opts.Refresh == RefreshNone}
	if e.Opts.Destroy {
		pctx, done := e.phase(ctx, "plan destroy")
		changes, err := e.planOrphans(pctx, nil)
		done(err)
		if err != nil {
			e.finishError("failed", err, false)
			return nil, err
		}
		plan.Changes = changes
		plan.Stats = e.statsNow()
		return plan, e.finishPhase(ctx, "planned", e.summary(plan), false)
	}
	_, done = e.phase(ctx, "moves")
	relabels, err := e.relabelProviders(true)
	if err != nil {
		done(err)
		e.finishError("failed", err, true)
		return nil, err
	}
	plan.Relabels = relabels
	moves, err := e.applyMoves(ctx, true)
	done(err)
	if err != nil {
		e.finishError("failed", err, true)
		return nil, err
	}
	plan.Changes = append(plan.Changes, moves...)
	pctx, done := e.phase(ctx, "evaluate and refresh", attribute.Int("pravo.tasks", len(e.Root.Resources)))
	final, err := e.run(pctx, false)
	done(err)
	if err == nil && e.Opts.Refresh == RefreshAge {
		rctx, done := e.phase(ctx, "read inputs")
		final, err = e.readInputs(rctx, final)
		done(err)
	}
	if err != nil {
		e.finishError("failed", err, false)
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
	octx, done := e.phase(ctx, "plan orphans")
	orphans, err := e.planOrphans(octx, present)
	done(err)
	if err != nil {
		e.finishError("failed", err, false)
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
	_, done = e.phase(ctx, "frontier")
	err = e.frontier(plan)
	done(err)
	if err != nil {
		e.finishError("failed", err, false)
		return nil, err
	}
	plan.Stats = e.statsNow()
	if plan.Policies, err = e.planPolicies(ctx); err != nil {
		e.finishError("failed", err, false)
		return nil, err
	}
	_, done = e.phase(ctx, "reservations")
	err = e.checkReservations(plan)
	if err == nil {
		err = e.reserve(plan)
	}
	done(err)
	if err != nil {
		e.finishError("rejected", err, true)
		return nil, err
	}
	return plan, e.finishPhase(ctx, "planned", e.summary(plan), false)
}

// checkSpec runs the checks of the specification that need no provider.
func (e *Engine) checkSpec() error {
	if err := e.checkOutputs(); err != nil {
		return err
	}
	if err := e.checkReplace(); err != nil {
		return err
	}
	if e.Opts.Destroy {
		return e.checkDestroy()
	}
	return nil
}

// finishPhase finishes the transaction in a span of its own.
func (e *Engine) finishPhase(ctx context.Context, status, summary string, record bool) error {
	_, done := e.phase(ctx, "finish transaction")
	err := e.txn.Finish(status, summary, record)
	done(err)
	return err
}

// errText is the summary a transaction records for a run that failed or was
// rejected. The text of an error can hold a value that a provider echoed, or a
// message of a policy over a value, so in a sealed state, where the history of
// the state is readable by anyone who can read the store, the summary does not
// hold it: finishError keeps the text sealed for the readers.
func (e *Engine) errText(err error) string {
	if e.State.Sealer() == nil {
		return err.Error()
	}
	// The notes about the changes whose result is unknown name addresses and
	// actions, and say what a person has to check.
	text := state.SealedErrorText
	if notes := e.unsettledNotes(); len(notes) > 0 {
		text += "; " + strings.Join(notes, "; ")
	}
	return text
}

// finishError ends the transaction of a run that failed or was rejected. A
// sealed state keeps the whole text of the error sealed in the transaction, so
// that a reader keeps the diagnostic (see errText).
func (e *Engine) finishError(status string, err error, record bool) {
	full := ""
	if e.State.Sealer() != nil {
		full = err.Error()
		if notes := e.unsettledNotes(); len(notes) > 0 {
			full += "; " + strings.Join(notes, "; ")
		}
	}
	_ = e.txn.FinishError(status, e.errText(err), full, record)
}

func (e *Engine) summary(plan *Plan) string {
	add, change, destroy, _ := plan.Counts()
	return fmt.Sprintf("%d to add, %d to change, %d to destroy; %d of %d reached, %d skipped, %d refreshed, %d within refresh age, %d not read, %d ephemeral opened, %d functions called, %d imported, %d moved",
		add, change, destroy, plan.Stats.Reached, plan.Stats.Total, plan.Stats.Skipped, plan.Stats.Refreshed, plan.Stats.Fresh, plan.Stats.NotRead, plan.Stats.Opened, plan.Stats.Called, plan.Stats.Imported, plan.Stats.Moved)
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
	e.stats = Stats{Total: e.countPlannable()}
	e.secretMu.Lock()
	e.secretPins = nil
	e.secretMu.Unlock()
}

// countPlannable is the number of resources and data sources in the
// configuration: the nodes that a plan plans or skips, and so the M of "planned N
// of M". The environment variables, the remote outputs, the ephemeral resources
// and the function calls are tasks of the graph that are not planned against a
// record; the last two have counts of their own.
func (e *Engine) countPlannable() int {
	n := 0
	for _, r := range e.Root.Resources {
		if r.Mode == "managed" || r.Mode == "data" {
			n++
		}
	}
	return n
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

// SetPlan records the plan that the next apply runs from: the id of a stored plan, empty
// for a plan file, and the digest of the plan file (see PlanDigest). The transaction of the
// apply carries both under its signature.
func (e *Engine) SetPlan(id, digest string) { e.planID, e.planDigest = id, digest }

// checkBound refuses an apply that reads, to change or replace, a record which no verified
// transaction of a writer names. It does nothing when the readers file gives no roles or
// the state is not sealed. See Store.CheckRecords.
func (e *Engine) checkBound(addrs []string) error {
	if !protect.RolesOn(e.Opts.Readers) || e.State.Sealer() == nil || e.State.Locked() {
		return nil
	}
	v := e.State.CheckRecordsWith(state.RecordRules{Readers: e.Opts.Readers, Vouched: e.Opts.Vouched, Strict: e.Opts.StrictKeys}, addrs...)
	if len(v.Problems) == 0 {
		return nil
	}
	var lines []string
	for _, p := range v.Problems {
		lines = append(lines, fmt.Sprintf("%s: %s", p.Addr, p.Why))
	}
	return WithCode(ExitUntrusted, fmt.Errorf("%w: a record that no transaction of a writer accounts for (a reader may have sealed it):\n%s", state.ErrUntrusted, strings.Join(lines, "\n")))
}
