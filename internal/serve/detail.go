package serve

import (
	"fmt"
	"sort"
	"strings"

	"github.com/pravo-systems/pravo/internal/registry"
)

// maxHistory bounds the transactions listed for a node.
const maxHistory = 50

// maxSearch bounds each list of /api/search.
const maxSearch = 50

// detail builds the answer of /api/detail for a node. The values of its record
// are read from the state as it was loaded, rendered against the schema of its
// type, and redacted.
func (m *model) detail(mn *mnode) *Detail { return m.detailFor(mn, m.plan) }

// detailFor is detail for the plan po, which is the one laid over the graph or a
// stored plan that a request names: the action of the node and its Change are
// that plan's.
func (m *model) detailFor(mn *mnode, po *planOverlay) *Detail {
	addr := mn.Addr
	sec := m.secretsFor(po)
	d := &Detail{Version: APIVersion, Node: mn.Node, Attrs: []Attr{}, DependsOn: nonNil(m.deps[addr]), UsedBy: nonNil(m.used[addr])}
	if po != m.plan {
		d.Node.Action = ""
		if po != nil {
			if e := po.entries[addr]; e != nil {
				d.Node.Action = e.action
			}
		}
	}
	d.Provider = m.providerRef(mn)
	d.SchemaKnown = mn.sch != nil || mn.rec == nil || transient(mn.Mode)
	rec := mn.rec
	if rec != nil {
		d.Times = Times{AppliedAt: rec.AppliedAt, RefreshedAt: rec.RefreshedAt, UpdatedAt: &rec.UpdatedAt, DriftedAt: rec.DriftedAt}
		if reach := m.store.Reach(addr); len(reach) > 0 {
			d.Reach = reach
		}
		attrs, err := decodeAttrs(rec.Attributes)
		switch {
		case err != nil:
			d.Notes = append(d.Notes, "The attributes of the record cannot be read: "+err.Error()+".")
		case mn.sch != nil:
			w := &walk{secrets: sec, build: true}
			d.Attrs = w.block(mn.sch.Block, attrs, 0)
			if w.derived > 0 {
				d.Notes = append(d.Notes, fmt.Sprintf("%d %s withheld: %s a sensitive value that the state keeps in another attribute.",
					w.derived, plural(w.derived, "value is", "values are"), plural(w.derived, "it contains", "they contain")))
			}
		default:
			d.Attrs = withholdAll(attrs)
			d.Notes = append(d.Notes, "The schema of this type is not cached, so every value is withheld. Run \"pravo init\" to fetch it.")
		}
	}
	if mn.schNote != "" {
		d.Notes = append(d.Notes, mn.schNote)
	}
	d.Change = m.changeIn(po, mn)
	if d.Change != nil && po != nil {
		if e := po.entries[addr]; e != nil {
			if e.applied {
				from := "plan file"
				if po.info.ID != "" {
					from = "stored plan"
				}
				d.Notes = append(d.Notes, "This change was applied already from the "+from+".")
			}
			if e.moved != "" {
				d.Notes = append(d.Notes, "The plan moves the record here from "+e.moved+".")
			}
			if len(e.conflict) > 0 {
				d.Notes = append(d.Notes, "This change conflicts with the state and is left out of the part of the plan that can be applied: "+
					maskText(sec, strings.Join(e.conflict, "; "), maxReason)+".")
			}
		}
		if d.Change.Attrs == nil && po.changes == nil && d.Change.Action != "no-op" {
			d.Notes = append(d.Notes, "The attribute changes of the plan are not shown; the warnings of the graph say why.")
		}
	}
	for _, t := range m.transactions() {
		if t.Kind == "plan" {
			continue // a plan reads the node, and may refresh its record; it does not write it
		}
		for _, c := range t.Changes {
			if c.Addr != addr {
				continue
			}
			d.History = append(d.History, HistoryEntry{ID: t.ID, Kind: t.Kind, Status: t.Status, Time: t.Started, User: maskWith(sec, t.User), Actor: maskWith(sec, t.Actor),
				Command: scrubCommand(t.Command, sec), Reason: maskWith(sec, t.Reason), Action: c.Action, RevBefore: c.Before, RevAfter: c.After})
			break
		}
		if len(d.History) >= maxHistory {
			break
		}
	}
	if l, ok := m.locks[addr]; ok {
		kind := l.Kind
		if kind == "" {
			kind = "lock"
		}
		d.Lock = &LockInfo{Kind: kind, User: maskWith(sec, l.User), Host: maskWith(sec, l.Host), Command: scrubCommand(l.Command, sec), Actor: maskWith(sec, l.Actor), Since: l.Since, Expires: l.Expires}
		if l.Stale {
			d.Notes = append(d.Notes, "The lock is stale: its holder is gone or has not renewed it, and the next command may take it over.")
		}
	}
	switch {
	case mn.Presence == "state" && m.spec.root != nil:
		d.Notes = append(d.Notes, "Recorded in the state, and the specification no longer declares it: the next apply would destroy it.")
	case mn.Presence == "state":
		d.Notes = append(d.Notes, "Shown from the state alone: the specification does not load.")
	case mn.Presence == "spec":
		d.Notes = append(d.Notes, "Declared in the specification and not created yet.")
	case transient(mn.Mode):
		d.Notes = append(d.Notes, "A "+modeNoun(mn.Mode)+" is evaluated when a command runs and is never recorded in the state.")
	}
	if mn.Gone {
		d.Notes = append(d.Notes, "A read found that the object no longer exists; the record keeps the attributes of the last state that was read.")
	} else if mn.Drifted {
		d.Notes = append(d.Notes, "A read found that the object differs from this record.")
	}
	return d
}

// secretsFor is the set of sensitive strings that a response about the plan po is
// judged against: those of the state and the ones the plan would write. A plan
// that has none of its own (the plan of the graph, which adds its secrets to the
// model's set, or no plan) is judged against the model's.
func (m *model) secretsFor(po *planOverlay) *secretSet {
	if po != nil && po.secrets != nil {
		return po.secrets
	}
	return m.secrets
}

// maskWith masks the sensitive strings of a set in a text.
func maskWith(sec *secretSet, text string) string {
	out, _ := sec.scrub(text)
	return out
}

// scrub masks the sensitive strings of the state in a text that comes from the
// record of a run.
func (m *model) scrub(text string) string {
	out, _ := m.secrets.scrub(text)
	return out
}

func nonNil(l []string) []string {
	if l == nil {
		return []string{}
	}
	return l
}

// providerRef describes the provider instance of a node.
func (m *model) providerRef(mn *mnode) ProviderRef {
	ref := ProviderRef{Key: mn.Provider}
	src := mn.providerSource
	if src == "" && m.spec.root != nil {
		if req, ok := m.spec.root.Providers[configLocal(mn.Provider)]; ok {
			src = req.Source.String()
		}
	}
	if s, err := registry.ParseSource(src); err == nil && src != "" {
		ref.Source = s.Display()
		if mn.providerVersion == "" {
			if e, ok := m.spec.lock.Providers[s.String()]; ok {
				ref.Version = e.Version
			}
		}
	}
	if mn.providerVersion != "" {
		ref.Version = mn.providerVersion
	}
	if mn.rec != nil && mn.rec.ProviderProtocol != "" {
		ref.Protocol = mn.rec.ProviderProtocol
	}
	return ref
}

func configLocal(key string) string {
	if i := strings.IndexByte(key, '.'); i >= 0 {
		return key[:i]
	}
	return key
}

// withholdAll lists the attributes of a record whose schema is not known, with
// every value withheld.
func withholdAll(attrs map[string]any) []Attr {
	names := make([]string, 0, len(attrs))
	for k := range attrs {
		names = append(names, k)
	}
	sort.Strings(names)
	out := make([]Attr, 0, len(names))
	for _, k := range names {
		at := Attr{Name: k}
		if attrs[k] == nil {
			at.Null = true
		} else {
			at.Redacted = "schema"
		}
		out = append(out, at)
	}
	return out
}

// search answers /api/search: the nodes and modules that match every word of
// the query, in their address, type or name.
func (m *model) search(q string) *Search {
	res := &Search{Version: APIVersion, Nodes: []Node{}, Modules: []Module{}}
	words := strings.Fields(strings.ToLower(q))
	if len(words) == 0 {
		return res
	}
	match := func(texts ...string) int {
		score := 0
		for _, w := range words {
			hit := 0
			for i, t := range texts {
				t = strings.ToLower(t)
				switch {
				case t == w:
					hit = max(hit, 4-i)
				case strings.HasPrefix(t, w):
					hit = max(hit, 3-i)
				case strings.Contains(t, w):
					hit = max(hit, 1)
				}
			}
			if hit == 0 {
				return 0
			}
			score += hit
		}
		return score
	}
	type scored struct {
		n     Node
		score int
	}
	var nodes []scored
	for _, a := range m.order {
		n := m.nodes[a].Node
		if s := match(n.Name, n.Addr, n.Type); s > 0 {
			nodes = append(nodes, scored{n, s})
		}
	}
	sort.SliceStable(nodes, func(i, j int) bool { return nodes[i].score > nodes[j].score })
	for i := 0; i < len(nodes) && i < maxSearch; i++ {
		res.Nodes = append(res.Nodes, nodes[i].n)
	}
	for _, p := range m.modList {
		mod := m.modules[p]
		if match(mod.Name, mod.Path) > 0 && len(res.Modules) < maxSearch {
			res.Modules = append(res.Modules, *mod)
		}
	}
	return res
}
