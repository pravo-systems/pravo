package serve

import (
	"encoding/json"
	"maps"
	"slices"
	"sort"

	"github.com/hashicorp/terraform-plugin-go/tftypes"

	"github.com/pravo-systems/pravo/internal/schema"
	"github.com/pravo-systems/pravo/internal/state"
)

// maxHistoryAttrs bounds the attribute changes sent for one entry of a history.
const maxHistoryAttrs = 100

// historyAttrs fills the attribute changes of an entry from the diff its
// transaction recorded for the node. The values are judged as the record is (j):
// a value that the schema or the name withholds, that reads a withheld attribute,
// or that holds a sensitive string, is reported as changed and not shown. Without
// the schema every value is withheld.
func historyAttrs(e *HistoryEntry, d state.Diff, sch *schema.Schema, j judgement) {
	if d.Omitted {
		e.AttrsOmitted = true
		return
	}
	var out []AttrChange
	if sch == nil || sch.Block == nil {
		for _, k := range diffKeys(d) {
			b, a := d.Before[k], d.After[k]
			out = append(out, AttrChange{Path: k, BeforeRedacted: b != nil, AfterRedacted: a != nil})
		}
	} else {
		before, errB := diffValue(sch, d.Before)
		after, errA := diffValue(sch, d.After)
		if errB != nil || errA != nil {
			e.AttrsOmitted = true
			return
		}
		df := &differ{secrets: j.text, taint: j.fields}
		df.block("", sch.Block, before, after)
		out = df.out
	}
	for _, k := range slices.Sorted(maps.Keys(d.Withheld)) {
		side := d.Withheld[k]
		out = append(out, AttrChange{Path: k, BeforeRedacted: side != "after", AfterRedacted: side != "before"})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	if len(out) > maxHistoryAttrs {
		e.AttrsMore = len(out) - maxHistoryAttrs
		out = out[:maxHistoryAttrs]
	}
	e.Attrs = out
}

// diffValue is the value of a record that holds the attributes of one side of a
// diff and none of the others.
func diffValue(sch *schema.Schema, side map[string]json.RawMessage) (tftypes.Value, error) {
	raw, err := json.Marshal(side)
	if err != nil {
		return tftypes.Value{}, err
	}
	t := sch.Block.Type()
	if len(side) == 0 {
		return tftypes.NewValue(t, nil), nil
	}
	return tftypes.ValueFromJSON(raw, t)
}

func diffKeys(d state.Diff) []string {
	set := map[string]bool{}
	for k := range d.Before {
		set[k] = true
	}
	for k := range d.After {
		set[k] = true
	}
	keys := make([]string, 0, len(set))
	for k := range set {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
