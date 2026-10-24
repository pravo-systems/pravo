package serve

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"cuelang.org/go/cue"

	"github.com/pravo-systems/pravo/internal/config"
	"github.com/pravo-systems/pravo/internal/engine"
	"github.com/pravo-systems/pravo/internal/registry"
	"github.com/pravo-systems/pravo/internal/schema"
	"github.com/pravo-systems/pravo/internal/state"
)

// model is everything one revision of the project is: the graph that
// /api/graph sends, and what /api/detail and /api/source need to answer. A model
// is built once for a revision and not changed after, except for the lazily read
// transactions. It holds no lock of the state and writes nothing.
type model struct {
	rev   string
	built time.Time
	srv   *Server
	spec  *specState
	store *state.Store
	root  *config.Root

	nodes      map[string]*mnode
	order      []string
	namespaces map[string]*Namespace
	nsList     []string
	// deps maps a node to the nodes it reads, used to the nodes that read it.
	deps, used map[string][]string
	locks      map[string]state.Lock
	secrets    *secretSet
	plan       *planOverlay
	// stored are the plans in the state, whether or not one of them is the plan.
	stored *storedPlans
	// definitions counts the nodes written by the same lines, by "file:line:col".
	definitions map[string]int
	// resources counts the resources and data sources: the M of "planned N of M",
	// as the engine counts it.
	resources int

	graph     Graph
	graphJSON []byte

	// acc is the protection of the state; access is the document of GET
	// /api/access, accessJSON its bytes.
	acc        accessModel
	access     *Access
	accessJSON []byte

	txOnce sync.Once
	txns   []state.Transaction

	// fieldsOnce builds fieldIdx the first time a field search asks for it.
	fieldsOnce sync.Once
	fieldIdx   *fieldIndex
}

// mnode is a node with what is needed to answer for it.
type mnode struct {
	Node
	rec *state.Resource
	res *config.Resource
	// sch is the schema of the type of the node, nil when it is not at hand.
	sch        *schema.Schema
	schVersion string
	schNote    string
	// provider is the provider as the record or the specification names it.
	providerSource  string
	providerVersion string
	// out is the declaration of an output node, nil for any other node and for an
	// output that is only recorded. output is the output as the graph lists it
	// (Graph.Outputs), and recorded its value as the state holds it, without the
	// mark of a sensitive one.
	out      *config.Output
	output   Output
	recorded json.RawMessage
}

// isOutput reports whether the node is an output of the root.
func (mn *mnode) isOutput() bool { return mn.Mode == "output" }

// transient reports whether a mode is never written to the state.
func transient(mode string) bool {
	return mode == "ephemeral" || mode == "call" || mode == "builtin"
}

// newModel builds the model of one revision from the specification and a state
// that was just read.
func (s *Server) newModel(ctx context.Context, rev string, ss *specState, store *state.Store, root *config.Root) *model {
	began := time.Now()
	m := &model{rev: rev, built: began.UTC(), srv: s, spec: ss, store: store, root: root, nodes: map[string]*mnode{},
		namespaces: map[string]*Namespace{}, deps: map[string][]string{}, used: map[string][]string{}, locks: map[string]state.Lock{},
		secrets: newSecretSet(), definitions: map[string]int{}}
	var warnings []string
	if ss.root == nil {
		warnings = append(warnings, "The specification does not load, so only the state is shown. "+headline(ss.err))
	}
	if locks, err := store.Locks(); err == nil {
		for _, l := range locks {
			m.locks[l.Addr] = l
		}
	} else {
		warnings = append(warnings, "The locks could not be listed: "+err.Error())
	}

	// The protection of the state, and the records that cannot be opened: they are
	// listed without a value, whatever the store returned for them.
	accessPath := s.accessPath()
	m.acc = newAccessModel(s.protection(ctx, store, accessPath), store)

	// The nodes: every declared resource and every record, and the outputs, which
	// are declared, recorded or both.
	recs := map[string]*state.Resource{}
	for _, r := range store.List() {
		if m.acc.sealedRec(r) {
			cp := *r
			cp.Attributes, cp.Private = nil, nil
			r = &cp
		}
		recs[r.Address()] = r
	}
	addrs := make([]string, 0, len(recs)+len(ss.byPath))
	for a := range recs {
		addrs = append(addrs, a)
	}
	for a := range ss.byPath {
		if _, ok := recs[a]; !ok {
			addrs = append(addrs, a)
		}
	}
	// An output has the address of its path, which no resource shares; a record in
	// the state may, and then the record is the node.
	outs := m.outputSet(recs)
	for a := range outs.byAddr {
		addrs = append(addrs, a)
	}
	for _, a := range outs.shadowed {
		warnings = append(warnings, fmt.Sprintf("The output %s has the address of a record in the state, so only the record is shown.", a))
	}
	sort.Strings(addrs)
	missing := map[string]*missingSchema{}
	for _, addr := range addrs {
		if st, ok := outs.byAddr[addr]; ok {
			mn := &mnode{out: ss.outputs[addr]}
			mn.Node = outputNode(addr, st)
			if ss.root != nil {
				mn.Source = ss.refs[addr]
			}
			if mn.Source != nil {
				m.definitions[refKey(mn.Source)]++
			}
			m.nodes[addr] = mn
			m.order = append(m.order, addr)
			m.place(addr, mn)
			continue
		}
		mn := &mnode{rec: recs[addr], res: ss.byPath[addr]}
		mn.Node = m.baseNode(addr, mn)
		if ss.root != nil {
			mn.Source = ss.refs[addr]
		}
		if mn.Source != nil {
			m.definitions[refKey(mn.Source)]++
		}
		m.nodes[addr] = mn
		m.order = append(m.order, addr)
		m.place(addr, mn)
		m.resolveSchema(mn)
		if mn.rec != nil && !transient(mn.Mode) && !mn.Sealed {
			m.countNode(mn, missing)
		}
	}
	m.graph.Outputs = m.outputs(outs)
	m.linkNamespaces()
	m.linkEdges()
	for _, ms := range sortedMissing(missing) {
		warnings = append(warnings, fmt.Sprintf("The schema of provider %s is not cached, so the values of %d %s are withheld. Run \"pravo init\" to fetch it.",
			ms.name, ms.count, plural(ms.count, "resource", "resources")))
	}
	if n := m.sealedCount(); n > 0 {
		warnings = append(warnings, fmt.Sprintf("The state is sealed and %d %s could not be opened, so their values are not shown.", n, plural(n, "record", "records")))
	}
	m.stored = s.loadStored(ctx, m)
	if m.stored.err != nil {
		warnings = append(warnings, "The plans stored in the state could not be listed: "+m.stored.err.Error())
	}
	if w := m.stored.skippedWarning(); w != "" {
		warnings = append(warnings, w)
	}
	m.plan = s.loadPlan(ctx, m)
	if m.plan != nil {
		warnings = append(warnings, m.plan.warnings...)
		m.collectPlanSecrets()
	}
	m.resources = 0
	for _, a := range m.order {
		if mode := m.nodes[a].Mode; mode == "managed" || mode == "data" {
			m.resources++
		}
	}
	m.graph.Version = APIVersion
	m.graph.Revision = rev
	m.graph.Project = m.project()
	// What the specification could not be loaded for, and every other sentence,
	// can quote a value: a tag that was given on the command line, a literal.
	m.graph.Project.SpecError = m.scrubText(m.graph.Project.SpecError)
	for i := range warnings {
		warnings[i] = m.scrubText(warnings[i])
	}
	m.graph.Warnings = warnings
	m.graph.Plan = m.planInfo()
	m.assemble()
	m.graph.Nodes = make([]Node, 0, len(m.order))
	for _, a := range m.order {
		m.graph.Nodes = append(m.graph.Nodes, m.nodes[a].Node)
	}
	m.graph.Edges = m.edgeList()
	m.graph.Namespaces = m.namespaceList()
	m.graphJSON, _ = json.Marshal(&m.graph)
	m.access = m.accessDoc(accessPath)
	m.accessJSON, _ = json.Marshal(m.access)
	return m
}

// sealedCount is the number of records that are sealed and cannot be opened.
func (m *model) sealedCount() int {
	n := 0
	for _, mn := range m.nodes {
		if mn.Sealed {
			n++
		}
	}
	return n
}

// scrubText masks, in a sentence for the reader, the sensitive values of the
// state and the values of the tags that were given on the command line under a
// name that says secret.
func (m *model) scrubText(text string) string { return m.scrubTextWith(m.secrets, text) }

// scrubTextWith is scrubText against another set of secrets: the one of a plan.
func (m *model) scrubTextWith(sec *secretSet, text string) string {
	if text == "" {
		return text
	}
	out, _ := sec.scrub(text)
	for _, t := range m.srv.opts.Tags {
		if k, v, ok := strings.Cut(t, "="); ok && len(v) >= 4 && guarded(k) {
			out = strings.ReplaceAll(out, v, mask)
		}
	}
	return out
}

// headline picks the line of a load error that says what is wrong: the hint
// that pravo adds, else the first line that is more than the name of the
// directory.
func headline(text string) string {
	var first string
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		switch {
		case line == "":
		case strings.HasPrefix(line, "hint:"):
			return clip(strings.TrimSpace(strings.TrimPrefix(line, "hint:")), 240)
		case strings.HasPrefix(line, "loading ") && strings.HasSuffix(line, ":"), strings.HasPrefix(line, "invalid configuration:"), strings.HasPrefix(line, "evaluating "):
		case first == "":
			first = line
		}
	}
	if first == "" {
		first = strings.TrimSpace(text)
	}
	return clip(first, 240)
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

func refKey(r *Ref) string { return fmt.Sprintf("%s:%d:%d", r.File, r.Line, r.Col) }

// baseNode fills the fields of a node that come from the record and the
// declaration.
func (m *model) baseNode(addr string, mn *mnode) Node {
	n := Node{Addr: addr}
	rec, res := mn.rec, mn.res
	switch {
	case res != nil:
		n.Mode, n.Type, n.Name = res.Mode, res.Type, res.Name
		if !res.Builtin {
			n.Provider = res.ProviderKey
		}
	case rec != nil:
		n.Mode, n.Type, n.Name, n.Provider = rec.Mode, rec.Type, rec.Name, rec.ProviderKey
	}
	switch {
	case res != nil && (rec != nil || transient(res.Mode)):
		n.Presence = "both"
	case res != nil:
		n.Presence = "spec"
	default:
		n.Presence = "state"
	}
	if rec != nil {
		n.Revision = rec.Revision
		n.Sealed = m.acc.sealedRec(rec)
		n.RefreshedAt = rec.RefreshedAt
		n.Gone = rec.Gone
		n.Drifted = rec.DriftedAt != nil && !rec.Gone
		mn.providerSource, mn.providerVersion = rec.Provider, rec.ProviderVersion
	}
	if _, ok := m.locks[addr]; ok {
		n.Locked = true
	}
	return n
}

// place puts the node in its namespace and creates the namespaces above it.
func (m *model) place(addr string, mn *mnode) {
	if mn.rec != nil && mn.res == nil && mn.rec.Addr == "" {
		return // written before addresses were paths: type.name is not a path to split
	}
	path := splitAddr(addr)
	if len(path.names) < 2 {
		return
	}
	for i := 1; i < len(path.names); i++ {
		p := path.prefix(i)
		ns := m.namespaces[p]
		if ns == nil {
			ns = &Namespace{Path: p, Name: path.names[i-1], Depth: i - 1}
			if i > 1 {
				ns.Parent = path.prefix(i - 1)
			}
			m.namespaces[p] = ns
			m.nsList = append(m.nsList, p)
		}
		if mn.isOutput() {
			ns.Outputs++
		} else {
			ns.Resources++
		}
	}
	mn.Namespace = path.prefix(len(path.names) - 1)
}

// linkNamespaces gives each namespace the position of its struct and of the definition
// it instantiates.
func (m *model) linkNamespaces() {
	sort.Strings(m.nsList)
	for _, p := range m.nsList {
		ns := m.namespaces[p]
		ns.Declared, ns.Definition = m.spec.namespaceRefs(p)
	}
}

// addrPath is an address split into its labels.
type addrPath struct {
	sels  []cue.Selector
	names []string
}

func (a addrPath) prefix(n int) string { return cue.MakePath(a.sels[:n]...).String() }

// splitAddr splits an address, a CUE path, into its labels. An address that is
// not a path is one label.
func splitAddr(addr string) addrPath {
	p := cue.ParsePath(addr)
	if p.Err() != nil {
		return addrPath{sels: []cue.Selector{cue.Str(addr)}, names: []string{addr}}
	}
	sels := p.Selectors()
	names := make([]string, len(sels))
	for i, s := range sels {
		if s.LabelType() == cue.StringLabel {
			names[i] = s.Unquoted()
		} else {
			names[i] = s.String()
		}
	}
	return addrPath{sels: sels, names: names}
}

// linkEdges derives the edges: from the specification for the nodes it declares
// and from the records for the others. An edge to a node that does not exist is
// left out.
func (m *model) linkEdges() {
	for _, addr := range m.order {
		mn := m.nodes[addr]
		var deps []string
		if m.spec.root != nil && (mn.res != nil || mn.out != nil) {
			deps = m.spec.edges[addr]
		} else if mn.rec != nil {
			deps = mn.rec.Dependencies
		}
		seen := map[string]bool{}
		var list []string
		for _, d := range deps {
			if d == addr || seen[d] || m.nodes[d] == nil {
				continue
			}
			seen[d] = true
			list = append(list, d)
		}
		sort.Strings(list)
		m.deps[addr] = list
		for _, d := range list {
			m.used[d] = append(m.used[d], addr)
		}
	}
}

func (m *model) edgeList() []Edge {
	edges := make([]Edge, 0, len(m.order))
	for _, addr := range m.order {
		for _, d := range m.deps[addr] {
			edges = append(edges, Edge{Dependency: d, Dependent: addr})
		}
	}
	return edges
}

func (m *model) namespaceList() []Namespace {
	out := make([]Namespace, 0, len(m.nsList))
	for _, p := range m.nsList {
		out = append(out, *m.namespaces[p])
	}
	return out
}

// resolveSchema finds the schema of the type of a node in the cache: the one of
// the provider version that wrote the record, else the one the lock file pins.
func (m *model) resolveSchema(mn *mnode) {
	if mn.Mode == "" || transient(mn.Mode) {
		return
	}
	type cand struct {
		src     registry.Source
		version string
	}
	var cands []cand
	if mn.rec != nil && mn.rec.Provider != "" {
		if src, err := registry.ParseSource(mn.rec.Provider); err == nil {
			cands = append(cands, cand{src, mn.rec.ProviderVersion})
		}
	}
	local := config.LocalName(mn.Provider)
	var src registry.Source
	have := false
	if m.spec.root != nil {
		if req, ok := m.spec.root.Providers[local]; ok {
			src, have = req.Source, true
		}
	}
	if !have && mn.rec != nil && mn.rec.Provider != "" {
		if s, err := registry.ParseSource(mn.rec.Provider); err == nil {
			src, have = s, true
		}
	}
	if have {
		if e, ok := m.spec.lock.Providers[src.String()]; ok {
			cands = append(cands, cand{src, e.Version})
		}
		if mn.providerSource == "" {
			mn.providerSource = src.String()
		}
	}
	for i, c := range cands {
		ps, ok := m.srv.lookupSchema(c.src, c.version)
		if !ok {
			continue
		}
		if sch := ps.Type(mn.Mode, mn.Type); sch != nil && sch.Block != nil {
			// The types of the attributes are worked out on first use and kept: work
			// them out now, while one goroutine holds the schema, whatever the Schemas
			// it came from.
			sch.Block.Type()
			mn.sch, mn.schVersion = sch, c.version
			if i > 0 && mn.rec != nil && mn.rec.ProviderVersion != "" && mn.rec.ProviderVersion != c.version {
				mn.schNote = fmt.Sprintf("The schema of %s v%s was used: the record was written by v%s, whose schema is not cached.", c.src.Display(), c.version, mn.rec.ProviderVersion)
			}
			return
		}
	}
	if mn.providerVersion == "" && len(cands) > 0 {
		mn.providerVersion = cands[len(cands)-1].version
	}
	if mn.providerSource == "" && len(cands) > 0 {
		mn.providerSource = cands[0].src.String()
	}
}

// missingSchema counts the nodes whose provider has no cached schema.
type missingSchema struct {
	name  string
	count int
}

func sortedMissing(m map[string]*missingSchema) []*missingSchema {
	out := make([]*missingSchema, 0, len(m))
	for _, v := range m {
		out = append(out, v)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].name < out[j].name })
	return out
}

// countNode walks the record of a node to count the values that are withheld and
// to collect the strings of those values.
func (m *model) countNode(mn *mnode, missing map[string]*missingSchema) {
	attrs, err := decodeAttrs(mn.rec.Attributes)
	if err != nil {
		return
	}
	if mn.sch == nil {
		mn.NoSchema = true
		name := providerName(mn)
		if ms := missing[name]; ms != nil {
			ms.count++
		} else {
			missing[name] = &missingSchema{name: name, count: 1}
		}
		// Nothing says which values are the secrets, so all of them count: a value
		// elsewhere that holds one of these is withheld.
		m.secrets.add(attrs)
		return
	}
	w := &walk{sink: m.secrets}
	w.block(mn.sch.Block, attrs, 0)
	mn.Sensitive = w.withheld
}

func providerName(mn *mnode) string {
	name := mn.providerSource
	if src, err := registry.ParseSource(name); err == nil {
		name = src.Display()
	}
	if name == "" {
		name = mn.Provider
	}
	if mn.providerVersion != "" {
		name += " v" + mn.providerVersion
	}
	return name
}

// outputState says where an output is: declared by the specification, recorded
// by an apply, or both.
type outputState struct{ declared, recorded bool }

// outputSet is the outputs of one revision. shadowed lists the declared outputs
// whose address is the one of a record, which keeps the address.
type outputSet struct {
	byAddr   map[string]outputState
	shadowed []string
}

// outputSet finds the outputs that become nodes: those the specification declares
// and those the state records. recs are the records, by address.
func (m *model) outputSet(recs map[string]*state.Resource) *outputSet {
	set := &outputSet{byAddr: map[string]outputState{}}
	if m.spec.root != nil {
		for name := range m.spec.outputs {
			set.byAddr[name] = outputState{declared: true}
		}
	}
	for name := range m.store.Outputs() {
		st := set.byAddr[name]
		st.recorded = true
		set.byAddr[name] = st
	}
	for name, st := range set.byAddr {
		_, isRec := recs[name]
		if _, isRes := m.spec.byPath[name]; isRec || isRes {
			if st.declared {
				set.shadowed = append(set.shadowed, name)
			}
			delete(set.byAddr, name)
		}
	}
	sort.Strings(set.shadowed)
	return set
}

// outputNode is the node of an output. It has no provider, no record and no lock,
// and its namespace is set by place.
func outputNode(addr string, st outputState) Node {
	n := Node{Addr: addr, Mode: "output", Type: "output", Presence: "state"}
	switch {
	case st.recorded && st.declared:
		n.Presence = "both"
	case st.declared:
		n.Presence = "spec"
	}
	names := splitAddr(addr).names
	n.Name = names[len(names)-1]
	return n
}

// outputs lists the declared and the recorded outputs, and sets what the nodes of
// the outputs hold. A sensitive one has no value, and neither has one that holds a
// sensitive value of the state.
func (m *model) outputs(set *outputSet) []Output {
	names := map[string]bool{}
	declared := map[string]bool{}
	if m.spec.root != nil {
		for _, o := range m.spec.root.Outputs {
			names[o.Name] = true
			declared[o.Name] = o.Sensitive
		}
	}
	recorded := m.store.Outputs()
	for n := range recorded {
		names[n] = true
	}
	if m.acc.locked {
		recorded = nil // sealed: the names stay, no value is read
	}
	list := make([]string, 0, len(names))
	for n := range names {
		list = append(list, n)
	}
	sort.Strings(list)
	// The value of a sensitive output is a secret like those of the records: an
	// attribute that holds it is withheld, whatever its schema says.
	for _, n := range list {
		raw, isRecorded := recorded[n]
		if !isRecorded {
			continue
		}
		val, recSens := engine.UnwrapOutput(raw)
		if !recSens && !declared[n] {
			continue
		}
		if dv, err := decodeAttrs(json.RawMessage(`{"v":` + string(val) + `}`)); err == nil {
			m.secrets.add(dv["v"])
		}
	}
	out := make([]Output, 0, len(list))
	for _, n := range list {
		o := Output{Name: n}
		raw, isRecorded := recorded[n]
		sens := declared[n]
		var val json.RawMessage
		if isRecorded {
			var recSens bool
			val, recSens = engine.UnwrapOutput(raw)
			sens = sens || recSens
		}
		switch {
		case m.acc.locked:
			o.Sensitive, o.Redacted = true, "sealed"
		case sens:
			o.Sensitive, o.Redacted = true, "sensitive"
		case !isRecorded:
			o.Unknown = true
		default:
			dv, err := decodeAttrs(json.RawMessage(`{"v":` + string(val) + `}`))
			if err != nil {
				o.Unknown = true
				break
			}
			if m.secrets.leaksIn(dv["v"]) {
				o.Sensitive, o.Redacted = true, "derived"
			} else {
				o.Value = bound(dv["v"], 0)
			}
		}
		out = append(out, o)
		if mn := m.nodes[n]; mn != nil && mn.isOutput() {
			mn.output = o
			if isRecorded && !o.Sensitive {
				mn.recorded = val
			}
			if o.Sensitive {
				mn.Sensitive = 1
			}
		}
	}
	return out
}

// bound cuts a value down to what a page shows: text of at most maxText bytes
// and collections of at most maxChildren elements.
func bound(v any, depth int) any {
	switch x := v.(type) {
	case string:
		if len(x) > maxText {
			cut := maxText
			for cut > 0 && x[cut]&0xC0 == 0x80 {
				cut--
			}
			return x[:cut] + "…"
		}
	case []any:
		if depth > maxDepth {
			return nil
		}
		n := min(len(x), maxChildren)
		out := make([]any, n)
		for i := 0; i < n; i++ {
			out[i] = bound(x[i], depth+1)
		}
		return out
	case map[string]any:
		if depth > maxDepth {
			return nil
		}
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		out := make(map[string]any, min(len(keys), maxChildren))
		for i, k := range keys {
			if i >= maxChildren {
				break
			}
			out[k] = bound(x[k], depth+1)
		}
		return out
	}
	return v
}

// project describes what is viewed.
func (m *model) project() Project {
	p := Project{Dir: m.srv.opts.Dir, Version: m.srv.opts.Version, LoadedAt: m.built, SpecError: m.spec.err}
	if m.spec.root != nil {
		p.Package = m.spec.root.PkgName
	}
	backend := "local"
	if m.root != nil && m.root.State.Backend != "" {
		backend = m.root.State.Backend
	}
	p.State = StateInfo{Location: m.store.Dir(), Backend: backend, Lineage: m.store.Lineage(), Format: m.store.Format(), Records: m.store.Len(), Locks: len(m.locks), Sealed: m.acc.protected}
	seen := map[string]ProviderInfo{}
	if m.spec.root != nil {
		for local, req := range m.spec.root.Providers {
			ver := ""
			if e, ok := m.spec.lock.Providers[req.Source.String()]; ok {
				ver = e.Version
			}
			seen[local] = ProviderInfo{Local: local, Source: req.Source.Display(), Version: ver}
		}
	}
	for _, addr := range m.order {
		mn := m.nodes[addr]
		if mn.rec == nil || mn.rec.Provider == "" {
			continue
		}
		local := config.LocalName(mn.rec.ProviderKey)
		if _, ok := seen[local]; ok {
			continue
		}
		src := mn.rec.Provider
		if s, err := registry.ParseSource(src); err == nil {
			src = s.Display()
		}
		seen[local] = ProviderInfo{Local: local, Source: src, Version: mn.rec.ProviderVersion}
	}
	// the instances: those that the specification configures and those that a record names
	counts := map[string]int{}
	if m.spec.root != nil {
		for _, d := range m.spec.root.ProviderDecls {
			counts[d.Key] = 0
		}
	}
	for _, addr := range m.order {
		if key := m.nodes[addr].Provider; key != "" {
			counts[key]++
		}
	}
	for key, n := range counts {
		local := config.LocalName(key)
		if pi, ok := seen[local]; ok {
			pi.Instances = append(pi.Instances, ProviderInstance{Key: key, Count: n})
			seen[local] = pi
		}
	}
	for _, pi := range seen {
		sort.Slice(pi.Instances, func(i, j int) bool { return pi.Instances[i].Key < pi.Instances[j].Key })
		p.Providers = append(p.Providers, pi)
	}
	sort.Slice(p.Providers, func(i, j int) bool { return p.Providers[i].Local < p.Providers[j].Local })
	return p
}

// assemble sets the fields that depend on the whole model.
func (m *model) assemble() {
	if m.plan != nil {
		for addr, e := range m.plan.entries {
			if mn := m.nodes[addr]; mn != nil {
				mn.Action = e.action
			}
		}
	}
	m.markNodes()
}

// planInfo returns the plan section of the graph.
func (m *model) planInfo() *PlanInfo {
	if m.plan == nil || m.plan.failed {
		return nil
	}
	info := m.cleanInfo(m.plan)
	info.Total = m.resources
	return &info
}

// transactions returns the recorded transactions, newest first, read once.
func (m *model) transactions() []state.Transaction {
	m.txOnce.Do(func() { m.txns, _ = m.store.Transactions() })
	return m.txns
}
