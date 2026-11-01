package engine

import (
	"cuelang.org/go/cue"
)

// TemplateRef names the template instance that a node belongs to: the template, by the
// name in its $template marker, and the address of the instance, the struct that carries
// the marker. A plan document gives it on each change, so that a policy can tell the
// resources of an instance from the rest without matching addresses.
type TemplateRef struct {
	Name     string `json:"name"`
	Instance string `json:"instance"`
}

// markTemplates notes, on each change, the template instance that holds its node: the
// nearest struct above the node that carries $template.name. The instance is found in the
// specification, so a node that the specification no longer declares still belongs to the
// instance that holds its address, while the instance is there. A change of a node outside
// every instance, and the delete of a whole instance, name none.
func (e *Engine) markTemplates(changes []*Change) {
	if e == nil || e.Root == nil || !e.Root.Value.Exists() {
		return
	}
	e.cueMu.Lock()
	defer e.cueMu.Unlock()
	paths := map[string]cue.Path{}
	for _, r := range e.Root.Resources {
		paths[r.Address()] = r.Path
	}
	found := TemplateCache{}
	for _, ch := range changes {
		p, ok := paths[ch.Addr]
		if !ok {
			p = cue.ParsePath(ch.Addr)
			if p.Err() != nil {
				continue
			}
		}
		ch.Template = TemplateOf(e.Root.Value, p, found)
	}
}

// TemplateCache holds the structs of a specification that TemplateOf looked at, by address,
// with nil for a struct that carries no marker. One cache serves one specification.
type TemplateCache map[string]*TemplateRef

// TemplateOf finds the template instance that holds the node at p in the specification spec:
// the nearest struct above p that carries $template.name. It is nil when there is none.
// found may be nil.
func TemplateOf(spec cue.Value, p cue.Path, found TemplateCache) *TemplateRef {
	if found == nil {
		found = TemplateCache{}
	}
	sels := p.Selectors()
	for i := len(sels) - 1; i >= 1; i-- {
		at := cue.MakePath(sels[:i]...)
		key := at.String()
		ref, seen := found[key]
		if !seen {
			ref = nil
			v := spec.LookupPath(at)
			if v.Exists() && v.IncompleteKind() == cue.StructKind {
				if name, err := v.LookupPath(cue.ParsePath("$template.name")).String(); err == nil && name != "" {
					ref = &TemplateRef{Name: name, Instance: key}
				}
			}
			found[key] = ref
		}
		if ref != nil {
			return ref
		}
	}
	return nil
}
