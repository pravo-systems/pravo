package engine

import (
	"encoding/json"
	"sort"
	"strings"

	"github.com/pravo-systems/pravo/internal/schema"
	"github.com/pravo-systems/pravo/internal/sensitivity"
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
		names[a.Name] = sensitivity.AttrHeld(a)
	}
	for _, nb := range sch.Block.BlockTypes {
		names[nb.TypeName] = sensitivity.BlockHeld(nb.Block)
	}
	flagged := func(name string, v json.RawMessage) bool {
		sensitive, known := names[name]
		return !known || sensitive || derived[name] || sensitivity.Withheld(name, false, false) || sensitivity.HasGuardedKeyJSON(v)
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
