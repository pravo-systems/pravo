package pravo

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/pravo-systems/pravo/internal/engine"
)

// OpportunisticGC is the most stored plans that storing a plan removes on its way.
const OpportunisticGC = 25

// processToken tells this process from every other one that has the same actor,
// host and process id, such as two containers that run pravo as process 1.
var processToken = func() string {
	b := make([]byte, 4)
	if _, err := rand.Read(b); err != nil {
		return strconv.FormatInt(time.Now().UnixNano(), 36)
	}
	return hex.EncodeToString(b)
}()

// ClaimOwner names this process in the claims that it makes on stored plans: who
// it acts as, where, and which process it is. actor, when not empty, replaces the
// owner of the state, except in a sealed state, where the owner is the identity
// that signs and a label cannot stand in for it.
func ClaimOwner(st *Store, actor string) string {
	who := st.Owner()
	if actor != "" && st.SigningKey() == "" {
		who = actor
	}
	host, _ := os.Hostname()
	return who + "@" + host + "#" + strconv.Itoa(os.Getpid()) + "." + processToken
}

// PlanStore returns the stored plans of the state of the workspace.
func (w *Workspace) PlanStore() (*PlanStore, error) {
	if w.state == nil {
		return nil, errors.New("pravo: stored plans live in the state: open the workspace with Config.State set")
	}
	return engine.NewPlanStoreFor(w.state), nil
}

// ClaimOwner is ClaimOwner for the state and the actor of the workspace.
func (w *Workspace) ClaimOwner() string { return ClaimOwner(w.state, w.cfg.Actor) }

// StorePlanOptions say how a plan is stored.
type StorePlanOptions struct {
	// TTL is how long the plan is kept unless it is applied. Zero is the default of the store.
	TTL time.Duration
}

// StoredPlan is what storing a plan did.
type StoredPlan struct {
	Meta *PlanMeta
	// Overlaps are the other stored plans that change the same resources.
	Overlaps []Overlap
	// Removed are the stored plans that were no longer needed and went with it.
	Removed []GCItem
}

// Store saves the plan in the state, looks for the stored plans that change the
// same resources, and removes a few that are no longer needed.
func (p *Planned) Store(ctx context.Context, o StorePlanOptions) (*StoredPlan, error) {
	return p.ws.StorePlan(ctx, p.eng, p.plan, o)
}

// Overlaps looks for the stored plans that change the resources that the plan
// changes, for a plan that is not stored. It looks quietly: a state that cannot
// be listed has none.
func (p *Planned) Overlaps(ctx context.Context) []Overlap {
	return p.ws.PlanOverlaps(ctx, p.eng, p.plan)
}

// StorePlan is Planned.Store for a plan that eng computed.
func (w *Workspace) StorePlan(ctx context.Context, eng *Engine, plan *Plan, o StorePlanOptions) (*StoredPlan, error) {
	meta, err := eng.StorePlan(ctx, plan, w.cfg.Tags, w.cfg.SysTags, o.TTL)
	if err != nil {
		return nil, err
	}
	sp := &StoredPlan{Meta: meta}
	ps := engine.NewPlanStoreFor(w.state)
	check := engine.PlanCheckFor(w.state)
	if entries, err := ps.Entries(ctx, check); err == nil {
		sp.Overlaps = engine.OverlapsOf(entries, meta)
	}
	if res, err := ps.GC(ctx, engine.GCOptions{Owner: w.ClaimOwner(), Check: check, Limit: OpportunisticGC, Skip: meta.ID}); err == nil {
		sp.Removed = res.Removed
	}
	return sp, nil
}

// PlanOverlaps is Planned.Overlaps for a plan that eng computed.
func (w *Workspace) PlanOverlaps(ctx context.Context, eng *Engine, plan *Plan) []Overlap {
	entries, err := engine.NewPlanStoreFor(w.state).Entries(ctx, engine.PlanCheckFor(w.state))
	if err != nil || len(entries) == 0 {
		return nil
	}
	meta, err := eng.SummarizePlan(plan, w.cfg.Tags, w.cfg.SysTags)
	if err != nil {
		return nil
	}
	return engine.OverlapsOf(entries, meta)
}

// CheckStoredTags checks that the tags of the workspace are the ones that the
// stored plan was made with, which select the state it is in and the
// specification it applies to. verb is the command the error tells to run.
func (w *Workspace) CheckStoredTags(m *PlanMeta, verb string) error {
	return CheckStoredTags(w.cfg.Tags, w.cfg.SysTags, m, verb)
}

// CheckStoredTags is Workspace.CheckStoredTags for the given tags.
func CheckStoredTags(tags []string, sysTags bool, m *PlanMeta, verb string) error {
	have := append([]string(nil), tags...)
	want := append([]string(nil), m.Tags...)
	sort.Strings(have)
	sort.Strings(want)
	if strings.Join(have, "\x00") == strings.Join(want, "\x00") && sysTags == m.SysTags {
		return nil
	}
	cmdline := strings.Replace(m.ApplyCommand(), "apply", verb, 1)
	if len(m.Tags) == 0 && !m.SysTags {
		return fmt.Errorf("%s was made without -t and -T: run %s without them", m.ID, cmdline)
	}
	return fmt.Errorf("%s was made with other -t and -T than these: run %s", m.ID, cmdline)
}

// StoredPlanRead is a stored plan as ReadStoredPlan found it.
type StoredPlanRead struct {
	Store *PlanStore
	File  *PlanFile
	Entry *PlanEntry
}

// ReadStoredPlan resolves a reference to a stored plan (an id, or a unique
// prefix of one), checks that the workspace has the tags the plan was made with
// (verb names the command, for the error), and reads the plan with what was
// applied from it.
func (w *Workspace) ReadStoredPlan(ctx context.Context, ref, verb string) (*StoredPlanRead, error) {
	ps, err := w.PlanStore()
	if err != nil {
		return nil, err
	}
	id, err := ps.Resolve(ctx, ref)
	if err != nil {
		return nil, err
	}
	entry, err := ps.Entry(ctx, id, engine.PlanCheckFor(w.state))
	if err != nil {
		return nil, err
	}
	if err := w.CheckStoredTags(entry.Meta, verb); err != nil {
		return nil, err
	}
	pf, _, err := ps.ReadFile(ctx, id)
	if err != nil {
		return nil, err
	}
	return &StoredPlanRead{Store: ps, File: pf, Entry: entry}, nil
}

// ApplyStoredOptions say how a stored plan is applied.
type ApplyStoredOptions struct {
	// Only limits the apply to these addresses (and what they need).
	Only []string
	// Event, when set, receives the steps of the apply.
	Event func(Event)
	// Selected is called before the apply starts when Only selects part of what
	// is left, with the id of the plan, the number of changes selected and of changes left.
	Selected func(id string, selected, left int)
	// Tune, when set, may change the options of the engine before it is made: the
	// readers file and the roles are the caller's to give.
	Tune func(*EngineOptions)
}

// StoredApply is what applying a stored plan did.
type StoredApply struct {
	// ID is the stored plan.
	ID   string
	Meta *PlanMeta
	// NothingLeft is true when the plan had nothing left to apply: no apply ran.
	NothingLeft bool
	// Result is what the apply did. After a failed apply it holds what was done before it.
	Result *ApplyResult
	// Destroy says whether the plan is a destroy plan.
	Destroy bool
	// Left is the number of changes of the plan that remain after the apply.
	Left int
	// Again is the command that applies the plan again.
	Again string
	// Partial is set when the apply failed or was interrupted after it applied some
	// of the changes. ApplyStored returns the error of the apply with it.
	Partial *PartialApply
}

// PartialApply describes the changes an apply finished before it failed.
type PartialApply struct {
	// Count is the number of changes of the plan that were applied.
	Count int
	// RecordErr is why they could not be recorded in the stored plan, if so. The
	// changes are applied all the same.
	RecordErr error
	// Next says what to do with the rest of the plan, looking at the plan as it
	// stands once the applied changes are recorded.
	Next string
}

// ApplyStored applies a plan that is stored in the state. The checks are those
// of a plan file; besides them the plan must not have expired. While the apply
// runs the plan is claimed, so that no collection removes it, and what the apply
// did is recorded in the plan when it returns.
func (w *Workspace) ApplyStored(ctx context.Context, ref string, o ApplyStoredOptions) (*StoredApply, error) {
	ps, err := w.PlanStore()
	if err != nil {
		return nil, err
	}
	id, err := ps.Resolve(ctx, ref)
	if err != nil {
		return nil, err
	}
	meta, err := ps.Meta(ctx, id)
	if err != nil {
		return nil, err
	}
	if err := w.CheckStoredTags(meta, "apply"); err != nil {
		return nil, err
	}
	progress, err := ps.Progress(ctx, id)
	if err != nil {
		return nil, err
	}
	if progress.DoneAt == nil && time.Now().After(meta.Expires) {
		return nil, engine.WithCode(engine.ExitStale, fmt.Errorf("%s expired %s ago; plans are kept for a limited time: run \"pravo plan --store\" again", id, engine.DurationText(time.Since(meta.Expires))))
	}
	// From here no collection removes the plan, until the apply is over. An apply
	// that fails or is interrupted records the changes it finished, as one from a
	// plan file does; the change that failed is not among them.
	owner := w.ClaimOwner()
	if err := ps.BeginApply(ctx, id, owner); err != nil {
		if errors.Is(err, engine.ErrPlanRemoving) {
			return nil, engine.WithCode(engine.ExitStale, err)
		}
		return nil, err
	}
	background := context.WithoutCancel(ctx)
	defer ps.EndApply(background, id, owner)
	// An apply may run for longer than a claim lasts: the claim is renewed until it ends.
	defer ps.KeepApply(background, id, owner)()
	pf, _, err := ps.ReadFile(ctx, id)
	if err != nil {
		return nil, err
	}
	// The note that the plan was made with is the reason of the apply, unless the
	// apply was given its own: the history of the resources says why.
	reason := w.cfg.Reason
	if reason == "" {
		reason = meta.Reason
	}
	// A plan that policies judged is the receipt of the apply, and no collection removes it.
	eopts := EngineOptions{Refresh: RefreshNone, Reason: reason, Event: o.Event, Judged: func() { _ = ps.Keep(background, id) }}
	if o.Tune != nil {
		o.Tune(&eopts)
	}
	eng := w.Engine(eopts)
	eng.SetPlan(id, pf.Digest)

	sa, err := ApplySavedPlan(ctx, eng, pf, SavedApplyOptions{
		Only: o.Only, Name: id, Again: meta.ApplyCommand(),
		Selected: func(selected, left int) {
			if o.Selected != nil {
				o.Selected(id, selected, left)
			}
		},
		Sink: storeSink{w: w, ps: ps, id: id},
	})
	res := &StoredApply{ID: id, Meta: meta, Again: meta.ApplyCommand()}
	if sa == nil {
		return nil, err
	}
	res.NothingLeft, res.Result, res.Destroy, res.Left, res.Partial = sa.NothingLeft, sa.Result, sa.Destroy, sa.Left, sa.Partial
	return res, err
}

// storeSink keeps the progress of an apply in the stored plan.
type storeSink struct {
	w  *Workspace
	ps *PlanStore
	id string
}

func (k storeSink) Applied(ctx context.Context, applied []string, txn string, pending []string) error {
	return k.ps.MarkApplied(ctx, k.id, applied, txn, pending)
}

func (k storeSink) PartlyApplied(ctx context.Context, applied []string, txn string) error {
	return k.ps.MarkPartlyApplied(ctx, k.id, applied, txn)
}

// Next looks at the stored plan as it stands: the records that the apply changed may have
// made the rest of the plan stale, or only a part of it.
func (k storeSink) Next(ctx context.Context, again string) string {
	e, err := k.ps.Entry(ctx, k.id, engine.PlanCheckFor(k.w.state))
	switch {
	case err != nil:
		return fmt.Sprintf("run \"%s\" again to apply the rest", again)
	case e.Status == engine.PlanPartlyStale:
		return fmt.Sprintf("some of the rest is stale; apply the part that is still valid with \"%s\"", e.ApplyLiveCommand())
	case e.Status == engine.PlanStale:
		return "the rest is stale now and must be planned again with \"pravo plan --store\""
	}
	return fmt.Sprintf("run \"%s\" again to apply the rest", again)
}
