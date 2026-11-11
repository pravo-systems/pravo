package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"

	"github.com/pravo-systems/pravo/internal/schema"
	"github.com/pravo-systems/pravo/internal/sensitivity"
	"github.com/pravo-systems/pravo/internal/state"
)

// withheldText stands for a value that --redact withholds.
const withheldText = "(withheld)"

// redactedSnapshot is the snapshot of the state with the values withheld that the viewer
// withholds by what they are: an attribute the provider schema marks sensitive or write-only,
// one whose name says it is a secret, every attribute of a type whose schema is not known,
// and any top-level attribute or output whose text holds a string of six characters or more
// that a sensitive attribute of the state holds (a field built from a password, an echo of it).
// The private blob of a record is dropped. It returns the number of values withheld. It
// works on copies: the store is not changed.
func redactedSnapshot(ctx context.Context, rt *runContext, st *state.Store) (*state.Snapshot, int, error) {
	s, err := newSession(ctx, rt, sessionOptions{schemas: true})
	if err != nil {
		return nil, 0, fmt.Errorf("--redact needs the provider schemas: %w", err)
	}
	defer s.close()
	snap, n := redactSnapshot(st.Snapshot(), func(r *state.Resource) *schema.Schema {
		ps := s.schemas[r.ProviderKey]
		if ps == nil {
			return nil
		}
		switch r.Mode {
		case "data":
			return ps.DataSource(r.Type)
		case "ephemeral":
			return ps.Ephemeral(r.Type)
		}
		return ps.Resource(r.Type)
	})
	return snap, n, nil
}

// redactSnapshot withholds the values of snap; schemaOf gives the schema of a record's type,
// or nil when it is not known.
// The snapshot that is passed is not changed.
func redactSnapshot(snap *state.Snapshot, schemaOf func(*state.Resource) *schema.Schema) (*state.Snapshot, int) {
	type rec struct {
		attrs map[string]json.RawMessage
		by    map[string]bool // withheld by what it is
		all   bool            // the schema is unknown
	}
	recs := map[string]*rec{}
	var taint sensitivity.Taint
	for addr, r := range snap.Resources {
		x := &rec{by: map[string]bool{}}
		recs[addr] = x
		if err := json.Unmarshal(r.Attributes, &x.attrs); err != nil || x.attrs == nil {
			x.attrs = map[string]json.RawMessage{}
		}
		sc := schemaOf(r)
		var top map[string]bool
		if sc == nil || sc.Block == nil {
			x.all = true
		} else {
			top = sensitivity.TopSensitive(sc.Block)
		}
		for name, raw := range x.attrs {
			if x.all || sensitivity.Withheld(name, top[name], false) {
				x.by[name] = true
				for _, x := range madeStrings(raw) {
					taint.Add(x)
				}
			}
		}
	}
	// A value is judged as it is written and as the strings in it read: the JSON of a record
	// writes & as \u0026, and a secret that holds one is in the strings and not in the text.
	judge := func(raw []byte) bool {
		if taint.Hits(string(raw)) {
			return true
		}
		for _, x := range madeStrings(raw) {
			if taint.Hits(x) {
				return true
			}
		}
		return false
	}
	n := 0
	res := make(map[string]*state.Resource, len(snap.Resources))
	for addr, r := range snap.Resources {
		x := recs[addr]
		cp := *r
		cp.Private = nil
		out := map[string]json.RawMessage{}
		for name, raw := range x.attrs {
			if x.by[name] || judge(raw) {
				out[name] = json.RawMessage(`"` + withheldText + `"`)
				n++
			} else {
				out[name] = raw
			}
		}
		cp.Attributes, _ = json.Marshal(out)
		res[addr] = &cp
	}
	outs := map[string]json.RawMessage{}
	for name, raw := range snap.Outputs {
		if sensitivity.Withheld(name, false, false) || judge(raw) {
			outs[name] = json.RawMessage(`"` + withheldText + `"`)
			n++
		} else {
			outs[name] = raw
		}
	}
	cs := *snap
	cs.Resources, cs.Outputs = res, outs
	return &cs, n
}

// madeStrings lists the strings and numbers in a JSON value; Taint ignores the short ones.
func madeStrings(raw json.RawMessage) []string {
	var v any
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if dec.Decode(&v) != nil {
		return nil
	}
	var out []string
	var walk func(any)
	walk = func(v any) {
		switch x := v.(type) {
		case string:
			out = append(out, x)
		case json.Number:
			// a sensitive number, a PIN or a code, is a secret as a string is
			out = append(out, string(x))
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
	return out
}
