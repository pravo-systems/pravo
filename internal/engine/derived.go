package engine

import (
	"cuelang.org/go/cue"

	"github.com/pravo-systems/pravo/internal/sensitivity"
)

// markDerived notes, on each change, the top-level fields whose value is built
// from something that is withheld: an attribute that the schema marks sensitive
// or write-only, one that is named like a secret, a pravo.#Secret, or another field
// that is derived from one. A file whose content embeds a password is as secret as
// the password, and the schema of the file says nothing about it. The viewer works
// it out with the same code (internal/sensitivity); what a person reads in the
// output of a plan then agrees with what the viewer shows.
//
// It reads the references of the specification, never the text of a value. A
// specification that cannot be read for references, or a resource with no schema,
// marks nothing for that resource beyond what its schema says.
func (e *Engine) markDerived(changes []*Change) {
	if e == nil || !e.current.Exists() {
		return
	}
	d := e.derivedFields()
	for _, ch := range changes {
		if f := d.Fields[ch.Addr]; len(f) > 0 {
			ch.Derived = f
		}
	}
}

func (e *Engine) derivedFields() sensitivity.Derived {
	e.cueMu.Lock()
	defer e.cueMu.Unlock()
	addr := func(p cue.Path) string {
		if r, ok := e.byPath[p.String()]; ok {
			return r.Address()
		}
		return ""
	}
	refs := sensitivity.Refs{}
	order := make([]string, 0, len(e.Root.Resources))
	base := map[string]map[string]bool{}
	secret := map[string]bool{}
	for _, r := range e.Root.Resources {
		a := r.Address()
		order = append(order, a)
		if rs := sensitivity.Record(e.current.LookupPath(r.Path), r.Path, e.resolver(), addr, ""); len(rs) > 0 {
			refs[a] = rs
		}
		if r.Builtin && (r.Type == "secret" || r.Type == "secret_env") {
			secret[a] = true
			continue
		}
		if s, err := e.resourceSchema(r); err == nil && s.Block != nil {
			if t := sensitivity.TopSensitive(s.Block); len(t) > 0 {
				base[a] = t
			}
		}
	}
	return sensitivity.Derive(order, refs, sensitivity.NewDerived(), func(task, attr string, so sensitivity.Derived) bool {
		if secret[task] {
			return true
		}
		if attr == "" {
			return len(base[task]) > 0 || len(so.Fields[task]) > 0
		}
		return base[task][attr] || so.Fields[task][attr]
	})
}
