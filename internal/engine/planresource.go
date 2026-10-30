package engine

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"slices"
	"strings"
	"time"

	"cuelang.org/go/cue"
	"github.com/hashicorp/terraform-plugin-go/tftypes"

	"github.com/pravo-systems/pravo/internal/config"
	"github.com/pravo-systems/pravo/internal/convert"
	"github.com/pravo-systems/pravo/internal/plugin"
	"github.com/pravo-systems/pravo/internal/schema"
	"github.com/pravo-systems/pravo/internal/state"
)

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
	s := ps.Resource(sr.Type)
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

// resourcePlan is the state of one planResource call: what the steps share
// once the configuration has been converted and the record looked up.
type resourcePlan struct {
	r      *config.Resource
	s      *schema.Schema
	conv   *convert.Converter
	ch     *Change
	sr     *state.Resource // the record in the state; nil when there is none
	cfg    tftypes.Value
	age    string // the refresh age of $lifecycle, empty for the default
	ignore []string
	strict bool
	forced bool // replacement was requested with -replace
	// unchanged is true when nothing the provider saw has changed since the
	// record was written.
	unchanged bool
}

// planResource runs the plan steps for one resource with the current task
// value. strict makes unknown values an error (apply time).
func (e *Engine) planResource(ctx context.Context, r *config.Resource, v cue.Value, strict bool) (*Change, error) {
	p, err := e.startPlan(r, v, strict)
	if err != nil {
		return nil, err
	}
	e.explainChange(p)
	if r.Mode == "data" {
		return e.planData(ctx, p)
	}
	return e.planManaged(ctx, p)
}

// startPlan converts the configuration of a resource, looks up its record and
// decides whether anything the provider saw has changed.
func (e *Engine) startPlan(r *config.Resource, v cue.Value, strict bool) (*resourcePlan, error) {
	s, err := e.resourceSchema(r)
	if err != nil {
		return nil, err
	}
	addr := r.Address()
	if e.onConvert != nil {
		e.onConvert(addr, strict)
	}
	e.lockCUE(e.traceCtx())
	conv := &convert.Converter{Strict: strict, Self: r.Path, Resolve: e.resolver(), IsEphemeral: e.ephemeralCheck()}
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
	sr := e.State.Get(addr)
	forced := sr != nil && r.Mode == "managed" && e.replaceRequested(addr)
	return &resourcePlan{
		r: r, s: s, conv: conv, ch: ch, sr: sr, cfg: cfg, age: age, ignore: ignore, strict: strict, forced: forced,
		// A node that a read left a mark on is planned until an apply writes it.
		unchanged: sr != nil && !forced && !sr.Marked() && !e.Opts.Full && ch.ConfigHash != "" && ch.ConfigHash == sr.ConfigHash &&
			sr.SchemaVersion == s.Version && sr.ProviderVersion == e.providerVersion(r.ProviderKey) && sr.ProviderKey == r.ProviderKey,
	}, nil
}

// explainChange adds to the change the reasons the resource is planned, from
// what differs between the record and the specification.
func (e *Engine) explainChange(p *resourcePlan) {
	ch, sr, s, r := p.ch, p.sr, p.s, p.r
	switch {
	case sr == nil:
		ch.Reasons = append(ch.Reasons, "not in the state: a new resource")
	case ch.ConfigHash == "":
		ch.Reasons = append(ch.Reasons, "inputs depend on values known only after apply: "+strings.Join(p.conv.DepAddrs(), ", "))
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
	if p.forced {
		ch.Reasons = append(ch.Reasons, "replacement requested with -replace")
	}
}

// planData plans a data source: it answers from a read already made, from the
// record, or by reading it, and defers the read when its inputs are not known.
func (e *Engine) planData(ctx context.Context, p *resourcePlan) (*Change, error) {
	ch, sr, r, addr := p.ch, p.sr, p.r, p.ch.Addr
	ch.Action = ActionRead
	// A data source that the plan read is not read again: the walk that follows
	// it, and the apply, use the answer.
	if st, ok := e.cachedData(addr, ch.ConfigHash); ok && (p.strict || !e.dependsOnPendingChange(addr)) {
		ch.Planned, ch.Refreshed = st, true
		ch.Kept = p.unchanged && e.recordHolds(sr, ch, st)
		e.count(func(st *Stats) { st.Refreshed++ })
		return ch, nil
	}
	if p.unchanged && !e.dependsOnPendingChange(addr) {
		skipped, err := e.skipDataRead(p)
		if err != nil {
			return nil, err
		}
		if skipped {
			return ch, nil
		}
	}
	prov, err := e.Providers.Provider(ctx, r.ProviderKey)
	if err != nil {
		return nil, err
	}
	diags, err := prov.ValidateDataSourceConfig(ctx, r.Type, p.cfg)
	if err != nil {
		return nil, fmt.Errorf("%s: validating: %w", addr, err)
	}
	if err := diags.Err(); err != nil {
		return nil, fmt.Errorf("%s: %w", addr, err)
	}
	if p.conv.Unknown || (!p.strict && e.dependsOnPendingChange(addr)) {
		ch.Deferred = true
		ch.Planned = tftypes.NewValue(p.s.Block.Type(), tftypes.UnknownValue)
		return ch, nil
	}
	e.step("read", addr, "start", "", time.Time{}, "%s: Reading...", addr)
	st, diags, err := prov.ReadDataSource(ctx, r.Type, p.cfg)
	if err != nil {
		return nil, fmt.Errorf("%s: reading: %w", addr, err)
	}
	if err := diags.Err(); err != nil {
		return nil, fmt.Errorf("%s: %w", addr, err)
	}
	ch.Warnings = diags.Warnings()
	ch.Planned, ch.Refreshed = st, true
	if ch.Kept = p.unchanged && e.recordHolds(sr, ch, st); ch.Kept && !p.strict {
		e.renewData(addr, sr, p.age)
	}
	e.rememberData(addr, ch.ConfigHash, st)
	e.count(func(st *Stats) { st.Refreshed++ })
	return ch, nil
}

// skipDataRead answers a data source that did not change from its record,
// without reading it, when the record is within its refresh age and no read
// is forced. It reports whether it did.
func (e *Engine) skipDataRead(p *resourcePlan) (bool, error) {
	ch := p.ch
	due, err := e.refreshDue(p.sr, p.age)
	if err != nil {
		return false, fmt.Errorf("%s: %w", ch.Addr, err)
	}
	if due {
		return false, nil
	}
	if ch.forced = e.readReason(p.sr); ch.forced != "" {
		ch.Reasons = append(ch.Reasons, ch.forced)
		return false, nil
	}
	stored, err := storedValue(p.sr, p.s)
	if err != nil {
		return false, nil
	}
	ch.Planned, ch.Skipped = stored, true
	e.count(func(st *Stats) { st.Skipped++ })
	if e.Opts.Refresh != RefreshNone {
		ch.Fresh = true
		e.count(func(st *Stats) { st.Fresh++ })
	}
	return true, nil
}

// planManaged plans a managed resource: it reads the prior object, asks the
// provider for the planned state and decides the action.
func (e *Engine) planManaged(ctx context.Context, p *resourcePlan) (*Change, error) {
	ch, sr, r, s, addr := p.ch, p.sr, p.r, p.s, p.ch.Addr
	// A plan in the default mode looks at the nodes it reaches and at the nodes
	// they read. The rest is not read, whatever its refresh age. A plan that
	// reads nothing does not start a provider for a node that did not change.
	if p.unchanged && (e.Opts.Refresh == RefreshAge || (e.Opts.Refresh == RefreshNone && !p.strict)) && e.cachedRead(addr) == nil {
		quiet, err := e.quiet(ch, sr, s, p.age)
		if err != nil || quiet {
			return ch, err
		}
	}

	prov, err := e.Providers.Provider(ctx, r.ProviderKey)
	if err != nil {
		return nil, err
	}
	prior, private, err := e.priorObject(ctx, prov, p)
	if err != nil {
		return nil, err
	}
	ch.Prior, ch.PriorPrivate = prior, private
	e.explainRefresh(p)
	if p.unchanged && !prior.IsNull() && (!ch.Refreshed || !ch.Drifted) {
		ch.Reasons = append(ch.Reasons, "skipped: nothing the provider saw has changed")
		// Nothing the provider saw has changed: reuse the recorded state.
		ch.Action, ch.Planned, ch.Skipped = ActionNoop, prior, true
		e.count(func(st *Stats) { st.Skipped++ })
		return ch, nil
	}
	e.count(func(st *Stats) { st.Reached++ })

	diags, err := prov.ValidateResourceConfig(ctx, r.Type, p.cfg)
	if err != nil {
		return nil, fmt.Errorf("%s: validating: %w", addr, err)
	}
	if err := diags.Err(); err != nil {
		return nil, fmt.Errorf("%s: %w", addr, err)
	}
	ch.Warnings = append(ch.Warnings, diags.Warnings()...)

	cfg := applyIgnoreChanges(s.Block, prior, p.cfg, p.ignore)
	ch.Config = cfg

	res, diags, err := prov.PlanResourceChange(ctx, r.Type, prior, ProposedNew(s.Block, prior, cfg), cfg, private)
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
	case p.forced || (len(res.RequiresReplace) > 0 && !prior.Equal(res.PlannedState)):
		if err := e.planReplacement(ctx, prov, p, cfg, res.RequiresReplace); err != nil {
			return nil, err
		}
	case prior.Equal(res.PlannedState):
		ch.Action = ActionNoop
	default:
		ch.Action = ActionUpdate
	}
	return ch, nil
}

// priorObject finds the object the plan starts from: the recorded one, read
// again when it is due, or the one an import declaration brings in. It is a
// null object when the resource is new.
func (e *Engine) priorObject(ctx context.Context, prov plugin.Provider, p *resourcePlan) (prior tftypes.Value, private []byte, err error) {
	addr, ch := p.ch.Addr, p.ch
	prior = tftypes.NewValue(p.s.Block.Type(), nil)
	if p.sr != nil {
		prior, private, err = e.priorState(ctx, prov, p.sr, p.s, ch, p.age)
		if err != nil {
			return prior, nil, err
		}
		if mark, ok := e.importedID(addr); ok && !p.strict {
			// The record was written by the import of an earlier walk of this plan.
			ch.Imported, ch.ImportID, ch.ImportIdentity = true, mark.label, mark.identity
		}
	} else if imp, ok := e.Root.Imports[addr]; ok && !p.strict {
		prior, private, err = e.importResource(ctx, prov, p.r, p.s, imp, ch)
	}
	return prior, private, err
}

// explainRefresh adds the reasons that follow from what the read of the prior
// object found.
func (e *Engine) explainRefresh(p *resourcePlan) {
	ch, sr := p.ch, p.sr
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
	if sr != nil && sr.Marked() && (!ch.Refreshed || !ch.Drifted) {
		// An earlier read left a mark that this run did not find again.
		if sr.Gone {
			ch.Reasons = append(ch.Reasons, "an earlier read did not find the object; it is planned until an apply writes the record")
		} else {
			ch.Reasons = append(ch.Reasons, "an earlier read found the real state differs from the state (drift); it is planned until an apply writes the record")
		}
	}
}

// planReplacement turns the change into a replacement and plans the object that
// takes the place of the prior one.
func (e *Engine) planReplacement(ctx context.Context, prov plugin.Provider, p *resourcePlan, cfg tftypes.Value, requires []*tftypes.AttributePath) error {
	ch, s := p.ch, p.s
	ch.Action = ActionReplace
	ch.Forced = p.forced
	for _, path := range requires {
		if steps := path.Steps(); len(steps) > 0 {
			if n, ok := steps[0].(tftypes.AttributeName); ok {
				ch.Reasons = append(ch.Reasons, "the provider cannot change "+string(n)+" in place; replacement required")
			}
		}
	}
	nullPrior := tftypes.NewValue(s.Block.Type(), nil)
	res, diags, err := prov.PlanResourceChange(ctx, p.r.Type, nullPrior, ProposedNew(s.Block, nullPrior, cfg), cfg, nil)
	if err != nil {
		return fmt.Errorf("%s: planning replacement: %w", ch.Addr, err)
	}
	if err := diags.Err(); err != nil {
		return fmt.Errorf("%s: planning replacement: %w", ch.Addr, err)
	}
	ch.PlannedCreate, ch.CreatePrivate = res.PlannedState, res.PlannedPrivate
	if ch.PreventDestroy {
		return fmt.Errorf("%s: replacement requires destroying the resource, but $lifecycle.prevent_destroy is set", ch.Addr)
	}
	return nil
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
