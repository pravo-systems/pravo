package state

import (
	"encoding/json"

	"github.com/pravo-systems/pravo/internal/protect"
)

// MaxDiffBytes bounds the values that one change keeps of the attributes it
// altered. A change that altered more says so (Diff.Omitted) and keeps none.
const MaxDiffBytes = 64 << 10

// Diff is what one change of a transaction did to the attributes of a record:
// for each top-level attribute that differs, the value before and the value
// after, as the record held them. An attribute that the record did not have, or
// held as null, is absent from the side that lacked it. An attribute that is or
// may hold a secret is named in Withheld and its values are not kept. A sealed
// state keeps the rest sealed (Transaction.DiffSealed), and a viewer redacts it
// again against the schema.
type Diff struct {
	Before map[string]json.RawMessage `json:"before,omitempty"`
	After  map[string]json.RawMessage `json:"after,omitempty"`
	// Withheld names the attributes that differ and whose values are not kept
	// because they are or may hold a secret, with the sides that had a value:
	// "before", "after" or "both".
	Withheld map[string]string `json:"withheld,omitempty"`
	Omitted  bool              `json:"omitted,omitempty"`
}

// DiffOf compares the attributes of two records, either of which may be empty
// for a record that did not exist. It returns nil when they say the same. A
// non-nil withhold is asked about each attribute that differs, and says whether
// its values are left out.
func DiffOf(before, after json.RawMessage, withhold func(name string, before, after json.RawMessage) bool) *Diff {
	b, a := topLevel(before), topLevel(after)
	d := &Diff{}
	size := 0
	hold := func(k string, x, y json.RawMessage) bool {
		if withhold == nil || !withhold(k, x, y) {
			return false
		}
		if d.Withheld == nil {
			d.Withheld = map[string]string{}
		}
		switch {
		case x != nil && y != nil:
			d.Withheld[k] = "both"
		case x != nil:
			d.Withheld[k] = "before"
		default:
			d.Withheld[k] = "after"
		}
		return true
	}
	for k, x := range b {
		if y, ok := a[k]; ok && sameJSON(x, y) {
			continue
		}
		if hold(k, x, a[k]) {
			continue
		}
		if d.Before == nil {
			d.Before = map[string]json.RawMessage{}
		}
		d.Before[k] = x
		size += len(k) + len(x)
	}
	for k, y := range a {
		if x, ok := b[k]; ok && sameJSON(x, y) {
			continue
		}
		if _, held := d.Withheld[k]; held || hold(k, b[k], y) {
			continue
		}
		if d.After == nil {
			d.After = map[string]json.RawMessage{}
		}
		d.After[k] = y
		size += len(k) + len(y)
	}
	if d.Before == nil && d.After == nil && d.Withheld == nil {
		return nil
	}
	if size > MaxDiffBytes {
		return &Diff{Omitted: true}
	}
	return d
}

// topLevel splits the attributes of a record into its top-level attributes,
// leaving out those that are null.
func topLevel(raw json.RawMessage) map[string]json.RawMessage {
	var m map[string]json.RawMessage
	if len(raw) == 0 || json.Unmarshal(raw, &m) != nil {
		return nil
	}
	for k, v := range m {
		if string(v) == "null" {
			delete(m, k)
		}
	}
	return m
}

func sameJSON(a, b json.RawMessage) bool {
	var x, y any
	if json.Unmarshal(a, &x) != nil || json.Unmarshal(b, &y) != nil {
		return string(a) == string(b)
	}
	ax, _ := json.Marshal(x)
	by, _ := json.Marshal(y)
	return string(ax) == string(by)
}

// RecordDiff attaches to the latest change of addr what it did to the
// attributes of the record (see DiffOf for withhold). A change with nothing to show gets none.
func (t *Txn) RecordDiff(addr string, before, after json.RawMessage, withhold func(name string, before, after json.RawMessage) bool) {
	d := DiffOf(before, after, withhold)
	if d == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	for i := len(t.t.Changes) - 1; i >= 0; i-- {
		if t.t.Changes[i].Addr == addr {
			t.t.Changes[i].Diff = d
			return
		}
	}
}

// sealDiffs moves the diffs of the changes of t into a box in a sealed state,
// where the attributes of a record are not in the clear either. It reports
// whether it changed t. When the store holds no key the diffs are dropped.
func (s *Store) sealDiffs(t *Transaction) bool {
	has := false
	diffs := map[string]Diff{}
	for i := range t.Changes {
		if d := t.Changes[i].Diff; d != nil {
			has = true
			diffs[t.Changes[i].Addr] = *d
			t.Changes[i].Diff = nil
		}
	}
	if !has {
		return false
	}
	sl := s.Sealer()
	if sl == nil {
		return true
	}
	plain, err := json.Marshal(diffs)
	if err != nil {
		return true
	}
	box, err := sl.Seal(protect.KindDiff, t.ID, plain)
	clear(plain)
	if err == nil {
		t.DiffSealed = &box
	}
	return true
}

// TxnDiffs returns what the changes of t did to the attributes of their
// records, by address. A sealed state keeps them in a box; sealed says that
// there is one and this store cannot open it.
func (s *Store) TxnDiffs(t Transaction) (diffs map[string]Diff, sealed bool) {
	diffs = map[string]Diff{}
	if t.DiffSealed != nil {
		sl := s.Sealer()
		if sl == nil {
			return diffs, true
		}
		plain, err := sl.Open(protect.KindDiff, t.ID, *t.DiffSealed)
		if err != nil {
			return diffs, true
		}
		if json.Unmarshal(plain, &diffs) != nil {
			diffs = map[string]Diff{}
		}
		clear(plain)
	}
	for _, c := range t.Changes {
		if c.Diff != nil {
			diffs[c.Addr] = *c.Diff
		}
	}
	return diffs, false
}
