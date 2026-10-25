package serve

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/pravo-systems/pravo/internal/engine"
	"github.com/pravo-systems/pravo/internal/state"
)

const (
	// maxTouches bounds the changes of a stored plan that the listing carries.
	maxTouches = 2000
	// maxReasons bounds the reasons of a stale plan that are sent, and the changes
	// of a partly stale plan that are named as conflicts; maxReason is the length
	// of each reason, maxOverlapAddrs the addresses named for one overlap, and
	// maxCommand the length of the command that applies the live part of a partly
	// stale plan.
	maxReasons      = 20
	maxReason       = 300
	maxOverlapAddrs = 20
	maxCommand      = 16 << 10
	// maxFreeText is how much of a text that a person wrote is looked at for
	// secrets, before it is clipped to what is sent: more than the longest secret
	// there is, so that one cut by this limit lies beyond the clip.
	maxFreeText = 1 << 17
)

var quotedText = regexp.MustCompile(`"[^"]*"`)

// storedPlans is the plans stored in the state, as they stand at one revision of
// the model: the listing, and the overlays of the ones that a request asked for.
// The viewer only reads them: the store is the read-only one of the model.
type storedPlans struct {
	srv *Server
	ps  *engine.PlanStore
	// err is why the listing failed; there are then no entries.
	err error
	// entries are newest first, as the store lists them.
	entries []*engine.PlanEntry
	byID    map[string]*engine.PlanEntry
	// skipped are the plans that are not listed because they cannot be read.
	skipped []engine.SkippedPlan
	// touched maps the id of each plan to the addresses that it still has a change
	// to apply on, once each, in the order of its summary.
	touched map[string][]string
	// lapse is when the first claim on a plan lapses: a plan that a collector hid
	// shows again, an apply that left its claim stops counting as running.
	lapse time.Time

	listOnce sync.Once
	list     *PlanList

	mu       sync.Mutex
	overlays map[string]*planOverlay
}

// loadStored lists the plans stored in the state of the model and works out
// where each stands, against the state alone: the configuration and the providers
// belong to one working copy, and "pravo apply" checks them.
func (s *Server) loadStored(ctx context.Context, m *model) *storedPlans {
	sp := &storedPlans{srv: s, byID: map[string]*engine.PlanEntry{}, overlays: map[string]*planOverlay{}, touched: map[string][]string{}}
	sp.ps = engine.NewPlanStoreFor(m.store)
	// The summaries are written once: a rebuild of the model reads them again only
	// for a plan it has not seen, and forgets the ones that were collected.
	sp.ps.Cache = s.planMeta
	listing, err := sp.ps.List(ctx, engine.PlanCheckFor(m.store))
	if err != nil {
		sp.err = err
		return sp
	}
	sp.skipped = listing.Skipped
	sp.lapse = listing.NextLapse()
	for _, e := range listing.Entries {
		// An id that the summary says is not taken on trust: it ends in a command
		// that a person copies and in the name of an object that is read.
		if id := e.Meta.ID; !engine.IsPlanID(id) || sp.byID[id] != nil {
			sp.skipped = append(sp.skipped, engine.SkippedPlan{ID: id, Err: errors.New("its summary does not name it")})
			continue
		}
		sp.entries = append(sp.entries, e)
		sp.byID[e.Meta.ID] = e
		sp.touched[e.Meta.ID] = unappliedAddrs(e)
	}
	return sp
}

// unappliedAddrs lists the resources that a plan still has a change to apply on,
// once each: what the plan has left to do, as the commands that list plans say.
func unappliedAddrs(e *engine.PlanEntry) []string {
	var out []string
	seen := map[string]bool{}
	for _, t := range e.Meta.Changes {
		if _, done := e.Progress.Applied[t.Addr]; done || seen[t.Addr] {
			continue
		}
		seen[t.Addr] = true
		out = append(out, t.Addr)
	}
	return out
}

// skippedWarning says which plans are not listed because they cannot be read, so
// that a person who looks for the plan of a colleague can tell why it is not there.
func (sp *storedPlans) skippedWarning() string {
	if len(sp.skipped) == 0 {
		return ""
	}
	var parts []string
	for i, k := range sp.skipped {
		if i == 5 {
			parts = append(parts, fmt.Sprintf("and %d more", len(sp.skipped)-5))
			break
		}
		id := "a plan whose id is not valid"
		if engine.IsPlanID(k.ID) {
			id = k.ID
		}
		// The message may quote what the object says, which a hostile writer chose:
		// what is quoted is left out.
		why := quotedText.ReplaceAllString(k.Err.Error(), `"…"`)
		parts = append(parts, fmt.Sprintf("%s (%s)", id, clip(why, 100)))
	}
	return "Stored plans that cannot be read are not listed: " + strings.Join(parts, ", ") + "."
}

// resolve finds the stored plan that an id, or the beginning of one, names.
func (sp *storedPlans) resolve(ref string) (*engine.PlanEntry, error) {
	if !engine.IsPlanID(ref) {
		return nil, fmt.Errorf("%q is not the id of a stored plan: ids look like plan-2f9c1a4e", ref)
	}
	if sp.err != nil {
		return nil, sp.err
	}
	if e := sp.byID[ref]; e != nil {
		return e, nil
	}
	var match []string
	for id := range sp.byID {
		if strings.HasPrefix(id, ref) {
			match = append(match, id)
		}
	}
	sort.Strings(match)
	switch len(match) {
	case 0:
		return nil, fmt.Errorf("%w: %s (see: pravo plan list --all)", engine.ErrNoPlan, ref)
	case 1:
		return sp.byID[match[0]], nil
	}
	return nil, fmt.Errorf("%s fits %d stored plans: %s", ref, len(match), strings.Join(match, ", "))
}

// readFile reads the plan, with what has been applied from it marked.
func (sp *storedPlans) readFile(ctx context.Context, id string) (*engine.PlanFile, error) {
	pf, _, err := sp.ps.ReadFile(ctx, id)
	return pf, err
}

// planFile reads the plan of an entry. A process that is not a reader of the plan
// gets its summary instead, with sealed set: the addresses, the types and the
// actions, which the state leaves readable, and no value.
func (sp *storedPlans) planFile(ctx context.Context, e *engine.PlanEntry) (pf *engine.PlanFile, sealed bool, err error) {
	pf, err = sp.readFile(ctx, e.Meta.ID)
	if errors.Is(err, state.ErrNotReader) {
		return e.SummaryFile(), true, nil
	}
	return pf, false, err
}

// nextExpiry is when the first claim on a plan lapses, or when the first of the plans that can still be applied (a partly
// stale one too), or that went stale, expires, or the zero time. A plan that
// passes its expiry is expired without anything being written, so the watcher
// counts that moment as a change.
func (sp *storedPlans) nextExpiry() time.Time {
	var next time.Time
	for _, e := range sp.entries {
		if (e.Status.Open() || e.Status == engine.PlanStale) && (next.IsZero() || e.Meta.Expires.Before(next)) {
			next = e.Meta.Expires
		}
	}
	if !sp.lapse.IsZero() && (next.IsZero() || sp.lapse.Before(next)) {
		next = sp.lapse
	}
	return next
}

// markNodes sets the plans that can still be applied on the nodes they have a
// change left on, and the overlap of the nodes that two or more can apply it on.
// A plan that applied its change on a node has nothing left to do there. A partly
// stale plan keeps its mark on the node of a change that conflicts, which it
// cannot apply: it does not count for the overlap.
func (m *model) markNodes() {
	live := map[string]int{}
	for _, e := range m.stored.entries {
		if !e.Status.Open() {
			continue
		}
		for _, addr := range m.stored.touched[e.Meta.ID] {
			if mn := m.nodes[addr]; mn != nil {
				mn.Plans = append(mn.Plans, e.Meta.ID)
				if _, blocked := e.Conflicts[addr]; !blocked {
					live[addr]++
				}
				mn.Overlap = live[addr] > 1
			}
		}
	}
}

// maskText bounds and masks a text that a person wrote: what is looked at for
// secrets is bounded, and so is what is sent.
func maskText(sec *secretSet, text string, n int) string {
	if len(text) > maxFreeText {
		text = strings.ToValidUTF8(text[:maxFreeText], "")
	}
	out, _ := sec.scrub(text)
	return clip(out, n)
}

// maskCommand is maskText for a recorded command line.
func maskCommand(sec *secretSet, cmd string, n int) string {
	if len(cmd) > maxFreeText {
		cmd = strings.ToValidUTF8(cmd[:maxFreeText], "")
	}
	return clip(scrubCommand(cmd, sec), n)
}

// planList is the answer of /api/plans. It is worked out once for the model.
func (m *model) planList() *PlanList {
	sp := m.stored
	sp.listOnce.Do(func() {
		list := &PlanList{Version: APIVersion, Revision: m.rev, Plans: make([]StoredPlan, 0, len(sp.entries))}
		overlaps := m.overlaps()
		for _, e := range sp.entries {
			p := m.storedPlan(e)
			p.Overlaps = overlaps[e.Meta.ID]
			list.Plans = append(list.Plans, p)
		}
		sp.list = list
	})
	return sp.list
}

// changing lists the resources that a plan can still change: what it has left to
// apply, less the changes that a partly stale plan cannot apply.
func (sp *storedPlans) changing(e *engine.PlanEntry) []string {
	var out []string
	for _, addr := range sp.touched[e.Meta.ID] {
		if _, blocked := e.Conflicts[addr]; !blocked {
			out = append(out, addr)
		}
	}
	return out
}

// overlaps works out, for every open plan, the other open plans that can apply a
// change on a resource that it can apply one on. The relation is symmetric. Each
// resource is looked at once for each plan that has a change on it.
func (m *model) overlaps() map[string][]PlanOverlap {
	sp := m.stored
	byAddr := map[string][]string{}
	for _, e := range sp.entries {
		if e.Status.Open() {
			for _, addr := range sp.changing(e) {
				byAddr[addr] = append(byAddr[addr], e.Meta.ID)
			}
		}
	}
	out := map[string][]PlanOverlap{}
	for _, e := range sp.entries {
		if !e.Status.Open() {
			continue
		}
		mine := sp.changing(e)
		sort.Strings(mine)
		with := map[string]*PlanOverlap{}
		for _, addr := range mine {
			for _, other := range byAddr[addr] {
				if other == e.Meta.ID {
					continue
				}
				o := with[other]
				if o == nil {
					o = &PlanOverlap{ID: other}
					with[other] = o
				}
				o.Count++
				if len(o.Addrs) < maxOverlapAddrs {
					o.Addrs = append(o.Addrs, addr)
				} else {
					o.Truncated++
				}
			}
		}
		for _, other := range sp.entries { // newest first
			if o := with[other.Meta.ID]; o != nil {
				out[e.Meta.ID] = append(out[e.Meta.ID], *o)
			}
		}
	}
	return out
}

// storedPlan describes one stored plan, with everything bounded. The author is
// masked against the sensitive values of the state; what the author wrote with
// the plan is not in the listing (see PlanOverlay).
func (m *model) storedPlan(e *engine.PlanEntry) StoredPlan {
	meta, prog := e.Meta, e.Progress
	sp := StoredPlan{
		ID: meta.ID, Status: string(e.Status), Open: e.Status.Open(),
		Author: maskText(m.secrets, meta.Author, maxText), Created: meta.Created, Expires: meta.Expires,
		Destroy: meta.Destroy, Summary: PlanCounts{Add: meta.Summary.Add, Change: meta.Summary.Change, Destroy: meta.Summary.Destroy, Read: meta.Summary.Read},
		Planned: len(meta.Changes), Applied: e.AppliedCount(),
		Applying: prog.ApplyRunning(time.Now()),
		Touches:  make([]PlanChange, 0, min(len(meta.Changes), maxTouches)),
		Apply:    meta.ApplyTemplate(),
		Policies: m.planPolicies(meta.Policies),
	}
	if e.Status == engine.PlanPartlyStale {
		m.partlyStale(&sp, e)
	} else {
		sp.Reasons, sp.ReasonsOmitted = m.capReasons(m.secrets, e.Reasons)
	}
	for i, t := range meta.Changes {
		if i >= maxTouches {
			sp.Truncated = len(meta.Changes) - maxTouches
			break
		}
		_, done := prog.Applied[t.Addr]
		_, conflict := e.Conflicts[t.Addr]
		sp.Touches = append(sp.Touches, PlanChange{Addr: t.Addr, Action: string(t.Action), Applied: done, Conflict: conflict && !done})
	}
	return sp
}

// partlyStale says what a partly stale plan cannot do and what it can: the
// changes that conflict, with the reasons masked as those of a stale plan are,
// and the part to apply by name, with the command that does.
func (m *model) partlyStale(sp *StoredPlan, e *engine.PlanEntry) {
	sp.Unapplied = e.Unapplied()
	addrs := make([]string, 0, len(e.Conflicts))
	for addr := range e.Conflicts {
		addrs = append(addrs, addr)
	}
	sort.Strings(addrs)
	for i, addr := range addrs {
		if i == maxReasons {
			sp.ConflictsOmitted = len(addrs) - maxReasons
			break
		}
		reasons, _ := m.capReasons(m.secrets, e.Conflicts[addr])
		sp.Conflicts = append(sp.Conflicts, PlanConflict{Addr: addr, Reasons: reasons})
	}
	sp.ApplyOnly = e.Live
	if len(sp.ApplyOnly) > maxTouches {
		sp.ApplyOnly, sp.ApplyOnlyOmitted = sp.ApplyOnly[:maxTouches], len(e.Live)-maxTouches
	}
	// The tags that the plan was made with are placeholders in the command, as they
	// are in Apply: they are what a person wrote, and a listing carries none of it.
	if only := e.OnlyFlag(); only != "" && len(only) <= maxCommand {
		sp.ApplyLive = sp.Apply + " " + only
	}
}

// capReasons masks the reasons of a stale plan, clips each and keeps the first
// maxReasons; the count of the others is returned.
func (m *model) capReasons(sec *secretSet, reasons []string) ([]string, int) {
	var out []string
	for i, r := range reasons {
		if i == maxReasons {
			return out, len(reasons) - maxReasons
		}
		out = append(out, maskText(sec, r, maxReason))
	}
	return out, 0
}

// errNoSuchPlan is what a request that names a plan that is not stored is told.
var errNoSuchPlan = errors.New("no such stored plan")

// overlayOf returns the overlay of a stored plan on the model, working it out
// the first time it is asked for: the plan is read from the state and decoded
// with the cached schemas. The plan that the viewer was started with (--plan ID)
// is the overlay of the graph itself.
func (m *model) overlayOf(ctx context.Context, id string) (*planOverlay, error) {
	e := m.stored.byID[id]
	if e == nil {
		return nil, errNoSuchPlan
	}
	if m.plan != nil && m.plan.info.ID == id {
		return m.plan, nil
	}
	sp := m.stored
	sp.mu.Lock()
	defer sp.mu.Unlock()
	if po := sp.overlays[id]; po != nil {
		return po, nil
	}
	pf, sealed, err := sp.planFile(ctx, e)
	if errors.Is(err, engine.ErrNoPlan) {
		return nil, errNoSuchPlan // collected since the listing
	}
	if err != nil {
		return nil, err
	}
	// The decoding evaluates nothing, but it uses the schemas and the types of the
	// specification, which the load of a model and a request for a source hold.
	m.spec.cueMu.Lock()
	defer m.spec.cueMu.Unlock()
	po := m.srv.overlay(ctx, m, pf, id, e, m.secrets.clone(), sealed)
	po.collectSecrets()
	sp.overlays[id] = po
	return po, nil
}

// cleanInfo returns the PlanInfo of an overlay as it may be sent: the author, the
// reasons and the hints are masked against the secrets of that plan and bounded.
func (m *model) cleanInfo(po *planOverlay) PlanInfo {
	info := po.info
	sec := m.secretsFor(po)
	info.Actor = maskText(sec, info.Actor, maxText)
	if sg := info.Signatures; sg != nil {
		// The names come from the bucket for a signature that did not verify: bounded and masked.
		items := make([]PlanSignature, len(sg.Items))
		for i, it := range sg.Items {
			it.Signer, it.Claimed = maskText(sec, it.Signer, maxText), maskText(sec, it.Claimed, maxText)
			items[i] = it
		}
		info.Signatures = &PlanSignatures{Items: items, Note: sg.Note}
	}
	if len(info.Reasons) > 0 {
		var omitted int
		info.Reasons, omitted = m.capReasons(sec, info.Reasons)
		if omitted > 0 {
			info.Reasons = append(info.Reasons, fmt.Sprintf("and %d more", omitted))
		}
	}
	if len(info.Hints) > 0 {
		hints := make([]string, 0, min(len(info.Hints), maxReasons))
		for i, h := range info.Hints {
			if i == maxReasons {
				hints = append(hints, fmt.Sprintf("and %d more", len(info.Hints)-maxReasons))
				break
			}
			hints = append(hints, maskText(sec, h, maxText))
		}
		info.Hints = hints
	}
	return info
}

// planOverlayDoc builds the answer of /api/plan.
func (m *model) planOverlayDoc(po *planOverlay) *PlanOverlay {
	sec := m.secretsFor(po)
	doc := &PlanOverlay{Version: APIVersion, Revision: m.rev, Plan: m.cleanInfo(po), Nodes: make([]PlanNode, 0, len(po.order)), Warnings: []string{}}
	doc.Plan.Total = m.resources
	for _, addr := range po.order {
		if m.nodes[addr] == nil {
			continue
		}
		e := po.entries[addr]
		doc.Nodes = append(doc.Nodes, PlanNode{Addr: addr, Action: e.action, Applied: e.applied})
	}
	for _, w := range po.warnings {
		doc.Warnings = append(doc.Warnings, m.scrubTextWith(sec, w))
	}
	if len(doc.Warnings) == 0 {
		doc.Warnings = nil
	}
	doc.Reason = maskText(sec, po.reason, maxText)
	doc.Command = maskCommand(sec, po.command, maxText)
	doc.GitRevision = maskText(sec, po.git, maxText)
	return doc
}
