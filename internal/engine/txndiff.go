package engine

import (
	"encoding/json"
	"sort"
	"strings"

	"github.com/pravo-systems/pravo/internal/schema"
)

// minDiffSecret is the shortest sensitive string that is looked for in other
// attributes, as the viewer does: a shorter one matches too much to say anything.
const minDiffSecret = 6

// recordDiff notes in the transaction what a change did to the attributes of a
// record. The state keeps nothing of past attributes but this, so it keeps no
// value that is or may hold a secret.
func (e *Engine) recordDiff(addr string, sch *schema.Schema, before, after json.RawMessage, derived map[string]bool) {
	if e.txn == nil {
		return
	}
	e.txn.RecordDiff(addr, before, after, diffWithhold(sch, derived, before, after))
}

// diffWithhold decides which attributes of a diff are left out. An attribute is
// withheld when the schema marks it, or anything inside it, sensitive or
// write-only; when its name, or a key inside it, says it is a secret; or when it
// holds a string that a withheld attribute of either record holds. An attribute
// that the plan marks as derived from a withheld one (derived) is withheld too,
// whatever its text. Without a schema every attribute is withheld.
func diffWithhold(sch *schema.Schema, derived map[string]bool, before, after json.RawMessage) func(string, json.RawMessage, json.RawMessage) bool {
	if sch == nil || sch.Block == nil {
		return func(string, json.RawMessage, json.RawMessage) bool { return true }
	}
	names := map[string]bool{}
	for _, a := range sch.Block.Attributes {
		names[a.Name] = attrSensitive(a, 0)
	}
	for _, nb := range sch.Block.BlockTypes {
		names[nb.TypeName] = blockSensitive(nb.Block, 0)
	}
	flagged := func(name string, v json.RawMessage) bool {
		sensitive, known := names[name]
		return !known || sensitive || derived[name] || schema.NameGuard.MatchString(name) || hasGuardedKey(v)
	}
	var secrets []string
	for _, rec := range []json.RawMessage{before, after} {
		var m map[string]json.RawMessage
		if len(rec) == 0 || json.Unmarshal(rec, &m) != nil {
			continue
		}
		for name, v := range m {
			if flagged(name, v) {
				collectStrings(v, &secrets)
			}
		}
	}
	sort.Strings(secrets)
	return func(name string, b, a json.RawMessage) bool {
		if flagged(name, b) || flagged(name, a) {
			return true
		}
		for _, v := range []json.RawMessage{b, a} {
			text := string(v)
			for _, s := range secrets {
				if strings.Contains(text, jsonText(s)) {
					return true
				}
			}
		}
		return false
	}
}

func attrSensitive(a *schema.Attribute, depth int) bool {
	if a.Sensitive || a.WriteOnly {
		return true
	}
	if a.NestedType != nil && depth < 12 {
		for _, n := range a.NestedType.Attributes {
			if attrSensitive(n, depth+1) {
				return true
			}
		}
	}
	return false
}

func blockSensitive(b *schema.Block, depth int) bool {
	if b == nil || depth > 12 {
		return false
	}
	for _, a := range b.Attributes {
		if attrSensitive(a, depth) {
			return true
		}
	}
	for _, nb := range b.BlockTypes {
		if blockSensitive(nb.Block, depth+1) {
			return true
		}
	}
	return false
}

// hasGuardedKey reports whether a JSON value holds an object with a key that
// says it is a secret, at any depth.
func hasGuardedKey(raw json.RawMessage) bool {
	var v any
	if json.Unmarshal(raw, &v) != nil {
		return false
	}
	var walk func(v any) bool
	walk = func(v any) bool {
		switch x := v.(type) {
		case []any:
			for _, e := range x {
				if walk(e) {
					return true
				}
			}
		case map[string]any:
			for k, e := range x {
				if schema.NameGuard.MatchString(k) || walk(e) {
					return true
				}
			}
		}
		return false
	}
	return walk(v)
}

// collectStrings adds the strings of a JSON value that are long enough to say
// something.
func collectStrings(raw json.RawMessage, out *[]string) {
	var v any
	if json.Unmarshal(raw, &v) != nil {
		return
	}
	var walk func(v any)
	walk = func(v any) {
		switch x := v.(type) {
		case string:
			if len(x) >= minDiffSecret && len(x) <= 1<<16 {
				*out = append(*out, x)
			}
		case []any:
			for _, e := range x {
				walk(e)
			}
		case map[string]any:
			for _, e := range x {
				walk(e)
			}
		}
	}
	walk(v)
}

// jsonText is how a string reads inside a JSON document, without its quotes.
func jsonText(s string) string {
	b, _ := json.Marshal(s)
	return string(b[1 : len(b)-1])
}
