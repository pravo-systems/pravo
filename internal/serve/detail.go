package serve

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"cuelang.org/go/cue"

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
	if mn.isOutput() {
		return m.outputDetail(mn, po)
	}
	addr := mn.Addr
	sec := m.secretsAt(po, addr)
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
	d.SchemaKnown = mn.sch != nil || mn.rec == nil || transient(mn.Mode) || mn.Sealed
	rec := mn.rec
	if rec != nil {
		d.Times = Times{AppliedAt: rec.AppliedAt, RefreshedAt: rec.RefreshedAt, UpdatedAt: &rec.UpdatedAt, DriftedAt: rec.DriftedAt}
		if reach := m.store.Reach(addr); len(reach) > 0 {
			d.Reach = reach
		}
		attrs, err := decodeAttrs(rec.Attributes)
		switch {
		case mn.Sealed:
			d.Notes = append(d.Notes, "The attributes of this record are sealed, and "+m.sealedReason()+" Resources are listed and no value is shown.")
		case err != nil:
			d.Notes = append(d.Notes, "The attributes of the record cannot be read: "+err.Error()+".")
		case mn.sch != nil:
			w := m.walkFor(po, addr, true)
			d.Attrs = w.block(mn.sch.Block, attrs, 0)
			if w.derived > 0 {
				d.Notes = append(d.Notes, fmt.Sprintf("%d %s withheld: %s a sensitive value of this resource or of one it reads.",
					w.derived, plural(w.derived, "value is", "values are"), plural(w.derived, "it holds", "they hold")))
			}
		default:
			d.Attrs = withholdAll(attrs)
			d.Notes = append(d.Notes, "The schema of this type is not cached, so every value is withheld. Run \"pravo init\" to fetch it.")
		}
	}
	if rec == nil {
		m.unrecorded(mn, d, sec)
	} else if args, unknown, ok := m.specArgs(mn); ok {
		d.SpecKnown = true
		for k := range args {
			d.SpecSet = append(d.SpecSet, k)
		}
		for k := range unknown {
			d.SpecSet = append(d.SpecSet, k)
		}
		sort.Strings(d.SpecSet)
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
		for _, c := range t.Changes {
			if c.Addr != addr {
				continue
			}
			if t.Kind == "plan" && c.Action != "drift" {
				continue // a plan reads the node, and may refresh its record; it does not write it, but a read that found it changed outside pravo is news
			}
			entry := HistoryEntry{ID: t.ID, Kind: t.Kind, Status: t.Status, Time: t.Started, User: t.User, Actor: t.Actor,
				Command: scrubCommand(t.Command), Reason: t.Reason, Action: c.Action, RevBefore: c.Before, RevAfter: c.After,
				Plan: t.PlanID, GitRevision: t.GitRevision}
			if diffs, sealed := m.store.TxnDiffs(t); sealed {
				entry.AttrsSealed = true
			} else if df, ok := diffs[addr]; ok {
				historyAttrs(&entry, df, mn.sch, sec)
			}
			if verify := m.srv.opts.VerifyTxn; verify != nil {
				t := t
				if status, signer := verify(m.store, &t); status != "" {
					entry.Signature, entry.Signer = status, signer
				}
			}
			d.History = append(d.History, entry)
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
		d.Lock = &LockInfo{Kind: kind, User: l.User, Host: l.Host, Command: scrubCommand(l.Command), Actor: l.Actor, Since: l.Since, Expires: l.Expires}
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
		noun := modeNoun(mn.Mode)
		if mn.res != nil && mn.res.Builtin {
			noun = "built-in value"
		}
		d.Notes = append(d.Notes, strings.ToUpper(article(noun)[:1])+article(noun)[1:]+" is evaluated when a command runs and is never recorded in the state.")
	}
	if mn.Gone {
		d.Notes = append(d.Notes, "A read found that the object no longer exists; the record keeps the attributes of the last state that was read.")
	} else if mn.Drifted {
		d.Notes = append(d.Notes, "A read found that the object differs from this record.")
	}
	return d
}

// outputDetail is detail for an output: its value, as one attribute, and its
// description. An output has no record, so no history, lock or times, and no
// schema to be unknown. The value is withheld when the output is sensitive or the
// value holds a sensitive string of the state (or of the plan po): it is the
// recorded value and not the one the specification would give now, and when no
// apply has recorded it, it is not known.
func (m *model) outputDetail(mn *mnode, po *planOverlay) *Detail {
	addr := mn.Addr
	sec := m.secretsAt(po, addr)
	d := &Detail{Version: APIVersion, Node: mn.Node, Attrs: []Attr{}, DependsOn: nonNil(m.deps[addr]), UsedBy: nonNil(m.used[addr]), SchemaKnown: true}
	d.Node.Action = ""
	at := Attr{Name: "value", Type: "output"}
	switch {
	case mn.output.Sensitive:
		at.Sensitive, at.Redacted = true, mn.output.Redacted
	case mn.recorded == nil:
		at.Unknown = true
	default:
		dv, err := decodeAttrs(json.RawMessage(`{"v":` + string(mn.recorded) + `}`))
		switch v := dv["v"]; {
		case err != nil:
			at.Unknown = true
			d.Notes = append(d.Notes, "The recorded value cannot be read: "+err.Error()+".")
		case v == nil:
			at.Null = true
		default:
			w := &walk{secrets: sec, build: true}
			w.free(&at, v, 0, nil)
			if at.Redacted != "" {
				at.Sensitive = true
			}
		}
	}
	d.Attrs = append(d.Attrs, at)
	if text := m.outputDescription(mn); text != "" {
		d.Attrs = append(d.Attrs, Attr{Name: "description", Type: "string", Value: clip(text, maxText)})
	}
	switch {
	case mn.Presence == "state" && m.spec.root != nil:
		d.Notes = append(d.Notes, "Recorded in the state, and the specification no longer declares it: the next apply would remove it.")
	case mn.Presence == "state":
		d.Notes = append(d.Notes, "Shown from the state alone: the specification does not load.")
	case mn.Presence == "spec":
		d.Notes = append(d.Notes, "Declared in the specification and not published yet: no apply has recorded its value.")
	default:
		d.Notes = append(d.Notes, "The value is the one the last apply recorded.")
	}
	return d
}

// outputDescription is the description of a declared output, "" when it has none
// or when it is not a concrete string.
func (m *model) outputDescription(mn *mnode) string {
	if mn.out == nil || m.spec.root == nil {
		return ""
	}
	ss := m.spec
	ss.cueMu.Lock()
	defer ss.cueMu.Unlock()
	v := ss.root.Value.LookupPath(cue.MakePath(append(mn.out.Path.Selectors(), cue.Str("description"))...))
	if !v.Exists() {
		return ""
	}
	text, err := v.String()
	if err != nil {
		return ""
	}
	return text
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

// secretsAt is secretsFor for what a reader of the node at addr can hold: the
// strings found in the node and in what it reads, directly or not.
func (m *model) secretsAt(po *planOverlay, addr string) *secretSet {
	return m.scoped(m.secretsFor(po), addr)
}

// scoped is sec as it applies to the node at addr. An output that the state records
// and the specification does not declare has no references to follow, so what it
// holds is judged against every string; so is a node that reads a data source.
func (m *model) scoped(sec *secretSet, addr string) *secretSet {
	if mn := m.nodes[addr]; mn != nil && mn.isOutput() && mn.out == nil {
		return sec
	}
	if m.readsData(addr) {
		return sec.withMade(m.scopeOf(addr))
	}
	return sec.scopedTo(m.scopeOf(addr))
}

// readsData reports whether the node at addr is, or reads, a data source. A data
// source reads from outside the graph, such as a file that a resource wrote, so
// it can hold any sensitive value without a reference to the node that made it.
func (m *model) readsData(addr string) bool {
	for n := range m.scopeOf(addr) {
		if dn := m.nodes[n]; dn != nil && dn.Mode == "data" {
			return true
		}
	}
	return false
}

// ownText is what a declared node is judged against by text, besides its taint:
// its own strings, or every string when it reads a data source.
func (m *model) ownText(po *planOverlay, addr string) *secretSet {
	if m.readsData(addr) {
		return m.secretsFor(po).withMade(m.scopeOf(addr))
	}
	return m.secretsFor(po).ownTo(addr)
}

// scopeOf is the node at addr and every node it reads, directly or not: the
// nodes whose sensitive values a value of the node may be built from.
func (m *model) scopeOf(addr string) map[string]bool {
	m.scopeMu.Lock()
	defer m.scopeMu.Unlock()
	if sc, ok := m.scopes[addr]; ok {
		return sc
	}
	sc := map[string]bool{addr: true}
	todo := []string{addr}
	for len(todo) > 0 {
		n := todo[len(todo)-1]
		todo = todo[:len(todo)-1]
		for _, d := range m.deps[n] {
			if !sc[d] {
				sc[d] = true
				todo = append(todo, d)
			}
		}
	}
	if m.scopes == nil {
		m.scopes = map[string]map[string]bool{}
	}
	m.scopes[addr] = sc
	return sc
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
// article puts "a" or "an" before a noun.
func article(noun string) string {
	if strings.ContainsRune("aeiou", rune(noun[0])) {
		return "an " + noun
	}
	return "a " + noun
}

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

// search answers /api/search: the nodes and namespaces that match every word of
// the query, in their address, type or name.
func (m *model) search(q string) *Search {
	res := &Search{Version: APIVersion, Nodes: []Node{}, Namespaces: []Namespace{}}
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
	for _, p := range m.nsList {
		ns := m.namespaces[p]
		if match(ns.Name, ns.Path) > 0 && len(res.Namespaces) < maxSearch {
			res.Namespaces = append(res.Namespaces, *ns)
		}
	}
	return res
}
