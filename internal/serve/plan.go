package serve

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/hashicorp/terraform-plugin-go/tftypes"

	"github.com/pravo-systems/pravo/internal/config"
	"github.com/pravo-systems/pravo/internal/engine"
	"github.com/pravo-systems/pravo/internal/schema"
)

// maxAttrChanges bounds the attribute changes of one node that are sent.
const maxAttrChanges = 400

// planOverlay is a plan laid over the model of one revision: the saved plan that
// the viewer was started with, or a stored plan.
type planOverlay struct {
	info    PlanInfo
	entries map[string]*planEntry
	// changes holds the decoded changes, by address; nil when the values of the
	// plan cannot be decoded (a schema is not cached).
	changes  map[string]*engine.Change
	warnings []string
	failed   bool
	err      error // why the plan failed
	// secrets are the sensitive strings that the values of this plan are judged
	// against: those of the state, and the ones the plan would write. The plan the
	// viewer was started with adds its own to the set of the model; a stored plan
	// that is asked for later has a copy, so that the model does not change.
	secrets *secretSet
	// order lists the addresses of entries, sorted.
	order []string
	// reason, command and git are what the author of a stored plan wrote with it,
	// as it was written: PlanOverlay sends them masked against secrets.
	reason, command, git string
}

// planEntry is what the plan file says of one node.
type planEntry struct {
	action          string
	reasons         []string
	requiresReplace []string
	applied         bool
	deferred        bool
	moved           string
	// conflict is why a partly stale stored plan cannot apply this change now, as
	// the engine words it: Detail masks it.
	conflict []string
}

// planCache keeps the plan file while it stays the same.
type planCache struct {
	sig string
	pf  *engine.PlanFile
	err error
}

// readPlan reads the plan file, again only when it changed.
func (s *Server) readPlan() (*engine.PlanFile, error) {
	sig := fileSignature(s.opts.PlanFile)
	if s.plan != nil && s.plan.sig == sig {
		return s.plan.pf, s.plan.err
	}
	pf, err := engine.ReadPlanFile(s.opts.PlanFile)
	s.plan = &planCache{sig: sig, pf: pf, err: err}
	return pf, err
}

// loadPlan overlays the saved plan on a model. It reads the plan file and, when
// the cached schemas allow, decodes the values of the changes. It starts no
// provider and writes nothing.
func (s *Server) loadPlan(ctx context.Context, m *model) *planOverlay {
	switch {
	case s.opts.PlanFile != "":
		po := &planOverlay{entries: map[string]*planEntry{}, secrets: m.secrets}
		pf, err := s.readPlan()
		if err != nil {
			po.failed = true
			po.warnings = append(po.warnings, "The plan file could not be read: "+err.Error())
			return po
		}
		return s.overlay(ctx, m, pf, s.planDisplay(), nil, m.secrets, false)
	case s.opts.PlanID != "":
		po := &planOverlay{entries: map[string]*planEntry{}, secrets: m.secrets}
		e, err := m.stored.resolve(s.opts.PlanID)
		if err == nil {
			var pf *engine.PlanFile
			var sealed bool
			if pf, sealed, err = m.stored.planFile(ctx, e); err == nil {
				return s.overlay(ctx, m, pf, e.Meta.ID, e, m.secrets, sealed)
			}
		}
		po.failed, po.err = true, err
		po.warnings = append(po.warnings, "The stored plan could not be read: "+err.Error())
		return po
	}
	return nil
}

// overlay lays a plan on the model: the action of each node it ran and, when the
// cached schemas allow, the values of its changes. stored is the listing entry
// of a stored plan, which says where the plan stands: it is judged against the
// shared state alone, as the listing is, and not against this working copy. For
// the plan of a file the configuration counts too. sealed is set when pf is the
// summary of a stored plan that this process cannot open: it holds no value, and
// the overlay says so.
func (s *Server) overlay(ctx context.Context, m *model, pf *engine.PlanFile, display string, stored *engine.PlanEntry, secrets *secretSet, sealed bool) *planOverlay {
	po := &planOverlay{entries: map[string]*planEntry{}, secrets: secrets}
	po.info.File = display
	po.info.Created = pf.Created
	po.info.Actor = pf.Actor
	if stored != nil {
		po.info.ID = stored.Meta.ID
		po.info.Actor = stored.Meta.Author
		po.info.Status = string(stored.Status)
		po.reason, po.command, po.git = stored.Meta.Reason, stored.Meta.Command, stored.Meta.GitRevision
		if verify := s.opts.VerifyPlan; verify != nil {
			if sealed {
				// A plan this process cannot open cannot be checked against its signatures.
				po.info.Signatures = &PlanSignatures{Items: []PlanSignature{}, Note: "not checked: this identity cannot open the plan"}
			} else if sigs, ok := verify(ctx, m.store, stored.Meta.ID, pf.Digest); ok {
				po.info.Signatures = sigs
			}
		}
	}
	for _, sc := range pf.Changes {
		a := engine.Action(sc.Action)
		if a == engine.ActionOpen || a == engine.ActionCall {
			continue
		}
		e := &planEntry{action: string(a), reasons: sc.Reasons, requiresReplace: sc.RequiresReplace, applied: sc.Applied, deferred: sc.Deferred, moved: sc.MovedFrom}
		if stored != nil && !sc.Applied {
			e.conflict = stored.Conflicts[sc.Addr]
		}
		if sc.Imported {
			e.action = "import"
		}
		po.entries[sc.Addr] = e
		if sc.Applied {
			po.info.Applied++
		}
	}
	po.info.Planned = len(po.entries)

	var plan *engine.Plan
	var stale *engine.ErrStalePlan
	var derr error
	if !sealed {
		plan, stale, derr = s.decodePlan(m, pf, stored != nil)
	}
	switch {
	case sealed:
		po.warnings = append(po.warnings, "The values of this plan are sealed.")
	case derr != nil:
		po.warnings = append(po.warnings, "The values of the plan are not shown: "+derr.Error()+".")
		if stored == nil {
			po.info.Stale, po.info.Reasons = s.staleReasons(m, pf)
		}
	case stale != nil:
		po.info.Stale, po.info.Reasons = true, stale.Reasons
	}
	if stored != nil {
		// A partly stale plan is not stale: it can be applied in part, and its
		// conflicts are in the listing and on the nodes (Detail.Notes).
		po.info.Stale, po.info.Reasons = stored.Status == engine.PlanStale, nil
		if po.info.Stale {
			po.info.Reasons = stored.Reasons
		}
	}
	po.info.Policies = m.planPolicies(pf.Policies)
	if plan != nil {
		po.changes = map[string]*engine.Change{}
		for _, ch := range plan.Changes {
			po.changes[ch.Addr] = ch
		}
		po.info.Summary.Add, po.info.Summary.Change, po.info.Summary.Destroy, po.info.Summary.Read = plan.Counts()
	} else {
		for _, sc := range pf.Changes {
			switch engine.Action(sc.Action) {
			case engine.ActionCreate:
				po.info.Summary.Add++
			case engine.ActionUpdate:
				po.info.Summary.Change++
			case engine.ActionReplace:
				po.info.Summary.Add++
				po.info.Summary.Destroy++
			case engine.ActionDelete:
				po.info.Summary.Destroy++
			case engine.ActionRead:
				if !sc.Skipped {
					po.info.Summary.Read++
				}
			}
		}
	}
	po.info.Hints = renameHints(m, pf)
	if len(po.info.Reasons) > 0 {
		po.info.Stale = true
	}
	if po.info.Stale {
		po.warnings = append(po.warnings, "The plan is stale and can no longer be applied: "+strings.Join(po.info.Reasons, "; ")+".")
	}
	// The blast radius: the nodes the pending changes touch and what depends on them.
	var changed []string
	touched := map[string]bool{}
	for addr, e := range po.entries {
		switch e.action {
		case "create", "update", "replace", "delete":
		case "read":
			if !e.deferred {
				continue
			}
		default:
			continue
		}
		if e.applied {
			continue
		}
		touched[addr] = true
		changed = append(changed, addr)
	}
	sort.Strings(changed)
	for _, d := range m.store.ReachOf(changed) {
		touched[d] = true
	}
	po.info.Summary.BlastRadius = len(touched)
	po.order = make([]string, 0, len(po.entries))
	for addr := range po.entries {
		po.order = append(po.order, addr)
	}
	sort.Strings(po.order)
	return po
}

// planDisplay is the path of the plan file as a page shows it: relative to the
// project when it lies inside.
func (s *Server) planDisplay() string {
	p := s.opts.PlanFile
	if abs, err := filepath.Abs(p); err == nil {
		if rel, err := filepath.Rel(s.opts.Dir, abs); err == nil && !strings.HasPrefix(rel, "..") {
			return filepath.ToSlash(rel)
		}
		return filepath.ToSlash(abs)
	}
	return filepath.ToSlash(p)
}

// decodePlan loads the plan into an engine that has the cached schemas and no
// provider. A plan that cannot be applied any more is returned as stale, and
// still decoded. With unchecked the plan is decoded and not judged: a stored
// plan is judged by the listing, against the state.
func (s *Server) decodePlan(m *model, pf *engine.PlanFile, unchecked bool) (*engine.Plan, *engine.ErrStalePlan, error) {
	root := m.spec.root
	if root == nil {
		return nil, nil, errors.New("the specification does not load")
	}
	provs := engine.NewProviders(root, nil)
	schemas := map[string]*schema.ProviderSchema{}
	for local, ver := range pf.Providers {
		req := root.Providers[local]
		if req == nil {
			continue
		}
		now := ver
		if e, ok := m.spec.lock.Providers[req.Source.String()]; ok {
			now = e.Version
		}
		if req.Path != "" {
			now = "dev"
		}
		provs.Resolved[local] = &engine.ResolvedProvider{Req: req, Version: now, Protocol: config.ProtocolTfplugin}
	}
	for _, sc := range pf.Changes {
		if engine.Action(sc.Action) == engine.ActionMove {
			continue
		}
		local := config.LocalName(sc.ProviderKey)
		req := root.Providers[local]
		if req == nil {
			return nil, nil, fmt.Errorf("provider %s of %s is not declared", local, sc.Addr)
		}
		ps, ok := s.lookupSchema(req.Source, pf.Providers[local])
		if !ok {
			return nil, nil, fmt.Errorf("the schema of provider %s v%s is not cached", req.Source.Display(), pf.Providers[local])
		}
		sch := ps.Type(sc.Mode, sc.Type)
		if sch == nil {
			return nil, nil, fmt.Errorf("the schema of %s %s is not in the cache of provider %s", sc.Mode, sc.Type, req.Source.Display())
		}
		if sch.Block != nil {
			sch.Block.Type() // see resolveSchema
		}
		set := schemas[local]
		if set == nil {
			set = &schema.ProviderSchema{Resources: map[string]*schema.Schema{}, DataSources: map[string]*schema.Schema{},
				Ephemerals: map[string]*schema.Schema{}, Functions: map[string]*schema.Function{}}
			schemas[local] = set
		}
		if sc.Mode == "data" {
			set.DataSources[sc.Type] = sch
		} else {
			set.Resources[sc.Type] = sch
		}
	}
	eng := engine.New(root, m.store, provs, schemas, root.Value, engine.Options{})
	if unchecked {
		plan, err := eng.LoadPlanUnchecked(pf)
		return plan, nil, err
	}
	plan, err := eng.LoadPlanFor(pf, nil)
	var stale *engine.ErrStalePlan
	if errors.As(err, &stale) {
		plan, err = eng.LoadPlanUnchecked(pf)
		if err != nil {
			return nil, nil, err
		}
		return plan, stale, nil
	}
	if err != nil {
		return nil, nil, err
	}
	return plan, nil, nil
}

// staleReasons judges a plan whose values cannot be decoded, from the parts that
// need no schema: the configuration, the state it was made against, and the
// revisions of the records it changes.
func (s *Server) staleReasons(m *model, pf *engine.PlanFile) (bool, []string) {
	var reasons []string
	if m.spec.root != nil {
		if reason := engine.ConfigChange(m.spec.root, pf); reason != "" {
			reasons = append(reasons, reason)
		}
	}
	if pf.Lineage != m.store.Lineage() {
		reasons = append(reasons, "the plan was made against a different state (lineage mismatch)")
	}
	for _, sc := range pf.Changes {
		if sc.Applied || sc.Skipped || sc.Kept || engine.Action(sc.Action) == engine.ActionMove {
			continue
		}
		have := 0
		if rec := m.store.Get(sc.Addr); rec != nil {
			have = rec.Revision
		}
		if have != sc.PriorRevision {
			reasons = append(reasons, fmt.Sprintf("%s changed in the state since the plan was made (revision %d, the plan expected %d)", sc.Addr, have, sc.PriorRevision))
		}
	}
	return len(reasons) > 0, reasons
}

// collectPlanSecrets adds to the secrets of the model the sensitive values that
// the plan would write: a new password is not in the state yet, and another
// attribute may embed it all the same.
func (m *model) collectPlanSecrets() {
	if m.plan == nil {
		return
	}
	if m.plan.secrets == nil {
		m.plan.secrets = m.secrets
	}
	m.plan.collectSecrets()
}

// collectSecrets adds to the secrets of the overlay the sensitive values that its
// plan would write.
func (po *planOverlay) collectSecrets() {
	for _, ch := range po.changes {
		if ch == nil || ch.Schema == nil || ch.Schema.Block == nil {
			continue
		}
		before, after := ch.Prior, ch.Planned
		if ch.Action == engine.ActionReplace && ch.PlannedCreate.Type() != nil {
			after = ch.PlannedCreate
		}
		d := &differ{sink: po.secrets}
		d.block("", ch.Schema.Block, before, after)
	}
}

// changeOf describes what the plan the viewer was started with does to a node,
// with the attributes it changes. Values are redacted as in the record.
func (m *model) changeOf(mn *mnode) *Change { return m.changeIn(m.plan, mn) }

// changeIn is changeOf for any plan laid over the model.
func (m *model) changeIn(po *planOverlay, mn *mnode) *Change {
	if po == nil || po.failed {
		return nil
	}
	e := po.entries[mn.Addr]
	if e == nil {
		return nil
	}
	c := &Change{Action: e.action, Applied: e.applied, Reasons: e.reasons, RequiresReplace: e.requiresReplace}
	ch := po.changes[mn.Addr]
	if ch == nil || ch.Schema == nil || ch.Schema.Block == nil {
		return c
	}
	secrets := po.secrets
	if secrets == nil {
		secrets = m.secrets
	}
	before, after := ch.Prior, ch.Planned
	switch ch.Action {
	case engine.ActionReplace:
		if ch.PlannedCreate.Type() != nil {
			after = ch.PlannedCreate
		}
	case engine.ActionDelete:
		after = tftypes.Value{}
	case engine.ActionNoop:
		return c
	}
	d := &differ{secrets: secrets}
	d.block("", ch.Schema.Block, before, after)
	c.Attrs = d.out
	return c
}

// differ walks the prior and the planned value of a node against its schema and
// lists what differs. A value that the schema or the name withholds, or that
// holds a sensitive string of the state, is reported as changed and not shown.
type differ struct {
	secrets *secretSet
	// sink, when set, collects the strings of every value that is withheld for
	// what the attribute is: the plan holds new secrets that the state does not
	// hold yet.
	sink *secretSet
	out  []AttrChange
}

func (d *differ) full() bool { return len(d.out) >= maxAttrChanges }

func isNull(v tftypes.Value) bool { return v.Type() == nil || v.IsNull() }

func isUnknown(v tftypes.Value) bool { return v.Type() != nil && !v.IsKnown() }

// fields returns the fields of an object or map value.
func fields(v tftypes.Value) map[string]tftypes.Value {
	if isNull(v) || isUnknown(v) {
		return nil
	}
	var m map[string]tftypes.Value
	if v.As(&m) != nil {
		return nil
	}
	return m
}

func elements(v tftypes.Value) ([]tftypes.Value, bool) {
	if isNull(v) || isUnknown(v) {
		return nil, true
	}
	var l []tftypes.Value
	if v.As(&l) != nil {
		return nil, false
	}
	return l, true
}

func join(path, name string) string {
	if path == "" {
		return name
	}
	return path + "." + name
}

func (d *differ) block(path string, b *schema.Block, before, after tftypes.Value) {
	bm, am := fields(before), fields(after)
	for _, a := range b.Attributes {
		if d.full() {
			return
		}
		d.attr(join(path, a.Name), a, bm[a.Name], am[a.Name])
	}
	for _, nb := range b.BlockTypes {
		if d.full() {
			return
		}
		d.nestedBlock(join(path, nb.TypeName), nb, bm[nb.TypeName], am[nb.TypeName])
	}
}

func (d *differ) attr(path string, a *schema.Attribute, before, after tftypes.Value) {
	if withholdReason(a.Name, a.Sensitive, a.WriteOnly) != "" {
		d.leaf(path, true, before, after)
		return
	}
	if o := a.NestedType; o != nil {
		d.nested(path, o, before, after)
		return
	}
	d.value(path, a.Type(), before, after)
}

func (d *differ) nested(path string, o *schema.Object, before, after tftypes.Value) {
	object := func(path string, before, after tftypes.Value) {
		bm, am := fields(before), fields(after)
		for _, a := range o.Attributes {
			d.attr(join(path, a.Name), a, bm[a.Name], am[a.Name])
		}
	}
	switch o.Nesting {
	case schema.NestingList:
		bl, bok := elements(before)
		al, aok := elements(after)
		if !bok || !aok || len(bl) != len(al) && len(bl) > 0 && len(al) > 0 {
			d.leaf(path, d.subtreeSensitive(o.Attributes), before, after)
			return
		}
		for i := 0; i < max(len(bl), len(al)); i++ {
			object(fmt.Sprintf("%s[%d]", path, i), at(bl, i), at(al, i))
		}
	case schema.NestingMap:
		bm, am := fields(before), fields(after)
		for _, k := range unionKeys(bm, am) {
			object(join(path, k), bm[k], am[k])
		}
	case schema.NestingSet:
		d.leaf(path, d.subtreeSensitive(o.Attributes), before, after)
	default:
		object(path, before, after)
	}
}

func (d *differ) nestedBlock(path string, nb *schema.NestedBlock, before, after tftypes.Value) {
	object := func(path string, before, after tftypes.Value) { d.block(path, nb.Block, before, after) }
	switch nb.Nesting {
	case schema.NestingList:
		bl, bok := elements(before)
		al, aok := elements(after)
		if !bok || !aok || len(bl) != len(al) && len(bl) > 0 && len(al) > 0 {
			d.leaf(path, blockSensitive(nb.Block), before, after)
			return
		}
		for i := 0; i < max(len(bl), len(al)); i++ {
			object(fmt.Sprintf("%s[%d]", path, i), at(bl, i), at(al, i))
		}
	case schema.NestingMap:
		bm, am := fields(before), fields(after)
		for _, k := range unionKeys(bm, am) {
			object(join(path, k), bm[k], am[k])
		}
	case schema.NestingSet:
		d.leaf(path, blockSensitive(nb.Block), before, after)
	default:
		object(path, before, after)
	}
}

func at(l []tftypes.Value, i int) tftypes.Value {
	if i < len(l) {
		return l[i]
	}
	return tftypes.Value{}
}

func unionKeys(a, b map[string]tftypes.Value) []string {
	set := map[string]bool{}
	for k := range a {
		set[k] = true
	}
	for k := range b {
		set[k] = true
	}
	keys := make([]string, 0, len(set))
	for k := range set {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// value diffs a value of a known type: an object or map key by key, a list of
// the same length element by element, anything else as one value.
func (d *differ) value(path string, t tftypes.Type, before, after tftypes.Value) {
	if d.full() {
		return
	}
	switch {
	case t != nil && (t.Is(tftypes.Map{}) || t.Is(tftypes.Object{})):
		bm, am := fields(before), fields(after)
		if bm == nil && am == nil {
			d.leaf(path, false, before, after)
			return
		}
		for _, k := range unionKeys(bm, am) {
			var kt tftypes.Type
			switch x := t.(type) {
			case tftypes.Map:
				kt = x.ElementType
			case tftypes.Object:
				kt = x.AttributeTypes[k]
			}
			if guarded(k) {
				d.leaf(join(path, k), true, bm[k], am[k])
				continue
			}
			d.value(join(path, k), kt, bm[k], am[k])
		}
	case t != nil && t.Is(tftypes.List{}):
		bl, bok := elements(before)
		al, aok := elements(after)
		if !bok || !aok || len(bl) != len(al) && len(bl) > 0 && len(al) > 0 {
			d.leaf(path, false, before, after)
			return
		}
		et := elementType(t)
		for i := 0; i < max(len(bl), len(al)); i++ {
			d.value(fmt.Sprintf("%s[%d]", path, i), et, at(bl, i), at(al, i))
		}
	default:
		d.leaf(path, false, before, after)
	}
}

// leaf reports one value that differs.
func (d *differ) leaf(path string, redact bool, before, after tftypes.Value) {
	if d.full() {
		return
	}
	bNull, aNull := isNull(before), isNull(after)
	bUnk, aUnk := isUnknown(before), isUnknown(after)
	if bNull && aNull {
		return
	}
	if !bUnk && !aUnk && !bNull && !aNull && before.Equal(after) {
		return
	}
	var bd, ad any
	if !bNull && !bUnk {
		bd = engine.ToData(before)
	}
	if !aNull && !aUnk {
		ad = engine.ToData(after)
	}
	if redact && d.sink != nil {
		d.sink.add(toJSONValue(bd))
		d.sink.add(toJSONValue(ad))
	}
	if !redact && (d.secrets.leaksIn(toJSONValue(bd)) || d.secrets.leaksIn(toJSONValue(ad)) || hasGuardedKey(bd) || hasGuardedKey(ad)) {
		redact = true
	}
	ch := AttrChange{Path: path, AfterUnknown: aUnk}
	if redact {
		ch.BeforeRedacted = !bNull
		ch.AfterRedacted = !aNull && !aUnk
	} else {
		ch.Before, ch.After = bound(toJSONValue(bd), 0), bound(toJSONValue(ad), 0)
	}
	d.out = append(d.out, ch)
}

// toJSONValue normalises a value from engine.ToData to the types the redaction
// works on: json.Number, string, bool, []any and map[string]any.
func toJSONValue(v any) any {
	switch x := v.(type) {
	case []any:
		out := make([]any, len(x))
		for i, e := range x {
			out[i] = toJSONValue(e)
		}
		return out
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, e := range x {
			out[k] = toJSONValue(e)
		}
		return out
	}
	return v
}

// hasGuardedKey reports whether a value holds a map or object with a key that
// looks like a secret, at any depth.
func hasGuardedKey(v any) bool {
	switch x := v.(type) {
	case []any:
		for _, e := range x {
			if hasGuardedKey(e) {
				return true
			}
		}
	case map[string]any:
		for k, e := range x {
			if guarded(k) || hasGuardedKey(e) {
				return true
			}
		}
	}
	return false
}

func blockSensitive(b *schema.Block) bool { return len(sensitiveNames(b)) > 0 }

func (d *differ) subtreeSensitive(attrs []*schema.Attribute) bool {
	return blockSensitive(&schema.Block{Attributes: attrs})
}

// renameHints finds, as the engine does when it plans, a create and a delete of
// the same type and configuration that look like one resource under a new
// address. The hints are not part of a plan file, so they are worked out again
// from the changes in it and the state.
func renameHints(m *model, pf *engine.PlanFile) []string {
	type change struct{ addr, mode, typ, hash string }
	var creates, deletes []change
	for _, sc := range pf.Changes {
		c := change{sc.Addr, sc.Mode, sc.Type, sc.ConfigHash}
		switch engine.Action(sc.Action) {
		case engine.ActionCreate:
			if c.mode == "managed" && c.hash != "" {
				creates = append(creates, c)
			}
		case engine.ActionDelete:
			deletes = append(deletes, c)
		}
	}
	used := map[string]bool{}
	var hints []string
	for _, c := range creates {
		for _, d := range deletes {
			if d.mode != c.mode || d.typ != c.typ || used[d.addr] {
				continue
			}
			if rec := m.store.Get(d.addr); rec != nil && rec.ConfigHash == c.hash {
				used[d.addr] = true
				hints = append(hints, fmt.Sprintf("%s looks like %s under a new address; to keep the existing object, add $from: %q to %s", c.addr, d.addr, d.addr, c.addr))
				break
			}
		}
	}
	return hints
}
