package engine

import (
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strings"
	"sync"

	"cuelang.org/go/cue"
	"github.com/hashicorp/terraform-plugin-go/tftypes"

	"pravo.dev/pravo/internal/config"
	"pravo.dev/pravo/internal/convert"
	"pravo.dev/pravo/internal/schema"
	"pravo.dev/pravo/internal/state"
)

// A plan does not read the records it does not reach. Three things can leave one
// of them wrong although nothing in its configuration changed, and a plan reads
// the record for any of them, whatever its refresh age: the provider instance
// that manages it points somewhere else now, an apply stopped while it held the
// node, or an apply could not tell what it did to the node and left the record
// with no time of a read.

// providerConfig converts the configuration of the provider instance key against
// the block schema of its provider, with the values the run has filled in so far.
// It returns the converter and the value it converted, which tell what the
// configuration refers to.
func (e *Engine) providerConfig(key string, blk *schema.Block) (tftypes.Value, *convert.Converter, cue.Value, error) {
	e.cueMu.Lock()
	defer e.cueMu.Unlock()
	if err := e.settleLocked(e.providerDeps[key]); err != nil {
		return tftypes.Value{}, nil, cue.Value{}, err
	}
	var cv cue.Value
	if pp, ok := e.Root.ProviderPath(key); ok {
		cv = e.current.LookupPath(pp)
	}
	if !cv.Exists() {
		cv = e.Root.Ctx.CompileString("{}")
	}
	conv := &convert.Converter{Resolve: e.resolver(), IsEphemeral: e.isEphemeral, AllowEphemeral: true}
	cfg := conv.Block("provider "+key, blk, cv)
	return cfg, conv, cv, conv.Err()
}

// target is the digest of the configuration of one provider instance.
type target struct {
	once   sync.Once
	digest string
	ok     bool
}

// providerTarget returns the digest of the part of the configuration of the
// provider instance key that says where it points: the attributes that are not
// sensitive, not unknown and not made of an ephemeral value. A credential that
// rotates changes none of it. It is worked out once per walk of the graph, from
// the configuration and the schema of the provider, without starting the
// provider. The digest is empty when the instance has no such attribute. ok is
// false when the digest cannot be told: the configuration has an unknown value
// or does not convert.
func (e *Engine) providerTarget(key string) (digest string, ok bool) {
	e.mu.Lock()
	t := e.targets[key]
	if t == nil {
		if e.targets == nil {
			e.targets = map[string]*target{}
		}
		t = &target{}
		e.targets[key] = t
	}
	e.mu.Unlock()
	t.once.Do(func() { t.digest, t.ok = e.targetOf(key) })
	return t.digest, t.ok
}

func (e *Engine) targetOf(key string) (string, bool) {
	ps := e.Schemas[config.LocalName(key)]
	if ps == nil || ps.Provider == nil || ps.Provider.Block == nil {
		return "", false
	}
	blk := ps.Provider.Block
	cfg, conv, cv, err := e.providerConfig(key, blk)
	if err != nil {
		return "", false
	}
	skip := map[string]bool{}
	e.cueMu.Lock()
	for _, a := range blk.Attributes {
		if _, ephemeral := conv.EphemeralRef(cv.LookupPath(cue.MakePath(cue.Str(a.Name)))); ephemeral {
			skip[a.Name] = true
		}
	}
	for _, nb := range blk.BlockTypes {
		if _, ephemeral := conv.EphemeralRef(cv.LookupPath(cue.MakePath(cue.Str(nb.TypeName)))); ephemeral {
			skip[nb.TypeName] = true
		}
	}
	e.cueMu.Unlock()
	unknown := false
	body := targetBlock(blk, cfg, skip, &unknown)
	if unknown {
		return "", false
	}
	if len(body) == 0 {
		return "", true
	}
	data, err := json.Marshal(body)
	if err != nil {
		return "", false
	}
	return configHashBytes(data), true
}

// targetBlock returns the part of v, a value of the block blk, that names where
// the provider points. unknown is set when a part that is not left out is not
// known.
func targetBlock(blk *schema.Block, v tftypes.Value, skip map[string]bool, unknown *bool) map[string]any {
	var attrs map[string]tftypes.Value
	if !v.IsKnown() {
		*unknown = true
		return nil
	}
	if v.IsNull() || v.As(&attrs) != nil {
		return nil
	}
	out := map[string]any{}
	for _, a := range blk.Attributes {
		if a.Sensitive || a.WriteOnly || skip[a.Name] {
			continue
		}
		if x := targetAttribute(a, attrs[a.Name], unknown); x != nil {
			out[a.Name] = x
		}
	}
	for _, nb := range blk.BlockTypes {
		if skip[nb.TypeName] {
			continue
		}
		if x := targetNested(nb, attrs[nb.TypeName], unknown); x != nil {
			out[nb.TypeName] = x
		}
	}
	return out
}

func targetAttribute(a *schema.Attribute, v tftypes.Value, unknown *bool) any {
	if !v.IsKnown() {
		*unknown = true
		return nil
	}
	if v.IsNull() {
		return nil
	}
	if a.NestedType != nil {
		return targetObjects(a.NestedType, v, unknown)
	}
	data, err := convert.ToJSON(v)
	if err != nil {
		*unknown = true // a part of the value is not known
		return nil
	}
	return json.RawMessage(data)
}

// targetNested returns the part of a nested block that names where the provider
// points, or nil when there is none.
func targetNested(nb *schema.NestedBlock, v tftypes.Value, unknown *bool) any {
	if !v.IsKnown() {
		*unknown = true
		return nil
	}
	if v.IsNull() {
		return nil
	}
	switch nb.Nesting {
	case schema.NestingList, schema.NestingSet:
		var elems []tftypes.Value
		if v.As(&elems) != nil || len(elems) == 0 {
			return nil
		}
		out := make([]any, len(elems))
		for i, el := range elems {
			out[i] = targetBlock(nb.Block, el, nil, unknown)
		}
		return out
	case schema.NestingMap:
		var elems map[string]tftypes.Value
		if v.As(&elems) != nil || len(elems) == 0 {
			return nil
		}
		out := make(map[string]any, len(elems))
		for k, el := range elems {
			out[k] = targetBlock(nb.Block, el, nil, unknown)
		}
		return out
	}
	if m := targetBlock(nb.Block, v, nil, unknown); len(m) > 0 {
		return m
	}
	return nil
}

// targetObjects is targetNested for an attribute that holds nested objects.
func targetObjects(o *schema.Object, v tftypes.Value, unknown *bool) any {
	switch o.Nesting {
	case schema.NestingList, schema.NestingSet:
		var elems []tftypes.Value
		if v.As(&elems) != nil || len(elems) == 0 {
			return nil
		}
		out := make([]any, len(elems))
		for i, el := range elems {
			out[i] = targetObject(o.Attributes, el, unknown)
		}
		return out
	case schema.NestingMap:
		var elems map[string]tftypes.Value
		if v.As(&elems) != nil || len(elems) == 0 {
			return nil
		}
		out := make(map[string]any, len(elems))
		for k, el := range elems {
			out[k] = targetObject(o.Attributes, el, unknown)
		}
		return out
	}
	if m := targetObject(o.Attributes, v, unknown); len(m) > 0 {
		return m
	}
	return nil
}

func targetObject(attrs []*schema.Attribute, v tftypes.Value, unknown *bool) map[string]any {
	var fields map[string]tftypes.Value
	if !v.IsKnown() {
		*unknown = true
		return nil
	}
	if v.IsNull() || v.As(&fields) != nil {
		return nil
	}
	out := map[string]any{}
	for _, a := range attrs {
		if a.Sensitive || a.WriteOnly {
			continue
		}
		if x := targetAttribute(a, fields[a.Name], unknown); x != nil {
			out[a.Name] = x
		}
	}
	return out
}

// stamp returns the digest to record on a node of the provider instance key: the
// digest of the configuration now, or old when it cannot be told.
func (e *Engine) stamp(key, old string) string {
	if d, ok := e.providerTarget(key); ok {
		return d
	}
	return old
}

// loadSuspects looks for the locks that an apply left behind when it stopped. A
// store that cannot list its locks has none to report: planning does not need to
// write, and the listing is allowed to fail where the plan is not.
func (e *Engine) loadSuspects() {
	e.suspects, _ = e.State.Suspects()
}

// warnSuspects names the nodes with a stale lock, and the nodes that an apply
// left with no time of a read, when the plan does not read anything: the record
// of such a node is what the command that stopped left.
func (e *Engine) warnSuspects() {
	if e.Opts.Refresh != RefreshNone {
		return
	}
	for _, addr := range slices.Sorted(maps.Keys(e.suspects)) {
		if e.State.Get(addr) != nil {
			e.Opts.Progress("Warning: %s: %s; it is not read because -refresh=false was given", addr, stoppedApply(e.suspects[addr]))
		}
	}
	for _, sr := range e.State.List() {
		if _, locked := e.suspects[sr.Address()]; !locked && sr.NoReadTime() {
			e.Opts.Progress("Warning: %s: %s; it is not read because -refresh=false was given", sr.Address(), noReadTime)
		}
	}
}

// noReadTime says why a record that carries no time of a read is read.
const noReadTime = "the record has no time of a read: an apply could not tell what it did to the node"

// stoppedApply says that the apply which took the lock l stopped while it held
// it, and which apply that was.
func stoppedApply(l state.Lock) string {
	var who []string
	if l.Txn != "" {
		who = append(who, "txn "+l.Txn)
	}
	by := l.Holder()
	if l.Host != "" {
		if by != "" {
			by += " "
		}
		by += "on host " + l.Host
	}
	if by != "" {
		who = append(who, by)
	}
	if len(who) == 0 {
		return "an apply stopped while this node was locked"
	}
	return "an apply (" + strings.Join(who, ", ") + ") stopped while this node was locked"
}

// readReason says why the record of a node has to be read whatever its refresh
// age, or returns "" when the age decides. Only the modes that read by age have
// a reason: -refresh=all reads everything and -refresh=false reads nothing.
func (e *Engine) readReason(sr *state.Resource) string {
	if e.Opts.Refresh != RefreshAge && e.Opts.Refresh != RefreshExpired {
		return ""
	}
	if l, ok := e.suspects[sr.Address()]; ok {
		return fmt.Sprintf("%s; its real state is read again whatever the refresh age", stoppedApply(l))
	}
	if sr.NoReadTime() {
		return noReadTime + "; its real state is read again whatever the refresh age"
	}
	if sr.ProviderConfig == "" {
		return ""
	}
	if now, ok := e.providerTarget(sr.ProviderKey); ok && now != sr.ProviderConfig {
		return "the non-sensitive configuration of its provider changed"
	}
	return ""
}
