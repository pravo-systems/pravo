package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand/v2"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"

	"pravo.dev/pravo/internal/state"
	"pravo.dev/pravo/internal/state/statetest"
)

// A default plan looks at what a change reaches and at the records those nodes
// read. A plan with -refresh=expired and -full reads every record that is past
// its refresh age and plans every node with the provider, as a tool does that
// has no reach. The two must decide the same wherever the default plan looked.
// -full also reads the data sources that are within their refresh age, and with
// -refresh=false, which the default plan does not: where the age is not always,
// or nothing is read, the default plan is held to the plan without -full.
//
// The tests in this file build random specifications on the fake provider, apply
// them, change them in random ways and compare the two plans, and the states that
// applying them leaves. Each test logs its seeds. PRAVO_EQUIVALENCE_SEED=N runs
// the single seed N, and PRAVO_EQUIVALENCE_ROUNDS=R sets the number of rounds.
//
// The tests that exercise the locks of applies that run together are
// TestDisjointAppliesAllSucceed, TestOverlappingAppliesHaveOneWinnerPerNode and
// TestOverlappingAppliesLoseNoRecord; TestEquivalenceUnderConcurrentApplies adds
// the check that what the applies leave is what the two plans agree on.

// eqForm is the way a generated resource builds its content.
type eqForm int

const (
	eqLeaf    eqForm = iota // a literal
	eqDefault               // a literal with a default
	eqIDs                   // the ids of the nodes it reads
	eqContent               // the content of the node it reads
	eqShared                // a plain field
	eqData                  // the data source
	eqCall                  // the result of a function call
	eqCond                  // one of two values, by the content of a literal node
	eqJoin                  // a comprehension over the container pets
)

// eqNode is a resource of a generated specification.
type eqNode struct {
	addr string
	// name is the name attribute: the replacement of the resource when it
	// changes, and the key by which the fake provider drifts.
	name   string
	form   eqForm
	reads  []*eqNode
	tag    string
	key    string // the provider instance, "" for fake
	pinned string // $lifecycle refresh, "" for none
	group  bool   // a member of the container pets
}

// eqOutput is an output of a generated specification: kind is "id" (the id of
// node), "pets" (the ids of the container), "data" or "shared".
type eqOutput struct {
	name string
	kind string
	node *eqNode
}

// eqShape sets what a generated specification may hold.
type eqShape struct {
	nodes int
	// calm leaves out what makes a plan depend on more than the configuration:
	// pinned refresh ages, a second provider instance.
	calm bool
}

// eqWorld is a specification that changes. Its random choices all come from rnd.
type eqWorld struct {
	rnd     *rand.Rand
	nodes   []*eqNode
	outs    []*eqOutput
	group   []*eqNode
	join    *eqNode
	counter int
	names   int
	shared  string
	region  string
	twoProv bool
	hasFn   bool
	data    int
	shape   eqShape
}

func newEqWorld(seed uint64, shape eqShape) *eqWorld {
	w := &eqWorld{rnd: rand.New(rand.NewPCG(seed, seed*0x9e3779b97f4a7c15+1)), shared: "t0", region: "eu-1", shape: shape}
	w.twoProv = !shape.calm && w.rnd.IntN(2) == 0
	w.hasFn = w.rnd.IntN(2) == 0
	if w.rnd.IntN(2) == 0 {
		for _, m := range []string{"a", "b", "c"}[:2+w.rnd.IntN(2)] {
			n := w.newNode(eqLeaf)
			n.addr, n.group = "pets."+m, true
			w.group = append(w.group, n)
		}
		w.join = w.newNode(eqJoin)
	}
	for len(w.nodes) < shape.nodes {
		w.newNode(w.pickForm())
	}
	for i := w.rnd.IntN(4); i > 0; i-- {
		w.addOutput()
	}
	return w
}

func (w *eqWorld) pickTag() string { return "v" + strconv.Itoa(w.rnd.IntN(4)) }

// otherTag returns a tag that is not tag.
func (w *eqWorld) otherTag(tag string) string {
	for {
		if t := w.pickTag(); t != tag {
			return t
		}
	}
}

func (w *eqWorld) freshName() string {
	w.names++
	return "n" + strconv.Itoa(w.names)
}

// leaves returns the nodes whose content is the literal their tag.
func (w *eqWorld) leaves() []*eqNode {
	var out []*eqNode
	for _, n := range w.nodes {
		if n.form == eqLeaf {
			out = append(out, n)
		}
	}
	return out
}

func (w *eqWorld) pickForm() eqForm {
	forms := []eqForm{eqLeaf, eqLeaf, eqDefault, eqShared, eqData}
	if len(w.nodes) > 0 {
		forms = append(forms, eqIDs, eqIDs, eqIDs, eqContent)
	}
	if len(w.leaves()) > 0 {
		forms = append(forms, eqCond)
	}
	if w.hasFn {
		forms = append(forms, eqCall)
	}
	return forms[w.rnd.IntN(len(forms))]
}

// newNode adds a resource of the form f that reads nodes picked at random.
func (w *eqWorld) newNode(f eqForm) *eqNode {
	w.counter++
	n := &eqNode{addr: "r" + strconv.Itoa(w.counter), name: w.freshName(), form: f, tag: w.pickTag()}
	switch f {
	case eqIDs:
		for _, i := range w.rnd.Perm(len(w.nodes))[:1+w.rnd.IntN(min(3, len(w.nodes)))] {
			n.reads = append(n.reads, w.nodes[i])
		}
	case eqContent:
		n.reads = []*eqNode{w.nodes[w.rnd.IntN(len(w.nodes))]}
	case eqCond:
		l := w.leaves()
		n.reads = []*eqNode{l[w.rnd.IntN(len(l))]}
	}
	if !w.shape.calm {
		if w.twoProv && w.rnd.IntN(3) == 0 {
			n.key = "fake.docs"
		}
		if w.rnd.IntN(12) == 0 {
			n.pinned = []string{"always", "1h", "never"}[w.rnd.IntN(3)]
		}
	}
	w.nodes = append(w.nodes, n)
	return n
}

func (w *eqWorld) addOutput() {
	o := &eqOutput{name: "o" + strconv.Itoa(len(w.outs)+w.counter)}
	kinds := []string{"id", "id", "data", "shared"}
	if w.join != nil {
		kinds = append(kinds, "pets")
	}
	o.kind = kinds[w.rnd.IntN(len(kinds))]
	if o.kind == "id" {
		o.node = w.nodes[w.rnd.IntN(len(w.nodes))]
	}
	w.outs = append(w.outs, o)
}

// readBy reports whether a node or an output reads n.
func (w *eqWorld) readBy(n *eqNode) bool {
	for _, m := range w.nodes {
		if slices.Contains(m.reads, n) {
			return true
		}
	}
	for _, o := range w.outs {
		if o.node == n {
			return true
		}
	}
	return false
}

// removable lists the nodes that can be left out of the specification without
// leaving a reference to them.
func (w *eqWorld) removable() []*eqNode {
	var out []*eqNode
	for _, n := range w.nodes {
		if !n.group && n != w.join && !w.readBy(n) {
			out = append(out, n)
		}
	}
	return out
}

func (w *eqWorld) remove(n *eqNode) {
	w.nodes = slices.DeleteFunc(w.nodes, func(m *eqNode) bool { return m == n })
}

func (w *eqWorld) nodeCUE(n *eqNode) string {
	var extra string
	switch n.form {
	case eqLeaf:
		extra = fmt.Sprintf("content: %q", n.tag)
	case eqDefault:
		extra = fmt.Sprintf("content: *%q | string", "d-"+n.tag)
	case eqIDs:
		parts := make([]string, len(n.reads))
		for i, m := range n.reads {
			parts[i] = `\(` + m.addr + `.id)`
		}
		extra = fmt.Sprintf(`content: "%s-%s"`, strings.Join(parts, "/"), n.tag)
	case eqContent:
		extra = fmt.Sprintf(`content: "\(%s.content)-%s"`, n.reads[0].addr, n.tag)
	case eqShared:
		extra = fmt.Sprintf(`content: "\(shared.tag)-%s"`, n.tag)
	case eqData:
		extra = fmt.Sprintf(`content: "\(src.content)-%s"`, n.tag)
	case eqCall:
		extra = fmt.Sprintf(`content: "\(fn.result)-%s"`, n.tag)
	case eqCond:
		up := n.reads[0].addr
		extra = fmt.Sprintf("if %s.content == \"v0\" {content: %q}\nif %s.content != \"v0\" {content: %q}", up, "yes-"+n.tag, up, "no-"+n.tag)
	case eqJoin:
		extra = fmt.Sprintf(`content: strings.Join([for k, v in pets {v.id}], ",") + %q`, "-"+n.tag)
	}
	if n.pinned != "" {
		extra += fmt.Sprintf("\n$lifecycle: refresh: %q", n.pinned)
	}
	return thing(n.name, extra)
}

// spec renders the world as a specification.
func (w *eqWorld) spec() spec {
	s := spec{data: []string{"src"}, keys: map[string]string{}}
	var b strings.Builder
	if w.join != nil {
		b.WriteString("import \"strings\"\n")
	}
	fmt.Fprintf(&b, "shared: {tag: %q, region: %q}\n", w.shared, w.region)
	if w.twoProv {
		b.WriteString("prov: {region: shared.region}\nprov2: {region: \"us-1\"}\n")
		s.providers = map[string]string{"fake": "prov", "fake.docs": "prov2"}
	}
	var pets []string
	for _, n := range w.nodes {
		s.resources = append(s.resources, n.addr)
		if n.key != "" {
			s.keys[n.addr] = n.key
		}
		if n.group {
			pets = append(pets, strings.TrimPrefix(n.addr, "pets.")+": "+w.nodeCUE(n))
			continue
		}
		fmt.Fprintf(&b, "%s: %s\n", n.addr, w.nodeCUE(n))
	}
	if len(pets) > 0 {
		fmt.Fprintf(&b, "pets: {%s}\n", strings.Join(pets, ", "))
	}
	b.WriteString("src: " + source("s") + "\n")
	if w.hasFn {
		b.WriteString("sess: {name: \"sess\", token: string | null}\n")
		b.WriteString("fn: {args: [\"abc\"], result: string | null}\n")
		s.ephemerals, s.calls = []string{"sess"}, []string{"fn"}
	}
	for _, o := range w.outs {
		switch o.kind {
		case "id":
			fmt.Fprintf(&b, "%s: {value: %s.id}\n", o.name, o.node.addr)
		case "pets":
			fmt.Fprintf(&b, "%s: {value: [for k, v in pets {v.id}]}\n", o.name)
		case "data":
			fmt.Fprintf(&b, "%s: {value: src.content}\n", o.name)
		case "shared":
			fmt.Fprintf(&b, "%s: {value: shared.tag}\n", o.name)
		}
		s.outputs = append(s.outputs, o.name)
	}
	s.cue = b.String()
	return s
}

// eqEdit is a change of one node that an apply makes: a new tag, or a new name,
// which replaces the resource.
type eqEdit struct {
	node      *eqNode
	tag, name string
}

// specWith renders the world as it would be with the edits made.
func (w *eqWorld) specWith(edits []eqEdit) spec {
	was := make([]eqEdit, len(edits))
	for i, e := range edits {
		was[i] = eqEdit{e.node, e.node.tag, e.node.name}
		w.set(e)
	}
	defer func() {
		for _, e := range was {
			w.set(e)
		}
	}()
	return w.spec()
}

func (w *eqWorld) set(e eqEdit) {
	if e.tag != "" {
		e.node.tag = e.tag
	}
	if e.name != "" {
		e.node.name = e.name
	}
}

// mutate changes the world at random and describes what it did. The fake
// provider takes the changes that happen outside the specification.
func (w *eqWorld) mutate(h *harness) []string {
	var did []string
	for i := w.rnd.IntN(3); i >= 0; i-- {
		// A node that has a record, for the changes that need one.
		var have []*eqNode
		for _, n := range w.nodes {
			if h.state.Get(n.addr) != nil {
				have = append(have, n)
			}
		}
		pick := func(from []*eqNode) *eqNode { return from[w.rnd.IntN(len(from))] }
		switch k := w.rnd.IntN(16); {
		case k < 4:
			n := pick(w.nodes)
			n.tag = w.otherTag(n.tag)
			did = append(did, fmt.Sprintf("edit %s: tag %s", n.addr, n.tag))
		case k == 4:
			w.shared = w.otherTag(w.shared)
			did = append(did, "shared.tag "+w.shared)
		case k == 5:
			n := pick(w.nodes)
			n.name = w.freshName()
			did = append(did, fmt.Sprintf("rename %s: name %s", n.addr, n.name))
		case k == 6 || k == 7:
			n := w.newNode(w.pickForm())
			did = append(did, fmt.Sprintf("add %s (form %d)", n.addr, n.form))
		case k == 8:
			if r := w.removable(); len(r) > 0 {
				n := pick(r)
				w.remove(n)
				did = append(did, "remove "+n.addr)
			}
		case k == 9:
			w.data++
			h.fake.setData("s", fmt.Sprintf("data-%d", w.data))
			did = append(did, fmt.Sprintf("data source answers data-%d", w.data))
		case k == 10:
			if w.twoProv {
				w.region = map[string]string{"eu-1": "eu-2", "eu-2": "eu-1"}[w.region]
				did = append(did, "region "+w.region)
			}
		case k == 11 || k == 12:
			if len(have) > 0 {
				n := pick(have)
				h.fake.drift(n.name, fmt.Sprintf("oob-%d", w.rnd.IntN(1000)))
				did = append(did, fmt.Sprintf("drift %s (%s)", n.addr, n.name))
			}
		case k == 13:
			if len(have) > 0 {
				n := pick(have)
				h.fake.vanish(n.name)
				did = append(did, fmt.Sprintf("vanish %s (%s)", n.addr, n.name))
			}
		case k == 14:
			if len(w.outs) > 0 && w.rnd.IntN(2) == 0 {
				w.outs = w.outs[:len(w.outs)-1]
				did = append(did, "drop an output")
			} else if len(w.nodes) > 0 {
				w.addOutput()
				did = append(did, "add output "+w.outs[len(w.outs)-1].name)
			}
		default:
			// Nothing changes: the plan has no change to find.
		}
	}
	return did
}

// forget ends the drift of the objects that no node of the specification has any
// more, so that a name that nothing reads does not keep the world dirty.
func (w *eqWorld) forget(f *fakeProvider) {
	f.mu.Lock()
	defer f.mu.Unlock()
	live := map[string]bool{}
	for _, n := range w.nodes {
		live[n.name] = true
	}
	for name := range f.drifted {
		if !live[name] {
			delete(f.drifted, name)
		}
	}
	for name := range f.vanished {
		if !live[name] {
			delete(f.vanished, name)
		}
	}
}

// outOfBand reports whether the real object of the resource named name differs
// from what anyone has recorded.
func (f *fakeProvider) outOfBand(name string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	_, drifted := f.drifted[name]
	return drifted || f.vanished[name]
}

func (f *fakeProvider) anyOutOfBand() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.drifted)+len(f.vanished) > 0
}

// reconcileAll ends every drift and disappearance, as a plan that read every
// object and an apply that followed it do.
func (f *fakeProvider) reconcileAll() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.drifted, f.vanished = nil, nil
}

// fork returns a harness over a copy of the state, with the same fake provider:
// a second command that starts from the records as they are now.
func (h *harness) fork() *harness {
	h.t.Helper()
	dir := h.t.TempDir()
	if err := os.CopyFS(filepath.Join(dir, "state"), os.DirFS(filepath.Join(h.dir, "state"))); err != nil {
		h.t.Fatal(err)
	}
	lb, err := state.NewLocalBackend(filepath.Join(dir, "state"))
	if err != nil {
		h.t.Fatal(err)
	}
	backend := statetest.NewCounting(lb)
	st, err := state.OpenBackend(context.Background(), backend)
	if err != nil {
		h.t.Fatal(err)
	}
	return &harness{t: h.t, dir: dir, state: st, fake: h.fake, backend: backend}
}

// eqDetail is how much of a decision is compared.
type eqDetail int

const (
	eqAction     eqDetail = iota // the action
	eqValues                     // and the planned values
	eqEverything                 // and whether the node drifted, and the reasons
)

// eqDecision is what a plan decides about a node. A node that nothing changes
// has no reasons worth comparing: a plan of the whole estate gives one for every
// node, the default plan one for none. The reasons that only a plan which did not
// look at a node, or planned all of them, can give are left out of the others.
func eqDecision(ch *Change, detail eqDetail) string {
	if detail == eqAction {
		return string(ch.Action)
	}
	if detail == eqValues {
		return fmt.Sprintf("%s planned=%v create=%v", ch.Action, ToData(ch.Planned), ToData(ch.PlannedCreate))
	}
	var reasons []string
	if ch.Action != ActionNoop && ch.Action != ActionRead {
		for _, r := range ch.Reasons {
			if strings.HasPrefix(r, "-full: ") || strings.HasPrefix(r, "skipped: ") || strings.HasPrefix(r, "not read: ") {
				continue
			}
			reasons = append(reasons, r)
		}
	}
	return fmt.Sprintf("%s planned=%v create=%v drifted=%v reasons=%q", ch.Action, ToData(ch.Planned), ToData(ch.PlannedCreate), ch.Drifted, reasons)
}

// eqDump lists every change of a plan, one to a line.
func eqDump(p *Plan) string {
	var lines []string
	for _, ch := range p.Changes {
		lines = append(lines, fmt.Sprintf("  %s deps=%v skipped=%v unread=%v refreshed=%v %s", ch.Addr, ch.Deps, ch.Skipped, ch.Unread, ch.Refreshed, eqDecision(ch, eqEverything)))
	}
	sort.Strings(lines)
	return strings.Join(lines, "\n")
}

// eqDiff lists how two plans of one specification differ. A node that the first
// plan did not read and whose object changed outside the specification is left
// out: finding it is what the second plan is for. So are the nodes downstream of
// it, whether or not the first plan reached them: the second plan plans them
// against the object it found.
func eqDiff(got, want *Plan, detail eqDetail, outOfBand func(addr string) bool) []string {
	var diffs []string
	byAddr := func(p *Plan) map[string]*Change {
		m := map[string]*Change{}
		for _, ch := range p.Changes {
			m[ch.Addr] = ch
		}
		return m
	}
	g, w := byAddr(got), byAddr(want)
	addrs := map[string]bool{}
	for a := range g {
		addrs[a] = true
	}
	for a := range w {
		addrs[a] = true
	}
	var all []string
	for a := range addrs {
		all = append(all, a)
	}
	sort.Strings(all)
	excused := map[string]bool{}
	for _, a := range all {
		if gc := g[a]; gc != nil && gc.Unread && outOfBand(a) {
			excused[a] = true
		}
	}
	for grew := true; grew; {
		grew = false
		for _, a := range all {
			gc := g[a]
			if gc == nil || excused[a] {
				continue
			}
			for _, d := range gc.Deps {
				if excused[d] {
					excused[a], grew = true, true
					break
				}
			}
		}
	}
	for _, a := range all {
		gc, wc := g[a], w[a]
		switch {
		case gc == nil:
			diffs = append(diffs, a+": only the second plan has it: "+eqDecision(wc, eqEverything))
			continue
		case wc == nil:
			diffs = append(diffs, a+": only the first plan has it: "+eqDecision(gc, eqEverything))
			continue
		}
		if excused[a] {
			if gc.Unread && (gc.Action != ActionNoop || !gc.Skipped) {
				diffs = append(diffs, a+": a node that was not reached is "+eqDecision(gc, eqEverything))
			}
			continue
		}
		// The default plan does not decode the record of a node that nothing reads.
		d := detail
		if gc.Skipped && gc.Action == ActionNoop && gc.Planned.Type() == nil {
			d = eqAction
		}
		if x, y := eqDecision(gc, d), eqDecision(wc, d); x != y {
			diffs = append(diffs, fmt.Sprintf("%s:\n    default  %s\n    expected %s", a, x, y))
		}
	}
	if len(excused) > 0 {
		// The second plan has changes and outputs that rest on what it found.
		return diffs
	}
	if got.HasChanges() != want.HasChanges() && len(diffs) == 0 {
		diffs = append(diffs, fmt.Sprintf("HasChanges %v against %v", got.HasChanges(), want.HasChanges()))
	}
	outs := map[string]bool{}
	for k := range got.Outputs {
		outs[k] = true
	}
	for k := range want.Outputs {
		outs[k] = true
	}
	var names []string
	for k := range outs {
		names = append(names, k)
	}
	sort.Strings(names)
	for _, k := range names {
		render := func(p *Plan) string {
			v, ok := p.Outputs[k]
			if !ok {
				return "(absent)"
			}
			b, err := outputJSON(v)
			if err != nil {
				return "(not known)"
			}
			return string(b)
		}
		if x, y := render(got), render(want); x != y {
			diffs = append(diffs, fmt.Sprintf("output %s: %s against %s", k, x, y))
		}
	}
	return diffs
}

var eqIDText = regexp.MustCompile(`id-[0-9]+`)

// eqStates describes the records and the outputs of a state so that two states
// that were built by different runs of the same operations compare equal: the ids
// that the provider handed out are replaced by the address of the node that has
// them, and the times and revisions are left out.
func eqStates(st *state.Store) map[string]string {
	ids := map[string]string{}
	for _, r := range st.List() {
		var attrs map[string]any
		if json.Unmarshal(r.Attributes, &attrs) == nil {
			if id, ok := attrs["id"].(string); ok {
				ids[id] = "ID(" + r.Addr + ")"
			}
		}
	}
	norm := func(s string) string {
		var v any
		if json.Unmarshal([]byte(s), &v) == nil {
			if b, err := json.Marshal(v); err == nil {
				s = string(b)
			}
		}
		return eqIDText.ReplaceAllStringFunc(s, func(m string) string {
			if to, ok := ids[m]; ok {
				return to
			}
			return m
		})
	}
	out := map[string]string{}
	for _, r := range st.List() {
		deps := slices.Clone(r.Dependencies)
		sort.Strings(deps)
		out[r.Addr] = fmt.Sprintf("%s %s marked=%v deps=%v %s", r.Mode, r.Type, r.Marked(), deps, norm(string(r.Attributes)))
	}
	outputs := st.Outputs()
	var keys []string
	for k := range outputs {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		out["output "+k] = norm(string(outputs[k]))
	}
	return out
}

func eqStateDiff(a, b map[string]string) []string {
	var diffs []string
	keys := map[string]bool{}
	for k := range a {
		keys[k] = true
	}
	for k := range b {
		keys[k] = true
	}
	var all []string
	for k := range keys {
		all = append(all, k)
	}
	sort.Strings(all)
	for _, k := range all {
		if a[k] != b[k] {
			diffs = append(diffs, fmt.Sprintf("%s:\n    %s\n    %s", k, a[k], b[k]))
		}
	}
	return diffs
}

// eqSeeds returns the seeds to run, and the number of rounds of each.
func eqSeeds(t *testing.T, seeds, rounds int) ([]uint64, int) {
	t.Helper()
	if testing.Short() {
		seeds, rounds = max(1, seeds/3), max(4, rounds/2)
	}
	if v := os.Getenv("PRAVO_EQUIVALENCE_ROUNDS"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 {
			t.Fatalf("PRAVO_EQUIVALENCE_ROUNDS=%q", v)
		}
		rounds = n
	}
	if v := os.Getenv("PRAVO_EQUIVALENCE_SEED"); v != "" {
		n, err := strconv.ParseUint(v, 10, 64)
		if err != nil {
			t.Fatalf("PRAVO_EQUIVALENCE_SEED=%q", v)
		}
		return []uint64{n}, rounds
	}
	out := make([]uint64, seeds)
	for i := range out {
		out[i] = uint64(101 + 7*i)
	}
	return out, rounds
}

// eqRun is one seed of the differential test: a world that changes, the state
// it is applied to, and counts of what the plans did.
type eqRun struct {
	t      *testing.T
	seed   uint64
	h      *harness
	w      *eqWorld
	valid  int
	reads  int // records the default plans read
	spared int // records the expired plans read that the default plans did not
}

// TestEquivalenceOfDefaultAndExpiredPlans plans random specifications after
// random changes, with the default rule and with the rule that reads and plans
// everything, and requires the two to decide alike wherever the default plan
// looked. It also holds the default plan to what it says about itself: the nodes
// it counts as read are the nodes the provider was asked about, none of them twice.
// After each round it applies the plan three ways (the default plan, the plan that
// reads everything, and the default plan through a saved plan file) and requires
// the three states to agree.
func TestEquivalenceOfDefaultAndExpiredPlans(t *testing.T) {
	seeds, rounds := eqSeeds(t, 24, 30)
	for _, seed := range seeds {
		t.Run(fmt.Sprintf("seed %d", seed), func(t *testing.T) {
			t.Parallel()
			t.Logf("seed %d, %d rounds; PRAVO_EQUIVALENCE_SEED=%d reproduces it", seed, rounds, seed)
			r := &eqRun{t: t, seed: seed, h: newHarness(t), w: newEqWorld(seed, eqShape{nodes: 6 + int(seed%9)})}
			r.run(rounds)
			t.Logf("%d rounds compared; the default plans read %d records and the expired plans read %d more", r.valid, r.reads, r.spared)
			if r.valid < rounds/2 {
				t.Errorf("only %d of %d rounds produced two plans to compare", r.valid, rounds)
			}
		})
	}
}

func (r *eqRun) fail(round int, did []string, s spec, format string, args ...any) {
	r.t.Helper()
	r.t.Fatalf("seed %d, round %d (%s)\n%s\nspecification:\n%s", r.seed, round, strings.Join(did, "; "), fmt.Sprintf(format, args...), s.cue)
}

// plan makes a plan as a command does. It returns the engine and the plan.
func (r *eqRun) plan(h *harness, s spec, opts Options) (*Engine, *Plan, error) {
	h.fake.resetCalls()
	e := h.engine(s, opts)
	p, err := e.Plan(context.Background())
	return e, p, err
}

func (r *eqRun) run(rounds int) {
	ctx := context.Background()
	t, h, w := r.t, r.h, r.w
	h.apply(w.spec())
	for round := 1; round <= rounds; round++ {
		did := w.mutate(h)
		s := w.spec()

		mode, oracle := RefreshAge, RefreshExpired
		switch w.rnd.IntN(8) {
		case 0:
			mode, oracle = RefreshNone, RefreshNone
		case 1:
			mode, oracle = RefreshAll, RefreshAll
		}
		age := []string{"", "", "", "", "1h", "never"}[w.rnd.IntN(6)]
		did = append(did, fmt.Sprintf("refresh mode %d, age %q", mode, age))
		// The default plan cannot see an object that changed outside the
		// specification unless it reads it; only the nodes it left unread are
		// excused, and only when the age would have had the second plan read them.
		blind := mode == RefreshAge && age == "" && h.fake.anyOutOfBand()

		// The names of the records, to tell which objects a plan read.
		names := map[string]string{}
		for _, sr := range h.state.List() {
			names[sr.Addr] = nameOf(t, sr.Attributes)
		}
		forkB, forkC := h.fork(), h.fork()
		// -full also reads the data sources that are within their refresh age, and
		// with -refresh=false; the default plan does not, and is held to the plan
		// that reads everything only where the two read alike.
		xe := forkB.engine(s, Options{Refresh: oracle, DefaultRefresh: age, Full: age == "" && mode != RefreshNone})
		xp, xerr := xe.Plan(ctx)
		e, dp, derr := r.plan(h, s, Options{Refresh: mode, DefaultRefresh: age})
		read := h.fake.readNames()
		if (derr != nil) != (xerr != nil) {
			r.fail(round, did, s, "the plans disagree about failing:\n  default: %v\n  expired: %v", derr, xerr)
		}
		if derr != nil {
			t.Logf("round %d: both plans fail (%v)", round, derr)
			continue
		}
		r.valid++

		inBand := func(addr string) bool { return h.fake.outOfBand(names[addr]) }
		if diffs := eqDiff(dp, xp, eqEverything, func(addr string) bool { return blind && inBand(addr) }); len(diffs) > 0 {
			r.fail(round, did, s, "the default plan and the plan that reads everything differ:\n%s\n\ndefault plan:\n%s\nplan that reads everything:\n%s", strings.Join(diffs, "\n"), eqDump(dp), eqDump(xp))
		}

		// What the plan says it read is what the provider was asked.
		if !slices.Equal(slices.Compact(slices.Clone(read)), read) {
			r.fail(round, did, s, "a record was read twice: %v", read)
		}
		var refreshed []string
		for _, ch := range dp.Changes {
			if ch.Mode == "managed" && ch.Refreshed {
				refreshed = append(refreshed, names[ch.Addr])
			}
		}
		sort.Strings(refreshed)
		if mode == RefreshNone && len(read) != 0 {
			r.fail(round, did, s, "-refresh=false read %v", read)
		}
		if mode != RefreshNone && !slices.Equal(refreshed, read) {
			r.fail(round, did, s, "the plan counts %v as refreshed, the provider read %v", refreshed, read)
		}
		// Every resource and data source that had a record is in one of the three
		// counts.
		recorded := 0
		for _, ch := range dp.Changes {
			if _, had := names[ch.Addr]; had && (ch.Mode == "managed" || ch.Mode == "data") {
				recorded++
			}
		}
		if st := dp.Stats; mode != RefreshNone && st.Refreshed+st.Fresh+st.NotRead != recorded {
			r.fail(round, did, s, "%d refreshed, %d within refresh age and %d not read do not add up to the %d records", st.Refreshed, st.Fresh, st.NotRead, recorded)
		}
		if mode == RefreshAge && age == "" {
			// Every record that a node the plan reached reads is read too: the
			// refresh age of always asks for it.
			byAddr := map[string]*Change{}
			for _, ch := range dp.Changes {
				byAddr[ch.Addr] = ch
			}
			declared := map[string]*eqNode{}
			for _, n := range w.nodes {
				declared[n.addr] = n
			}
			for _, ch := range dp.Changes {
				if n := declared[ch.Addr]; n == nil || ch.Skipped || (ch.Mode != "managed" && ch.Mode != "data") {
					continue
				}
				for _, d := range ch.Deps {
					// A record with an age of its own is read when that age says so.
					if in := byAddr[d]; in != nil && in.Unread && (declared[d] == nil || declared[d].pinned == "" || declared[d].pinned == "always") {
						r.fail(round, did, s, "%s is planned and reads %s, which the plan did not read", ch.Addr, d)
					}
				}
			}
		}
		r.reads += len(read)
		xread := 0
		for _, ch := range xp.Changes {
			if ch.Mode == "managed" && ch.Refreshed {
				xread++
			}
		}
		r.spared += max(0, xread-len(refreshed))

		// A saved plan of the default plan, made on a third copy.
		ce, cp, err := r.plan(forkC, s, Options{Refresh: mode, DefaultRefresh: age})
		if err != nil {
			r.fail(round, did, s, "the plan on the third copy: %v", err)
		}
		file := filepath.Join(t.TempDir(), "p.plan")
		if err := ce.SavePlan(file, cp, nil, false); err != nil {
			r.fail(round, did, s, "save: %v", err)
		}

		if w.rnd.IntN(6) == 0 {
			// A plan that is not applied: what it wrote while it read stays for the
			// next one.
			continue
		}
		if _, err := e.Apply(ctx, dp); err != nil {
			r.fail(round, did, s, "apply of the default plan: %v", err)
		}
		// The nodes the plan looked at are as the specification says now.
		for _, ch := range dp.Changes {
			if !ch.Unread {
				h.fake.restore(names[ch.Addr])
			}
		}
		if mode == RefreshAll {
			h.fake.reconcileAll()
		}
		w.forget(h.fake)

		if blind {
			continue
		}
		if _, err := xe.Apply(ctx, xp); err != nil {
			r.fail(round, did, s, "apply of the plan that reads everything: %v", err)
		}
		pf, err := ReadPlanFile(file)
		if err != nil {
			r.fail(round, did, s, "read of the saved plan: %v", err)
		}
		ae := forkC.engine(s, Options{Refresh: RefreshNone, DefaultRefresh: age})
		loaded, err := ae.LoadPlan(pf)
		if err != nil {
			r.fail(round, did, s, "load of the saved plan: %v", err)
		}
		// pravo apply FILE applies what the file leaves to do, and says so when it
		// holds no change: a record that only takes a new configuration and an
		// output are written by an apply of the plan itself, not of its file.
		remaining := loaded.Remaining()
		if remaining.HasChanges() {
			if _, err := ae.ApplySaved(ctx, remaining); err != nil {
				r.fail(round, did, s, "apply of the saved plan: %v", err)
			}
		}
		a, b, c := eqStates(h.state), eqStates(forkB.state), eqStates(forkC.state)
		if diffs := eqStateDiff(a, b); len(diffs) > 0 {
			r.fail(round, did, s, "the states after the default plan and after the plan that reads everything differ:\n%s", strings.Join(diffs, "\n"))
		}
		if diffs := eqStateDiff(a, c); len(diffs) > 0 && (dp.HasChanges() || !dp.NeedsApply()) {
			r.fail(round, did, s, "the states after the default plan and after the saved plan differ:\n%s", strings.Join(diffs, "\n"))
		}
	}

	// A plan that reads everything and the apply of it bring the records to the
	// objects and the specification. After that no plan finds anything to do.
	s := w.spec()
	e := h.engine(s, Options{Refresh: RefreshAll})
	final, err := e.Plan(ctx)
	if err != nil {
		r.fail(rounds, nil, s, "plan with -refresh=all: %v", err)
	}
	if _, err := e.Apply(ctx, final); err != nil {
		r.fail(rounds, nil, s, "apply of the plan with -refresh=all: %v", err)
	}
	h.fake.reconcileAll()
	for _, mode := range []RefreshMode{RefreshAge, RefreshExpired, RefreshAll} {
		p, err := h.engine(s, Options{Refresh: mode, Full: mode != RefreshAge}).Plan(ctx)
		if err != nil {
			r.fail(rounds, nil, s, "final plan, mode %d: %v", mode, err)
		}
		if p.HasChanges() {
			var ds []string
			for _, ch := range p.Changes {
				if ch.Action != ActionNoop {
					ds = append(ds, ch.Addr+" "+eqDecision(ch, eqEverything))
				}
			}
			r.fail(rounds, nil, s, "the last plan, mode %d, has changes:\n%s", mode, strings.Join(ds, "\n"))
		}
	}
}

// Applies that run together, each from its own plan, and a plan of what they
// left. Every apply either applies in full or is turned away before it changes
// anything; the applies that win have no node in common; every record is at the
// revision that the winners give it; and the specification that holds the edits of
// the winners alone is what the default plan and the plan that reads everything
// both find nothing to change in.
func TestEquivalenceUnderConcurrentApplies(t *testing.T) {
	seeds, rounds := eqSeeds(t, 4, 8)
	const applies = 4
	for _, seed := range seeds {
		t.Run(fmt.Sprintf("seed %d", seed), func(t *testing.T) {
			t.Parallel()
			t.Logf("seed %d, %d rounds of %d applies", seed, rounds, applies)
			ctx := context.Background()
			w := newEqWorld(seed, eqShape{nodes: 24, calm: true})
			h := newHarness(t)
			h.apply(w.spec())
			want := map[string]int{}
			for _, sr := range h.state.List() {
				want[sr.Addr] = 1
			}
			var winners, turned, spurious int
			for round := 1; round <= rounds; round++ {
				edits := make([][]eqEdit, applies)
				plans := make([]*Plan, applies)
				engines := make([]*Engine, applies)
				for k := range edits {
					for _, i := range w.rnd.Perm(len(w.nodes))[:1+w.rnd.IntN(3)] {
						n := w.nodes[i]
						if w.rnd.IntN(5) == 0 {
							edits[k] = append(edits[k], eqEdit{node: n, name: w.freshName()})
						} else {
							edits[k] = append(edits[k], eqEdit{node: n, tag: w.otherTag(n.tag)})
						}
					}
					engines[k] = h.engineOn(h.process(), w.specWith(edits[k]), Options{})
					plans[k] = plan(t, engines[k])
				}
				var (
					wg   sync.WaitGroup
					errs = make([]error, applies)
					gate = make(chan struct{})
				)
				for k := range engines {
					wg.Add(1)
					go func() {
						defer wg.Done()
						<-gate
						_, errs[k] = engines[k].Apply(ctx, plans[k])
					}()
				}
				close(gate)
				wg.Wait()

				var won []int
				for k, err := range errs {
					if err == nil {
						won = append(won, k)
						continue
					}
					var coded *CodedError
					if !errors.As(err, &coded) || (coded.Code != ExitStale && coded.Code != ExitLocked) {
						t.Fatalf("seed %d, round %d, apply %d: %v", seed, round, k, err)
					}
					turned++
				}
				owner := map[string]int{}
				for _, k := range won {
					winners++
					for _, addr := range plans[k].Write {
						if prev, dup := owner[addr]; dup {
							t.Fatalf("seed %d, round %d: applies %d and %d both wrote %s", seed, round, prev, k, addr)
						}
						owner[addr] = k
						want[addr]++
					}
					for _, e := range edits[k] {
						w.set(e)
					}
				}
				// An apply that was turned away although none of the winners wrote a
				// node that its plan writes or reads.
				for k, err := range errs {
					if err == nil {
						continue
					}
					free := true
					for _, addr := range plans[k].Write {
						if _, taken := owner[addr]; taken {
							free = false
						}
					}
					for addr := range plans[k].Inputs {
						if _, taken := owner[addr]; taken {
							free = false
						}
					}
					if free {
						spurious++
					}
				}

				now := h.process()
				for addr, rev := range want {
					if sr := now.Get(addr); sr == nil || sr.Revision != rev {
						t.Fatalf("seed %d, round %d: %s is %+v, want revision %d", seed, round, addr, sr, rev)
					}
				}
				if locks, _ := now.Locks(); len(locks) != 0 {
					t.Fatalf("seed %d, round %d: %d locks left", seed, round, len(locks))
				}
				s := w.spec()
				fresh := h.process()
				a, err := h.engineOn(fresh, s, Options{}).Plan(ctx)
				if err != nil {
					t.Fatalf("seed %d, round %d: plan: %v", seed, round, err)
				}
				b, err := h.engineOn(fresh, s, Options{Refresh: RefreshExpired, Full: true}).Plan(ctx)
				if err != nil {
					t.Fatalf("seed %d, round %d: expired plan: %v", seed, round, err)
				}
				if a.HasChanges() || b.HasChanges() {
					t.Fatalf("seed %d, round %d: the specification of the winners still has changes (default %v, expired %v):\n%s",
						seed, round, a.HasChanges(), b.HasChanges(), s.cue)
				}
				if diffs := eqDiff(a, b, eqEverything, func(string) bool { return false }); len(diffs) > 0 {
					t.Fatalf("seed %d, round %d: the plans differ:\n%s", seed, round, strings.Join(diffs, "\n"))
				}
			}
			t.Logf("%d applies won and %d were turned away, %d of them although no winner wrote a node that their plan writes or reads", winners, turned, spurious)
			if winners == 0 {
				t.Error("no apply won in any round")
			}
			if applies*rounds > 0 && float64(spurious) > 0.05*float64(applies*rounds)+1 {
				t.Errorf("%d of %d applies were turned away with no winner to blame", spurious, applies*rounds)
			}
		})
	}
}
