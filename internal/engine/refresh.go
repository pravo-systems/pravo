package engine

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"cuelang.org/go/cue"
	"github.com/hashicorp/terraform-plugin-go/tftypes"

	"github.com/pravo-systems/pravo/internal/convert"
	"github.com/pravo-systems/pravo/internal/plugin"
	"github.com/pravo-systems/pravo/internal/schema"
	"github.com/pravo-systems/pravo/internal/state"
)

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
		e.persistRefreshed(addr, sr, ch.Schema, attrs, newPrivate)
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
func (e *Engine) persistRefreshed(addr string, sr *state.Resource, sch *schema.Schema, attrs, private []byte) {
	e.persistRead(addr, sr, sch, attrs, private, true)
}

// persistRead writes the record that a read returned. With markDrift, a read that
// differs from the record leaves the mark; the read that follows an apply does not.
func (e *Engine) persistRead(addr string, sr *state.Resource, sch *schema.Schema, attrs, private []byte, markDrift bool) {
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
	if e.recordRefresh(addr, action, before, &n) && action == "drift" {
		// What the provider found differs from what the record said: keep how.
		e.recordDiff(addr, sch, sr.Attributes, attrs)
	}
}

// recordRefresh writes the record of a refresh and notes it in the transaction. A
// record that another command wrote, removed or created since this command read
// it is left as it is: what the refresh read belongs to the record this command
// holds, not to the one in the state.
func (e *Engine) recordRefresh(addr, action string, before int, n *state.Resource) bool {
	err := e.State.Refresh(n, e.txnID())
	switch {
	case errors.Is(err, state.ErrChanged):
		e.Opts.Progress("Warning: %s changed in the state since this command read it; what the refresh read is not recorded", addr)
	case err == nil && e.txn != nil:
		e.txn.Record(addr, action, before, n.Revision)
		return true
	}
	return false
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
	ctx = plugin.WithResource(ctx, ch.Addr)
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
