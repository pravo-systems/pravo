package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"cuelang.org/go/cue"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
	"go.opentelemetry.io/otel/attribute"

	"github.com/pravo-systems/pravo/internal/config"
	"github.com/pravo-systems/pravo/internal/convert"
	"github.com/pravo-systems/pravo/internal/plog"
	"github.com/pravo-systems/pravo/internal/plugin"
	"github.com/pravo-systems/pravo/internal/state"
	"github.com/pravo-systems/pravo/internal/telemetry"
)

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
		PreventDestroy: ch.PreventDestroy,
	}
	if action == "partial" {
		// The provider returned the state it had reached before it failed. Without
		// a hash, the next plan reaches the node.
		n.ConfigHash = ""
	}
	before := ch.removedRevision
	n.Revision = ch.removedRevision
	rekeyed := false
	var priorAttrs json.RawMessage
	if prev := e.State.Get(ch.Addr); prev != nil {
		priorAttrs = prev.Attributes
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
		e.recordDiff(ch.Addr, ch.Schema, priorAttrs, n.Attributes, ch.Derived)
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
		e.unrecorded(ch.Addr, what, idOf(ch, v))
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
	e.inheritCBD(ch)
	e.adoptPredestroyed(ch)
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
		e.step(string(ch.Action), ch.Addr, "start", idOf(ch, ch.Prior), time.Time{}, "%s: %s... %s", ch.Addr, verb, idSuffix(ch, ch.Prior))
		// A create over a record (the object it records is gone) is noted like the
		// creation of a replacement: the lock cannot carry the mark, because a plan
		// reclaims the stale lock of a node that has a record.
		noted := false
		if ch.Action == ActionCreate {
			if rec := e.State.Get(ch.Addr); rec != nil && rec.ID() != "" {
				if err := e.State.NotePendingCreate(ch.Addr, rec.ID(), rec.AppliedAt, true, e.txnID(), e.Opts.Command); err != nil {
					return tftypes.Value{}, fmt.Errorf("%s: noting the create: %w", ch.Addr, err)
				}
				noted = true
			}
		}
		res, err := e.applyChange(ctx, prov, ch, ch.Prior, ch.Planned, ch.Config, ch.PlannedPrivate)
		if err != nil {
			// A create that failed with nothing in doubt leaves no note.
			if noted && !e.isUnsettled(ch.Addr) {
				_ = e.State.ClearPendingCreate(ch.Addr)
			}
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
		if noted {
			_ = e.State.ClearPendingCreate(ch.Addr)
		}
		e.step(string(ch.Action), ch.Addr, "done", idOf(ch, final), start, "%s: %s complete after %s %s", ch.Addr, done, since(start), idSuffix(ch, final))
	case ActionReplace:
		nullV := tftypes.NewValue(ch.Schema.Block.Type(), nil)
		if ch.CreateBeforeDestroy {
			e.Opts.Progress("%s: Creating replacement...", ch.Addr)
			// The record keeps the old object until the replacement is recorded. A note that
			// outlives a command that stops is what keeps the next apply from creating the
			// replacement again (the lock does not: a plan reclaims it).
			noted := false
			var oldRec *state.Resource
			if rec := e.State.Get(ch.Addr); rec != nil {
				cp := *rec
				oldRec = &cp
			}
			if rec := e.State.Get(ch.Addr); rec != nil && rec.ID() != "" {
				if err := e.State.NotePendingCreate(ch.Addr, rec.ID(), rec.AppliedAt, ch.Action == ActionCreate, e.txnID(), e.Opts.Command); err != nil {
					return tftypes.Value{}, fmt.Errorf("%s: noting the replacement: %w", ch.Addr, err)
				}
				noted = true
			}
			// The old object is kept in the state under an address of its own until its
			// destroy has finished, so that an apply that stops or fails from here on does
			// not forget it, whether the create is killed, fails with a state of the new
			// object (which is recorded over the node's record) or succeeds: a plan lists
			// the record as one to destroy, and the next apply retries. While the node's
			// record still holds the old object the plan takes the two records for one
			// object, and the note refuses a second create.
			deposed := e.depose(ch.Addr, oldRec)
			res, err := e.applyChange(ctx, prov, ch, nullV, ch.PlannedCreate, ch.Config, ch.CreatePrivate)
			if err != nil {
				// A create that failed with nothing in doubt leaves no note and, when the
				// node's record still names the old object, no second record of it.
				if !e.isUnsettled(ch.Addr) {
					if rec := e.State.Get(ch.Addr); oldRec != nil && rec != nil && rec.ID() == oldRec.ID() && sameApplied(rec, oldRec) {
						e.undepose(deposed)
					}
					if noted {
						_ = e.State.ClearPendingCreate(ch.Addr)
					}
				}
				return tftypes.Value{}, err
			}
			final, private = res.NewState, res.Private
			if err := e.recordApplied(ch, "creation", final, private, "replace"); err != nil {
				return tftypes.Value{}, err
			}
			if noted {
				_ = e.State.ClearPendingCreate(ch.Addr)
			}
			// The destroy waits for the end of the walk: what depends on this node is updated
			// first, so that nothing points at the old object when it goes. The closures run
			// in reverse order of registration, which puts dependents before what they use.
			e.deferDestroy(ch, func(ctx context.Context) error {
				e.Opts.Progress("%s: Destroying previous instance... %s", ch.Addr, idSuffix(ch, ch.Prior))
				if _, err := e.applyChange(ctx, prov, ch, ch.Prior, nullV, nullV, ch.PriorPrivate); err != nil {
					if deposed != "" {
						err = fmt.Errorf("%w; the previous instance (id %s) is kept in the state as %s, and the next apply destroys it", err, oldRec.ID(), deposed)
					}
					return err
				}
				e.undepose(deposed)
				return nil
			})
		} else {
			if !ch.predestroyed {
				if err := e.destroyOld(ctx, prov, ch); err != nil {
					return tftypes.Value{}, err
				}
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
		e.step("replace", ch.Addr, "done", idOf(ch, final), start, "%s: Replacement complete after %s %s", ch.Addr, since(start), idSuffix(ch, final))
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
	// A create is noted on the lock before the provider is asked, so that an apply that is
	// killed from here on is known to have maybe created the object.
	if prior.IsNull() && !planned.IsNull() && e.held != nil {
		if err := e.State.MarkCreating(ch.Addr); err != nil {
			var locked *state.ErrLocked
			if errors.Is(err, state.ErrNotFound) || errors.As(err, &locked) {
				return nil, lostLock(ch.Addr, err)
			}
			return nil, fmt.Errorf("%s: noting the create on its lock: %w", ch.Addr, err)
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
	if res != nil && !res.NewState.IsNull() && !planned.IsNull() && !res.LegacyTypeSystem {
		// A provider written for the legacy SDK is known to return values other than the
		// planned ones, and the check leaves it alone, as OpenTofu does. Any other provider
		// that does so has a bug, and the state that it returned is recorded all the same, so
		// that the object is not forgotten.
		if bad := inconsistencies(planned, res.NewState); len(bad) > 0 {
			err := fmt.Errorf("%s: the provider produced an inconsistent result after apply (this is a bug in the provider, not in the specification):\n  %s", ch.Addr, strings.Join(bad, "\n  "))
			if werr := e.recordApplied(ch, what, res.NewState, res.Private, "partial"); werr != nil {
				return nil, fmt.Errorf("%w; the state it returned: %w", err, werr)
			}
			return nil, err
		}
	}
	return res, nil
}

// ApplyResult summarises an apply. It is returned with the error of an apply that
// failed or was interrupted, so that the caller knows what that apply did.
type ApplyResult struct {
	Added, Changed, Destroyed int
	Outputs                   map[string]json.RawMessage
	// ChangedOutputs lists, sorted, the outputs whose recorded value is new or
	// differs from the one recorded when the apply began.
	ChangedOutputs []string
	Txn            string
	// Applied lists, sorted, the addresses of the plan's changes that the apply
	// finished and recorded in the state: the creates, updates, replacements and
	// deletes whose provider call returned and whose record was written, and the
	// records and data sources the plan only wrote. A change that failed, that
	// returned part of its state, whose result is not known or whose record
	// could not be written is not in it, nor is a change that was not started.
	// Outputs are not changes: an apply that fails before it records them
	// leaves them to the next one, whatever Applied holds.
	Applied []string
	// Policies lists the policy versions the apply wrote: the new policies, the
	// new texts and the removals, with the sequence number of each version.
	Policies []PolicyWritten
	// Deferred is the number of changes that the plan deferred to the next plan: the apply
	// left them alone.
	Deferred int
}

// PolicyWritten is a version of a policy that an apply wrote.
type PolicyWritten struct {
	Name string `json:"name"`
	// Seq is the number of the version written.
	Seq int `json:"seq"`
	// Change is "new", "changed" or "removed".
	Change string `json:"change"`
}

// deferDestroy keeps the destroy of the old instance of a create_before_destroy replacement
// for the end of the apply.
func (e *Engine) deferDestroy(ch *Change, f func(context.Context) error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.destroyLater = append(e.destroyLater, deferredDestroy{ch: ch, run: f})
}

// sameApplied reports whether two records were applied at the same time, or one does not say.
func sameApplied(a, b *state.Resource) bool {
	return a.AppliedAt == nil || b.AppliedAt == nil || a.AppliedAt.Equal(*b.AppliedAt)
}

// holdDeferredDestroys drops the destroys that wait for the end of the apply, and returns how many
// there were: the old instances stay as deposed records.
func (e *Engine) holdDeferredDestroys() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	n := len(e.destroyLater)
	e.destroyLater = nil
	return n
}

// runDeferredDestroys destroys the old instances that replacements left, with the parallelism
// of the apply. The old instance of a node goes after the old instances of the nodes that
// depend on it, directly or through nodes that are not replaced that way. After a failure no
// further destroy starts, the ones that run finish, and the old instances that did not run stay
// as deposed records for the next apply.
func (e *Engine) runDeferredDestroys(ctx context.Context) error {
	e.mu.Lock()
	fs := e.destroyLater
	e.destroyLater = nil
	byAddr := make(map[string]*Change, len(e.changes))
	for addr, ch := range e.changes {
		byAddr[addr] = ch
	}
	e.mu.Unlock()
	if len(fs) == 0 {
		return nil
	}
	runs := make(map[string]func(context.Context) error, len(fs))
	for _, d := range fs {
		runs[d.ch.Addr] = d.run
	}
	// Every node of the walk that these depend on, through whatever chain of nodes, is an
	// edge: the pseudo changes list the replaced nodes that each one reads.
	pseudo := make([]*Change, 0, len(fs))
	for _, d := range fs {
		var deps []string
		seen := map[string]bool{d.ch.Addr: true}
		var walk func(addr string)
		walk = func(addr string) {
			c := byAddr[addr]
			if c == nil {
				return
			}
			for _, dep := range c.Deps {
				if seen[dep] {
					continue
				}
				seen[dep] = true
				if runs[dep] != nil {
					deps = append(deps, dep)
				}
				walk(dep)
			}
		}
		walk(d.ch.Addr)
		pseudo = append(pseudo, &Change{Addr: d.ch.Addr, Deps: deps})
	}
	return inReverse(pseudo, Parallelism, func(c *Change) error { return runs[c.Addr](ctx) })
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
	// The state must serve the environment of the run, and an environment that follows
	// another takes only a release that was promoted into it.
	admitted, note, err := e.admit(ctx, plan)
	if err != nil {
		return &ApplyResult{Outputs: map[string]json.RawMessage{}}, err
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
		if plan.NeedsApply() {
			if err := e.preflightRecord(ctx); err != nil {
				return &ApplyResult{Outputs: map[string]json.RawMessage{}}, err
			}
			if err := e.preflightPolicies(plan); err != nil {
				return &ApplyResult{Outputs: map[string]json.RawMessage{}}, err
			}
		}
		if !plan.NeedsApply() {
			// Nothing to write and no output to record or remove: no lock, no record, no
			// transaction. A state of an environment notes that it runs this release, unless
			// the release was not promoted into it.
			if !plan.Partial && note {
				// The note writes the record with the pin of the roles, which a key that may
				// not put meta.json cannot: that is the same refusal as for a changing apply.
				if err := e.preflightRecord(ctx); err != nil {
					return &ApplyResult{Outputs: map[string]json.RawMessage{}}, err
				}
				if err := e.noteRun(ctx, admitted, "", plan.DeferredCount()); err != nil {
					return &ApplyResult{Outputs: map[string]json.RawMessage{}}, err
				}
			}
			return &ApplyResult{Outputs: map[string]json.RawMessage{}, Deferred: plan.DeferredCount()}, nil
		}
	}
	if err := e.checkInterruptedCreates(plan); err != nil {
		return &ApplyResult{Outputs: map[string]json.RawMessage{}}, err
	}
	e.txn = e.State.Begin(kind, e.Opts.Command)
	e.txn.SetPlan(e.planID, e.planDigest)
	e.mu.Lock()
	e.completed = nil
	e.mu.Unlock()
	result := &ApplyResult{Outputs: map[string]json.RawMessage{}, Txn: e.txn.ID(), Deferred: plan.DeferredCount()}
	// The outputs as they were when this apply began. Another apply that runs
	// at the same time may record outputs of its own; only the outputs this
	// apply changes are written over them.
	baseline := e.State.Outputs()
	fail := func(err error) (*ApplyResult, error) {
		err = e.withUnsettled(err)
		result.Applied = e.completedAddrs()
		e.finishError("failed", err, true)
		return result, err
	}
	// What the specification declares about the state is written first: the plan staged it.
	if !e.Opts.Destroy {
		_, done := e.phase(ctx, "commit moves")
		err := e.commitMoves(plan)
		done(err)
		if err != nil {
			e.finishError("rejected", err, true)
			return result, err
		}
	}
	lockStart := time.Now()
	_, done := e.phase(ctx, "lock")
	held, err := e.lockAll(plan)
	done(err)
	if err != nil {
		e.finishError("rejected", err, true)
		return result, err
	}
	defer held.release()
	locked := len(held.held())
	telemetry.LockWait(ctx, time.Since(lockStart), locked)
	plog.Debug(ctx, fmt.Sprintf("locked %d resources", locked))
	// With the locks held, the records are read from the backend again: a node
	// that another process wrote after the plan was made is not applied over.
	_, done = e.phase(ctx, "check revisions")
	err = e.checkRevisions(plan, sc.inputsOf(plan))
	done(err)
	if err != nil {
		e.finishError("rejected", err, true)
		return result, err
	}
	// With roles, an apply does not build on a record that no transaction of a writer
	// accounts for: someone who holds the data key sealed it.
	if err := e.checkBound(held.held()); err != nil {
		e.finishError("rejected", err, true)
		return result, err
	}
	// The policies in force judge the resource changes of the whole plan file before
	// the start is signed, which lists the versions that judged.
	var policies *state.PolicySet
	if locked > 0 || len(plan.Policies) > 0 {
		var err error
		if policies, err = e.judgeApply(ctx, plan, locked > 0); err != nil {
			e.finishError("rejected", err, true)
			return result, err
		}
	}
	if locked > 0 || e.txn.Started() {
		// An apply that is killed from here on leaves a transaction that is still
		// running, and locks that go stale. One that started for the moves it committed
		// is written again, with the policies that judged the plan.
		if err := e.txn.Start(); err != nil {
			return fail(err)
		}
	}
	e.held = held
	defer func() { e.held = nil }()
	defer e.register(ctx, held)()
	if !e.Opts.Destroy {
		if err := e.commitImports(plan); err != nil {
			return fail(err)
		}
	}
	// A provider that the plan configured before what its configuration refers to existed is
	// started again, and configured with the values the apply fills in.
	e.Providers.DropPartial()
	if !e.Opts.Destroy {
		defer e.approve(plan)()
		e.scope = sc
		defer func() { e.scope = nil }()
		e.resetRun(true)
		e.mu.Lock()
		e.applyPlan = plan
		e.mu.Unlock()
		defer func() { e.mu.Lock(); e.applyPlan = nil; e.mu.Unlock() }()
		if err := e.destroyReplaced(ctx, plan); err != nil {
			return fail(err)
		}
		actx, done := e.phase(ctx, "apply changes", attribute.Int("pravo.tasks", len(plan.Changes)))
		final, err := e.run(actx, true)
		done(err)
		if err != nil {
			return fail(err)
		}
		e.tally(result)
		if plan.DeferredCount() > 0 {
			// A change that waits for a value of this apply still points at the old
			// instances: they stay as deposed records, and the plan that follows destroys them
			// after the deferred changes were applied.
			result.Destroyed -= e.holdDeferredDestroys()
		}
		if err := e.evalOutputs(final, plan, result); err != nil {
			return fail(err)
		}
	}
	// Orphans were planned already; destroy them, dependents first.
	dctx, done := e.phase(ctx, "destroy orphans")
	destroyed, err := e.destroyOrphans(dctx, plan)
	done(err)
	result.Destroyed += destroyed
	if err != nil {
		return fail(err)
	}
	// The old instances of create_before_destroy replacements go last: the nodes that were
	// updated in the walk and the ones that were removed are not pointing at them any more.
	if err := e.runDeferredDestroys(ctx); err != nil {
		return fail(err)
	}
	if !e.Opts.Destroy {
		bctx, done := e.phase(ctx, "read back")
		e.readBack(bctx)
		done(nil)
	}
	_, outDone := e.phase(ctx, "record outputs")
	err = e.recordOutputs(plan, baseline, result)
	outDone(err)
	if err != nil {
		return fail(err)
	}
	for name, raw := range result.Outputs {
		if prev, ok := baseline[name]; !ok || !jsonEqual(prev, raw) {
			result.ChangedOutputs = append(result.ChangedOutputs, name)
		}
	}
	sort.Strings(result.ChangedOutputs)
	// The versions of the policies come last, from the stage that leaves no resource change
	// unapplied.
	if len(plan.Policies) > 0 && !plan.Partial {
		if err := e.writePolicies(ctx, policies, plan); err != nil {
			return fail(err)
		}
		var written []string
		for _, en := range plan.Policies {
			result.Policies = append(result.Policies, PolicyWritten{Name: en.Name, Seq: en.Base + 1, Change: en.Kind()})
			digest := en.Digest
			if en.removal() {
				digest = state.TextDigest("")
			}
			written = append(written, fmt.Sprintf("%s@%d:%s", en.Name, en.Base+1, digest))
		}
		e.txn.SetPolicyWrites(written)
	}
	result.Applied = e.completedAddrs()
	summary := fmt.Sprintf("%d added, %d changed, %d destroyed", result.Added, result.Changed, result.Destroyed)
	if err := e.finishPhase(ctx, "applied", summary, true); err != nil {
		return result, err
	}
	// The state of an environment notes what it runs now: what the environments after it
	// are promoted from. A partial apply leaves part of the specification unapplied.
	if !plan.Partial && !e.Opts.Destroy {
		if err := e.noteRun(ctx, admitted, e.txn.ID(), plan.DeferredCount()); err != nil {
			return result, err
		}
	}
	return result, nil
}

// tally counts the changes this apply made.
func (e *Engine) tally(result *ApplyResult) {
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
}

// evalOutputs reads the outputs the plan evaluates from the final value of the
// specification into the result.
func (e *Engine) evalOutputs(final cue.Value, plan *Plan, result *ApplyResult) error {
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
				continue
			}
			return fmt.Errorf("output %s is not concrete after apply", o.Name)
		}
		b, err := outputJSON(ov)
		if err != nil {
			return fmt.Errorf("output %s: %w", o.Name, err)
		}
		result.Outputs[o.Name] = WrapOutput(b, o.Sensitive)
	}
	return nil
}

// recordOutputs writes the outputs of the apply to the state.
func (e *Engine) recordOutputs(plan *Plan, baseline map[string]json.RawMessage, result *ApplyResult) error {
	// The outputs are recorded by a read, a merge and a write while their lock is
	// held: an apply that finished meanwhile has recorded outputs of its own.
	if e.Opts.Destroy {
		return e.State.UpdateOutputs(func(recorded map[string]json.RawMessage, _ bool) (map[string]json.RawMessage, bool, error) {
			outputs, err := e.outputsAfterDestroy(plan, recorded)
			return outputs, err == nil, err
		})
	}
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
	recorded, present, err := e.State.LoadOutputs()
	if err != nil {
		return err
	}
	if _, write := merge(recorded, present); !write {
		return nil
	}
	return e.State.UpdateOutputs(func(recorded map[string]json.RawMessage, present bool) (map[string]json.RawMessage, bool, error) {
		outputs, write := merge(recorded, present)
		return outputs, write, nil
	})
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

func idSuffix(ch *Change, v tftypes.Value) string {
	if ch != nil && ch.Schema != nil && ch.Schema.Block != nil {
		// The id of a sensitive file is the hash of its content.
		if a := ch.Schema.Block.Attribute("id"); a != nil && a.Sensitive {
			return ""
		}
	}
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
func idOf(ch *Change, v tftypes.Value) string {
	s := idSuffix(ch, v)
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

// isUnsettled reports whether the apply has noted that the result of the change to addr is
// not known.
func (e *Engine) isUnsettled(addr string) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	_, ok := e.unsettled[addr]
	return ok
}

// depose writes the record of the old object that a create_before_destroy replacement
// replaced under an address of its own, and returns the address ("" when it could not be
// written, in which case the old object is destroyed with nothing to keep it).
func (e *Engine) depose(addr string, old *state.Resource) string {
	if old == nil {
		return ""
	}
	sels := cue.ParsePath(addr).Selectors()
	label := "deposed " + old.ID()
	dep := cue.MakePath(append(append([]cue.Selector{}, sels...), cue.Str(label))...).String()
	cp := *old
	cp.Addr, cp.Name, cp.Revision, cp.Generation = dep, label, 0, ""
	if err := e.State.Set(&cp, e.txnID()); err != nil {
		e.Opts.Progress("Warning: %s: the previous instance (id %s) could not be kept in the state: %v", addr, old.ID(), err)
		return ""
	}
	e.txn.Record(dep, "deposed", 0, cp.Revision)
	return dep
}

// undepose removes the record of an old object that was destroyed.
func (e *Engine) undepose(dep string) {
	if dep == "" {
		return
	}
	rev := 0
	if r := e.State.Get(dep); r != nil {
		rev = r.Revision
	}
	if _, err := e.State.Remove(dep); err != nil {
		e.Opts.Progress("Warning: %s: the destroyed previous instance is still in the state: %v", dep, err)
		return
	}
	e.txn.Record(dep, "destroy", rev, 0)
}

// deposedBase returns the address of the node whose old instance the record at addr keeps,
// when addr is the address of a deposed record (see depose).
func deposedBase(addr string) (string, bool) {
	sels := cue.ParsePath(addr).Selectors()
	if len(sels) < 2 || !strings.HasPrefix(sels[len(sels)-1].String(), `"deposed `) {
		return "", false
	}
	return cue.MakePath(sels[:len(sels)-1]...).String(), true
}

// redundantDeposed reports whether the deposed record at addr holds the object that the
// record of its node holds now: the replacement was not recorded, so there is one object, and
// destroying it would destroy the node.
func (e *Engine) redundantDeposed(addr string) bool {
	base, ok := deposedBase(addr)
	if !ok {
		return false
	}
	dep, live := e.State.Get(addr), e.State.Get(base)
	if dep == nil || live == nil || dep.ID() == "" || dep.ID() != live.ID() {
		return false
	}
	// An id is not always unique to an object: a replacement can have the id of the old
	// object. The replacement's record was applied later than the copy that the deposed
	// record holds; when either time is not known the records are taken for one object.
	return dep.AppliedAt == nil || live.AppliedAt == nil || dep.AppliedAt.Equal(*live.AppliedAt)
}

// goneObject is the old object of a replacement that was destroyed ahead of the walk.
type goneObject struct {
	prior    tftypes.Value
	private  []byte
	revision int
}

// destroyOld destroys the old object of a replacement that is not create_before_destroy and removes
// its record. The record goes and comes back: its revision continues, so a plan made against the
// old record is stale.
func (e *Engine) destroyOld(ctx context.Context, prov plugin.Provider, ch *Change) error {
	nullV := tftypes.NewValue(ch.Schema.Block.Type(), nil)
	e.Opts.Progress("%s: Destroying... %s", ch.Addr, idSuffix(ch, ch.Prior))
	if _, err := e.applyChange(ctx, prov, ch, ch.Prior, nullV, nullV, ch.PriorPrivate); err != nil {
		return err
	}
	if prev := e.State.Get(ch.Addr); prev != nil {
		ch.removedRevision = prev.Revision
	}
	if _, err := e.State.Remove(ch.Addr); err != nil {
		e.unrecorded(ch.Addr, "destruction", "")
		return fmt.Errorf("%s: the record could not be removed: %w", ch.Addr, err)
	}
	return nil
}

// replacedIn lists the replacements of a plan whose old object an apply may destroy ahead of
// their own task: managed, not create_before_destroy, with an object to destroy.
func replacedIn(plan *Plan) map[string]*Change {
	replaced := map[string]*Change{}
	for _, ch := range plan.Changes {
		if ch.Action == ActionReplace && !ch.CreateBeforeDestroy && ch.Mode == "managed" && !ch.Applied && !ch.Prior.IsNull() {
			replaced[ch.Addr] = ch
		}
	}
	return replaced
}

// planDeps maps each node of a plan to the nodes it reads. The graph of the walk is not built when
// the destroys are decided: the changes of the plan carry the edges, also for the nodes that do
// not change.
func planDeps(plan *Plan) map[string][]string {
	deps := make(map[string][]string, len(plan.Changes))
	for _, ch := range plan.Changes {
		deps[ch.Addr] = ch.Deps
	}
	return deps
}

// reaches lists, in address order, the nodes of in that addr reads through any chain of nodes.
func reaches(deps map[string][]string, addr string, in func(string) bool) []string {
	var list []string
	seen := map[string]bool{addr: true}
	var walk func(a string)
	walk = func(a string) {
		for _, d := range deps[a] {
			if seen[d] {
				continue
			}
			seen[d] = true
			if in(d) {
				list = append(list, d)
			}
			walk(d)
		}
	}
	walk(addr)
	sort.Strings(list)
	return list
}

// destroyReplaced destroys, before the walk, the old objects of the replacements the plan lists
// that wait for each other: the old object of a node goes after the old objects of the replaced
// nodes that depend on it, directly or through nodes that are not replaced, so a provider that
// refuses to delete what is still in use (a bucket that is not empty) finds it free. The walk then
// creates the new objects in dependency order. A replacement that no other replacement waits for,
// and one with create_before_destroy, is destroyed by its own task as before. After a failure no
// further destroy starts and the ones that run finish; a node whose old object is destroyed has no
// record, so the next apply plans it as a new one.
func (e *Engine) destroyReplaced(ctx context.Context, plan *Plan) error {
	replaced := replacedIn(plan)
	if len(replaced) < 2 {
		return nil
	}
	deps := planDeps(plan)
	pseudo := map[string]*Change{}
	linked := map[string]bool{}
	for addr := range replaced {
		list := reaches(deps, addr, func(d string) bool { return replaced[d] != nil })
		for _, d := range list {
			linked[d], linked[addr] = true, true
		}
		pseudo[addr] = &Change{Addr: addr, Deps: list}
	}
	var chain []*Change
	for _, ch := range plan.Changes {
		if linked[ch.Addr] {
			chain = append(chain, pseudo[ch.Addr])
		}
	}
	return e.destroyChain(ctx, replaced, chain)
}

// destroyChain destroys the old objects of the chain, each after the ones that depend on it, and
// notes them for the walk.
func (e *Engine) destroyChain(ctx context.Context, replaced map[string]*Change, chain []*Change) error {
	if len(chain) == 0 {
		return nil
	}
	warm := newWarmth()
	for _, c := range chain {
		warm.up(replaced[c.Addr].Schema)
	}
	e.mu.Lock()
	if e.predestroyed == nil {
		e.predestroyed = map[string]*goneObject{}
	}
	e.mu.Unlock()
	return inReverse(chain, Parallelism, func(c *Change) error {
		if ctx.Err() != nil {
			return fmt.Errorf("interrupted before %s was destroyed", c.Addr)
		}
		ch := replaced[c.Addr]
		prov, err := e.Providers.Provider(ctx, ch.ProviderKey)
		if err != nil {
			return err
		}
		if err := e.destroyOld(ctx, prov, ch); err != nil {
			return err
		}
		e.mu.Lock()
		e.predestroyed[ch.Addr] = &goneObject{prior: ch.Prior, private: ch.PriorPrivate, revision: ch.removedRevision}
		e.mu.Unlock()
		return nil
	})
}

// adoptPredestroyed turns the plan of a node whose old object was destroyed ahead of the walk back
// into the replacement it was: the record is gone, so the plan sees a new resource.
func (e *Engine) adoptPredestroyed(ch *Change) {
	e.mu.Lock()
	gone := e.predestroyed[ch.Addr]
	e.mu.Unlock()
	if gone == nil || ch.Action != ActionCreate {
		return
	}
	ch.Action = ActionReplace
	ch.PlannedCreate, ch.CreatePrivate = ch.Planned, ch.PlannedPrivate
	ch.Prior, ch.PriorPrivate = gone.prior, gone.private
	ch.removedRevision = gone.revision
	ch.predestroyed = true
	ch.Reasons = append(ch.Reasons, "replacement: the old object was destroyed before the nodes it depends on")
}

// propagateCBD marks the replacements that a create_before_destroy replacement reads, directly or
// through other nodes: the old object of a node cannot go while a dependent that is replaced the
// same way still uses it, and that one is destroyed last, so the node is replaced the same way.
func (e *Engine) propagateCBD(changes []*Change) {
	byAddr := make(map[string]*Change, len(changes))
	deps := make(map[string][]string, len(changes))
	for _, ch := range changes {
		byAddr[ch.Addr] = ch
		deps[ch.Addr] = ch.Deps
	}
	for _, ch := range changes {
		if ch.Action != ActionReplace || !ch.CreateBeforeDestroy {
			continue
		}
		for _, d := range reaches(deps, ch.Addr, func(a string) bool { return byAddr[a] != nil }) {
			if dc := byAddr[d]; dc.Action == ActionReplace && !dc.CreateBeforeDestroy && dc.Mode == "managed" {
				dc.CreateBeforeDestroy = true
				dc.Reasons = append(dc.Reasons, "create_before_destroy: "+ch.Addr+" depends on it and is replaced that way")
			}
		}
	}
}

// inheritCBD gives a node the create_before_destroy that the plan gave it through a dependent.
func (e *Engine) inheritCBD(ch *Change) {
	if ch.CreateBeforeDestroy || ch.Action != ActionReplace {
		return
	}
	e.mu.Lock()
	plan := e.applyPlan
	e.mu.Unlock()
	if plan == nil {
		return
	}
	for _, p := range plan.Changes {
		if p.Addr == ch.Addr && p.Action == ActionReplace && p.CreateBeforeDestroy {
			ch.CreateBeforeDestroy = true
			return
		}
	}
}
