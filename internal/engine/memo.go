package engine

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"

	"cuelang.org/go/cue"
	"cuelang.org/go/cue/format"
	"github.com/hashicorp/terraform-plugin-go/tftypes"

	"github.com/pravo-systems/pravo/internal/config"
	"github.com/pravo-systems/pravo/internal/convert"
	"github.com/pravo-systems/pravo/internal/filelock"
	"github.com/pravo-systems/pravo/internal/plog"
	"github.com/pravo-systems/pravo/internal/schema"
	"github.com/pravo-systems/pravo/internal/sensitivity"
)

// A plan converts the configuration of every resource to tell whether it is
// still what the record holds, and for most resources of most plans the answer
// is that it is. The conversion needs the body of the resource with the values
// of everything it reads put in, so a plan of an estate where one thing changed
// evaluates all of it.
//
// The memo is what a plan remembers of the conversions it made, so that a
// later plan can leave a resource alone without evaluating its body. For a
// resource it holds one keyed digest of
//
//   - the fingerprint of everything the configuration is made from, and
//   - what the conversion came to: the digest of the configuration that a
//     record keeps, prevent_destroy and the labels.
//
// A later plan works out the fingerprint again, from the specification as it is
// now and from the values of this run, and makes the digest with what the
// record holds. If it is the digest of the memo, the conversion would come to
// what the record holds, and the plan goes on as if it had made it: the node is
// left alone when its record is within its refresh age and nothing else asks for
// a read, exactly as after a conversion (leftAlone). In any other case the node
// is planned in full. The memo can say "this is what the record holds" and
// nothing else, so a plan never acts on a value that came out of the file.
//
// # The fingerprint
//
// Only a body of one shape is remembered (convert.BodyRefs): the value the
// specification gives it, where the only leaves that another task can change are
// plain references to an attribute of that task (subnet_id: net.id, or v.id for
// the variable of a loop that is read by element). No comprehension, condition,
// call or interpolation in it reads a task. Such a body with the values put in
// is the body as loaded with those leaves set, so it is known from
//
//   - the body as CUE evaluated it without any computed value, printed leaf by
//     leaf, which covers every plain field, definition, tag and package that the
//     body is built from, since CUE resolved them;
//   - the expressions of the body, as CUE prints them;
//   - for each task the body reads: the same two of that task, the digest of its
//     configuration and the value it has in this run.
//
// A task of any other shape has no fingerprint and is planned in full by every
// plan, and so is every task that reads one, a secret or an ephemeral resource:
// the value of a secret is in no digest of the memo. The tasks a body reads are
// the ones the graph has for it, so the memo is as right as the graph is, like
// the order of the walk.
//
// When a conversion is remembered, the body it read is compared with the body
// as loaded: a difference anywhere but at the reference leaves means that
// something this file does not account for changed the body, and nothing is
// remembered for the task.
//
// # The file
//
// The digests are HMAC-SHA256 with a key that is not in the file. Without the key
// the file says nothing about a configuration or a value, which a plain digest
// would (see recordedHash). A file that is lost, damaged or from another key is
// a memo that remembers nothing.

// PlanMemo says where a plan keeps its memo.
type PlanMemo struct {
	// Path is the file. It is read when a plan starts and written when a plan that
	// learned something ends without an error.
	Path string
	// Key keys every digest in the file. It is kept somewhere else.
	Key []byte
	// Check makes a plan convert every configuration that the memo answers for
	// and fail when the two differ.
	Check bool
}

// memoVersion is in every digest: a change to what a fingerprint covers makes
// the memos of the versions before it remember nothing.
const memoVersion = "pravo plan memo 1"

// memoEntry is what the memo holds for one address.
type memoEntry struct {
	// Same is the keyed digest of the fingerprint and of what the conversion came
	// to.
	Same string `json:"same"`
	// CBD and Age are $lifecycle.create_before_destroy and $lifecycle.refresh,
	// which a record does not hold. They are part of Same.
	CBD bool   `json:"cbd,omitempty"`
	Age string `json:"age,omitempty"`
}

type memoFile struct {
	Version string               `json:"version"`
	Entries map[string]memoEntry `json:"entries"`
}

// memoState is the memo of the plan that runs.
type memoState struct {
	memoMu sync.Mutex
	// memoOn is set while a plan runs with a memo. memoOld is the file as it
	// was read and memoNew what the plan ends with.
	memoOn           bool
	memoOld, memoNew map[string]memoEntry
	// fps holds the fingerprint of each task that the walk started, "" when it
	// has none; outs what each finished task gave its readers; inputs the memo of
	// inputOf. They are of one walk.
	fps    map[string]string
	outs   map[string]*taskOut
	inputs map[string]string
	// memoHits counts the nodes left alone on the word of the memo.
	memoHits int
	// memoSeen holds the nodes that the plan asked the memo about. An entry of a
	// node that it did not ask about, as in a plan of a target, is kept.
	memoSeen map[string]bool
	// said holds the facts that the memo gave for a node of this walk: the ones
	// of its record. A conversion of the node that the plan makes after all is
	// held against them (memoAgainst).
	said map[string]planFacts

	// shapes holds what is known of each body from the specification alone, and
	// hashedTop the names of a block that the digest of a configuration covers.
	// lazy holds the fills that the nodes left alone did not make. cueMu guards
	// the three.
	shapes    map[string]*bodyShape
	hashedTop map[*schema.Block]map[string]bool
	lazy      map[string]*lazyFill
	// lazyAll is set while fillLazyAllLocked runs.
	lazyAll bool
	// shapeInert is the memo of the analysis of the shapes (shapeResolver).
	// cueMu guards it.
	shapeInert map[string]convert.InertField
	// shadowing is set while a body is worked out to check the memo, with
	// everything it changes put back after. cueMu guards it.
	shadowing bool
}

// bodyShape is what the specification alone says of a task body.
type bodyShape struct {
	// ok says the body is of the shape that is remembered.
	ok bool
	// fp is the keyed digest of the body as loaded, static of its print alone.
	fp, static string
	refs       map[string]convert.LeafRef
	// covered says that every leaf that reads another task sits in an attribute
	// that the digest of the configuration covers.
	covered bool
}

// taskOut is what a finished task gives the tasks that read it: the planned
// object of a resource, or the fill of a builtin or a function call.
type taskOut struct {
	final tftypes.Value
	sum   string
	done  bool
}

// lazyFill is the fill that a node left alone would have made.
type lazyFill struct {
	res   *config.Resource
	block *schema.Block
	final tftypes.Value
}

// memoShadow, when set, is told what differs between the memo and the full
// conversion of a node that the memo answered for. The tests set it, and every
// node any of them leaves alone is converted as well.
var memoShadow func(e *Engine, problem string)

// memoChecks counts the nodes that the memo answered for and that were
// converted as well.
var memoChecks atomic.Int64

// hk is the keyed digest of the parts.
func (e *Engine) hk(parts ...string) string {
	h := hmac.New(sha256.New, e.Opts.Memo.Key)
	var n [8]byte
	binary.BigEndian.PutUint64(n[:], uint64(len(memoVersion)))
	h.Write(n[:])
	h.Write([]byte(memoVersion))
	for _, p := range parts {
		binary.BigEndian.PutUint64(n[:], uint64(len(p)))
		h.Write(n[:])
		h.Write([]byte(p))
	}
	return hex.EncodeToString(h.Sum(nil))
}

// memoBegin reads the memo for the plan that starts.
func (e *Engine) memoBegin() {
	e.memoMu.Lock()
	defer e.memoMu.Unlock()
	e.memoOn, e.memoOld, e.memoNew, e.memoHits = false, nil, nil, 0
	m := e.Opts.Memo
	if m == nil || len(m.Key) == 0 || e.Opts.Full {
		return
	}
	e.memoOn = true
	e.memoSeen = map[string]bool{}
	e.memoNew = map[string]memoEntry{}
	e.memoOld = map[string]memoEntry{}
	data, err := os.ReadFile(m.Path)
	if err != nil {
		return
	}
	var f memoFile
	if json.Unmarshal(data, &f) == nil && f.Version == memoVersion && f.Entries != nil {
		e.memoOld = f.Entries
	}
}

// memoEnd writes the memo of the plan that ended, when it ended without an
// error and learned something. The file is replaced in one step. A memo that
// cannot be written is one that the next plan does not have.
func (e *Engine) memoEnd(ctx context.Context, err error) {
	e.memoMu.Lock()
	defer e.memoMu.Unlock()
	if !e.memoOn {
		return
	}
	e.memoOn = false
	for addr, ent := range e.memoOld {
		if _, declared := e.graph[addr]; declared && !e.memoSeen[addr] {
			e.memoNew[addr] = ent
		}
	}
	plog.Debug(ctx, "memo", "left alone", e.memoHits, "remembered", len(e.memoNew), "had", len(e.memoOld))
	if err != nil || maps.Equal(e.memoOld, e.memoNew) {
		return
	}
	data, merr := json.Marshal(memoFile{Version: memoVersion, Entries: e.memoNew})
	if merr != nil {
		return
	}
	path := e.Opts.Memo.Path
	if os.MkdirAll(filepath.Dir(path), 0o700) != nil {
		return
	}
	_ = filelock.WriteFileAtomic(path, data, filelock.AtomicOptions{TempPattern: "%s.*"})
}

// memoRun clears what the memo holds of one walk.
func (e *Engine) memoRun() {
	e.memoMu.Lock()
	e.fps, e.outs, e.inputs, e.said = map[string]string{}, map[string]*taskOut{}, map[string]string{}, map[string]planFacts{}
	e.memoMu.Unlock()
	e.lazy = nil
}

// memoActive reports whether the plan that runs has a memo.
func (e *Engine) memoActive() bool {
	e.memoMu.Lock()
	defer e.memoMu.Unlock()
	return e.memoOn
}

// memoOut notes the object that the plan of a resource gave its readers. An
// object with no type is none: the plan deferred it, or nothing reads it.
func (e *Engine) memoOut(addr string, final tftypes.Value) {
	e.memoMu.Lock()
	defer e.memoMu.Unlock()
	if !e.memoOn {
		return
	}
	e.outs[addr] = &taskOut{final: final}
}

// memoFill notes the fill of a builtin task or a function call. The fill of a
// resource is made from its planned object, which memoOut has. The value of a
// secret is noted nowhere. The caller holds cueMu.
func (e *Engine) memoFill(r *config.Resource, fill map[string]any) {
	if e.shadowing || (!r.Builtin && r.Mode != "call") || secretTask(r) {
		return
	}
	e.memoMu.Lock()
	defer e.memoMu.Unlock()
	if !e.memoOn {
		return
	}
	out := &taskOut{done: true}
	if data, err := json.Marshal(fill); err == nil {
		out.sum = e.hk("fill", string(data))
	}
	e.outs[r.Address()] = out
}

// secretTask reports whether the value of the task is a secret, or lives only
// for the run.
func secretTask(r *config.Resource) bool {
	return r.Mode == "ephemeral" || (r.Builtin && (r.Type == "secret" || r.Type == "secret_env"))
}

// digest is the keyed digest of what the task gave its readers, "" when that is
// not all known. The caller holds memoMu.
func (o *taskOut) digest(e *Engine) string {
	if o.done {
		return o.sum
	}
	o.done = true
	if o.final.Type() == nil || !o.final.IsFullyKnown() {
		return ""
	}
	data, err := convert.ToJSON(o.final)
	if err != nil {
		return ""
	}
	o.sum = e.hk("object", string(data))
	return o.sum
}

// shapeLocked returns what the specification alone says of the body of r. The
// caller holds cueMu.
func (e *Engine) shapeLocked(r *config.Resource) *bodyShape {
	addr := r.Address()
	if s, ok := e.shapes[addr]; ok {
		return s
	}
	if e.shapes == nil {
		e.shapes = map[string]*bodyShape{}
	}
	s := &bodyShape{}
	e.shapes[addr] = s
	v := e.atRoot(r.Path)
	if !v.Exists() {
		return s
	}
	if e.shapeInert == nil {
		e.shapeInert = map[string]convert.InertField{}
	}
	refs, ok := convert.BodyRefs(v, r.Path, shapeResolver{taskResolver{e}})
	if !ok {
		return s
	}
	s.refs = make(map[string]convert.LeafRef, len(refs))
	for _, ref := range refs {
		s.refs[ref.Path.String()] = ref
	}
	static, ok := printBody(v, s.refs)
	if !ok {
		return s
	}
	raw, ok := rawSyntax(v)
	if !ok {
		return s
	}
	parts := []string{"body", addr, r.Mode, r.Type, r.ProviderKey}
	s.covered = len(refs) == 0
	if !r.Builtin && r.Mode != "call" {
		sc, err := e.resourceSchema(r)
		if err != nil || sc.Block == nil {
			return s
		}
		parts = append(parts, e.providerVersion(r.ProviderKey), strconv.FormatInt(sc.Version, 10))
		top := e.coveredTop(sc.Block)
		s.covered = true
		for _, ref := range refs {
			sels := ref.Path.Selectors()
			if len(sels) == 0 || sels[0].LabelType() != cue.StringLabel || !top[sels[0].Unquoted()] {
				s.covered = false
			}
		}
	}
	s.static = e.hk("static", static)
	s.fp = e.hk(append(parts, raw, s.static)...)
	s.ok = true
	return s
}

// shapeResolver is the resolver of the engine with a memo of its own for the
// plain fields that reach no task. Which fields those are is a matter of the
// references in the specification, which the values of finished tasks do not
// change, so the memo holds for every shape that the engine works out. The memo
// of the graph (Engine.inert) ends with the build of the graph.
type shapeResolver struct{ taskResolver }

func (r shapeResolver) InertFields() map[string]convert.InertField { return r.e.shapeInert }

// coveredTop lists the attributes and the nested blocks of blk that the digest
// of a configuration covers whole: the ones that hold no write-only attribute.
// The caller holds cueMu.
func (e *Engine) coveredTop(blk *schema.Block) map[string]bool {
	if top, ok := e.hashedTop[blk]; ok {
		return top
	}
	if e.hashedTop == nil {
		e.hashedTop = map[*schema.Block]map[string]bool{}
	}
	top := map[string]bool{}
	for _, a := range blk.Attributes {
		if !sensitivity.HasWriteOnly(&schema.Block{Attributes: []*schema.Attribute{a}}) {
			top[a.Name] = true
		}
	}
	for _, nb := range blk.BlockTypes {
		if !sensitivity.HasWriteOnly(nb.Block) {
			top[nb.TypeName] = true
		}
	}
	e.hashedTop[blk] = top
	return top
}

// printBody prints a body as CUE evaluated it, leaf by leaf, hidden fields and
// definitions included. A leaf of refs is printed as the reference it is, and
// not as its value. ok is false for a body that cannot be walked.
func printBody(v cue.Value, refs map[string]convert.LeafRef) (string, bool) {
	var b strings.Builder
	ok := printBodyAt(&b, v, nil, refs, 0)
	return b.String(), ok
}

func printBodyAt(b *strings.Builder, v cue.Value, rel []cue.Selector, refs map[string]convert.LeafRef, depth int) bool {
	if depth > 64 {
		return false
	}
	switch v.IncompleteKind() {
	case cue.StructKind:
		it, err := v.Fields(cue.Hidden(true), cue.Definitions(true))
		if err != nil {
			return false
		}
		// The fields are sorted: the order in which CUE met them is not the same
		// after the values of other tasks were unified in, and no conversion reads
		// it.
		var fields []string
		for it.Next() {
			var fb strings.Builder
			sel := it.Selector()
			fb.WriteString(sel.String())
			fb.WriteByte(':')
			if !printBodyAt(&fb, it.Value(), append(rel[:len(rel):len(rel)], sel), refs, depth+1) {
				return false
			}
			fields = append(fields, fb.String())
		}
		sort.Strings(fields)
		b.WriteByte('{')
		for _, f := range fields {
			b.WriteString(f)
			b.WriteByte(' ')
		}
		b.WriteByte('}')
		return true
	case cue.ListKind:
		it, err := v.List()
		if err != nil {
			return false
		}
		b.WriteByte('[')
		for i := 0; it.Next(); i++ {
			if !printBodyAt(b, it.Value(), append(rel[:len(rel):len(rel)], cue.Index(i)), refs, depth+1) {
				return false
			}
			b.WriteByte(',')
		}
		b.WriteByte(']')
		return true
	}
	if len(refs) > 0 {
		if ref, ok := refs[cue.MakePath(rel...).String()]; ok {
			fmt.Fprintf(b, "<%s %s>", ref.Task, cue.MakePath(ref.Attr...))
			return true
		}
	}
	if d, _ := v.Default(); d.IsConcrete() {
		if js, err := d.MarshalJSON(); err == nil {
			b.Write(js)
			return true
		}
	}
	fmt.Fprintf(b, "?%v", v)
	return true
}

// rawSyntax prints the expressions that a value is the unification of, with
// the references in them as they were written.
func rawSyntax(v cue.Value) (out string, ok bool) {
	defer func() {
		if recover() != nil {
			out, ok = "", false
		}
	}()
	data, err := format.Node(v.Syntax(cue.Raw(), cue.Docs(false)))
	if err != nil {
		return "", false
	}
	return string(data), true
}

// fingerprint is the keyed digest of everything the configuration of r is made
// from, "" when that is not all known to the memo. See the top of the file.
func (e *Engine) fingerprint(r *config.Resource) string {
	addr := r.Address()
	deps := e.bodyDeps[addr]
	e.lockCUE(e.traceCtx())
	own := e.shapeLocked(r)
	shapes := make([]*bodyShape, len(deps))
	if own.ok {
		for i, d := range deps {
			if dr := e.addrRes(d); dr != nil {
				shapes[i] = e.shapeLocked(dr)
			}
		}
	}
	e.cueMu.Unlock()
	if !own.ok {
		return ""
	}
	// Every task that a leaf of the body reads is one the graph has for it.
	for _, ref := range own.refs {
		dr, ok := e.byPath[ref.Task.String()]
		if !ok {
			return ""
		}
		if _, found := sort.Find(len(deps), func(i int) int { return strings.Compare(dr.Address(), deps[i]) }); !found {
			return ""
		}
	}
	parts := make([]string, 0, 2+2*len(deps))
	parts = append(parts, "fingerprint", own.fp)
	e.memoMu.Lock()
	defer e.memoMu.Unlock()
	for i, d := range deps {
		in := e.inputOf(d, shapes[i])
		if in == "" {
			return ""
		}
		parts = append(parts, d, in)
	}
	return e.hk(parts...)
}

// inputOf is the keyed digest of everything a reader can see of the finished
// task at addr, "" when that is not all known to the memo: the task is a secret
// or ephemeral, its body is not of the shape that is remembered, a leaf of it
// that reads another task sits outside what the digest of its configuration
// covers, or its values are not all known. The caller holds memoMu.
func (e *Engine) inputOf(addr string, shape *bodyShape) string {
	if in, ok := e.inputs[addr]; ok {
		return in
	}
	in := func() string {
		r := e.addrRes(addr)
		out := e.outs[addr]
		if r == nil || shape == nil || !shape.ok || !shape.covered || out == nil || secretTask(r) {
			return ""
		}
		sum := out.digest(e)
		if sum == "" {
			return ""
		}
		if r.Builtin || r.Mode == "call" {
			return e.hk("input", shape.fp, sum)
		}
		ch := e.changeFor(addr)
		if ch == nil || ch.ConfigHash == "" || ch.Deferred || ch.Action == ActionDefer {
			return ""
		}
		return e.hk("input", shape.fp, ch.ConfigHash, sum)
	}()
	e.inputs[addr] = in
	return in
}

// sameDigest is the digest that the memo holds for a configuration with the
// fingerprint fp that converts to these facts.
func (e *Engine) sameDigest(fp string, f planFacts) string {
	labels := make([]string, 0, len(f.labels))
	for k, v := range f.labels {
		labels = append(labels, fmt.Sprintf("%q=%q", k, v))
	}
	sort.Strings(labels)
	return e.hk("same", fp, f.hash, strconv.FormatBool(f.prevent), strings.Join(labels, ","), strconv.FormatBool(f.cbd), f.age)
}

// planFromMemo plans r without evaluating its body when the memo says that its
// configuration is what its record holds, and the plan would leave it alone
// then. done is false when the node has to be planned in full.
func (e *Engine) planFromMemo(r *config.Resource) (done bool, err error) {
	if !e.memoActive() {
		return false, nil
	}
	addr := r.Address()
	fp := e.fingerprint(r)
	e.memoMu.Lock()
	e.fps[addr] = fp
	e.memoSeen[addr] = true
	ent, known := e.memoOld[addr]
	e.memoMu.Unlock()
	sr := e.State.Get(addr)
	if fp == "" || !known || sr == nil || sr.ConfigHash == "" {
		return false, nil
	}
	facts := planFacts{hash: sr.ConfigHash, labels: sr.Labels, prevent: sr.PreventDestroy, cbd: ent.CBD, age: ent.Age}
	if ent.Same != e.sameDigest(fp, facts) {
		return false, nil
	}
	// The conversion would come to what the record holds. That stays true
	// whatever the plan does with the node now.
	e.memoMu.Lock()
	e.memoNew[addr] = ent
	e.said[addr] = facts
	e.memoMu.Unlock()
	s, serr := e.resourceSchema(r)
	if serr != nil {
		return false, nil
	}
	e.cueMu.Lock()
	refused := e.refused
	e.cueMu.Unlock()
	if refused != nil {
		return true, refused
	}
	p := e.planOf(r, s, facts, false)
	if !p.unchanged || e.deferredDep(addr) != "" {
		return false, nil
	}
	if left, lerr := e.leftAlone(p); lerr != nil || !left {
		// The plan in full says what is wrong, or does what is to be done.
		return false, nil
	}
	if e.Opts.Memo.Check || memoShadow != nil {
		memoChecks.Add(1)
		if problem := e.shadowCheck(r, p, e.Opts.Memo.Check); problem != "" {
			if memoShadow != nil {
				memoShadow(e, problem)
			}
			if e.Opts.Memo.Check {
				return true, fmt.Errorf("%s (the plan memo is wrong: plan with PRAVO_PLAN=full, and report this)", problem)
			}
		}
	}
	ch := p.ch
	ch.PriorRevision, ch.PriorGeneration = e.versionOf(addr)
	e.record(ch)
	e.memoOut(addr, ch.Planned)
	e.memoMu.Lock()
	e.memoHits++
	e.memoMu.Unlock()
	if ch.Planned.Type() != nil {
		// What reads the node gets its values when it asks for them.
		e.cueMu.Lock()
		if e.lazy == nil {
			e.lazy = map[string]*lazyFill{}
		}
		e.lazy[addr] = &lazyFill{res: r, block: s.Block, final: ch.Planned}
		e.cueMu.Unlock()
	}
	return true, nil
}

// leftAlone finishes the plan of a node that nothing the provider saw has
// changed of, when the plan has no reason to look at it either: a resource whose
// record is within its refresh age (quiet), a data source that is answered from
// its record (skipDataRead). It makes the decisions of planManaged and planData
// up to the point where they need the configuration, and reports false for a
// node that they have to plan.
func (e *Engine) leftAlone(p *resourcePlan) (bool, error) {
	ch, addr := p.ch, p.ch.Addr
	if p.r.Mode == "data" {
		ch.Action = ActionRead
		if _, ok := e.cachedData(addr, ch.ConfigHash); ok || e.dependsOnPendingChange(addr) {
			return false, nil
		}
		return e.skipDataRead(p)
	}
	if (e.Opts.Refresh != RefreshAge && e.Opts.Refresh != RefreshNone) || e.cachedRead(addr) != nil {
		return false, nil
	}
	return e.quiet(ch, p.sr, p.s, p.age)
}

// memoAgainst holds a conversion that the plan made against what the memo said
// of the node in this walk, when it said anything: a node that the memo
// answered for and that the plan had another reason to look at. It returns what
// differs, which is an error of the memo. Tests fail on it, and so does a plan
// that checks its memo; any other plan goes by the conversion, as it does
// without a memo.
func (e *Engine) memoAgainst(addr string, f planFacts) error {
	e.memoMu.Lock()
	said, ok := e.said[addr]
	e.memoMu.Unlock()
	if !ok {
		return nil
	}
	memoChecks.Add(1)
	if f.hash == said.hash && f.cbd == said.cbd && f.prevent == said.prevent && f.age == said.age && maps.Equal(f.labels, said.labels) {
		return nil
	}
	problem := fmt.Sprintf("%s: the memo says its configuration is what the record holds (hash %s, create_before_destroy %v, prevent_destroy %v, refresh %q, labels %v), and converting it gives hash %s, create_before_destroy %v, prevent_destroy %v, refresh %q, labels %v",
		addr, said.hash, said.cbd, said.prevent, said.age, said.labels, f.hash, f.cbd, f.prevent, f.age, f.labels)
	if memoShadow != nil {
		memoShadow(e, problem)
	}
	plog.Debug(e.traceCtx(), "memo is wrong", "addr", addr)
	if e.Opts.Memo != nil && e.Opts.Memo.Check {
		return fmt.Errorf("%s (the plan memo is wrong: plan with PRAVO_PLAN=full, and report this)", problem)
	}
	return nil
}

// memoStore remembers the conversion that startPlan made of the body v, when
// the task has a fingerprint and the configuration is all known.
func (e *Engine) memoStore(r *config.Resource, p *resourcePlan, v cue.Value) {
	addr := r.Address()
	e.memoMu.Lock()
	on, fp := e.memoOn, e.fps[addr]
	e.memoMu.Unlock()
	if !on || fp == "" || p.ch.ConfigHash == "" {
		return
	}
	// The body that was converted is the body as loaded but for the leaves that
	// read another task: that is what the fingerprint stands for.
	e.lockCUE(e.traceCtx())
	shape := e.shapeLocked(r)
	static, ok := printBody(v, shape.refs)
	e.cueMu.Unlock()
	if !ok || e.hk("static", static) != shape.static {
		return
	}
	f := planFacts{hash: p.ch.ConfigHash, labels: p.ch.Labels, prevent: p.ch.PreventDestroy, cbd: p.ch.CreateBeforeDestroy, age: p.age}
	ent := memoEntry{Same: e.sameDigest(fp, f), CBD: f.cbd, Age: f.age}
	e.memoMu.Lock()
	e.memoNew[addr] = ent
	e.memoMu.Unlock()
}

// fillLazyLocked makes the fills that the nodes at addrs did not make when the
// memo left them alone. The body of such a node is worked out as a task would
// have it, which makes the fills of the nodes it reads first. The caller holds
// cueMu.
func (e *Engine) fillLazyLocked(addrs []string) error {
	if len(e.lazy) == 0 {
		return nil
	}
	for _, a := range addrs {
		lf, ok := e.lazy[a]
		if !ok {
			continue
		}
		delete(e.lazy, a)
		v, err := e.snapshotLocked(lf.res)
		if err != nil {
			return err
		}
		if fill := convert.Fill(v, lf.block, lf.final); len(fill) > 0 {
			e.fillFromLocked(lf.res, fill, v)
		}
	}
	return nil
}

// settleEveryLocked is settleAllLocked for a task that needs the configuration
// with the values of what it reads in it. Unifying evaluates the whole
// configuration, so the fills that the nodes left alone have not made are made
// first and unified in the same step: a task after this one that reads another
// of them finds its values there. Without this, a thousand resources that each
// read one node that was left alone would evaluate the configuration a thousand
// times. The caller holds cueMu.
func (e *Engine) settleEveryLocked() error {
	if err := e.fillLazyAllLocked(); err != nil {
		return err
	}
	return e.settleAllLocked()
}

// fillLazyAllLocked makes the fill of every node that was left alone so far.
// The nodes are taken in layers, a node after everything it reads, so that the
// body of a node whose values cannot be put in leaf by leaf is worked out after
// one unification for its layer and not after one of its own. The caller holds
// cueMu.
func (e *Engine) fillLazyAllLocked() error {
	if len(e.lazy) == 0 || e.lazyAll {
		return nil
	}
	e.lazyAll = true
	defer func() { e.lazyAll = false }()
	wait := map[string]int{}
	var layer []string
	for _, r := range e.Root.Resources {
		addr := r.Address()
		if _, ok := e.lazy[addr]; !ok {
			continue
		}
		n := 0
		for _, d := range e.deps[addr] {
			if _, ok := e.lazy[d]; ok {
				n++
			}
		}
		if n == 0 {
			layer = append(layer, addr)
		} else {
			wait[addr] = n
		}
	}
	for len(layer) > 0 {
		if err := e.fillLazyLocked(layer); err != nil {
			return err
		}
		var next []string
		for _, addr := range layer {
			for _, reader := range e.readersOf(addr) {
				if n, ok := wait[reader]; ok {
					if wait[reader] = n - 1; n == 1 {
						delete(wait, reader)
						next = append(next, reader)
					}
				}
			}
		}
		sort.Strings(next)
		layer = next
	}
	return nil
}

// fillExternalLocked makes the fills of the nodes left alone that something
// other than a task body reads: an output, the configuration of a provider, a
// plain field. The caller holds cueMu.
func (e *Engine) fillExternalLocked() error {
	if len(e.lazy) == 0 {
		return nil
	}
	if !e.externalDone {
		e.externalReads, e.externalAll = e.scanExternal()
		e.externalDone = true
	}
	var addrs []string
	for _, r := range e.Root.Resources {
		if _, ok := e.lazy[r.Address()]; ok && (e.externalAll || e.externalReads[r.Path.String()]) {
			addrs = append(addrs, r.Address())
		}
	}
	return e.fillLazyLocked(addrs)
}

// shadowCheck converts the configuration of a node that the memo answered for,
// as a plan without a memo would, and says what differs; "" when nothing does.
//
// Working out the body can unify fills into the configuration. A plan that
// checks its memo keeps them: it is then a plan that evaluates as much as one
// without a memo, and costs what that costs. With keep false all of it is put
// back, so that the walk goes on as if the check had not been made, which is
// what the tests want of every plan they make; each check then evaluates on its
// own, and a specification of thousands of resources is too large for that.
func (e *Engine) shadowCheck(r *config.Resource, p *resourcePlan, keep bool) string {
	addr := r.Address()
	e.lockCUE(e.traceCtx())
	var saved savedCue
	if !keep {
		saved = e.saveCue()
		e.shadowing = true
	}
	v, err := e.snapshotLocked(r)
	var f planFacts
	var cfg tftypes.Value
	if err == nil {
		conv := &convert.Converter{Self: r.Path, Resolve: e.resolver(), IsEphemeral: e.ephemeralCheck()}
		cfg = conv.Block(addr, p.s.Block, v)
		f.cbd, f.prevent, f.ignore, f.age = lifecycle(v)
		f.labels = labelsOf(v)
		err = conv.Err()
	}
	if !keep {
		e.shadowing = false
		e.restoreCue(saved)
	}
	e.cueMu.Unlock()
	if err != nil {
		return fmt.Sprintf("%s: the memo says its configuration is what the record holds, and converting it fails: %v", addr, err)
	}
	f.hash = e.recordedHash(p.s.Block, cfg)
	ch := p.ch
	if f.hash != ch.ConfigHash || f.cbd != ch.CreateBeforeDestroy || f.prevent != ch.PreventDestroy || f.age != p.age || !maps.Equal(f.labels, ch.Labels) {
		return fmt.Sprintf("%s: the memo says its configuration is what the record holds (hash %s), and converting it gives hash %s, create_before_destroy %v, prevent_destroy %v, refresh %q, labels %v",
			addr, ch.ConfigHash, f.hash, f.cbd, f.prevent, f.age, f.labels)
	}
	return ""
}

// savedCue is the part of the evaluation that working out a body can change.
type savedCue struct {
	current      cue.Value
	filled       bool
	pending      map[string]*pendingFill
	pendingOrder []string
	refused      error
	seeded       bool
	settles      int
	fastFills    int
	lazy         map[string]*lazyFill
	iterated     map[string]bool
}

// saveCue copies what restoreCue puts back. The caller holds cueMu.
func (e *Engine) saveCue() savedCue {
	s := savedCue{current: e.current, filled: e.filled, pendingOrder: append([]string(nil), e.pendingOrder...), refused: e.refused,
		seeded: e.seeded, settles: e.settles, fastFills: e.fastFills, lazy: maps.Clone(e.lazy)}
	if e.pending != nil {
		s.pending = make(map[string]*pendingFill, len(e.pending))
		for a, pf := range e.pending {
			cp := *pf
			s.pending[a] = &cp
		}
	}
	e.iterMu.Lock()
	s.iterated = maps.Clone(e.iterated)
	e.iterMu.Unlock()
	return s
}

func (e *Engine) restoreCue(s savedCue) {
	e.current, e.filled, e.pending, e.pendingOrder, e.refused = s.current, s.filled, s.pending, s.pendingOrder, s.refused
	e.seeded, e.settles, e.fastFills, e.lazy = s.seeded, s.settles, s.fastFills, s.lazy
	e.iterMu.Lock()
	e.iterated = s.iterated
	e.iterMu.Unlock()
}
