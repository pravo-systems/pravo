package serve

import (
	"encoding/json"
	"net/http"

	"github.com/pravo-systems/pravo/internal/state"
)

// lapseIDs is the set of the runs that are steps of the time lapse, for the entries of a history that link to it.
func (m *model) lapseIDs() map[string]bool {
	ids := map[string]bool{}
	for _, s := range m.timelapse().Steps {
		ids[s.ID] = true
	}
	return ids
}

// detailAt is /api/detail for a node as the run id left it. The record of a node holds its attributes as the newest
// run wrote them; each run kept what it altered (Diff), so the attributes after an earlier run are the record with the
// later runs undone, newest first. What a diff does not hold cannot be told: a value that is or may hold a secret is
// never kept, a change that altered too much keeps none, a sealed state keeps them for its readers, and a run recorded
// before diffs were kept has none. Then no attribute is shown and AsOf says why, and never a value of another time.
func (m *model) detailAt(addr, id string) (*Detail, bool) {
	txns := m.transactions() // newest first
	at := -1
	for i, t := range txns {
		if t.ID == id {
			at = i
			break
		}
	}
	if at < 0 {
		return nil, false
	}
	asOf := &AsOf{ID: id, Kind: txns[at].Kind, Time: txns[at].Started, Known: true}
	mn := m.nodes[addr]
	if mn != nil && mn.isOutput() {
		mn = nil // an output has no record to undo; only the runs that wrote nodes are in the time lapse
	}
	cur := map[string]json.RawMessage{}
	switch {
	case mn == nil:
		asOf.Gone = true
	case mn.Sealed:
		asOf.Known, asOf.Why = false, "The attributes of this record are sealed: "+m.sealedReason()
	case mn.rec != nil:
		cur = topLevelOf(mn.rec.Attributes)
	}
	withheld := m.undoLater(addr, txns[:at], asOf, cur)
	asOf.Withheld = withheld
	var d *Detail
	if mn == nil {
		d = &Detail{Version: APIVersion, Node: Node{Addr: addr}, Attrs: []Attr{}, DependsOn: []string{}, UsedBy: []string{}}
		if asOf.Known && len(cur) > 0 {
			attrs := map[string]any{}
			for k, v := range cur {
				attrs[k] = v
			}
			d.Attrs = withholdAll(attrs)
			d.Notes = append(d.Notes, "A run removed this resource, so the schema of its type is not at hand: the names of its attributes are shown and no value.")
		}
		d.History = m.historyOf(addr, nil, m.judge(nil, addr))
	} else {
		c := *mn
		r := state.Resource{}
		if mn.rec != nil {
			r = *mn.rec
		}
		if asOf.Known {
			raw, _ := json.Marshal(cur)
			r.Attributes = raw
		} else {
			r.Attributes = nil
		}
		c.rec = &r
		d = m.detailFor(&c, nil)
		d.Change, d.Lock, d.Times = nil, nil, Times{}
		d.Notes = nil
		if !asOf.Known {
			d.Attrs = []Attr{}
		}
	}
	d.AsOf = asOf
	return d, true
}

func topLevelOf(raw json.RawMessage) map[string]json.RawMessage {
	var out map[string]json.RawMessage
	if len(raw) == 0 || json.Unmarshal(raw, &out) != nil || out == nil {
		return map[string]json.RawMessage{}
	}
	return out
}

func union(a, b map[string]json.RawMessage) map[string]struct{} {
	out := make(map[string]struct{}, len(a)+len(b))
	for k := range a {
		out[k] = struct{}{}
	}
	for k := range b {
		out[k] = struct{}{}
	}
	return out
}

// detailRequest answers /api/detail: the node as it is, or with at=ID as that run left it.
func (s *Server) detailRequest(w http.ResponseWriter, r *http.Request, m *model) {
	if r.URL.Query().Has("at") {
		s.detailAtRequest(w, r, m)
		return
	}
	if mn := s.node(w, r, m); mn != nil {
		if po, ok := s.planOf(w, r, m); ok {
			writeJSON(w, http.StatusOK, m.detailFor(mn, po))
		}
	}
}

// detailAtRequest answers /api/detail?addr=…&at=ID.
func (s *Server) detailAtRequest(w http.ResponseWriter, r *http.Request, m *model) {
	addr, id := r.URL.Query().Get("addr"), r.URL.Query().Get("at")
	if addr == "" || len(addr) > 2048 || len(id) > 256 {
		writeError(w, http.StatusBadRequest, "addr is required")
		return
	}
	d, ok := m.detailAt(addr, id)
	if !ok {
		writeError(w, http.StatusNotFound, "no such run")
		return
	}
	writeJSON(w, http.StatusOK, d)
}

// undoLater undoes the runs in later (newest first) on cur, the attributes of the node at addr, and says how many attributes
// were left out for want of a kept value. It clears asOf.Known, and says why, when a run's diff cannot be had.
func (m *model) undoLater(addr string, later []state.Transaction, asOf *AsOf, cur map[string]json.RawMessage) int {
	withheld := map[string]bool{}
	for i := 0; i < len(later) && asOf.Known; i++ {
		t := later[i]
		var c *state.Change
		for k := range t.Changes {
			if t.Changes[k].Addr == addr {
				c = &t.Changes[k]
				break
			}
		}
		if c == nil {
			continue
		}
		diffs, sealed := m.store.TxnDiffs(t)
		df, ok := diffs[addr]
		switch {
		case sealed:
			asOf.Known, asOf.Why = false, "A later run kept what it altered sealed, and this viewer holds no key. Start it with --identity to read it."
		case !ok && lapseAction(c.Action) != "":
			asOf.Known, asOf.Why = false, "A later run, "+t.ID+", was recorded before the state kept what a run altered."
		case df.Omitted:
			asOf.Known, asOf.Why = false, "A later run, "+t.ID+", altered too much to keep the values."
		default:
			for k := range df.Withheld {
				delete(cur, k)
				withheld[k] = true
			}
			for k := range union(df.Before, df.After) {
				if _, held := df.Withheld[k]; held {
					continue
				}
				if b, had := df.Before[k]; had {
					cur[k] = b
				} else {
					delete(cur, k)
				}
			}
		}
	}
	return len(withheld)
}
