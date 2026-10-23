package engine

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"

	"github.com/pravo-systems/pravo/internal/protect"
	"github.com/pravo-systems/pravo/internal/state"
)

// In a sealed state the objects of a stored plan are sealed as follows.
//
//	plans/<id>.plan.json      {"v":1,"id":"plan-2f9c1a4e","sealed":{...}}: the plan file, sealed. The id is
//	                          still the digest of the plan file, the plaintext, which only a reader can
//	                          check; the envelope carries it in the clear and the seal binds it.
//	plans/<id>.meta.json      the summary, readable, but with the command line and the tags in the form that
//	                          the history shows (see state.RedactArgs), and the originals sealed in "sealed".
//	plans/<id>.progress.json  not sealed: addresses, transaction ids and the names of the claims.
//
// A plan that was stored before the state was sealed stays as it is until SealAll
// reaches it, and a reader can still read it; the PlanStore says so.

// sealedPlanVersion is the version of the envelope of a sealed plan.
const sealedPlanVersion = 1

// sealedPlan is the plan object of a sealed state.
type sealedPlan struct {
	V      int             `json:"v"`
	ID     string          `json:"id"`
	Sealed *protect.Sealed `json:"sealed"`
}

// asSealedPlan returns the envelope in data, or false when data is not one, as
// the plan file of an unsealed state is not. A plan file starts with "format",
// an envelope with "v", so the test does not decode a plan of any size.
func asSealedPlan(data []byte) (*sealedPlan, bool) {
	rest := bytes.TrimLeft(data, " \t\r\n")
	rest, found := bytes.CutPrefix(rest, []byte("{"))
	if !found || !bytes.HasPrefix(bytes.TrimLeft(rest, " \t\r\n"), []byte(`"v"`)) {
		return nil, false
	}
	var p sealedPlan
	if json.Unmarshal(data, &p) != nil || p.V == 0 || p.Sealed == nil {
		return nil, false
	}
	return &p, true
}

// planSecrets are the parts of a summary that a sealed state does not show: the
// command line and the tags as they were typed.
type planSecrets struct {
	Command string   `json:"command,omitempty"`
	Tags    []string `json:"tags,omitempty"`
}

// sealer returns the sealer of the state that the store belongs to: the one set in
// Seal, otherwise the one of the state that NewPlanStoreFor was given, looked up
// each time because a state that is being sealed gets its sealer meanwhile. Nil
// means that nothing is sealed.
func (ps *PlanStore) sealer() state.Sealer {
	if ps.Seal != nil {
		return ps.Seal
	}
	if ps.st != nil {
		return ps.st.Sealer()
	}
	return nil
}

// errSealedPlan is the error for a sealed plan in a store that holds no sealer.
func errSealedPlan(id string) error {
	return fmt.Errorf("the plan %s is sealed: %w", id, state.ErrNotReader)
}

// sealPlan seals the plan file under the id that its digest gave.
func sealPlan(sl state.Sealer, id string, plain []byte) ([]byte, error) {
	box, err := sl.Seal(protect.KindPlan, id, plain)
	if err != nil {
		return nil, err
	}
	return indentJSON(&sealedPlan{V: sealedPlanVersion, ID: id, Sealed: &box})
}

// openPlan returns the plan file of the plan object data, which is sealed or not.
// A sealed plan that the store cannot open is ErrNotReader; one that does not open
// where it is, because it was changed or is another plan's, is ErrUntrusted.
func (ps *PlanStore) openPlan(id string, data []byte) ([]byte, error) {
	env, ok := asSealedPlan(data)
	if !ok {
		return data, nil
	}
	if env.ID != id {
		return nil, fmt.Errorf("the plan %s names another plan, %q: %w", id, env.ID, state.ErrUntrusted)
	}
	if env.V > sealedPlanVersion {
		return nil, fmt.Errorf("the plan %s was stored by a newer pravo (sealed plan format %d)", id, env.V)
	}
	sl := ps.sealer()
	if sl == nil {
		return nil, errSealedPlan(id)
	}
	plain, err := sl.Open(protect.KindPlan, id, *env.Sealed)
	if err != nil {
		return nil, openError("plan", id, err)
	}
	return plain, nil
}

// openError says that a sealed object did not open. ErrNotReader is kept for the
// caller to test; a failure to authenticate is ErrUntrusted, and says nothing more
// about why.
func openError(what, id string, err error) error {
	switch {
	case errors.Is(err, state.ErrNotReader):
		return fmt.Errorf("the %s %s is sealed: %w", what, id, state.ErrNotReader)
	case errors.Is(err, protect.ErrAuth), errors.Is(err, state.ErrUntrusted):
		return fmt.Errorf("the %s %s does not open where it is: %w", what, id, state.ErrUntrusted)
	}
	return fmt.Errorf("the %s %s is sealed and cannot be opened by this key: %w", what, id, state.ErrNotReader)
}

// planDigestMatches reports whether the plan object data is the plan that id
// names: for a sealed plan the id in the envelope, which only a reader can check
// against the plaintext, and for the plan file of an unsealed state its digest.
func planDigestMatches(data []byte, id string) bool {
	if env, ok := asSealedPlan(data); ok {
		return env.ID == id
	}
	return digestID(data, len(id)-len("plan-")) == id
}

// samePlan reports whether the plan object old, stored under id, holds the plan
// file data.
func (ps *PlanStore) samePlan(id string, old, data []byte) bool {
	if _, ok := asSealedPlan(old); !ok {
		return bytes.Equal(old, data)
	}
	plain, err := ps.openPlan(id, old)
	if err != nil {
		return false
	}
	defer clear(plain)
	return bytes.Equal(plain, data)
}

// redactMeta returns a copy of the summary as a sealed state stores it: the
// command line and the tags in the form that the history shows, and the originals
// sealed beside them.
func redactMeta(sl state.Sealer, m *PlanMeta) (*PlanMeta, error) {
	tag := state.TagFunc(sl)
	shown := m.shownCommand
	if shown == "" {
		shown = state.RedactLine(m.Command, tag)
	}
	var tags []string
	for _, t := range m.Tags {
		tags = append(tags, state.RedactTag(t, tag))
	}
	plain, err := json.Marshal(planSecrets{Command: m.Command, Tags: m.Tags})
	if err != nil {
		return nil, err
	}
	defer clear(plain)
	box, err := sl.Seal(protect.KindPlanCmd, m.ID, plain)
	if err != nil {
		return nil, err
	}
	c := *m
	c.Command, c.Tags, c.Sealed = shown, tags, &box
	return &c, nil
}

// openMeta restores the command line and the tags of a summary that was stored in
// a sealed state, when the store can open them. A summary that the store cannot
// open keeps the form that the history shows and is marked Redacted. A summary
// without a box in a sealed state was written before the seal and is marked
// Unsealed.
func (ps *PlanStore) openMeta(m *PlanMeta) error {
	sl := ps.sealer()
	box := m.Sealed
	m.Sealed = nil
	if box == nil {
		m.Unsealed = sl != nil
		return nil
	}
	if sl == nil {
		m.Redacted = true
		return nil
	}
	plain, err := sl.Open(protect.KindPlanCmd, m.ID, *box)
	if err != nil {
		if errors.Is(err, protect.ErrAuth) || errors.Is(err, state.ErrUntrusted) {
			return openError("summary", m.ID, err)
		}
		m.Redacted = true
		return nil
	}
	defer clear(plain)
	var sec planSecrets
	if err := json.Unmarshal(plain, &sec); err != nil {
		return fmt.Errorf("the summary of %s is not readable: sealed value is not the expected JSON", m.ID)
	}
	m.Command, m.Tags = sec.Command, sec.Tags
	return nil
}

// planObjects returns the ids of the plans that have a plan or a summary, in order,
// with the kinds each has.
func (ps *PlanStore) planObjects(ctx context.Context) ([]string, map[string]map[string]bool, error) {
	all, err := ps.ids(ctx)
	if err != nil {
		return nil, nil, err
	}
	var ids []string
	for id, kinds := range all {
		if kinds["plan"] || kinds["meta"] {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	return ids, all, nil
}

// inUse reports whether an apply runs from the plan, or a collector is about to
// remove it.
func (ps *PlanStore) inUse(ctx context.Context, id string) (bool, error) {
	pr, err := ps.Progress(ctx, id)
	if err != nil {
		return false, err
	}
	now := ps.now()
	return pr.ApplyRunning(now) || pr.BeingCollected(now), nil
}

// SealAll seals in place every stored plan that is not sealed yet and that no
// apply uses: the plan file, and the command line and the tags of the summary. The
// ids do not change. A plan with a claim on it is skipped and counted, and so is
// one that changed or went while it was being sealed; every object is replaced
// only if it is still the one that was read, so that an apply or a collection
// that runs meanwhile wins. A plan that is sealed already is left alone, so a seal
// that was interrupted can be run again.
func (ps *PlanStore) SealAll(ctx context.Context) (sealed, skipped int, err error) {
	return ps.sealAll(ctx, false)
}

// ResealAll seals every stored plan again under the current group of the state,
// as SealAll does for the plans that are not sealed, and returns how many plans it
// changed and how many it left alone. A plan that is under the current group
// already is not written.
func (ps *PlanStore) ResealAll(ctx context.Context) (sealed, skipped int, err error) {
	return ps.sealAll(ctx, true)
}

func (ps *PlanStore) sealAll(ctx context.Context, again bool) (changed, skipped int, err error) {
	sl := ps.sealer()
	if sl == nil {
		return 0, 0, errors.New("the state is not sealed")
	}
	ids, all, err := ps.planObjects(ctx)
	if err != nil {
		return 0, 0, err
	}
	var errs []error
	for _, id := range ids {
		if err := ctx.Err(); err != nil {
			return changed, skipped, err
		}
		busy, err := ps.inUse(ctx, id)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", id, err))
			continue
		}
		n, left, err := ps.sealPlanObjects(ctx, sl, id, all[id], busy, again)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", id, err))
			continue
		}
		switch {
		case left:
			skipped++
		case n > 0:
			changed++
		}
	}
	if ps.Cache != nil {
		for _, id := range ids {
			ps.Cache.forget(id)
		}
	}
	return changed, skipped, errors.Join(errs...)
}

// sealPlanObjects seals the plan and the summary of one plan, and returns how many
// of the two it wrote and whether it left one alone. With again set it also
// replaces objects that are sealed under another group than the current one. A
// plan in use is not sealed if it is not yet; its sealed objects are still
// replaced, which an apply does not notice.
func (ps *PlanStore) sealPlanObjects(ctx context.Context, sl state.Sealer, id string, kinds map[string]bool, busy, again bool) (wrote int, left bool, err error) {
	if kinds["plan"] {
		w, l, err := ps.sealPlanFile(ctx, sl, id, busy, again)
		if err != nil {
			return wrote, left, err
		}
		wrote, left = wrote+w, left || l
		if left {
			return wrote, left, nil // the summary stays with its plan
		}
	}
	if kinds["meta"] {
		w, l, err := ps.sealSummary(ctx, sl, id, busy, again)
		if err != nil {
			return wrote, left, err
		}
		wrote, left = wrote+w, left || l
	}
	return wrote, left, nil
}

func (ps *PlanStore) sealPlanFile(ctx context.Context, sl state.Sealer, id string, busy, again bool) (wrote int, left bool, err error) {
	key := planKey(id)
	data, err := ps.B.Get(ctx, key)
	if errors.Is(err, state.ErrNotFound) {
		return 0, true, nil
	}
	if err != nil {
		return 0, false, err
	}
	env, isSealed := asSealedPlan(data)
	plain := data
	switch {
	case isSealed && !again:
		return 0, false, nil
	case isSealed:
		if plain, err = ps.openPlan(id, data); err != nil {
			return 0, false, err
		}
		defer clear(plain)
	case busy:
		return 0, true, nil
	case digestID(data, len(id)-len("plan-")) != id:
		// Not what its name says it is: a collection does not touch it either.
		return 0, true, nil
	}
	next, err := sealPlan(sl, id, plain)
	if err != nil {
		return 0, false, err
	}
	if cur, _ := asSealedPlan(next); isSealed && cur.Sealed.Group == env.Sealed.Group {
		return 0, false, nil // already under the current group
	}
	return ps.replace(ctx, key, data, next)
}

func (ps *PlanStore) sealSummary(ctx context.Context, sl state.Sealer, id string, busy, again bool) (wrote int, left bool, err error) {
	key := metaKey2(id)
	data, err := ps.B.Get(ctx, key)
	if errors.Is(err, state.ErrNotFound) {
		return 0, true, nil
	}
	if err != nil {
		return 0, false, err
	}
	m, err := decodeMeta(id, data)
	if err != nil {
		return 0, true, nil // not readable: not ours to rewrite
	}
	var next *PlanMeta
	switch {
	case m.Sealed != nil && !again:
		return 0, false, nil
	case m.Sealed != nil:
		// The command line and the tags that are shown stay; the box is sealed again.
		plain, err := sl.Open(protect.KindPlanCmd, id, *m.Sealed)
		if err != nil {
			return 0, false, openError("summary", id, err)
		}
		defer clear(plain)
		box, err := sl.Seal(protect.KindPlanCmd, id, plain)
		if err != nil {
			return 0, false, err
		}
		if box.Group == m.Sealed.Group {
			return 0, false, nil
		}
		m.Sealed = &box
		next = m
	case busy:
		return 0, true, nil
	default:
		// Written before the seal: it holds the command line as it was typed.
		if next, err = redactMeta(sl, m); err != nil {
			return 0, false, err
		}
	}
	enc, err := indentJSON(next)
	if err != nil {
		return 0, false, err
	}
	return ps.replace(ctx, key, data, enc)
}

// replace replaces the object at key by next if it is still old. It returns how
// many objects it wrote, and left if the object had changed or gone.
func (ps *PlanStore) replace(ctx context.Context, key string, old, next []byte) (wrote int, left bool, err error) {
	ok, err := ps.B.ReplaceIf(ctx, key, func(cur []byte) ([]byte, bool) { return next, bytes.Equal(cur, old) })
	switch {
	case errors.Is(err, state.ErrNotFound):
		return 0, true, nil
	case err != nil:
		return 0, false, state.Advise(err)
	case !ok:
		return 0, true, nil
	}
	return 1, false, nil
}

// UnsealAll writes in the open every stored plan that is sealed and that no apply uses:
// the plan file, and the command line and the tags of the summary, which come back as
// they were typed. The ids do not change, since an id is the digest of the plan file.
// A plan with a claim on it is left sealed and counted, and so is one that changed or
// went while it was being unsealed; every object is replaced only if it is still the one
// that was read. A plan that is in the open already is left alone, so an unseal that was
// interrupted can be run again.
func (ps *PlanStore) UnsealAll(ctx context.Context) (unsealed, skipped int, err error) {
	sl := ps.sealer()
	if sl == nil {
		return 0, 0, errors.New("the state is not sealed")
	}
	ids, all, err := ps.planObjects(ctx)
	if err != nil {
		return 0, 0, err
	}
	var errs []error
	for _, id := range ids {
		if err := ctx.Err(); err != nil {
			return unsealed, skipped, err
		}
		busy, err := ps.inUse(ctx, id)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", id, err))
			continue
		}
		n, left, err := ps.unsealPlanObjects(ctx, sl, id, all[id], busy)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", id, err))
			continue
		}
		switch {
		case left:
			skipped++
		case n > 0:
			unsealed++
		}
	}
	if ps.Cache != nil {
		for _, id := range ids {
			ps.Cache.forget(id)
		}
	}
	return unsealed, skipped, errors.Join(errs...)
}

func (ps *PlanStore) unsealPlanObjects(ctx context.Context, sl state.Sealer, id string, kinds map[string]bool, busy bool) (wrote int, left bool, err error) {
	if kinds["plan"] {
		w, l, err := ps.unsealPlanFile(ctx, id, busy)
		if err != nil {
			return wrote, left, err
		}
		wrote, left = wrote+w, left || l
		if left {
			return wrote, left, nil // the summary stays with its plan
		}
	}
	if kinds["meta"] {
		w, l, err := ps.unsealSummary(ctx, sl, id, busy)
		if err != nil {
			return wrote, left, err
		}
		wrote, left = wrote+w, left || l
	}
	return wrote, left, nil
}

func (ps *PlanStore) unsealPlanFile(ctx context.Context, id string, busy bool) (wrote int, left bool, err error) {
	key := planKey(id)
	data, err := ps.B.Get(ctx, key)
	if errors.Is(err, state.ErrNotFound) {
		return 0, false, nil // a summary without its plan: nothing here is sealed
	}
	if err != nil {
		return 0, false, err
	}
	if _, isSealed := asSealedPlan(data); !isSealed {
		return 0, false, nil
	}
	if busy {
		return 0, true, nil
	}
	plain, err := ps.openPlan(id, data)
	if err != nil {
		return 0, false, err
	}
	defer clear(plain)
	if digestID(plain, len(id)-len("plan-")) != id {
		return 0, false, fmt.Errorf("the plan %s opens, and is not the plan its name says it is: %w", id, state.ErrUntrusted)
	}
	return ps.replace(ctx, key, data, bytes.Clone(plain))
}

func (ps *PlanStore) unsealSummary(ctx context.Context, sl state.Sealer, id string, busy bool) (wrote int, left bool, err error) {
	key := metaKey2(id)
	data, err := ps.B.Get(ctx, key)
	if errors.Is(err, state.ErrNotFound) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	m, err := decodeMeta(id, data)
	if err != nil {
		// Not readable by this pravo: not ours to rewrite, and no reason to hold the
		// unseal. Only a plan that an apply uses does that.
		return 0, false, nil
	}
	if m.Sealed == nil {
		return 0, false, nil
	}
	if busy {
		return 0, true, nil
	}
	plain, err := sl.Open(protect.KindPlanCmd, id, *m.Sealed)
	if err != nil {
		return 0, false, openError("summary", id, err)
	}
	defer clear(plain)
	var sec planSecrets
	if err := json.Unmarshal(plain, &sec); err != nil {
		return 0, false, fmt.Errorf("the summary of %s is not readable: sealed value is not the expected JSON", id)
	}
	m.Command, m.Tags, m.Sealed = sec.Command, sec.Tags, nil
	enc, err := indentJSON(m)
	if err != nil {
		return 0, false, err
	}
	return ps.replace(ctx, key, data, enc)
}
