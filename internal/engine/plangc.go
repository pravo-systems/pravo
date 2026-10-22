package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/pravo-systems/pravo/internal/state"
)

// GCOptions are the rules of a collection of stored plans. A zero duration is the
// default.
type GCOptions struct {
	// OlderThan collects a plan that was not applied and was made longer ago than
	// this, whatever its time to live says.
	OlderThan time.Duration
	// AppliedKeep is how long a plan that was applied completely is kept.
	AppliedKeep time.Duration
	// StaleGrace is how long a stale plan is kept after a collection first saw it
	// to be stale.
	StaleGrace time.Duration
	// DryRun says what would be removed and removes nothing. It marks nothing either.
	DryRun bool
	// Check is what the plans are compared with. Without it no plan is stale.
	Check *PlanCheck
	// Owner names this collector in its claims.
	Owner string
	// Limit is the most plans one collection removes; zero is no limit.
	Limit int
	// Skip is the id of a plan that this collection leaves alone: the one that its
	// caller has just stored.
	Skip string
}

// GCItem is a plan that a collection acted on or left alone.
type GCItem struct {
	ID     string `json:"id"`
	Author string `json:"author,omitempty"`
	Reason string `json:"reason"`
}

// GCResult says what a collection did.
type GCResult struct {
	DryRun  bool     `json:"dry_run,omitempty"`
	Looked  int      `json:"looked"`
	Removed []GCItem `json:"removed"`
	// Marked are the plans that were seen to be stale for the first time. A
	// collection that runs after the grace period removes them. In a dry run they
	// are the plans that would be marked.
	Marked []GCItem `json:"marked,omitempty"`
	// Kept are the plans that a rule would remove and that are in use.
	Kept []GCItem `json:"kept,omitempty"`
}

func (o *GCOptions) defaults() {
	if o.AppliedKeep <= 0 {
		o.AppliedKeep = DefaultAppliedKeep
	}
	if o.StaleGrace <= 0 {
		o.StaleGrace = DefaultStaleGrace
	}
	if o.Owner == "" {
		o.Owner = "collector"
	}
}

// gcVerdict is what the rules say about a plan.
type gcVerdict struct {
	collect bool
	// mark says that the plan is stale and no collection has seen it so yet.
	mark bool
	// clear says that the plan carries a mark of staleness and is not stale now.
	clear bool
	// inUse is set when a rule would remove the plan and something keeps it.
	inUse  string
	reason string
}

// gcJudge applies the rules to a plan, which is known by its summary and progress.
// The rules:
//   - A plan that was applied completely is removed after AppliedKeep.
//   - A plan that was not is removed when it has expired, or was made more than
//     OlderThan ago.
//   - A plan that is stale is marked the first time a collection sees it so, and
//     removed by a collection that runs StaleGrace later. A plan that is not stale
//     any more loses its mark. A plan that is stale only in part can still be
//     applied in part: it is neither marked nor removed for it, it is left to its
//     time to live, and it loses a mark that it carries like a plan that is not
//     stale at all.
//   - Whatever the rules say, a plan that an apply is running from, that a collector
//     has claimed, or that holds a reservation that has not ended is not removed.
func gcJudge(m *PlanMeta, p *PlanProgress, o *GCOptions, now time.Time) gcVerdict {
	v := gcRules(m, p, o, now)
	if v.collect || v.mark {
		if why := planInUse(m, p, now); why != "" {
			return gcVerdict{inUse: why, reason: v.reason}
		}
	}
	return v
}

func gcRules(m *PlanMeta, p *PlanProgress, o *GCOptions, now time.Time) gcVerdict {
	status, why := planStatus(m, p, o.Check, now)
	switch {
	case status == PlanApplied:
		if now.Sub(*p.DoneAt) > o.AppliedKeep {
			return gcVerdict{collect: true, reason: "applied " + DurationText(now.Sub(*p.DoneAt)) + " ago"}
		}
		return gcVerdict{}
	case status == PlanExpired:
		return gcVerdict{collect: true, reason: "expired " + DurationText(now.Sub(m.Expires)) + " ago"}
	case o.OlderThan > 0 && now.Sub(m.Created) > o.OlderThan:
		return gcVerdict{collect: true, reason: "made more than " + SpanText(o.OlderThan) + " ago"}
	case status == PlanStale:
		if p.StaleSince == nil {
			return gcVerdict{mark: true, reason: "stale, " + why[0]}
		}
		if now.Sub(*p.StaleSince) > o.StaleGrace {
			return gcVerdict{collect: true, reason: "stale for " + DurationText(now.Sub(*p.StaleSince)) + ", " + why[0]}
		}
	case p.StaleSince != nil:
		// The plan is not stale as a whole, whether it is pending, partial or partly stale.
		return gcVerdict{clear: true}
	}
	return gcVerdict{}
}

// planInUse says why a plan must not be removed now, or "".
func planInUse(m *PlanMeta, p *PlanProgress, now time.Time) string {
	switch {
	case p.ApplyRunning(now):
		return "an apply from it is running (" + p.runningApplies(now)[0].By + ")"
	case p.Collecting != nil && now.Sub(p.Collecting.At) < claimTimeout:
		return "another collection is removing it"
	case m != nil && m.Reservation != nil && m.Reservation.Until.After(now):
		return "it holds a reservation until " + m.Reservation.Until.UTC().Format(time.RFC3339)
	}
	return ""
}

// DurationText writes a length of time in at most two units, the larger one and the
// next one down, rounded to the nearest minute: "6 days 23 hours", "1 hour 30 minutes".
func DurationText(d time.Duration) string {
	minutes := int((d + 30*time.Second) / time.Minute)
	unit := func(n int, one, many string) string {
		if n == 1 {
			return "1 " + one
		}
		return fmt.Sprintf("%d %s", n, many)
	}
	switch {
	case minutes < 1:
		return "a moment"
	case minutes < 60:
		return unit(minutes, "minute", "minutes")
	case minutes < 24*60:
		out := unit(minutes/60, "hour", "hours")
		if m := minutes % 60; m > 0 {
			out += " " + unit(m, "minute", "minutes")
		}
		return out
	}
	days, hours := minutes/(24*60), (minutes%(24*60)+30)/60
	if hours == 24 {
		days, hours = days+1, 0
	}
	out := unit(days, "day", "days")
	if hours > 0 {
		out += " " + unit(hours, "hour", "hours")
	}
	return out
}

// SpanText writes a length of time that a person chose, as they would have written
// it: in the largest unit that divides it exactly, 7 days, 36 hours, 90 minutes.
// Another length is written as DurationText writes it.
func SpanText(d time.Duration) string {
	for _, u := range []struct {
		n         time.Duration
		one, many string
	}{{24 * time.Hour, "day", "days"}, {time.Hour, "hour", "hours"}, {time.Minute, "minute", "minutes"}} {
		if d >= u.n && d%u.n == 0 {
			if d == u.n {
				return "1 " + u.one
			}
			return fmt.Sprintf("%d %s", d/u.n, u.many)
		}
	}
	return DurationText(d)
}

// incompleteGrace is how long the objects of a plan that was not stored whole are
// left alone, so that a write that is still running is not taken for one that failed.
const incompleteGrace = time.Hour

// GC removes the stored plans that the rules say are no longer needed. Two
// collections and an apply that run at the same time cannot damage one another:
// a collector claims a plan in its progress object with a conditional write that
// fails while an apply has claimed it, and an apply that starts after the claim
// refuses to use the plan. Only objects of plans are removed, and each is
// removed only if it is what its name says it is.
func (ps *PlanStore) GC(ctx context.Context, o GCOptions) (*GCResult, error) {
	o.defaults()
	o.Check = memoized(o.Check)
	all, err := ps.ids(ctx)
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(all))
	for id := range all {
		if id != o.Skip {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	reads := ps.readPlans(ctx, all, ids)
	res := &GCResult{DryRun: o.DryRun, Removed: []GCItem{}}
	for _, id := range ids {
		if o.Limit > 0 && len(res.Removed) >= o.Limit {
			break
		}
		if err := ctx.Err(); err != nil {
			return res, err
		}
		res.Looked++
		now := ps.now()
		r := reads[id]
		kinds := all[id]
		if r.metaErr != nil && !errors.Is(r.metaErr, ErrNoPlan) {
			// A summary that is there and cannot be read now, because the read failed
			// or because a newer pravo wrote it, says nothing about the plan.
			continue
		}
		if kinds["plan"] && !kinds["meta"] && ps.foreignPlan(ctx, id) {
			// A plan object that is not the plan its name says it is, and that no
			// summary goes with: no write of a plan makes one. See gcForeign.
			ps.gcForeign(ctx, id, kinds, &o, now, res)
			continue
		}
		if !kinds["plan"] || !kinds["meta"] || r.metaErr != nil {
			// The objects of a plan that is not whole: a write that did not finish, or
			// the remains of a removal.
			when, ok := ps.orphanAge(ctx, id, kinds)
			if !ok || now.Sub(when) <= incompleteGrace {
				continue
			}
			item := GCItem{ID: id, Reason: "incomplete: the plan was not stored whole"}
			if o.DryRun || ps.remove(ctx, id, &o, nil, false) {
				res.Removed = append(res.Removed, item)
			}
			continue
		}
		if r.progErr != nil {
			// A progress object that cannot be read keeps the plan from being applied
			// and listed; once no write of it can be running, it is made empty.
			if !o.DryRun {
				ps.repairProgress(ctx, id, r.meta.Created, now)
			}
			continue
		}
		m, p := r.meta, r.progress
		v := gcJudge(m, p, &o, now)
		if p.Collecting.adoptable(now) {
			v = gcVerdict{collect: true, reason: "its removal was begun by another collection and not finished"}
		}
		item := GCItem{ID: id, Author: m.Author, Reason: v.reason}
		switch {
		case v.inUse != "":
			item.Reason = v.reason + "; kept: " + v.inUse
			res.Kept = append(res.Kept, item)
		case v.clear:
			if !o.DryRun {
				_ = ps.updateProgress(ctx, id, func(cur *PlanProgress) error {
					if cur.StaleSince == nil {
						return errSkip
					}
					cur.StaleSince = nil
					return nil
				})
			}
		case v.mark:
			if !o.DryRun {
				err := ps.updateProgress(ctx, id, func(cur *PlanProgress) error {
					if cur.StaleSince != nil {
						return errSkip
					}
					t := now
					cur.StaleSince = &t
					return nil
				})
				if err != nil {
					continue
				}
			}
			res.Marked = append(res.Marked, item)
		case v.collect:
			if o.DryRun || ps.remove(ctx, id, &o, m, false) {
				res.Removed = append(res.Removed, item)
			}
		}
	}
	return res, nil
}

// foreignPlan reports whether the plan object of id is not the plan that its name
// says: its digest is not the id, or a sealed envelope names another id. A plan
// that is stored has its digest as its id, so such an object was planted, or damaged.
func (ps *PlanStore) foreignPlan(ctx context.Context, id string) bool {
	data, err := ps.B.Get(ctx, planKey(id))
	return err == nil && !planDigestMatches(data, id)
}

// gcForeign collects a plan object that is not the plan its name says it is and
// that has no summary. The backend has no time of writing to ask for, so the
// first collection that sees the object marks it, by a progress object, and a
// collection that runs incompleteGrace later removes it, as it removes the objects
// of a write that did not finish. A plan object with a summary is left alone, as
// are the objects of a plan whose summary cannot be read.
func (ps *PlanStore) gcForeign(ctx context.Context, id string, kinds map[string]bool, o *GCOptions, now time.Time, res *GCResult) {
	item := GCItem{ID: id, Reason: "the plan file is not the plan that its name says: its digest is not the id"}
	var seen time.Time
	if kinds["progress"] {
		p, err := ps.Progress(ctx, id)
		if err != nil {
			return
		}
		seen = p.Updated
	}
	if seen.IsZero() {
		if !o.DryRun {
			err := ps.updateProgress(ctx, id, func(cur *PlanProgress) error {
				if !cur.Updated.IsZero() {
					return errSkip
				}
				return nil
			})
			if err != nil {
				return
			}
		}
		res.Marked = append(res.Marked, item)
		return
	}
	if now.Sub(seen) <= incompleteGrace {
		return
	}
	if o.DryRun || ps.remove(ctx, id, o, nil, true) {
		res.Removed = append(res.Removed, item)
	}
}

// orphanAge says when the objects of a plan that is not whole were written, as far
// as it can be told: by the time in the summary, in the plan or in the progress. A
// plan object that is not what its name says it is has none: it is not touched.
func (ps *PlanStore) orphanAge(ctx context.Context, id string, kinds map[string]bool) (time.Time, bool) {
	if kinds["meta"] {
		// The time is in the clear in a sealed summary: nothing is opened.
		if data, err := ps.B.Get(ctx, metaKey2(id)); err == nil {
			if m, err := decodeMeta(id, data); err == nil {
				return m.Created, true
			}
		}
	}
	if kinds["plan"] {
		if data, err := ps.B.Get(ctx, planKey(id)); err == nil {
			if !planDigestMatches(data, id) {
				return time.Time{}, false
			}
			// A sealed plan is opened to read the time, and has none for a store that cannot.
			if plain, err := ps.openPlan(id, data); err == nil {
				pf, err := parsePlanFile(id, plain)
				if _, sealed := asSealedPlan(data); sealed {
					clear(plain)
				}
				if err == nil {
					return pf.Created, true
				}
			}
		}
	}
	if kinds["progress"] {
		if p, err := ps.Progress(ctx, id); err == nil && !p.Updated.IsZero() {
			return p.Updated, true
		}
	}
	return time.Time{}, false
}

// repairProgress replaces a progress object that cannot be read with an empty
// one, once the plan is old enough that no write of the object can be running: a
// filesystem that can neither rename exclusively nor link creates a file and then
// writes it, and a crash between the two leaves an empty file.
func (ps *PlanStore) repairProgress(ctx context.Context, id string, created, now time.Time) {
	if now.Sub(created) <= incompleteGrace {
		return
	}
	empty, err := indentJSON(&PlanProgress{Format: PlanMetaFormat, ID: id, Updated: now})
	if err != nil {
		return
	}
	_, _ = ps.B.ReplaceIf(ctx, progressKey(id), func(current []byte) ([]byte, bool) {
		var p PlanProgress
		if json.Unmarshal(current, &p) == nil {
			return nil, false
		}
		return empty, true
	})
}

// remove claims a plan and deletes its objects, and reports whether this call
// removed any of them. m is nil for a plan that is not whole. foreign is set for a
// plan object that is not what its name says it is, which is deleted for that.
func (ps *PlanStore) remove(ctx context.Context, id string, o *GCOptions, m *PlanMeta, foreign bool) bool {
	now := ps.now()
	existed := false
	err := ps.updateProgress(ctx, id, func(p *PlanProgress) error {
		// updateProgress makes a progress object for a plan that has none, and that
		// one has no time of update yet.
		existed = !p.Updated.IsZero()
		// The rules are applied again to the progress as it is now, so that an apply
		// that began, or ended, since the plan was judged is respected.
		q := *p
		// A removal that another collector committed to and did not finish is finished by this one,
		// whatever the rules say now: no apply can have begun since the commit.
		adopt := q.Collecting.adoptable(now) && q.Collecting.By != o.Owner
		if q.Collecting != nil && (q.Collecting.By == o.Owner || adopt) {
			q.Collecting = nil
		}
		if m == nil || adopt {
			if planInUse(m, &q, now) != "" {
				return errSkip
			}
		} else if v := gcJudge(m, &q, o, now); !v.collect {
			return errSkip
		}
		p.Collecting = &PlanClaim{By: o.Owner, At: now}
		return nil
	})
	if err != nil {
		return false
	}
	// The claim holds for claimTimeout; a collector that was stopped for most of it
	// leaves the plan alone.
	if ps.now().Sub(now) > claimTimeout/2 || !ps.claimed(ctx, id, o.Owner) {
		return false
	}
	// The commit: after this write no apply is let in, whatever its clock says, so nothing
	// that an apply could read is deleted from under it. The write fails when the claim is not
	// this collector's any more, because an apply that saw it lapse took the plan, or when an
	// apply runs.
	if err := ps.updateProgress(ctx, id, func(p *PlanProgress) error {
		if p.Collecting == nil || p.Collecting.By != o.Owner || p.ApplyRunning(ps.now()) {
			return errSkip
		}
		p.Collecting.Committed = true
		return nil
	}); err != nil {
		return false
	}
	// Another collector may have removed the plan after this one judged it: the claim
	// above then made a progress object for a plan that is gone.
	_, metaErr := ps.B.Get(ctx, metaKey2(id))
	_, planErr := ps.B.Get(ctx, planKey(id))
	if errors.Is(metaErr, state.ErrNotFound) && errors.Is(planErr, state.ErrNotFound) && !existed {
		ps.deleteObject(ctx, id, "progress", o.Owner, false)
		return false
	}
	removed := false
	for _, kind := range []string{"plan", "meta"} {
		if deleted := ps.deleteObject(ctx, id, kind, o.Owner, foreign); deleted {
			removed = true
		}
	}
	if deleted := ps.deleteObject(ctx, id, "progress", o.Owner, false); deleted && existed && m == nil {
		removed = true
	}
	return removed
}

// claimed reports whether the collector still holds its claim on the plan.
func (ps *PlanStore) claimed(ctx context.Context, id, owner string) bool {
	p, err := ps.Progress(ctx, id)
	return err == nil && p.Collecting != nil && p.Collecting.By == owner
}

// deleteObject deletes one object of a plan if it is what its name says it is: a
// plan whose digest is its id, a summary that names the id, and a progress object
// that the collector has claimed. With foreign set it deletes the plan object if it
// is not what its name says, as a plan object that was planted is. It reports
// whether this call deleted the object.
func (ps *PlanStore) deleteObject(ctx context.Context, id, kind, owner string, foreign bool) bool {
	var key string
	switch kind {
	case "plan":
		key = planKey(id)
	case "meta":
		key = metaKey2(id)
	default:
		key = progressKey(id)
	}
	if gotID, gotKind, ok := planObject(key); !ok || gotID != id || gotKind != kind {
		return false
	}
	match := func(current []byte) bool {
		switch kind {
		case "plan":
			return planDigestMatches(current, id) != foreign
		case "meta":
			var h struct {
				ID string `json:"id"`
			}
			return json.Unmarshal(current, &h) == nil && h.ID == id
		}
		var h PlanProgress
		return json.Unmarshal(current, &h) == nil && h.Collecting != nil && h.Collecting.By == owner
	}
	done, err := ps.B.DeleteIf(ctx, key, match)
	return err == nil && done
}
