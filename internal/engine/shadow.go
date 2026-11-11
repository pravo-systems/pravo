package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/hashicorp/terraform-plugin-go/tftypes"

	"github.com/pravo-systems/pravo/internal/convert"
	"github.com/pravo-systems/pravo/internal/plugin"
	"github.com/pravo-systems/pravo/internal/schema"
	"github.com/pravo-systems/pravo/internal/state"
)

// Shadow decompiles a Terraform state into a specification: every value in a state is
// evaluated already, so no HCL is read, and the provider is the oracle that says whether
// the specification restates the object (the plan is empty).

// ShadowStep is one module call on the way to a resource of a Terraform state.
type ShadowStep struct {
	Name  string
	Key   string
	Keyed bool
}

// ShadowInput is one converted record of a Terraform state and where it sat there.
type ShadowInput struct {
	Record  *state.Resource
	Module  []ShadowStep
	Name    string
	Key     string
	Keyed   bool
	Address string // the Terraform address of the instance
}

// ShadowOptions says how a state is decompiled.
type ShadowOptions struct {
	// Repair asks the provider about every resource and changes the specification
	// until the provider plans no change.
	Repair bool
	// Minimise leaves out what the provider says it derives from the rest.
	Minimise bool
	// Oracle asks the provider for its verdict without repairing (the first plan).
	Oracle bool
	// Refresh reads every object before it is rendered, as the first plan does.
	Refresh bool
	// Reserved are the names that the package has at its top level already.
	Reserved []string
	// Flat writes every resource at the top level instead of in a struct per module.
	Flat bool
	// Vault says that the caller keeps the sensitive values in the vault of a sealed state,
	// under the names of ShadowSecret, and the comments of the specification say so.
	Vault bool
	// NoReferences writes every value as a literal.
	NoReferences bool
}

// ShadowNode is what happened to one resource.
type ShadowNode struct {
	Address   string   `json:"address"`
	From      string   `json:"from"`
	Type      string   `json:"type"`
	First     string   `json:"first"`
	Final     string   `json:"final"`
	Repairs   []string `json:"repairs,omitempty"`
	Sensitive []string `json:"sensitive,omitempty"`
	Drifted   bool     `json:"drifted,omitempty"`
}

// ShadowEdges counts where the dependencies of the state went.
type ShadowEdges struct {
	State         int `json:"state"`
	Reference     int `json:"reference"`
	Interpolation int `json:"interpolation"`
	Implied       int `json:"implied"`
	DependsOn     int `json:"depends_on"`
	Data          int `json:"to_data_sources"`
	Lost          int `json:"lost"`
}

// ShadowReport is the numbers of a decompilation.
type ShadowReport struct {
	Resources          int            `json:"resources"`
	DataSources        int            `json:"data_sources_dropped"`
	Modules            int            `json:"modules"`
	Edges              ShadowEdges    `json:"edges"`
	SensitiveOmitted   int            `json:"sensitive_omitted"`
	SensitiveReferred  int            `json:"sensitive_by_reference"`
	SensitiveComputed  int            `json:"sensitive_computed_in_state"`
	SensitiveRequired  int            `json:"sensitive_required_without_source"`
	FirstClean         int            `json:"first_clean"`
	FirstInvalid       int            `json:"first_invalid"`
	FirstChanges       int            `json:"first_changes"`
	FinalClean         int            `json:"final_clean"`
	FinalInvalid       int            `json:"final_invalid"`
	FinalChanges       int            `json:"final_changes"`
	Repairs            map[string]int `json:"repairs"`
	OracleCalls        int            `json:"oracle_calls"`
	Drifted            int            `json:"drifted"`
	Lines              int            `json:"lines"`
	References         int            `json:"reference_sites"`
	Interpolations     int            `json:"interpolation_sites"`
	Nodes              []ShadowNode   `json:"nodes"`
	Notes              []string       `json:"notes,omitempty"`
	AttributesWritten  int            `json:"attributes_written"`
	AttributesLeftOut  int            `json:"attributes_left_out"`
	IgnoredAttributes  int            `json:"ignore_changes_entries"`
	OracleAsked        bool           `json:"oracle_asked"`
	RepairAsked        bool           `json:"repair_asked"`
	MinimiseAsked      bool           `json:"minimise_asked"`
	ReferencesDisabled bool           `json:"references_disabled,omitempty"`
	// Verified says that the plan of the written specification was empty; PlanWants lists
	// what it wanted to change otherwise. They are set by the command that plans it.
	Verified bool `json:"verified"`
	// Folded counts the definitions that fold alike resources (pravo shadow --fold), and
	// FoldedResources the resources that became instances of them.
	Folded          int      `json:"folded_definitions,omitempty"`
	FoldedResources int      `json:"folded_resources,omitempty"`
	PlanWants       []string `json:"plan_wants,omitempty"`
}

// ShadowResult is a decompiled state: the source of the resources, without a package
// clause, and the records at the addresses that the source gives them.
type ShadowResult struct {
	Source  string
	Records []*state.Resource
	Report  ShadowReport
	// Secrets are the sensitive string values that the specification leaves out, for a
	// caller that can keep them in the vault of a sealed state.
	Secrets []ShadowSecret
}

// ShadowSecret is a sensitive value of the state that the specification never holds.
type ShadowSecret struct {
	// Address is the address of the resource in this root, and Attribute the attribute.
	Address, Attribute string
	Value              string
	// Name is the name of the secret that holds it: shadow/ADDRESS/ATTRIBUTE.
	Name string
}

// shadowSecretName is the name of the secret that keeps a sensitive value of a shadow. An
// address has brackets and quotes that a name does not allow: each becomes an underscore.
func shadowSecretName(addr, attr string) string {
	word := func(s string) string {
		b := []byte(s)
		for i, c := range b {
			if (c < 'a' || c > 'z') && (c < 'A' || c > 'Z') && (c < '0' || c > '9') && c != '.' && c != '-' && c != '_' {
				b[i] = '_'
			}
		}
		return strings.TrimLeft(string(b), "._-")
	}
	return "shadow/" + word(addr) + "/" + word(attr)
}

type shadowNode struct {
	in    ShadowInput
	path  []string
	addr  string
	sch   *schema.Schema
	prior tftypes.Value
	pm    map[string]tftypes.Value
	// omit names the top-level attributes and blocks that the specification leaves out.
	omit map[string]bool
	// ignore names the top-level attributes in $lifecycle.ignore_changes.
	ignore map[string]bool
	// why holds a comment per left-out or ignored attribute.
	why    map[string]string
	deps   []*shadowNode
	depSet map[*shadowNode]bool
	// groups holds the dependencies as the state writes them: one entry per resource of
	// the configuration, whatever number of instances it has.
	groups    map[string][]*shadowNode
	dataEdges map[string]bool
	lost      int
	exact     map[string]shadowCandidate
	alts      map[string][]shadowCandidate
	subs      []shadowCandidate
	refTo     map[*shadowNode]bool
	interTo   map[*shadowNode]bool
	explicit  []*shadowNode
	out       ShadowNode
	private   []byte
	// sensitiveKept holds the paths of sensitive values that a reference restates.
	sensitiveKept map[string]bool
	// pre names what was left out before the provider was asked, on a guess that the
	// plan confirms or undoes.
	pre map[string]bool
	// placeholder names the sensitive attributes that the provider wants set: the
	// source holds a placeholder, and ignore_changes keeps what the object has.
	placeholder map[string]bool
}

// ShadowPlaceholder stands where a sensitive value would be, when the provider refuses
// the resource without the attribute.
const ShadowPlaceholder = "kept-in-the-state-by-pravo-shadow"

type shadowCandidate struct {
	value string
	node  *shadowNode
	attr  string
	rank  int
}

type shadowGen struct {
	e     *Engine
	ctx   context.Context
	opts  ShadowOptions
	nodes []*shadowNode
	by    map[string]*shadowNode // legacy address -> node
	rep   *ShadowReport
	// secrets collects the sensitive values that the specification leaves out.
	secrets     []ShadowSecret
	secretNames map[string]bool
	provs       map[string]plugin.Provider
	ids         map[string]*shadowNode // the id and the arn of every object
	// literal is set while the name that a resource gives itself is written: resources
	// that share a name share a variable, not a reference.
	literal bool
	// hint is the name of the attribute that is being written.
	hint string
}

// Shadow decompiles the records of a converted Terraform state.
func (e *Engine) Shadow(ctx context.Context, in []ShadowInput, opts ShadowOptions) (*ShadowResult, error) {
	g := &shadowGen{e: e, ctx: ctx, opts: opts, by: map[string]*shadowNode{}, provs: map[string]plugin.Provider{}}
	g.rep = &ShadowReport{Repairs: map[string]int{}, OracleAsked: opts.Oracle || opts.Repair, RepairAsked: opts.Repair, MinimiseAsked: opts.Minimise, ReferencesDisabled: opts.NoReferences}
	if err := g.collect(in); err != nil {
		return nil, err
	}
	g.name()
	if err := g.load(); err != nil {
		return nil, err
	}
	for _, n := range g.nodes {
		g.candidates(n)
	}
	// What the rules of an adoption leave out, before the provider is asked anything.
	for _, n := range g.nodes {
		n.out = ShadowNode{Address: n.addr, From: n.in.Address, Type: n.in.Record.Type, Drifted: n.out.Drifted}
		g.sensitive(n)
	}
	if opts.Oracle || opts.Repair {
		if err := g.interrogate(); err != nil {
			return nil, err
		}
	}
	return g.finish()
}

// collect makes a node of every managed resource and links the dependencies that the
// state lists: to nodes, to data sources (which are dropped) or nowhere.
func (g *shadowGen) collect(in []ShadowInput) error {
	dataAddrs := map[string]string{}
	keyOf := func(x ShadowInput) string {
		var b strings.Builder
		for _, st := range x.Module {
			b.WriteString("module." + st.Name + ".")
		}
		return b.String() + x.Record.Mode + "." + x.Record.Type + "." + x.Name
	}
	groupKey := map[*shadowNode]string{}
	for _, x := range in {
		if x.Record.Mode != "managed" {
			g.rep.DataSources++
			dataAddrs[x.Record.Address()] = keyOf(x)
			continue
		}
		ps := g.e.Schemas[providerLocal(x.Record.ProviderKey)]
		if ps == nil {
			return fmt.Errorf("%s: no schema for provider %s: declare it in the specification and run pravo init", x.Address, x.Record.ProviderKey)
		}
		s := ps.Resource(x.Record.Type)
		if s == nil {
			return fmt.Errorf("%s: provider %s has no resource type %s", x.Address, x.Record.ProviderKey, x.Record.Type)
		}
		n := &shadowNode{in: x, sch: s, omit: map[string]bool{}, ignore: map[string]bool{}, why: map[string]string{}, depSet: map[*shadowNode]bool{},
			refTo: map[*shadowNode]bool{}, interTo: map[*shadowNode]bool{}, sensitiveKept: map[string]bool{}, pre: map[string]bool{}, private: x.Record.Private}
		g.nodes = append(g.nodes, n)
		g.by[x.Record.Address()] = n
		groupKey[n] = keyOf(x)
	}
	g.rep.Resources = len(g.nodes)
	for _, n := range g.nodes {
		n.groups, n.dataEdges = map[string][]*shadowNode{}, map[string]bool{}
		for _, d := range n.in.Record.Dependencies {
			switch dn := g.by[d]; {
			case dn != nil && dn != n:
				if !n.depSet[dn] {
					n.depSet[dn] = true
					n.deps = append(n.deps, dn)
					n.groups[groupKey[dn]] = append(n.groups[groupKey[dn]], dn)
				}
			case dn == n:
			case dataAddrs[d] != "":
				n.dataEdges[dataAddrs[d]] = true
			default:
				n.lost++
			}
		}
	}
	return nil
}

// load gives every node the value it is rendered from: the record, or the object that
// the provider reads when it is asked.
func (g *shadowGen) load() error {
	opts := g.opts
	needProvider := opts.Oracle || opts.Repair || opts.Minimise || opts.Refresh
	for _, n := range g.nodes {
		var prior tftypes.Value
		var err error
		if needProvider {
			prov, perr := g.provider(n.in.Record.ProviderKey)
			if perr != nil {
				return perr
			}
			prior, err = g.e.upgrade(g.ctx, prov, n.in.Record, n.sch, n.in.Address)
			if err == nil && opts.Refresh {
				prior, err = g.readObject(prov, n, prior)
			}
		} else {
			prior, err = storedValue(n.in.Record, n.sch)
		}
		if err != nil {
			return fmt.Errorf("%s: %w", n.in.Address, err)
		}
		n.prior = prior
		if err := prior.As(&n.pm); err != nil {
			return fmt.Errorf("%s: %w", n.in.Address, err)
		}
	}
	return nil
}

// readObject reads the object of a node and returns what it is now. An object that is
// gone is rendered from the record.
func (g *shadowGen) readObject(prov plugin.Provider, n *shadowNode, prior tftypes.Value) (tftypes.Value, error) {
	read, private, diags, err := prov.ReadResource(g.ctx, n.in.Record.Type, prior, n.private)
	switch {
	case err != nil:
		return prior, err
	case diags.Err() != nil:
		return prior, diags.Err()
	case read.IsNull():
		g.rep.Notes = append(g.rep.Notes, n.in.Address+": the object is gone; rendered from the state")
		return prior, nil
	}
	if !read.Equal(prior) {
		n.out.Drifted = true
		g.rep.Drifted++
	}
	n.private = private
	return read, nil
}

// interrogate asks the provider about every resource, repairs and minimises what it
// objects to or derives, and counts the verdicts.
func (g *shadowGen) interrogate() error {
	rep := g.rep
	tally := func(v shadowVerdict, clean, invalid, changes *int) {
		switch {
		case v.clean():
			*clean++
		case len(v.invalid) > 0:
			*invalid++
		default:
			*changes++
		}
	}
	for _, n := range g.nodes {
		v, err := g.ask(n)
		if err == nil {
			n.out.First = v.String()
			tally(v, &rep.FirstClean, &rep.FirstInvalid, &rep.FirstChanges)
			v, err = g.settle(n, v)
		}
		if err != nil {
			return fmt.Errorf("%s: %w", n.in.Address, err)
		}
		n.out.Final = v.String()
		tally(v, &rep.FinalClean, &rep.FinalInvalid, &rep.FinalChanges)
	}
	return nil
}

// settle repairs the specification of n and then minimises it, as far as the options say.
func (g *shadowGen) settle(n *shadowNode, v shadowVerdict) (shadowVerdict, error) {
	var err error
	if g.opts.Repair {
		if g.foreign(n) > 0 {
			if v, err = g.ask(n); err != nil {
				return v, err
			}
		}
		if !v.clean() {
			if v, err = g.repair(n, v); err != nil {
				return v, err
			}
		}
	}
	if g.opts.Minimise && v.clean() {
		return g.minimise(n)
	}
	return v, nil
}

// finish writes the source and the records, and counts where the edges of the state went.
func (g *shadowGen) finish() (*ShadowResult, error) {
	rep := g.rep
	res := &ShadowResult{}
	res.Source = g.render()
	rep.Lines = strings.Count(res.Source, "\n")
	// The edges are counted once the source is written: a reference is an edge only
	// where the source has it.
	for _, n := range g.nodes {
		g.countEdges(n)
		rep.IgnoredAttributes += len(n.ignore)
		rep.Nodes = append(rep.Nodes, n.out)
	}
	opts := g.opts
	refreshed := opts.Oracle || opts.Repair || opts.Minimise || opts.Refresh
	for _, n := range g.nodes {
		sr := *n.in.Record
		sr.Addr, sr.Name = n.addr, n.path[len(n.path)-1]
		// The record holds what the provider said last, upgraded to the schema of this
		// provider: the first plan then finds nothing that changed behind its back.
		if refreshed {
			attrs, err := convert.ToJSON(n.prior)
			if err != nil {
				return nil, fmt.Errorf("%s: %w", n.in.Address, err)
			}
			sr.Attributes, sr.Private, sr.SchemaVersion = attrs, n.private, n.sch.Version
		}
		var deps []string
		for _, d := range n.deps {
			if n.refTo[d] || n.interTo[d] || n.isExplicit(d) {
				deps = append(deps, d.addr)
			}
		}
		sort.Strings(deps)
		sr.Dependencies = deps
		res.Records = append(res.Records, &sr)
	}
	res.Report = *rep
	res.Secrets = g.secrets
	return res, nil
}

// countEdges adds the dependencies of n to the report, by how the source has them.
func (g *shadowGen) countEdges(n *shadowNode) {
	rep := g.rep
	rep.Edges.State += len(n.groups) + len(n.dataEdges) + n.lost
	rep.Edges.Data += len(n.dataEdges)
	rep.Edges.Lost += n.lost
	for _, ds := range n.groups {
		ref, inter, explicit := false, false, false
		for _, d := range ds {
			ref = ref || n.refTo[d]
			inter = inter || n.interTo[d]
			explicit = explicit || n.isExplicit(d)
		}
		switch {
		case ref:
			rep.Edges.Reference++
		case inter:
			rep.Edges.Interpolation++
		case explicit:
			rep.Edges.DependsOn++
		default:
			rep.Edges.Implied++
		}
	}
}

func (n *shadowNode) isExplicit(d *shadowNode) bool {
	for _, x := range n.explicit {
		if x == d {
			return true
		}
	}
	return false
}

func (g *shadowGen) provider(key string) (plugin.Provider, error) {
	if p := g.provs[key]; p != nil {
		return p, nil
	}
	done, err := g.e.prepareProvider(g.ctx, key)
	if err != nil {
		return nil, err
	}
	_ = done // the providers stay up until the engine closes
	p, err := g.e.Providers.Provider(g.ctx, key)
	if err != nil {
		return nil, err
	}
	g.provs[key] = p
	return p, nil
}

// --- names -------------------------------------------------------------------

var shadowPredeclared = map[string]bool{
	"package": true, "import": true, "for": true, "in": true, "if": true, "let": true, "true": true, "false": true, "null": true,
	"string": true, "int": true, "bool": true, "bytes": true, "number": true, "float": true, "len": true, "close": true,
	"and": true, "or": true, "div": true, "mod": true, "quo": true, "rem": true, "uint": true, "rune": true,
	"int8": true, "int16": true, "int32": true, "int64": true, "int128": true, "uint8": true, "uint16": true, "uint32": true, "uint64": true, "uint128": true,
	"float32": true, "float64": true,
}

func shadowIdent(s string) string {
	var b strings.Builder
	for i, c := range s {
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c == '_', c >= '0' && c <= '9' && i > 0:
			b.WriteRune(c)
		case c >= '0' && c <= '9':
			b.WriteString("n" + string(c))
		default:
			b.WriteByte('_')
		}
	}
	out := strings.Trim(b.String(), "_")
	if out == "" {
		out = "x"
	}
	if shadowPredeclared[out] {
		out += "_"
	}
	return out
}

func shadowTypeShort(typ string) string {
	if i := strings.IndexByte(typ, '_'); i > 0 {
		return typ[i+1:]
	}
	return typ
}

// shadowGroup is the resources of one module instance, and the structs of the module
// instances inside it.
type shadowGroup struct {
	path  []string
	nodes []*shadowNode
	kids  map[string]bool
}

// groupByModule sorts the nodes into the module instances they sat in, and counts the
// instances. order lists the keys of the groups, the top level first.
func (g *shadowGen) groupByModule() (groups map[string]*shadowGroup, order []string) {
	groups = map[string]*shadowGroup{}
	get := func(path []string) *shadowGroup {
		k := strings.Join(path, ".")
		if groups[k] == nil {
			groups[k] = &shadowGroup{path: append([]string(nil), path...), kids: map[string]bool{}}
			order = append(order, k)
		}
		return groups[k]
	}
	get(nil)
	modules := map[string]bool{}
	for _, n := range g.nodes {
		var path []string
		if !g.opts.Flat {
			for _, st := range n.in.Module {
				label := st.Name
				if st.Keyed {
					label += "_" + st.Key
				}
				label = shadowIdent(label)
				get(path).kids[label] = true
				path = append(path, label)
				modules[strings.Join(path, ".")] = true
			}
		}
		gr := get(path)
		gr.nodes = append(gr.nodes, n)
	}
	g.rep.Modules = len(modules)

	return groups, order
}

// innerNames lists the names of the attributes of the types in use: a name at the top
// level that is also the name of a field further in would be hidden from a reference
// there.
func (g *shadowGen) innerNames() map[string]bool {
	inner := map[string]bool{}
	var walk func(b *schema.Block)
	walk = func(b *schema.Block) {
		for _, a := range b.Attributes {
			inner[a.Name] = true
			if a.NestedType != nil {
				for _, na := range a.NestedType.Attributes {
					inner[na.Name] = true
				}
			}
		}
		for _, nb := range b.BlockTypes {
			inner[nb.TypeName] = true
			walk(nb.Block)
		}
	}
	seenType := map[string]bool{}
	for _, n := range g.nodes {
		if !seenType[n.in.Record.Type] {
			seenType[n.in.Record.Type] = true
			walk(n.sch.Block)
		}
	}
	return inner
}

// labelGroup names the resources of a group: by the name they have, with the type in
// front where two types share it and the key where an instance has one.
func labelGroup(gr *shadowGroup) map[*shadowNode]string {
	// How many types use a name, and how many instances a resource has.
	types := map[string]map[string]bool{}
	insts := map[string]int{}
	for _, n := range gr.nodes {
		if types[n.in.Name] == nil {
			types[n.in.Name] = map[string]bool{}
		}
		types[n.in.Name][n.in.Record.Type] = true
		insts[n.in.Record.Type+"."+n.in.Name]++
	}
	labels := map[*shadowNode]string{}
	used := map[string]int{}
	for kid := range gr.kids {
		used[kid]++
	}
	label := func(n *shadowNode, qualify bool) string {
		base := n.in.Name
		if qualify || len(types[n.in.Name]) > 1 {
			if n.in.Name == "this" {
				base = shadowTypeShort(n.in.Record.Type)
			} else {
				base = shadowTypeShort(n.in.Record.Type) + "_" + n.in.Name
			}
		}
		if n.in.Keyed && (insts[n.in.Record.Type+"."+n.in.Name] > 1 || n.in.Key != "0") {
			base += "_" + n.in.Key
		}
		return shadowIdent(base)
	}
	for _, n := range gr.nodes {
		labels[n] = label(n, false)
		used[labels[n]]++
	}
	for _, n := range gr.nodes {
		if used[labels[n]] > 1 {
			used[labels[n]]--
			labels[n] = label(n, true)
			used[labels[n]]++
		}
	}
	taken := map[string]bool{}
	for kid := range gr.kids {
		taken[kid] = true
	}
	sort.Slice(gr.nodes, func(i, j int) bool { return gr.nodes[i].in.Address < gr.nodes[j].in.Address })
	for _, n := range gr.nodes {
		l := labels[n]
		for i := 2; taken[l]; i++ {
			l = fmt.Sprintf("%s_%d", labels[n], i)
		}
		taken[l] = true
		labels[n] = l
	}
	return labels
}

// name gives every resource its place in the specification: a struct per module
// instance, and in it a field per resource.
func (g *shadowGen) name() {
	groups, order := g.groupByModule()
	inner := g.innerNames()
	reserved := map[string]bool{}
	for _, r := range g.opts.Reserved {
		reserved[r] = true
	}
	sort.Strings(order)
	labelsOf := map[string]map[*shadowNode]string{}
	for _, k := range order {
		labelsOf[k] = labelGroup(groups[k])
	}

	// The labels at the top level are the first step of every reference.
	rename := map[string]string{}
	top := groups[""]
	takenTop := map[string]bool{}
	for kid := range top.kids {
		takenTop[kid] = true
	}
	for _, l := range labelsOf[""] {
		takenTop[l] = true
	}
	innerLabels := map[string]bool{}
	for k, labels := range labelsOf {
		if k == "" {
			continue
		}
		for _, l := range labels {
			innerLabels[l] = true
		}
		for kid := range groups[k].kids {
			innerLabels[kid] = true
		}
	}
	fix := func(l, alt string) string {
		if !inner[l] && !innerLabels[l] && !reserved[l] {
			return l
		}
		c := alt
		for i := 2; takenTop[c] || inner[c] || innerLabels[c] || reserved[c]; i++ {
			c = fmt.Sprintf("%s_%d", alt, i)
		}
		takenTop[c] = true
		return c
	}
	for kid := range top.kids {
		if f := fix(kid, kid+"_module"); f != kid {
			rename[kid] = f
		}
	}
	for n, l := range labelsOf[""] {
		labelsOf[""][n] = fix(l, shadowIdent(shadowTypeShort(n.in.Record.Type)+"_"+l))
	}
	for _, k := range order {
		gr := groups[k]
		for _, n := range gr.nodes {
			path := append([]string(nil), gr.path...)
			if len(path) > 0 {
				if r, ok := rename[path[0]]; ok {
					path[0] = r
				}
			}
			n.path = append(append([]string(nil), path...), labelsOf[k][n])
			n.addr = strings.Join(n.path, ".")
		}
	}
}

// --- references --------------------------------------------------------------

func shadowString(v tftypes.Value) (string, bool) {
	if v.IsNull() || !v.IsKnown() || !v.Type().Is(tftypes.String) {
		return "", false
	}
	var s string
	if v.As(&s) != nil {
		return "", false
	}
	return s, true
}

// candidates lists the values of the dependencies of n that a string of n may restate.
func (g *shadowGen) candidates(n *shadowNode) {
	n.exact = map[string]shadowCandidate{}
	if g.opts.NoReferences {
		return
	}
	byValue := map[string][]shadowCandidate{}
	for _, d := range n.deps {
		sh := shape{attrs: d.sch.Block.Attributes}
		for name, v := range d.pm {
			s, ok := shadowString(v)
			if !ok || len(s) < 4 {
				continue
			}
			a := sh.attribute(name)
			if a == nil || a.Deprecated {
				continue // a deprecated attribute mirrors what another resource sets
			}
			rank := 3
			switch name {
			case "id":
				rank = 0
			case "arn":
				rank = 1
			case "name":
				rank = 2
			default:
				if !a.Computed {
					continue // a value the specification of the dependency sets says nothing about a flow
				}
			}
			byValue[s] = append(byValue[s], shadowCandidate{value: s, node: d, attr: name, rank: rank})
		}
	}
	for s, cs := range byValue {
		sort.Slice(cs, func(i, j int) bool {
			if cs[i].rank != cs[j].rank {
				return cs[i].rank < cs[j].rank
			}
			if cs[i].node != cs[j].node {
				// The one the other depends on is where the value comes from.
				if cs[j].node.depSet[cs[i].node] {
					return true
				}
				if cs[i].node.depSet[cs[j].node] {
					return false
				}
				return cs[i].node.addr < cs[j].node.addr
			}
			return cs[i].attr < cs[j].attr
		})
		best := cs[0]
		ambiguous := false
		for _, c := range cs[1:] {
			if c.node != best.node && c.rank == best.rank && !c.node.depSet[best.node] {
				ambiguous = true
			}
		}
		if ambiguous {
			continue
		}
		n.exact[s] = best
		for _, c := range cs {
			if c.node == best.node && c.attr != best.attr {
				if n.alts == nil {
					n.alts = map[string][]shadowCandidate{}
				}
				n.alts[s] = append(n.alts[s], c)
			}
		}
		if len(s) >= 8 {
			n.subs = append(n.subs, best)
		}
	}
	sort.Slice(n.subs, func(i, j int) bool {
		if len(n.subs[i].value) != len(n.subs[j].value) {
			return len(n.subs[i].value) > len(n.subs[j].value)
		}
		return n.subs[i].value < n.subs[j].value
	})
}

func (c shadowCandidate) expr() string {
	return c.node.addr + "." + convert.FieldLabel(c.attr)
}

func shadowQuote(s string) string {
	return convert.RenderCUE(tftypes.NewValue(tftypes.String, s), "", nil, nil)
}

// str writes a string of n: a reference when a dependency holds the same value, an
// interpolation when it holds a long part of it, and the literal otherwise.
func (g *shadowGen) str(n *shadowNode, s string, count bool) string {
	if g.literal {
		return shadowQuote(s)
	}
	if c, ok := n.exact[s]; ok && !c.node.pre[c.attr] {
		// policy_arn takes the arn where the id holds the same value.
		for _, alt := range n.alts[s] {
			if g.hint != "" && (g.hint == alt.attr || strings.HasSuffix(g.hint, "_"+alt.attr)) && !alt.node.pre[alt.attr] {
				c = alt
				break
			}
		}
		n.refTo[c.node] = true
		if count {
			g.rep.References++
		}
		return c.expr()
	}
	if len(s) < 8 || len(n.subs) == 0 {
		return shadowQuote(s)
	}
	type hit struct {
		at int
		c  shadowCandidate
	}
	var hits []hit
	covered := make([]bool, len(s))
	for _, c := range n.subs {
		if len(c.value) >= len(s) || c.node.pre[c.attr] {
			continue
		}
		from := 0
		for {
			i := strings.Index(s[from:], c.value)
			if i < 0 {
				break
			}
			i += from
			free := true
			for j := i; j < i+len(c.value); j++ {
				if covered[j] {
					free = false
					break
				}
			}
			if free {
				for j := i; j < i+len(c.value); j++ {
					covered[j] = true
				}
				hits = append(hits, hit{i, c})
			}
			from = i + len(c.value)
		}
	}
	if len(hits) == 0 {
		return shadowQuote(s)
	}
	sort.Slice(hits, func(i, j int) bool { return hits[i].at < hits[j].at })
	var b strings.Builder
	b.WriteByte('"')
	at := 0
	lit := func(part string) {
		q := shadowQuote(part)
		b.WriteString(q[1 : len(q)-1])
	}
	for _, h := range hits {
		lit(s[at:h.at])
		b.WriteString(`\(` + h.c.expr() + `)`)
		at = h.at + len(h.c.value)
		n.interTo[h.c.node] = true
		if count {
			g.rep.Interpolations++
		}
	}
	lit(s[at:])
	b.WriteByte('"')
	return b.String()
}

// --- what the specification sets ---------------------------------------------

// sensitive leaves out every sensitive value that no dependency holds: the provider
// keeps the value it has, because the attribute is in ignore_changes.
func (g *shadowGen) sensitive(n *shadowNode) {
	sh := shape{attrs: n.sch.Block.Attributes, blocks: n.sch.Block.BlockTypes}
	for name, v := range n.pm {
		if v.IsNull() || !v.IsKnown() {
			continue
		}
		a := sh.attribute(name)
		if a != nil && a.Sensitive {
			if a.Computed && !a.Optional && !a.Required {
				g.rep.SensitiveComputed++
				continue
			}
			if isEmptyString(v) {
				continue
			}
			if s, ok := shadowString(v); ok {
				if _, ok := n.exact[s]; ok {
					n.sensitiveKept[name] = true
					g.rep.SensitiveReferred++
					continue
				}
			}
			n.omit[name], n.ignore[name] = true, true
			n.why[name] = "sensitive: the value stays in the state and is never written here"
			if s, ok := shadowString(v); ok {
				sec := ShadowSecret{Address: n.addr, Attribute: name, Value: s, Name: shadowSecretName(n.addr, name)}
				for i := 2; g.secretNames[sec.Name]; i++ {
					sec.Name = fmt.Sprintf("%s-%d", shadowSecretName(n.addr, name), i)
				}
				if g.secretNames == nil {
					g.secretNames = map[string]bool{}
				}
				g.secretNames[sec.Name] = true
				g.secrets = append(g.secrets, sec)
				if g.opts.Vault {
					n.why[name] += "; a copy is in the vault as " + sec.Name
				}
			}
			n.out.Sensitive = append(n.out.Sensitive, name)
			g.rep.SensitiveOmitted++
			if a.Required {
				g.rep.SensitiveRequired++
				g.rep.Notes = append(g.rep.Notes, fmt.Sprintf("%s: %s is required and sensitive and no dependency holds its value: give it by hand", n.in.Address, name))
			}
			continue
		}
		// A sensitive value further in: the whole block is left to the provider.
		if nested := shadowNestedSensitive(sh, name, v); nested > 0 {
			n.ignore[name] = true
			n.why[name] = "holds a sensitive value: the block keeps what the state has"
			n.out.Sensitive = append(n.out.Sensitive, name+" (nested)")
			g.rep.SensitiveOmitted += nested
		}
	}
	sort.Strings(n.out.Sensitive)
}

func shadowNestedSensitive(sh shape, name string, v tftypes.Value) int {
	var attrs []*schema.Attribute
	var blocks []*schema.NestedBlock
	if a := sh.attribute(name); a != nil {
		if a.NestedType == nil {
			return 0
		}
		attrs = a.NestedType.Attributes
	} else if nb := sh.block(name); nb != nil {
		attrs, blocks = nb.Block.Attributes, nb.Block.BlockTypes
	} else {
		return 0
	}
	inner := shape{attrs: attrs, blocks: blocks}
	count := 0
	var visit func(v tftypes.Value)
	visit = func(v tftypes.Value) {
		if v.IsNull() || !v.IsKnown() {
			return
		}
		var m map[string]tftypes.Value
		if v.Type().Is(tftypes.Object{}) && v.As(&m) == nil {
			for k, x := range m {
				if a := inner.attribute(k); a != nil && a.Sensitive && !x.IsNull() && !isEmptyString(x) && (!a.Computed || a.Optional || a.Required) {
					count++
					continue
				}
				count += shadowNestedSensitive(inner, k, x)
			}
			return
		}
		var elems []tftypes.Value
		if v.As(&elems) == nil {
			for _, e := range elems {
				visit(e)
			}
			return
		}
		if v.As(&m) == nil {
			for _, e := range m {
				visit(e)
			}
		}
	}
	visit(v)
	return count
}

// config is the object that the specification of n sets: the stored object without
// what only the provider sets and without what was left out.
func (n *shadowNode) config() tftypes.Value {
	sh := shape{attrs: n.sch.Block.Attributes, blocks: n.sch.Block.BlockTypes}
	// The id is the provider's to set, as in an adoption.
	omit := map[string]bool{"id": true}
	for k, v := range n.omit {
		omit[k] = v
	}
	return shadowProject(sh, n.prior, omit, n.sensitiveKept)
}

func shadowProject(sh shape, v tftypes.Value, omit, keepSensitive map[string]bool) tftypes.Value {
	var m map[string]tftypes.Value
	if v.IsNull() || !v.IsKnown() || v.As(&m) != nil {
		return v
	}
	out := make(map[string]tftypes.Value, len(m))
	for name, x := range m {
		null := tftypes.NewValue(x.Type(), nil)
		if x.IsNull() || !x.IsKnown() {
			out[name] = null
			continue
		}
		if omit[name] {
			out[name] = shadowEmpty(sh, name, x)
			continue
		}
		if a := sh.attribute(name); a != nil {
			switch {
			case a.Computed && !a.Optional && !a.Required, a.WriteOnly:
				out[name] = null
			case !a.Required && isEmptyString(x):
				out[name] = null
			case a.Sensitive && !keepSensitive[name]:
				out[name] = null
			case a.NestedType != nil:
				out[name] = shadowNested(shape{attrs: a.NestedType.Attributes}, x)
			default:
				out[name] = x
			}
			continue
		}
		if nb := sh.block(name); nb != nil {
			out[name] = shadowNested(shape{attrs: nb.Block.Attributes, blocks: nb.Block.BlockTypes}, x)
			continue
		}
		out[name] = x
	}
	return tftypes.NewValue(v.Type(), out)
}

// shadowEmpty is what a left-out attribute or block is in a configuration: null for an
// attribute, and no elements for a block.
func shadowEmpty(sh shape, name string, x tftypes.Value) tftypes.Value {
	if nb := sh.block(name); nb != nil {
		t := x.Type()
		switch {
		case t.Is(tftypes.List{}), t.Is(tftypes.Set{}):
			return tftypes.NewValue(t, []tftypes.Value{})
		case t.Is(tftypes.Map{}):
			return tftypes.NewValue(t, map[string]tftypes.Value{})
		}
	}
	return tftypes.NewValue(x.Type(), nil)
}

func shadowNested(sh shape, v tftypes.Value) tftypes.Value {
	if v.IsNull() || !v.IsKnown() {
		return v
	}
	t := v.Type()
	switch {
	case t.Is(tftypes.List{}), t.Is(tftypes.Set{}), t.Is(tftypes.Tuple{}):
		var elems []tftypes.Value
		if v.As(&elems) != nil {
			return v
		}
		out := make([]tftypes.Value, len(elems))
		for i, e := range elems {
			out[i] = shadowProject(sh, e, nil, nil)
		}
		return tftypes.NewValue(t, out)
	case t.Is(tftypes.Map{}):
		var m map[string]tftypes.Value
		if v.As(&m) != nil {
			return v
		}
		out := make(map[string]tftypes.Value, len(m))
		for k, e := range m {
			out[k] = shadowProject(sh, e, nil, nil)
		}
		return tftypes.NewValue(t, out)
	}
	return shadowProject(sh, v, nil, nil)
}

// --- the oracle --------------------------------------------------------------

// shadowVerdict is what the provider says about the specification of one resource.
type shadowVerdict struct {
	invalid []plugin.Diagnostic
	changed []string // top-level attributes that the plan changes
	failed  string
}

func (v shadowVerdict) clean() bool {
	return len(v.invalid) == 0 && len(v.changed) == 0 && v.failed == ""
}

func (v shadowVerdict) String() string {
	switch {
	case v.failed != "":
		return "error: " + v.failed
	case len(v.invalid) > 0:
		var parts []string
		for _, d := range v.invalid {
			parts = append(parts, strings.TrimSpace(d.Summary+": "+firstLine(d.Detail)))
		}
		return "invalid: " + strings.Join(parts, "; ")
	case len(v.changed) > 0:
		return "changes: " + strings.Join(v.changed, ", ")
	}
	return "clean"
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

func (n *shadowNode) ignoreList() []string {
	out := make([]string, 0, len(n.ignore))
	for k := range n.ignore {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// ask validates and plans the specification of n as a plan would.
func (g *shadowGen) ask(n *shadowNode) (shadowVerdict, error) {
	prov, err := g.provider(n.in.Record.ProviderKey)
	if err != nil {
		return shadowVerdict{}, err
	}
	g.rep.OracleCalls++
	typ, blk := n.in.Record.Type, n.sch.Block
	cfg := n.config()
	diags, err := prov.ValidateResourceConfig(g.ctx, typ, cfg)
	if err != nil {
		return shadowVerdict{failed: err.Error()}, nil
	}
	var v shadowVerdict
	for _, d := range diags {
		if d.Severity == "error" {
			v.invalid = append(v.invalid, d)
		}
	}
	if len(v.invalid) > 0 {
		return v, nil
	}
	cfg = applyIgnoreChanges(blk, n.prior, cfg, n.ignoreList())
	res, diags, err := prov.PlanResourceChange(g.ctx, typ, n.prior, ProposedNew(blk, n.prior, cfg), cfg, n.private)
	if err != nil {
		return shadowVerdict{failed: err.Error()}, nil
	}
	if err := diags.Err(); err != nil {
		for _, d := range diags {
			if d.Severity == "error" {
				v.invalid = append(v.invalid, d)
			}
		}
		if len(v.invalid) == 0 {
			v.failed = err.Error()
		}
		return v, nil
	}
	var qm map[string]tftypes.Value
	if err := res.PlannedState.As(&qm); err != nil {
		return shadowVerdict{failed: err.Error()}, nil
	}
	for k, pv := range n.pm {
		if qv, ok := qm[k]; !ok || !pv.Equal(qv) {
			v.changed = append(v.changed, k)
		}
	}
	sort.Strings(v.changed)
	return v, nil
}

func shadowPathHead(p *tftypes.AttributePath) string {
	if p == nil {
		return ""
	}
	if steps := p.Steps(); len(steps) > 0 {
		if a, ok := steps[0].(tftypes.AttributeName); ok {
			return string(a)
		}
	}
	return ""
}

func (g *shadowGen) did(n *shadowNode, kind, attr string) {
	g.rep.Repairs[kind]++
	n.out.Repairs = append(n.out.Repairs, kind+" "+attr)
}

// repair changes what the specification of n sets until the provider plans nothing,
// with no knowledge of the type: an attribute that the provider objects to is left out
// when the specification need not set it, and otherwise left to the provider.
func (g *shadowGen) repair(n *shadowNode, v shadowVerdict) (shadowVerdict, error) {
	for round := 0; round < 12 && !v.clean() && v.failed == ""; round++ {
		var progress bool
		var err error
		if len(v.invalid) > 0 {
			v, progress, err = g.repairInvalid(n, v)
		} else {
			v, progress, err = g.repairChanges(n, v)
		}
		if err != nil {
			return v, err
		}
		if !progress {
			break
		}
	}
	return v, nil
}

func (n *shadowNode) shape() shape {
	return shape{attrs: n.sch.Block.Attributes, blocks: n.sch.Block.BlockTypes}
}

// settable says that the specification need not set the attribute.
func (n *shadowNode) settable(name string) bool {
	if a := n.shape().attribute(name); a != nil {
		return !a.Required
	}
	return false
}

// isSet says that the specification sets the attribute or the block now.
func (n *shadowNode) isSet(name string) bool {
	sh := n.shape()
	x, ok := n.pm[name]
	if !ok || x.IsNull() || n.omit[name] {
		return false
	}
	if a := sh.attribute(name); a != nil {
		if (a.Computed && !a.Optional && !a.Required) || a.WriteOnly || (!a.Required && isEmptyString(x)) {
			return false
		}
		return true
	}
	return sh.block(name) != nil
}

// repairInvalid answers a provider that refuses the configuration: it leaves out an
// attribute that the message names, or puts a placeholder where a sensitive value is
// wanted. It says whether it changed anything.
func (g *shadowGen) repairInvalid(n *shadowNode, v shadowVerdict) (shadowVerdict, bool, error) {
	// The attributes that the provider names, and those its message quotes.
	var names []string
	seen := map[string]bool{}
	add := func(name string) {
		if name != "" && !seen[name] && n.isSet(name) && n.settable(name) {
			seen[name] = true
			names = append(names, name)
		}
	}
	for _, d := range v.invalid {
		add(shadowPathHead(d.Path))
		for _, w := range shadowQuoted(d.Summary + " " + d.Detail) {
			add(w)
		}
	}
	if len(names) == 0 {
		return g.putPlaceholders(n, v)
	}
	// Leave out the one that says least: the value that another one holds, then the
	// longer name (name_prefix before name).
	sort.SliceStable(names, func(i, j int) bool {
		si, _ := shadowString(n.pm[names[i]])
		sj, _ := shadowString(n.pm[names[j]])
		ci, cj := si != "" && sj != "" && si != sj && strings.Contains(sj, si), si != "" && sj != "" && si != sj && strings.Contains(si, sj)
		if ci != cj {
			return ci
		}
		zi, zj := shadowZero(n.pm[names[i]]), shadowZero(n.pm[names[j]])
		if zi != zj {
			return zi
		}
		if len(names[i]) != len(names[j]) {
			return len(names[i]) > len(names[j])
		}
		return names[i] < names[j]
	})
	before := len(v.invalid)
	for _, name := range names {
		n.omit[name] = true
		nv, err := g.ask(n)
		if err != nil {
			return v, false, err
		}
		if len(nv.invalid) < before || nv.failed == "" && len(nv.invalid) == 0 {
			n.why[name] = "left out: the provider refuses it beside another attribute"
			g.did(n, "left out (conflict)", name)
			return nv, true, nil
		}
		delete(n.omit, name)
	}
	return v, false, nil
}

// putPlaceholders writes a placeholder for a sensitive value that was left out and that
// the provider wants set, and asks again.
func (g *shadowGen) putPlaceholders(n *shadowNode, v shadowVerdict) (shadowVerdict, bool, error) {
	sh := n.shape()
	put := false
	for _, d := range v.invalid {
		words := append([]string{shadowPathHead(d.Path)}, shadowQuoted(d.Summary+" "+d.Detail)...)
		for _, w := range words {
			if a := sh.attribute(w); a != nil && a.Sensitive && n.omit[w] && n.ignore[w] && !n.placeholder[w] {
				if n.placeholder == nil {
					n.placeholder = map[string]bool{}
				}
				n.placeholder[w], n.sensitiveKept[w] = true, true
				delete(n.omit, w)
				n.why[w] = "sensitive: the provider wants it set, so a placeholder stands here and the object keeps the value it has"
				g.did(n, "placeholder for a sensitive value", w)
				put = true
			}
		}
	}
	if !put {
		return v, false, nil
	}
	nv, err := g.ask(n)
	return nv, true, err
}

// repairChanges answers a plan that would change the object: it puts back what was left
// out on a guess, leaves out what the provider computes and ignores what it reads back
// in another form.
func (g *shadowGen) repairChanges(n *shadowNode, v shadowVerdict) (shadowVerdict, bool, error) {
	progress := false
	for _, name := range v.changed {
		a := n.shape().attribute(name)
		switch {
		case n.pre[name] && n.omit[name]:
			// Left out on a guess, and the plan would change it: it goes back.
			delete(n.omit, name)
			delete(n.pre, name)
			delete(n.why, name)
			g.did(n, "put back (the plan would change it)", name)
			progress = true
		case n.omit[name] && !n.pre[name] && !n.ignore[name] && a != nil && !a.Computed && !a.Required:
			// Left out because the provider refuses it, and the plan would change it
			// to nothing: the object keeps the value it has.
			n.ignore[name] = true
			n.why[name] += "; ignore_changes keeps the value of the object"
			g.did(n, "ignore_changes", name)
			progress = true
		case !n.isSet(name):
			// Nothing the specification sets: the change follows from another one.
		case a != nil && a.Optional && a.Computed:
			n.omit[name] = true
			n.why[name] = "left out: the provider computes it"
			g.did(n, "left out (provider computes it)", name)
			progress = true
		default:
			n.ignore[name] = true
			n.why[name] = "the provider reads it back in another form"
			g.did(n, "ignore_changes", name)
			progress = true
		}
	}
	if !progress {
		return v, false, nil
	}
	nv, err := g.ask(n)
	return nv, true, err
}

func shadowZero(v tftypes.Value) bool {
	if v.IsNull() || !v.IsKnown() {
		return true
	}
	t := v.Type()
	switch {
	case t.Is(tftypes.String):
		var s string
		return v.As(&s) == nil && s == ""
	case t.Is(tftypes.Bool):
		var b bool
		return v.As(&b) == nil && !b
	case t.Is(tftypes.Number):
		return convert.RenderCUE(v, "", nil, nil) == "0"
	case t.Is(tftypes.List{}), t.Is(tftypes.Set{}), t.Is(tftypes.Tuple{}):
		var e []tftypes.Value
		return v.As(&e) == nil && len(e) == 0
	case t.Is(tftypes.Map{}), t.Is(tftypes.Object{}):
		var m map[string]tftypes.Value
		return v.As(&m) == nil && len(m) == 0
	}
	return false
}

// shadowQuoted returns the words that a message writes in quotes or backquotes.
func shadowQuoted(s string) []string {
	var out []string
	for _, q := range []byte{'"', '`'} {
		rest := s
		for {
			i := strings.IndexByte(rest, q)
			if i < 0 {
				break
			}
			j := strings.IndexByte(rest[i+1:], q)
			if j < 0 {
				break
			}
			for _, w := range strings.FieldsFunc(rest[i+1:i+1+j], func(r rune) bool { return r == ',' || r == ' ' }) {
				if k := strings.IndexAny(w, ".["); k > 0 {
					w = w[:k]
				}
				out = append(out, w)
			}
			rest = rest[i+j+2:]
		}
	}
	return out
}

// foreign leaves out the values that the provider may set and that name an object
// which n does not depend on: nobody wrote a reference there, or the state would have
// the edge, so the provider read the value from the object (the subnets of a default
// network ACL). It returns how many attributes it left out.
func (g *shadowGen) foreign(n *shadowNode) int {
	if g.ids == nil {
		g.ids = map[string]*shadowNode{}
		for _, m := range g.nodes {
			for _, name := range []string{"id", "arn"} {
				if s, ok := shadowString(m.pm[name]); ok && len(s) >= 8 {
					if _, dup := g.ids[s]; !dup {
						g.ids[s] = m
					}
				}
			}
		}
	}
	sh := shape{attrs: n.sch.Block.Attributes, blocks: n.sch.Block.BlockTypes}
	left := 0
	for name, x := range n.pm {
		if nb := sh.block(name); nb != nil && nb.Block != nil && nb.Block.Deprecated && !n.omit[name] && !x.IsNull() && !shadowZero(x) {
			// A deprecated block that still holds something: the provider fills it from
			// the object (the versioning of a bucket, which has a resource of its own
			// now). The plan says whether it is right to leave it out.
			n.omit[name], n.pre[name] = true, true
			n.why[name] = "left out: deprecated, and the provider fills it from the object"
			g.did(n, "left out (deprecated and computed)", name)
			left++
			continue
		}
		a := sh.attribute(name)
		if a == nil || !a.Optional || n.omit[name] || name == "id" || x.IsNull() || a.Sensitive {
			continue
		}
		if name == "arn" && a.Computed {
			// Like the id: the name the provider gave the object.
			n.omit[name], n.pre[name] = true, true
			left++
			continue
		}
		if a.Deprecated && a.Computed && !isEmptyString(x) && !shadowZero(x) {
			// The same for an attribute.
			n.omit[name], n.pre[name] = true, true
			n.why[name] = "left out: deprecated, and the provider fills it from the object"
			g.did(n, "left out (deprecated and computed)", name)
			left++
			continue
		}
		var named *shadowNode
		var visit func(v tftypes.Value)
		visit = func(v tftypes.Value) {
			if named != nil || v.IsNull() || !v.IsKnown() {
				return
			}
			if s, ok := shadowString(v); ok {
				if m := g.ids[s]; m != nil && m != n && !n.depSet[m] {
					named = m
				}
				return
			}
			if v.Type().Is(tftypes.String) || v.Type().Is(tftypes.Number) || v.Type().Is(tftypes.Bool) {
				return
			}
			var elems []tftypes.Value
			if v.As(&elems) == nil {
				for _, e := range elems {
					visit(e)
				}
				return
			}
			var m map[string]tftypes.Value
			if v.As(&m) == nil {
				for _, e := range m {
					visit(e)
				}
			}
		}
		visit(x)
		if named != nil {
			n.omit[name] = true
			n.why[name] = "left out: it names " + named.addr + ", which this resource does not depend on, so the provider read it"
			if !a.Computed {
				// The provider does not keep it by itself: it is left to what the object has.
				n.ignore[name] = true
				n.why[name] = "it names " + named.addr + ", which this resource does not depend on, so the provider read it: left as the object has it"
			}
			g.did(n, "left out (names an object it does not depend on)", name)
			left++
		}
	}
	return left
}

// minimise leaves out what the plan stays empty without: the values that are what the
// provider would put there anyway (false, 0, an empty list), and the values that the
// provider derives from the rest when it plans the object anew.
func (g *shadowGen) minimise(n *shadowNode) (shadowVerdict, error) {
	sh := shape{attrs: n.sch.Block.Attributes, blocks: n.sch.Block.BlockTypes}
	prov, err := g.provider(n.in.Record.ProviderKey)
	if err != nil {
		return shadowVerdict{}, err
	}
	var tried []string
	for name, x := range n.pm {
		a := sh.attribute(name)
		if a == nil || a.Required || n.omit[name] || x.IsNull() || (a.Computed && !a.Optional) || a.WriteOnly || a.Sensitive {
			continue
		}
		if isEmptyString(x) {
			continue
		}
		if shadowZero(x) {
			tried = append(tried, name)
		}
	}
	// What a plan of a new object derives without being told.
	blk := n.sch.Block
	null := tftypes.NewValue(blk.Type(), nil)
	probe := map[string]bool{}
	for k, v := range n.omit {
		probe[k] = v
	}
	var derived []string
	for name, x := range n.pm {
		a := sh.attribute(name)
		if a == nil || !a.Optional || !a.Computed || n.omit[name] || x.IsNull() || isEmptyString(x) || a.Sensitive || shadowZero(x) {
			continue
		}
		probe[name] = true
		derived = append(derived, name)
	}
	if len(derived) > 0 {
		probe["id"] = true
		cfg := shadowProject(sh, n.prior, probe, n.sensitiveKept)
		g.rep.OracleCalls++
		res, diags, perr := prov.PlanResourceChange(g.ctx, n.in.Record.Type, null, ProposedNew(blk, null, cfg), cfg, nil)
		var qm map[string]tftypes.Value
		if perr == nil && diags.Err() == nil && res.PlannedState.As(&qm) == nil {
			for _, name := range derived {
				if qv, ok := qm[name]; ok && qv.IsKnown() && qv.Equal(n.pm[name]) {
					tried = append(tried, name)
				}
			}
		}
	}
	sort.Strings(tried)
	for _, name := range tried {
		n.omit[name] = true
	}
	v, err := g.ask(n)
	if err != nil {
		return v, err
	}
	for round := 0; round < 6 && !v.clean(); round++ {
		// Put back what the plan misses, or everything when the provider objects.
		back := map[string]bool{}
		if len(v.invalid) > 0 || v.failed != "" {
			for _, name := range tried {
				back[name] = true
			}
		} else {
			for _, name := range v.changed {
				back[name] = true
			}
		}
		restored := false
		for _, name := range tried {
			if back[name] && n.omit[name] {
				delete(n.omit, name)
				restored = true
			}
		}
		if !restored {
			for _, name := range tried {
				delete(n.omit, name)
			}
		}
		if v, err = g.ask(n); err != nil {
			return v, err
		}
		if !restored {
			break
		}
	}
	for _, name := range tried {
		if n.omit[name] {
			g.rep.Repairs["left out (the provider derives it)"]++
		}
	}
	return v, nil
}

// --- the source --------------------------------------------------------------

type shadowTree struct {
	kids  map[string]*shadowTree
	nodes []*shadowNode
}

func (g *shadowGen) render() string {
	root := &shadowTree{kids: map[string]*shadowTree{}}
	for _, n := range g.nodes {
		t := root
		for _, l := range n.path[:len(n.path)-1] {
			if t.kids[l] == nil {
				t.kids[l] = &shadowTree{kids: map[string]*shadowTree{}}
			}
			t = t.kids[l]
		}
		t.nodes = append(t.nodes, n)
	}
	// The edges that no value carries are worked out before anything is written, and
	// they need the references, so the bodies are written twice: once to find them.
	for _, n := range g.nodes {
		var sink strings.Builder
		g.body(&sink, "", n, true)
	}
	// A state names a dependency without instance keys, so a reference to one instance
	// of a resource keeps the edge to that resource. An edge that no value carries is
	// kept by the edges of the others when one of them depends on the same resource,
	// and is written in $depends_on otherwise.
	call := func(d *shadowNode) string {
		var b strings.Builder
		for _, st := range d.in.Module {
			b.WriteString("module." + st.Name + ".")
		}
		return b.String()
	}
	instance := func(d *shadowNode) string { return strings.Join(d.path[:len(d.path)-1], ".") }
	for _, n := range g.nodes {
		n.explicit = nil
		// A state names a module without its instance keys too. Where a value of n comes
		// from one instance of a module, the edges without a value into that module are
		// taken to mean the same instance.
		affinity := map[string]map[string]bool{}
		for _, d := range n.deps {
			if (n.refTo[d] || n.interTo[d]) && len(d.in.Module) > 0 {
				if affinity[call(d)] == nil {
					affinity[call(d)] = map[string]bool{}
				}
				affinity[call(d)][instance(d)] = true
			}
		}
		for key, ds := range n.groups {
			if len(ds) > 0 && len(affinity[call(ds[0])]) > 0 {
				var near []*shadowNode
				for _, d := range ds {
					if affinity[call(d)][instance(d)] {
						near = append(near, d)
					}
				}
				if len(near) > 0 {
					ds = near
				}
			}
			_ = key
			carried := false
			in := map[*shadowNode]bool{}
			for _, d := range ds {
				carried = carried || n.refTo[d] || n.interTo[d]
				in[d] = true
			}
			if carried {
				continue
			}
			for _, d := range ds {
				implied := false
				for _, via := range n.deps {
					if !in[via] && via.depSet[d] {
						implied = true
						break
					}
				}
				if !implied {
					n.explicit = append(n.explicit, d)
				}
			}
		}
		sort.Slice(n.explicit, func(i, j int) bool { return n.explicit[i].addr < n.explicit[j].addr })
	}
	var b strings.Builder
	g.tree(&b, "", root)
	src := b.String()
	for _, tabs := range []string{"\t\t\t", "\t\t", "\t", ""} {
		src = strings.ReplaceAll(src, "}\n\n"+tabs+"}", "}\n"+tabs+"}")
	}
	return FormatCUE(src)
}

func (g *shadowGen) tree(b *strings.Builder, indent string, t *shadowTree) {
	sort.Slice(t.nodes, func(i, j int) bool { return t.nodes[i].addr < t.nodes[j].addr })
	for _, n := range t.nodes {
		fmt.Fprintf(b, "%s// %s\n", indent, n.in.Address)
		fmt.Fprintf(b, "%s%s: $%s.#Resource.%s & {\n", indent, n.path[len(n.path)-1], strings.ReplaceAll(n.in.Record.ProviderKey, ".", "_"), n.in.Record.Type)
		g.body(b, indent+"\t", n, false)
		fmt.Fprintf(b, "%s}\n\n", indent)
	}
	kids := make([]string, 0, len(t.kids))
	for k := range t.kids {
		kids = append(kids, k)
	}
	sort.Strings(kids)
	for _, k := range kids {
		fmt.Fprintf(b, "%s%s: {\n", indent, k)
		g.tree(b, indent+"\t", t.kids[k])
		fmt.Fprintf(b, "%s}\n\n", indent)
	}
}

func (g *shadowGen) body(b *strings.Builder, indent string, n *shadowNode, dry bool) {
	sh := shape{attrs: n.sch.Block.Attributes, blocks: n.sch.Block.BlockTypes}
	cfg := n.config()
	var m map[string]tftypes.Value
	_ = cfg.As(&m)
	delete(m, "id")
	written := g.fields(b, indent, sh, m, n, !dry, nil)
	if dry {
		return
	}
	g.rep.AttributesWritten += written
	if len(n.explicit) > 0 {
		var refs []string
		for _, d := range n.explicit {
			refs = append(refs, d.addr)
		}
		fmt.Fprintf(b, "%s$depends_on: [%s]\n", indent, strings.Join(refs, ", "))
	}
	left := make([]string, 0, len(n.omit))
	for k := range n.omit {
		if !n.ignore[k] {
			left = append(left, k)
		}
	}
	sort.Strings(left)
	for _, k := range left {
		if n.why[k] != "" {
			fmt.Fprintf(b, "%s// %s %s\n", indent, k, n.why[k])
		}
	}
	g.rep.AttributesLeftOut += len(n.omit)
	if ig := n.ignoreList(); len(ig) > 0 {
		for _, k := range ig {
			fmt.Fprintf(b, "%s// %s: %s\n", indent, k, n.why[k])
		}
		q := make([]string, len(ig))
		for i, k := range ig {
			q[i] = shadowQuote(k)
		}
		fmt.Fprintf(b, "%s$lifecycle: ignore_changes: [%s]\n", indent, strings.Join(q, ", "))
	}
}

// fields writes the fields of an object of the configuration and returns how many
// leaves it wrote.
func (g *shadowGen) fields(b *strings.Builder, indent string, sh shape, m map[string]tftypes.Value, n *shadowNode, count bool, path []string) int {
	names := make([]string, 0, len(m))
	for k := range m {
		names = append(names, k)
	}
	sort.Strings(names)
	written := 0
	for _, name := range names {
		v := m[name]
		if v.IsNull() || !v.IsKnown() {
			continue
		}
		label := convert.FieldLabel(name)
		var attrs []*schema.Attribute
		var blocks []*schema.NestedBlock
		var nesting string
		if a := sh.attribute(name); a != nil {
			if len(path) == 0 && n.placeholder[name] {
				fmt.Fprintf(b, "%s%s: %s\n", indent, label, shadowQuote(ShadowPlaceholder))
				written++
				continue
			}
			if a.NestedType == nil {
				g.literal, g.hint = len(path) == 0 && (name == "name" || name == "name_prefix"), name
				fmt.Fprintf(b, "%s%s: %s\n", indent, label, g.value(v, indent, n, count))
				g.literal, g.hint = false, ""
				written++
				continue
			}
			attrs, nesting = a.NestedType.Attributes, string(a.NestedType.Nesting)
		} else if nb := sh.block(name); nb != nil {
			attrs, blocks, nesting = nb.Block.Attributes, nb.Block.BlockTypes, string(nb.Nesting)
		} else {
			fmt.Fprintf(b, "%s%s: %s\n", indent, label, g.value(v, indent, n, count))
			written++
			continue
		}
		inner := shape{attrs: attrs, blocks: blocks}
		object := func(ind string, ov tftypes.Value) (string, bool) {
			var om map[string]tftypes.Value
			if ov.IsNull() || !ov.IsKnown() || ov.As(&om) != nil {
				return "", false
			}
			var ob strings.Builder
			written += g.fields(&ob, ind+"\t", inner, om, n, count, append(path, name))
			if ob.Len() == 0 {
				return "{}", true
			}
			return "{\n" + ob.String() + ind + "}", true
		}
		switch nesting {
		case string(schema.NestingList), string(schema.NestingSet):
			var elems []tftypes.Value
			if v.As(&elems) != nil || len(elems) == 0 {
				continue
			}
			var lb strings.Builder
			lb.WriteString("[\n")
			for _, e := range elems {
				if s, ok := object(indent+"\t", e); ok {
					lb.WriteString(indent + "\t" + s + ",\n")
				}
			}
			lb.WriteString(indent + "]")
			fmt.Fprintf(b, "%s%s: %s\n", indent, label, lb.String())
		case string(schema.NestingMap):
			var entries map[string]tftypes.Value
			if v.As(&entries) != nil || len(entries) == 0 {
				continue
			}
			keys := make([]string, 0, len(entries))
			for k := range entries {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			var mb strings.Builder
			mb.WriteString("{\n")
			for _, k := range keys {
				if s, ok := object(indent+"\t", entries[k]); ok {
					fmt.Fprintf(&mb, "%s\t%s: %s\n", indent, convert.FieldLabel(k), s)
				}
			}
			mb.WriteString(indent + "}")
			fmt.Fprintf(b, "%s%s: %s\n", indent, label, mb.String())
		default:
			if s, ok := object(indent, v); ok {
				fmt.Fprintf(b, "%s%s: %s\n", indent, label, s)
			}
		}
	}
	return written
}

// value writes a value with the strings that a dependency holds as references.
func (g *shadowGen) value(v tftypes.Value, indent string, n *shadowNode, count bool) string {
	if v.IsNull() || !v.IsKnown() {
		return "null"
	}
	t := v.Type()
	switch {
	case t.Is(tftypes.String):
		s, _ := shadowString(v)
		return g.str(n, s, count)
	case t.Is(tftypes.List{}), t.Is(tftypes.Set{}), t.Is(tftypes.Tuple{}):
		var elems []tftypes.Value
		_ = v.As(&elems)
		if len(elems) == 0 {
			return "[]"
		}
		var b strings.Builder
		b.WriteString("[\n")
		for _, e := range elems {
			b.WriteString(indent + "\t" + g.value(e, indent+"\t", n, count) + ",\n")
		}
		b.WriteString(indent + "]")
		return b.String()
	case t.Is(tftypes.Map{}), t.Is(tftypes.Object{}):
		var m map[string]tftypes.Value
		_ = v.As(&m)
		if len(m) == 0 {
			return "{}"
		}
		keys := make([]string, 0, len(m))
		for k := range m {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		var b strings.Builder
		b.WriteString("{\n")
		for _, k := range keys {
			fmt.Fprintf(&b, "%s\t%s: %s\n", indent, convert.FieldLabel(k), g.value(m[k], indent+"\t", n, count))
		}
		b.WriteString(indent + "}")
		return b.String()
	}
	return convert.RenderCUE(v, indent, nil, nil)
}

// ShadowJSON is the report as indented JSON.
func (r ShadowReport) JSON() []byte {
	b, _ := json.MarshalIndent(r, "", "  ")
	return append(b, '\n')
}
