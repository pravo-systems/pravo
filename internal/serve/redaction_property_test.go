package serve

// A property test of the redaction model, with a generator.
//
// For each seed the generator builds a small consistent estate over a fake provider of its own: a CUE specification,
// the state records that an apply of it would have written, and the outputs. It knows the data flow, since it builds it,
// so it knows for every string whether it is public, typed (what a person wrote into a sensitive attribute), made (what a
// provider generated into a sensitive computed attribute), or derived (a copy or an embedding of a secret, by a reference
// or by a route that no reference shows: a data source that reads back a file). The oracle then asks of the viewer:
//
//   - soundness: no string that is secret or derived is in /api/graph, /api/detail, /api/fields or /api/source;
//   - precision: a public value with no data flow from a secret is shown, even when it is the same text as a typed secret
//     elsewhere (a region, a port, a short word);
//   - consistency: every surface gives the same verdict for one value of one node (detail against fields rows, outputs of
//     the graph against the detail of the output, the count of withheld values of a node against its detail).
//
// The surfaces it reads: /api/graph (nodes, counts, outputs), /api/detail (attrs, the history attrs of an update written
// with a whole diff, and, on a second viewer started with a saved plan, the attribute changes of the plan), /api/fields
// (a row for each value the detail shows, none for a withheld one), /api/source (the literals of a node and of the nodes
// it depends on), and the picker of the template wizard (/api/templates/refs, which gives the value of an attribute as
// the Inventory shows it). The choices of a wizard input read a list or a map of a node and the fake provider's data source
// has none, so they are not covered here.
//
// The rules the generator follows are the model of the viewer: a value is withheld when (1) MARK its attribute is sensitive
// or write-only, its attribute or map key is named like a secret, the schema does not know it, or the type has no schema;
// (2) READ the specification reads, by a reference, a withheld attribute of another node or a field that is derived or
// seeded (the whole top-level field); (3) MADE it contains a string that a provider made, anywhere; (4) WORLD, for a data
// source, it contains a string (typed or made) of a node it depends on, which also seeds its field for rule 2;
// (5) UNDECLARED, for a node only the state holds, it contains a typed or made string of the nodes it records as
// dependencies, or a made string. A typed string never withholds by being the same text.
//
// Replay: RP_SEED=17 go test -count=1 -run TestRPRedactionProperty ./internal/serve prints the specification, the records
// and every violation of that one seed. RP_SEEDS=300 runs more seeds, RP_VERBOSE=1 prints the detail of every failing seed.

import (
	"encoding/json"
	"fmt"
	"math/rand"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/hashicorp/terraform-plugin-go/tftypes"

	"github.com/pravo-systems/pravo/internal/config"
	"github.com/pravo-systems/pravo/internal/registry"
	"github.com/pravo-systems/pravo/internal/schema"
	"github.com/pravo-systems/pravo/internal/state"
)

// ---------------------------------------------------------------------------------------------------------------------
// The fake provider: the attributes flagged the way the aws, random, tls and local providers flag theirs.

const rpSource = "example.test/x/fake"

func rpSchemas() Schemas {
	str := json.RawMessage(`"string"`)
	num := json.RawMessage(`"number"`)
	mp := json.RawMessage(`["map","string"]`)
	blk := func(attrs ...*schema.Attribute) *schema.Schema {
		return &schema.Schema{Version: 1, Block: &schema.Block{Attributes: attrs}}
	}
	return ProviderSchemas{PS: &schema.ProviderSchema{
		Resources: map[string]*schema.Schema{
			"sec_param": blk(
				&schema.Attribute{Name: "name", TypeJSON: str, Required: true},
				&schema.Attribute{Name: "value", TypeJSON: str, Required: true, Sensitive: true},
				&schema.Attribute{Name: "arn", TypeJSON: str, Computed: true},
				&schema.Attribute{Name: "note", TypeJSON: str, Optional: true},
			),
			"rand_pw": blk(
				&schema.Attribute{Name: "length", TypeJSON: num, Optional: true},
				&schema.Attribute{Name: "result", TypeJSON: str, Computed: true, Sensitive: true},
			),
			"plain": blk(
				&schema.Attribute{Name: "name", TypeJSON: str, Required: true},
				&schema.Attribute{Name: "label", TypeJSON: str, Optional: true},
				&schema.Attribute{Name: "blob", TypeJSON: str, Optional: true, Sensitive: true},
				&schema.Attribute{Name: "url", TypeJSON: str, Optional: true},
				&schema.Attribute{Name: "tags", TypeJSON: mp, Optional: true},
				&schema.Attribute{Name: "echo", TypeJSON: str, Computed: true},
			),
			"file_w": blk(
				&schema.Attribute{Name: "path", TypeJSON: str, Required: true},
				&schema.Attribute{Name: "content", TypeJSON: str, Optional: true},
			),
		},
		DataSources: map[string]*schema.Schema{
			"file_r": blk(
				&schema.Attribute{Name: "path", TypeJSON: str, Required: true},
				&schema.Attribute{Name: "content", TypeJSON: str, Computed: true},
				&schema.Attribute{Name: "id", TypeJSON: str, Computed: true},
			),
		},
		Ephemerals: map[string]*schema.Schema{}, Functions: map[string]*schema.Function{},
	}}
}

func rpSchemaLookup(src registry.Source, version string) (Schemas, bool) {
	if src.Type == "fake" && version == "1.0.0" {
		return rpSchemas(), true
	}
	return nil, false
}

const rpSpecHeader = `package demo

$fake: {
	$kind:   "provider"
	$source: "` + rpSource + `"
	#Resource: {
		sec_param: {
			$kind: "resource", $type: "sec_param", $source: "` + rpSource + `"
			name:  string
			value: string
			arn:   string | null
			note?: string
			...
		}
		rand_pw: {
			$kind: "resource", $type: "rand_pw", $source: "` + rpSource + `"
			length?: int
			result:  string | null
			...
		}
		plain: {
			$kind: "resource", $type: "plain", $source: "` + rpSource + `"
			name:   string
			label?: string
			blob?:  string
			url?:   string
			tags?:  {[string]: string}
			echo:   string | null
			...
		}
		file_w: {
			$kind: "resource", $type: "file_w", $source: "` + rpSource + `"
			path:     string
			content?: string
			...
		}
	}
	#Data: {
		file_r: {
			$kind: "data", $type: "file_r", $source: "` + rpSource + `"
			path:    string
			content: string | null
			id:      string | null
			...
		}
	}
}

`

// rpAttrMark says that the schema withholds the attribute (sensitive here; no attribute of this provider is named like a secret).
func rpAttrMark(typ, field string) bool {
	switch typ {
	case "sec_param":
		return field == "value"
	case "rand_pw":
		return field == "result"
	case "plain":
		return field == "blob"
	}
	return false
}

// ---------------------------------------------------------------------------------------------------------------------
// The estate.

type rpRef struct{ node, field string }

// rpVal is a string as the specification writes it (cue, empty when the provider fills it in) and the record holds it.
type rpVal struct {
	cue   string
	text  string
	reads []rpRef
}

// rpLeaf is one value of one node and the verdict the model gives it. why is empty when it must be shown.
type rpLeaf struct {
	node, field, path string
	text              string
	why               string
	hidden            bool
}

type rpNode struct {
	addr, typ      string
	data, declared bool
	f              map[string]*rpVal
	nullF          map[string]bool
	tags           map[string]*rpVal
	tagKeys        []string
	guarded        map[string]bool
	length         int
	extra          map[string]string
	deps           []string
	typed, made    []string
	coincident     map[string]bool // typed texts that are also public words
	hiddenRoute    bool
	leaves         []*rpLeaf
}

type rpOutput struct {
	name string
	val  *rpVal
	why  string // "" shown, "derived" withheld
}

type rpEstate struct {
	seed    int64
	words   []string
	nodes   []*rpNode
	byAddr  map[string]*rpNode
	outputs []*rpOutput
	madeAll []string
	// aliases are plain fields of the specification that stand for an attribute of a node (_al0: a.b.value): an expression
	// reads the attribute through the name, and the ordering of the tasks sees no dependency in that.
	aliases []string
	derived map[string]map[string]bool // top-level field read from a withheld attribute, or a derived or seeded field
	seeded  map[string]map[string]bool // top-level field of a data source that rule 4 withholds
	noRead  map[string]map[string]bool // top-level field of a data source that only rule 3 withholds: no expression reads it
}

func (n *rpNode) schemaless() bool { return n.typ == "mystery" }

// onlyMarks says that every value of n that is withheld is withheld by what it is (the schema, the name), not by what it reads
// or contains.
func (n *rpNode) onlyMarks() bool {
	for _, l := range n.leaves {
		if l.why != "" && l.why != "mark" && l.why != "name" && l.why != "schema" {
			return false
		}
	}
	return true
}

func (n *rpNode) addDep(a string) {
	if a == n.addr {
		return
	}
	for _, d := range n.deps {
		if d == a {
			return
		}
	}
	n.deps = append(n.deps, a)
	sort.Strings(n.deps)
}

// closure lists the nodes n depends on, directly or not.
func (e *rpEstate) closure(n *rpNode) []*rpNode {
	seen := map[string]bool{n.addr: true}
	var out []*rpNode
	var walk func(m *rpNode)
	walk = func(m *rpNode) {
		for _, d := range m.deps {
			if seen[d] || e.byAddr[d] == nil {
				continue
			}
			seen[d] = true
			out = append(out, e.byAddr[d])
			walk(e.byAddr[d])
		}
	}
	walk(n)
	return out
}

// closureSecrets are the typed and made strings, long enough to be looked for, of the nodes n depends on.
func (e *rpEstate) closureSecrets(n *rpNode) []string {
	var out []string
	for _, m := range e.closure(n) {
		for _, s := range m.typed {
			if len(s) >= 6 {
				out = append(out, s)
			}
		}
		out = append(out, m.made...)
	}
	return out
}

func rpContainsAny(text string, list []string) bool {
	for _, s := range list {
		if s != "" && strings.Contains(text, s) {
			return true
		}
	}
	return false
}

func (e *rpEstate) mark(m map[string]map[string]bool, addr, field string) {
	if m[addr] == nil {
		m[addr] = map[string]bool{}
	}
	m[addr][field] = true
}

// judge works out the verdict of every value of n. It only looks at nodes created before n, so it is final when called.
func (e *rpEstate) judge(n *rpNode) {
	n.leaves = nil
	clo := e.closureSecrets(n)
	reads := map[string][]rpRef{}
	for name, v := range n.f {
		reads[name] = v.reads
	}
	if len(n.tags) > 0 {
		var rs []rpRef
		for _, k := range n.tagKeys {
			rs = append(rs, n.tags[k].reads...)
		}
		reads["tags"] = rs
	}
	if n.declared {
		for field, rs := range reads {
			for _, r := range rs {
				if rn := e.byAddr[r.node]; rn != nil && (rpAttrMark(rn.typ, r.field) || e.derived[r.node][r.field] || e.seeded[r.node][r.field]) {
					e.mark(e.derived, n.addr, field)
				}
			}
		}
	}
	verdict := func(field, path, text string) *rpLeaf {
		l := &rpLeaf{node: n.addr, field: field, path: path, text: text}
		switch {
		case n.schemaless():
			l.why = "schema"
		case rpAttrMark(n.typ, field):
			l.why = "mark"
		case n.declared && e.derived[n.addr][field]:
			l.why = "derived"
		case n.data && n.declared && rpContainsAny(text, clo):
			// rule 4 comes first: a made string of a node it depends on is also a string of the world it reads
			l.why = "world"
			l.hidden = true
			e.mark(e.seeded, n.addr, field)
		case rpContainsAny(text, e.madeAll):
			l.why = "made"
			if n.data {
				// only rule 3 withholds it; whether such a field seeds the fields that read it is not said, so nothing reads it
				l.hidden = true
				e.mark(e.noRead, n.addr, field)
			}
		case !n.declared && rpContainsAny(text, clo):
			l.why = "undeclared"
		}
		n.leaves = append(n.leaves, l)
		return l
	}
	var names []string
	for name := range n.f {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		verdict(name, name, n.f[name].text)
	}
	if n.typ == "rand_pw" {
		verdict("length", "length", strconv.Itoa(n.length))
	}
	for _, k := range n.tagKeys {
		l := verdict("tags", "tags."+k, n.tags[k].text)
		if n.guarded[k] {
			l.why = "name"
		}
	}
	var extra []string
	for k := range n.extra {
		extra = append(extra, k)
	}
	sort.Strings(extra)
	for _, k := range extra {
		verdict(k, k, n.extra[k]).why = "schema"
	}
}

// ---------------------------------------------------------------------------------------------------------------------
// The generator.

var rpWords = []string{"eu-north-1", "us-east-2", "production", "postgres", "localhost", "5432", "admin", "hello"}

type rpGen struct {
	r   *rand.Rand
	e   *rpEstate
	ctr int
	cur *rpNode // the node being built: it reads the others, never itself
}

func rpAlnum(r *rand.Rand, n int) string {
	const a = "abcdefghjkmnpqrstuvwxyzABCDEFGHJKLMNPQRSTUVWXYZ23456789"
	b := make([]byte, n)
	for i := range b {
		b[i] = a[r.Intn(len(a))]
	}
	return string(b)
}

func (g *rpGen) uniq(prefix string) string {
	g.ctr++
	return fmt.Sprintf("%s%d-%d-%s", prefix, g.e.seed, g.ctr, rpAlnum(g.r, 6))
}

func (g *rpGen) word() string { return g.e.words[g.r.Intn(len(g.e.words))] }

func (g *rpGen) pct(p int) bool { return g.r.Intn(100) < p }

// pubText is a public value, often one that is the same text as a typed secret of an unrelated node.
func (g *rpGen) pubText(addr, field string) string {
	switch g.r.Intn(7) {
	case 0, 1:
		return g.word()
	case 2:
		return "https://db." + g.word() + ".example/x"
	case 3:
		return "arn:aws:s3:::" + g.word() + "-bucket"
	case 4:
		return "zones:" + g.word() + "a," + g.word() + "b"
	case 5:
		return "host-" + g.word() + ":" + g.word()
	}
	return "pub-" + addr + "-" + field
}

func (g *rpGen) typedSecret() (string, bool) {
	if g.pct(55) {
		return g.word(), true
	}
	return g.uniq("tk-"), false
}

type rpCand struct {
	node   *rpNode
	field  string
	text   string
	cue    string
	secret bool
}

// cands lists the string values of the declared nodes that an expression can read. A secret one is a sensitive attribute,
// a derived field or a seeded field.
func (g *rpGen) cands(onlyPublic bool) []rpCand {
	var out []rpCand
	for _, m := range g.e.nodes {
		if !m.declared || m == g.cur {
			continue
		}
		names := make([]string, 0, len(m.f))
		for name := range m.f {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			if g.e.noRead[m.addr][name] {
				continue
			}
			v := m.f[name]
			secret := rpAttrMark(m.typ, name) || g.e.derived[m.addr][name] || g.e.seeded[m.addr][name]
			if onlyPublic && secret {
				continue
			}
			out = append(out, rpCand{node: m, field: name, text: v.text, cue: m.addr + "." + name, secret: secret})
		}
		hasGuard := len(m.guarded) > 0
		if !hasGuard {
			for _, k := range m.tagKeys {
				secret := g.e.derived[m.addr]["tags"]
				if onlyPublic && secret {
					continue
				}
				out = append(out, rpCand{node: m, field: "tags", text: m.tags[k].text, cue: m.addr + ".tags." + k, secret: secret})
			}
		}
	}
	return out
}

func (g *rpGen) madeCands() []rpCand {
	var out []rpCand
	for _, m := range g.e.nodes {
		if m.typ == "rand_pw" {
			out = append(out, rpCand{node: m, field: "result", text: m.f["result"].text, cue: m.addr + ".result", secret: true})
		}
	}
	return out
}

// compose writes an expression that reads one or two of the candidates: a bare reference, or an interpolation after a
// prefix that no other value has, so that the text of the result is never the text of a public value.
func (g *rpGen) compose(n *rpNode, field string, cs []rpCand, maxParts int) *rpVal {
	if len(cs) == 0 {
		return nil
	}
	k := 1 + g.r.Intn(maxParts)
	var picked []rpCand
	for range k {
		picked = append(picked, cs[g.r.Intn(len(cs))])
	}
	v := &rpVal{}
	for _, c := range picked {
		v.reads = append(v.reads, rpRef{c.node.addr, c.field})
		n.addDep(c.node.addr)
	}
	if k == 1 && picked[0].secret && strings.HasPrefix(picked[0].text, "Pw") && g.pct(40) {
		v.cue, v.text = g.viaAlias(picked[0].cue), picked[0].text
		return v
	}
	prefix := n.addr + "-" + field + "="
	var b strings.Builder
	b.WriteString(`"` + prefix)
	v.text = prefix
	for i, c := range picked {
		if i > 0 {
			b.WriteString("/")
			v.text += "/"
		}
		b.WriteString(`\(` + g.viaAlias(c.cue) + `)`)
		v.text += c.text
	}
	b.WriteString(`"`)
	v.cue = b.String()
	return v
}

// viaAlias is the expression, or a name for it that the specification declares once: a fifth of the references go through
// one.
func (g *rpGen) viaAlias(cue string) string {
	if !g.pct(20) {
		return cue
	}
	name := "_al" + strconv.Itoa(len(g.e.aliases))
	g.e.aliases = append(g.e.aliases, name+": "+cue)
	return name
}

// valOrRef is a public literal, or an expression that reads other nodes (secret ones too when allowSecret).
func (g *rpGen) valOrRef(n *rpNode, field string, pRef int, allowSecret bool) *rpVal {
	if g.pct(pRef) {
		if cs := g.cands(!allowSecret || !g.pct(65)); len(cs) > 0 {
			return g.compose(n, field, cs, 2)
		}
	}
	t := g.pubText(n.addr, field)
	return &rpVal{cue: strconv.Quote(t), text: t}
}

// typedVal is what a person writes into a sensitive attribute: a literal, or the made string of a resource.
func (g *rpGen) typedVal(n *rpNode) *rpVal {
	if ms := g.madeCands(); len(ms) > 0 && g.pct(25) {
		c := ms[g.r.Intn(len(ms))]
		n.addDep(c.node.addr)
		n.typed = append(n.typed, c.text)
		return &rpVal{cue: c.cue, text: c.text, reads: []rpRef{{c.node.addr, c.field}}}
	}
	t, coin := g.typedSecret()
	if coin {
		n.coincident[t] = true
	}
	n.typed = append(n.typed, t)
	return &rpVal{cue: strconv.Quote(t), text: t}
}

func (g *rpGen) newNode(typ string, data bool) *rpNode {
	n := &rpNode{addr: "n" + strconv.Itoa(len(g.e.nodes)), typ: typ, data: data, declared: true,
		f: map[string]*rpVal{}, nullF: map[string]bool{}, tags: map[string]*rpVal{}, guarded: map[string]bool{}, extra: map[string]string{}, coincident: map[string]bool{}}
	g.e.nodes = append(g.e.nodes, n)
	g.e.byAddr[n.addr] = n
	g.cur = n
	return n
}

func (g *rpGen) lit(n *rpNode, field, text string) {
	n.f[field] = &rpVal{cue: strconv.Quote(text), text: text}
}

func (g *rpGen) fillSecParam(n *rpNode) {
	g.lit(n, "name", n.addr)
	n.f["value"] = g.typedVal(n)
	region := "eu-west-1"
	if n.coincident[n.f["value"].text] {
		region = n.f["value"].text
	}
	n.f["arn"] = &rpVal{text: "arn:aws:ssm:" + region + ":000000000000:parameter/" + n.addr}
	if g.pct(65) {
		n.f["note"] = g.valOrRef(n, "note", 45, true)
	} else {
		n.nullF["note"] = true
	}
}

func (g *rpGen) fillRandPw(n *rpNode) {
	n.length = 16 + g.r.Intn(16)
	text := fmt.Sprintf("Pw%d-%d-%s", g.e.seed, len(g.e.nodes), rpAlnum(g.r, 12))
	n.f["result"] = &rpVal{text: text}
	n.made = append(n.made, text)
	g.e.madeAll = append(g.e.madeAll, text)
}

func (g *rpGen) fillPlain(n *rpNode) {
	g.lit(n, "name", n.addr)
	if g.pct(70) {
		n.f["label"] = g.valOrRef(n, "label", 40, true)
	} else {
		n.nullF["label"] = true
	}
	if g.pct(55) {
		n.f["blob"] = g.typedVal(n)
	} else {
		n.nullF["blob"] = true
	}
	if g.pct(55) {
		n.f["url"] = g.valOrRef(n, "url", 35, true)
	}
	echo := "echo:" + n.addr
	if b := n.f["blob"]; b != nil && n.coincident[b.text] {
		echo += ":" + b.text
	}
	n.f["echo"] = &rpVal{text: echo}
	if g.pct(75) {
		for i, cnt := 1, 1+g.r.Intn(3); i <= cnt; i++ {
			k := "k" + strconv.Itoa(i)
			v := g.valOrRef(n, "tags."+k, 40, true)
			n.tagKeys = append(n.tagKeys, k)
			n.tags[k] = v
		}
		if g.pct(25) {
			k := []string{"api_token", "db_password", "client_secret"}[g.r.Intn(3)]
			n.tagKeys = append(n.tagKeys, k)
			n.guarded[k] = true
			t := g.uniq("tg-")
			n.tags[k] = &rpVal{cue: strconv.Quote(t), text: t}
		}
	}
	if g.pct(15) {
		n.extra["legacy"] = g.uniq("lg-")
	}
}

func (g *rpGen) fillFileW(n *rpNode) {
	g.lit(n, "path", "/tmp/"+n.addr)
	n.f["content"] = g.valOrRef(n, "content", 80, true)
}

// fillFileR fills a data source. A data source that is not hidden reads nothing that holds a secret it would show.
func (g *rpGen) fillFileR(n *rpNode, hidden bool, via *rpNode, secret string) {
	n.f["id"] = &rpVal{text: "file:" + n.addr}
	var path *rpVal
	switch {
	case via != nil:
		field := "path"
		if via.typ == "sec_param" || via.typ == "plain" {
			field = "name"
		}
		path = &rpVal{cue: `"\(` + via.addr + "." + field + `).out"`, text: via.f[field].text + ".out", reads: []rpRef{{via.addr, field}}}
		n.addDep(via.addr)
	case g.pct(50):
		if cs := g.cands(true); len(cs) > 0 {
			path = g.compose(n, "path", cs, 1)
		}
	}
	if path == nil {
		t := "/srv/" + n.addr
		path = &rpVal{cue: strconv.Quote(t), text: t}
	}
	clo := g.e.closureSecrets(n)
	if rpContainsAny(path.text, clo) {
		t := "/srv/" + n.addr
		path = &rpVal{cue: strconv.Quote(t), text: t}
		n.deps = nil
		if via != nil {
			n.addDep(via.addr)
		}
		clo = g.e.closureSecrets(n)
	}
	n.f["path"] = path
	switch {
	case hidden:
		n.hiddenRoute = true
		n.f["content"] = &rpVal{text: "dump:" + n.addr + ":" + secret}
	case g.pct(30) && len(g.e.madeAll) > 0:
		// a made string of a node it does not depend on: the made rule alone withholds it
		m := g.e.madeAll[g.r.Intn(len(g.e.madeAll))]
		n.f["content"] = &rpVal{text: "dump:" + n.addr + ":" + m}
		if rpContainsAny(n.f["content"].text, clo) {
			n.hiddenRoute = true
		}
	default:
		t := "zones:" + g.word() + "a," + g.word() + "b"
		if rpContainsAny(t, clo) {
			t = "plain:" + n.addr
		}
		n.f["content"] = &rpVal{text: t}
	}
}

// secretHolders lists the nodes with a made string or a typed secret that is long enough to be looked for.
func (g *rpGen) secretHolders() []*rpNode {
	var out []*rpNode
	for _, m := range g.e.nodes {
		if !m.declared {
			continue
		}
		ok := len(m.made) > 0
		for _, s := range m.typed {
			ok = ok || len(s) >= 6
		}
		if ok {
			out = append(out, m)
		}
	}
	return out
}

// addHidden builds the hidden route: a data source whose content embeds a secret of a node it depends on, through a chain of
// references that does not mention the field that holds the secret.
func (g *rpGen) addHidden() {
	hs := g.secretHolders()
	if len(hs) == 0 {
		pw := g.newNode("rand_pw", false)
		g.fillRandPw(pw)
		g.e.judge(pw)
		hs = []*rpNode{pw}
	}
	s := hs[g.r.Intn(len(hs))]
	var secrets []string
	secrets = append(secrets, s.made...)
	for _, t := range s.typed {
		if len(t) >= 6 {
			secrets = append(secrets, t)
		}
	}
	secret := secrets[g.r.Intn(len(secrets))]
	var via *rpNode
	if s.typ == "rand_pw" || g.pct(60) {
		// a file that a resource writes: it reads the secret, and the data source reads the file by its path
		via = g.newNode("file_w", false)
		g.lit(via, "path", "/tmp/"+via.addr)
		attr := "result"
		switch s.typ {
		case "sec_param":
			attr = "value"
		case "plain":
			attr = "blob"
		}
		cue := g.viaAlias(s.addr + "." + attr)
		via.f["content"] = &rpVal{cue: `"` + via.addr + `-content=\(` + cue + `)"`, text: via.addr + "-content=" + s.f[attr].text, reads: []rpRef{{s.addr, attr}}}
		via.addDep(s.addr)
		g.e.judge(via)
	} else {
		via = s
	}
	r := g.newNode("file_r", true)
	g.fillFileR(r, true, via, secret)
	g.e.judge(r)
	if g.pct(75) {
		// a resource that reads what the data source found: the text is the secret, and the reference is the only way to know
		rd := g.newNode("file_w", false)
		g.lit(rd, "path", "/tmp/"+rd.addr)
		rd.f["content"] = g.compose(rd, "content", []rpCand{{node: r, field: "content", text: r.f["content"].text, cue: r.addr + ".content", secret: true}}, 1)
		g.e.judge(rd)
	}
}

func (g *rpGen) addNode() {
	typs := []string{"sec_param", "sec_param", "rand_pw", "rand_pw", "plain", "plain", "plain", "file_w", "file_w", "file_w", "file_r"}
	typ := typs[g.r.Intn(len(typs))]
	if typ == "file_r" && len(g.e.nodes) < 2 {
		typ = "plain"
	}
	n := g.newNode(typ, typ == "file_r")
	switch typ {
	case "sec_param":
		g.fillSecParam(n)
	case "rand_pw":
		g.fillRandPw(n)
	case "plain":
		g.fillPlain(n)
	case "file_w":
		g.fillFileW(n)
	case "file_r":
		g.fillFileR(n, false, nil, "")
	}
	g.e.judge(n)
}

func (g *rpGen) addOutputs() {
	g.cur = nil
	for i, cnt := 0, 1+g.r.Intn(2); i < cnt; i++ {
		name := "o" + strconv.Itoa(i)
		cs := g.cands(g.pct(35))
		if len(cs) == 0 {
			continue
		}
		holder := &rpNode{addr: name, deps: nil}
		v := g.compose(holder, "value", cs, 2)
		o := &rpOutput{name: name, val: v}
		for _, r := range v.reads {
			if rn := g.e.byAddr[r.node]; rn != nil && (rpAttrMark(rn.typ, r.field) || g.e.derived[r.node][r.field] || g.e.seeded[r.node][r.field]) {
				o.why = "derived"
			}
		}
		if rpContainsAny(v.text, g.e.madeAll) {
			o.why = "derived"
		}
		g.e.outputs = append(g.e.outputs, o)
	}
}

// addGhosts adds records the specification does not declare, and one whose type has no schema.
func (g *rpGen) addGhosts() {
	if g.pct(55) && len(g.e.nodes) > 0 && len(g.e.nodes) < 9 {
		n := &rpNode{addr: "g0", typ: "file_w", declared: false, f: map[string]*rpVal{}, nullF: map[string]bool{}, tags: map[string]*rpVal{}, guarded: map[string]bool{}, extra: map[string]string{}, coincident: map[string]bool{}}
		for i, cnt := 0, 1+g.r.Intn(2); i < cnt; i++ {
			n.addDep(g.e.nodes[g.r.Intn(len(g.e.nodes))].addr)
		}
		g.e.nodes = append(g.e.nodes, n)
		g.e.byAddr[n.addr] = n
		g.lit(n, "path", "/tmp/g0")
		n.f["path"].cue = ""
		clo := g.e.closureSecrets(n)
		text := "ghost-g0:" + g.word()
		switch {
		case len(clo) > 0 && g.pct(50):
			text = "ghost-g0:" + clo[g.r.Intn(len(clo))]
		case len(g.e.madeAll) > 0 && g.pct(30):
			text = "ghost-g0:" + g.e.madeAll[g.r.Intn(len(g.e.madeAll))]
		}
		n.f["content"] = &rpVal{text: text}
		g.e.judge(n)
	}
	if g.pct(25) && len(g.e.nodes) < 9 {
		n := &rpNode{addr: "g1", typ: "mystery", declared: false, f: map[string]*rpVal{}, nullF: map[string]bool{}, tags: map[string]*rpVal{}, guarded: map[string]bool{}, extra: map[string]string{}, coincident: map[string]bool{}}
		n.f["a"] = &rpVal{text: g.uniq("my-")}
		n.f["b"] = &rpVal{text: "plainly-public-" + n.addr}
		g.e.nodes = append(g.e.nodes, n)
		g.e.byAddr[n.addr] = n
		g.e.judge(n)
	}
}

func rpGenerate(seed int64) *rpEstate {
	r := rand.New(rand.NewSource(seed))
	e := &rpEstate{seed: seed, byAddr: map[string]*rpNode{}, derived: map[string]map[string]bool{}, seeded: map[string]map[string]bool{}, noRead: map[string]map[string]bool{}}
	for len(e.words) < 2 {
		w := rpWords[r.Intn(len(rpWords))]
		if len(e.words) == 0 || e.words[0] != w {
			e.words = append(e.words, w)
		}
	}
	g := &rpGen{r: r, e: e}
	for i, k := 0, 2+r.Intn(2); i < k; i++ {
		g.addNode()
	}
	if g.pct(70) {
		g.addHidden()
	}
	// nodes made after the hidden route can read what it found
	for i, k := 0, 1+r.Intn(2); i < k; i++ {
		g.addNode()
	}
	for len(e.nodes) < 4 {
		g.addNode()
	}
	g.addOutputs()
	g.addGhosts()
	return e
}

// ---------------------------------------------------------------------------------------------------------------------
// What the estate looks like to the project: the specification, the records and the outputs.

func (n *rpNode) spec() string {
	var parts []string
	set := func(name string) {
		if v := n.f[name]; v != nil && v.cue != "" {
			parts = append(parts, "\t"+name+": "+v.cue)
		}
	}
	switch n.typ {
	case "sec_param":
		set("name")
		set("value")
		set("note")
	case "rand_pw":
		parts = append(parts, "\tlength: "+strconv.Itoa(n.length))
	case "plain":
		set("name")
		set("label")
		set("blob")
		set("url")
		if len(n.tagKeys) > 0 {
			var tg []string
			for _, k := range n.tagKeys {
				tg = append(tg, "\t\t"+strconv.Quote(k)+": "+n.tags[k].cue)
			}
			parts = append(parts, "\ttags: {\n"+strings.Join(tg, "\n")+"\n\t}")
		}
	case "file_w":
		set("path")
		set("content")
	case "file_r":
		set("path")
	}
	def := "$fake.#Resource." + n.typ
	if n.data {
		def = "$fake.#Data." + n.typ
	}
	return n.addr + ": " + def + " & {\n" + strings.Join(parts, "\n") + "\n}\n"
}

// rpFieldLevel says that the verdict is about the field and not about the text of one value: the schema, the name, or what the
// specification reads. A former value of such a field is withheld as the current one is; a former value of a field that is
// withheld for the text it holds (a made string, a string of a node the data source reads) says nothing of itself.
func rpFieldLevel(why string) bool {
	switch why {
	case "mark", "name", "schema", "derived":
		return true
	}
	return false
}

// rpOld is a former value of a withheld value: a provider made another string before, or the person typed another. It shares no
// text with the current one, so that only what the model knows about the field can withhold it.
func rpOld(s string) string {
	b := []byte(s)
	for i, j := 0, len(b)-1; i < j; i, j = i+1, j-1 {
		b[i], b[j] = b[j], b[i]
	}
	return "old:" + string(b)
}

func (n *rpNode) attrsJSON() string { return n.attrsJSONAt(false) }

// attrsJSONAt is the record; old gives the record of the revision before, in which every value that is withheld was another.
func (n *rpNode) attrsJSONAt(old bool) string {
	m := map[string]any{}
	oldOf := map[string]string{}
	if old {
		for _, l := range n.leaves {
			if rpFieldLevel(l.why) {
				oldOf[l.path] = rpOld(l.text)
			}
		}
	}
	txt := func(path, text string) string {
		if o, ok := oldOf[path]; ok {
			return o
		}
		return text
	}
	for k, v := range n.f {
		m[k] = txt(k, v.text)
	}
	for k := range n.nullF {
		if _, ok := m[k]; !ok {
			m[k] = nil
		}
	}
	if len(n.tagKeys) > 0 {
		tm := map[string]any{}
		for k, v := range n.tags {
			tm[k] = txt("tags."+k, v.text)
		}
		m["tags"] = tm
	}
	if n.typ == "rand_pw" {
		m["length"] = n.length
	}
	for k, v := range n.extra {
		m[k] = txt(k, v)
	}
	b, _ := json.Marshal(m)
	return string(b)
}

func (e *rpEstate) specText() string {
	var b strings.Builder
	b.WriteString(rpSpecHeader)
	for _, a := range e.aliases {
		b.WriteString(a + "\n")
	}
	for _, n := range e.nodes {
		if n.declared {
			b.WriteString(n.spec())
		}
	}
	for _, o := range e.outputs {
		b.WriteString(o.name + `: {$kind: "output", value: ` + o.val.cue + "}\n")
	}
	return b.String()
}

func rpRecord(n *rpNode, old bool) *state.Resource {
	now := time.Now().UTC()
	mode := "managed"
	if n.data {
		mode = "data"
	}
	return &state.Resource{Addr: n.addr, Mode: mode, Type: n.typ, Name: n.addr, Provider: rpSource, ProviderKey: "fake", ProviderVersion: "1.0.0",
		Attributes: json.RawMessage(n.attrsJSONAt(old)), Private: []byte("PRIVATE-rp-blob"), Dependencies: n.deps, ConfigHash: "cfg-rp-hash", RefreshedAt: &now, AppliedAt: &now}
}

func (e *rpEstate) recordsText() string {
	var b strings.Builder
	for _, n := range e.nodes {
		mode := "managed"
		if n.data {
			mode = "data"
		}
		fmt.Fprintf(&b, "%s %s %s deps=%v declared=%v\n    %s\n", mode, n.addr, n.typ, n.deps, n.declared, n.attrsJSON())
	}
	for _, o := range e.outputs {
		fmt.Fprintf(&b, "output %s = %q\n", o.name, o.val.text)
	}
	return b.String()
}

// rpNew is what a saved plan would write into a field whose value is withheld: another secret.
func rpNew(s string) string { return "new:" + rpOld(s)[4:] }

// rpPlanValue builds a tftypes value of a type from plain data; a missing attribute is null.
func rpPlanValue(t tftypes.Type, v any) tftypes.Value {
	if v == nil {
		return tftypes.NewValue(t, nil)
	}
	switch {
	case t.Is(tftypes.Object{}):
		m := v.(map[string]any)
		out := map[string]tftypes.Value{}
		for k, at := range t.(tftypes.Object).AttributeTypes {
			out[k] = rpPlanValue(at, m[k])
		}
		return tftypes.NewValue(t, out)
	case t.Is(tftypes.Map{}):
		out := map[string]tftypes.Value{}
		for k, e := range v.(map[string]any) {
			out[k] = rpPlanValue(t.(tftypes.Map).ElementType, e)
		}
		return tftypes.NewValue(t, out)
	}
	return tftypes.NewValue(t, v)
}

// planMaps gives the record as the state holds it (prior) and as a saved plan would leave it (planned): a public value has
// a new public text, and a value that is withheld because of what its field is has a new secret.
func (n *rpNode) planMaps() (prior, planned map[string]any) {
	prior, planned = map[string]any{}, map[string]any{}
	_ = json.Unmarshal([]byte(n.attrsJSON()), &prior)
	_ = json.Unmarshal([]byte(n.attrsJSON()), &planned)
	for _, l := range n.leaves {
		var next string
		switch {
		case l.why == "":
			next = l.text + "~plan"
		case rpFieldLevel(l.why):
			next = rpNew(l.text)
		default:
			continue
		}
		if k := strings.TrimPrefix(l.path, "tags."); k != l.path {
			if m, ok := planned["tags"].(map[string]any); ok {
				m[k] = next
			}
			continue
		}
		if _, ok := planned[l.path].(string); ok {
			planned[l.path] = next
		}
	}
	return prior, planned
}

// rpWritePlan writes a saved plan that updates every declared managed resource.
func rpWritePlan(t *testing.T, dir string, st *state.Store, e *rpEstate) string {
	t.Helper()
	root, err := config.Load(dir, config.LoadOptions{})
	if err != nil {
		t.Fatal(err)
	}
	fp, err := root.Fingerprint()
	if err != nil {
		t.Fatal(err)
	}
	pack := func(typ tftypes.Type, v any) []byte {
		b, err := rpPlanValue(typ, v).MarshalMsgPack(typ)
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	var changes []map[string]any
	for _, n := range e.nodes {
		if !n.declared || n.data {
			continue
		}
		typ := rpSchemas().Type("managed", n.typ).Block.Type()
		prior, planned := n.planMaps()
		rec := st.Get(n.addr)
		changes = append(changes, map[string]any{"addr": n.addr, "mode": "managed", "type": n.typ, "name": n.addr, "action": "update", "provider_key": "fake",
			"prior": pack(typ, prior), "planned": pack(typ, planned), "config": pack(typ, planned),
			"reasons": []string{"the specification changed"}, "prior_revision": rec.Revision, "prior_generation": rec.Generation})
	}
	doc := map[string]any{"format": 1, "created": time.Now().UTC(), "command": "pravo plan -out plan.pravo", "lineage": st.Lineage(), "config_fingerprint": fp,
		"providers": map[string]string{"fake": "1.0.0"}, "actor": "ci@example", "stats": map[string]any{"total": len(e.nodes)}, "changes": changes}
	data, err := json.MarshalIndent(doc, "", " ")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "plan.pravo")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// rpProject writes the estate to a project and runs the viewer over it.
func rpProject(t *testing.T, e *rpEstate) *project { return rpBuild(t, e, false) }

// rpBuild is rpProject, with a saved plan laid over the graph when withPlan: a plan counts the values it would write as
// secrets, so the checks of the estate itself run without one and the plan has a viewer of its own.
func rpBuild(t *testing.T, e *rpEstate, withPlan bool) *project {
	t.Helper()
	dir := t.TempDir()
	writeSpec(t, dir, e.specText())
	st, err := state.Open(filepath.Join(dir, "pravo.state"))
	if err != nil {
		t.Fatal(err)
	}
	// two applies: the first wrote other values wherever a value is withheld, the second wrote the estate, so that every
	// record has a history whose changes the viewer has to judge as it judges the record
	for pass := range 2 {
		txn := st.Begin("apply", "pravo apply")
		for _, n := range e.nodes {
			r := rpRecord(n, pass == 0)
			before := r.Revision
			if err := st.Set(r, txn.ID()); err != nil {
				t.Fatal(err)
			}
			action := "create"
			if pass == 1 {
				action = "update"
			}
			txn.Record(r.Address(), action, before, r.Revision)
			if pass == 1 {
				// the diff is kept whole (no withhold function): the viewer has to judge every value in it
				txn.RecordDiff(r.Address(), json.RawMessage(n.attrsJSONAt(true)), r.Attributes, nil)
			}
		}
		if err := txn.Finish("applied", fmt.Sprintf("%d written", len(e.nodes)), true); err != nil {
			t.Fatal(err)
		}
	}
	outs := map[string]json.RawMessage{}
	for _, o := range e.outputs {
		b, _ := json.Marshal(o.val.text)
		outs[o.name] = b
	}
	if err := st.SetOutputs(outs); err != nil {
		t.Fatal(err)
	}
	opts := Options{Schemas: rpSchemaLookup}
	if withPlan {
		opts.PlanFile = rpWritePlan(t, dir, st, e)
	}
	return start(t, dir, st, opts)
}

// mustHide lists the strings that no response may carry: the text of every value that is withheld, and every typed secret
// that is not a public word and every made string, less what a shown value contains (the same text elsewhere is allowed).
func (e *rpEstate) mustHide() []string {
	var shown []string
	for _, n := range e.nodes {
		for _, l := range n.leaves {
			if l.why == "" {
				shown = append(shown, l.text)
			}
		}
	}
	for _, o := range e.outputs {
		if o.why == "" {
			shown = append(shown, o.val.text)
		}
	}
	seen := map[string]bool{}
	var out []string
	add := func(s string) {
		if len(s) < 6 || seen[s] {
			return
		}
		for _, sh := range shown {
			if strings.Contains(sh, s) {
				return
			}
		}
		seen[s] = true
		out = append(out, s)
	}
	for _, n := range e.nodes {
		for _, l := range n.leaves {
			if l.why != "" {
				add(l.text)
				if rpFieldLevel(l.why) {
					add(rpOld(l.text))
					add(rpNew(l.text))
				}
			}
		}
		for _, s := range n.typed {
			add(s)
		}
		for _, s := range n.made {
			add(s)
		}
	}
	for _, o := range e.outputs {
		if o.why != "" {
			add(o.val.text)
		}
	}
	sort.Strings(out)
	return out
}

// ---------------------------------------------------------------------------------------------------------------------
// The oracle.

type rpFail struct {
	class, node, path, msg string
}

type rpSeen struct {
	withheld, null, unknown bool
	why                     string
	text                    string
}

func rpFlatten(path string, a *Attr, out map[string]rpSeen) {
	if a.Redacted != "" {
		out[path] = rpSeen{withheld: true, why: a.Redacted}
		return
	}
	if len(a.Children) > 0 {
		for i := range a.Children {
			rpFlatten(path+"."+a.Children[i].Name, &a.Children[i], out)
		}
		return
	}
	s := rpSeen{null: a.Null, unknown: a.Unknown}
	if a.Value != nil {
		s.text = fmt.Sprint(a.Value)
	}
	out[path] = s
}

func rpFind(seen map[string]rpSeen, path string) (rpSeen, bool) {
	for p := path; ; {
		if s, ok := seen[p]; ok {
			return s, true
		}
		i := strings.LastIndex(p, ".")
		if i < 0 {
			return rpSeen{}, false
		}
		p = p[:i]
	}
}

func rpCountWithheld(attrs []Attr) int {
	n := 0
	for i := range attrs {
		a := &attrs[i]
		switch {
		case a.Redacted != "" && !a.Null:
			n++
		case a.Redacted == "":
			n += rpCountWithheld(a.Children)
		}
	}
	return n
}

// rpStats counts what the oracle looked at, so that a run that compared nothing does not pass. The seeds run one after the
// other.
var rpStats = map[string]int{}

type rpCheck struct {
	t     *testing.T
	e     *rpEstate
	p     *project
	fails []rpFail
}

func (c *rpCheck) fail(class, node, path, format string, args ...any) {
	c.fails = append(c.fails, rpFail{class: class, node: node, path: path, msg: fmt.Sprintf(format, args...)})
}

func (c *rpCheck) fieldsHas(addr, path, text string) bool {
	c.t.Helper()
	q := url.QueryEscape(path + "=" + strings.ToLower(text))
	var f Fields
	if err := json.Unmarshal(c.p.get("/api/fields?q="+q), &f); err != nil {
		c.t.Fatal(err)
	}
	rpStats["fields-queries"]++
	for _, a := range f.Addrs {
		if a == addr {
			return true
		}
	}
	return false
}

func (c *rpCheck) scan(class, node, what string, body []byte, secrets []string) {
	s := string(body)
	for _, sec := range secrets {
		if strings.Contains(s, sec) {
			c.fail(class, node, what, "%s carries %q", what, sec)
		}
	}
}

func rpRun(t *testing.T, e *rpEstate) []rpFail {
	t.Helper()
	p := rpProject(t, e)
	c := &rpCheck{t: t, e: e, p: p}
	hide := e.mustHide()

	graphBody := p.get("/api/graph")
	var g Graph
	if err := json.Unmarshal(graphBody, &g); err != nil {
		t.Fatal(err)
	}
	if g.Project.SpecError != "" {
		t.Fatalf("the generated specification does not load: %s\n%s", g.Project.SpecError, e.specText())
	}
	c.scan("soundness/graph", "", "/api/graph", graphBody, hide)
	gnodes := map[string]Node{}
	for _, n := range g.Nodes {
		gnodes[n.Addr] = n
	}
	gouts := map[string]Output{}
	for _, o := range g.Outputs {
		gouts[o.Name] = o
	}

	for _, n := range e.nodes {
		gn, ok := gnodes[n.addr]
		if !ok {
			t.Fatalf("the graph has no node %s", n.addr)
		}
		if want := map[bool]string{true: "both", false: "state"}[n.declared]; gn.Presence != want {
			t.Fatalf("node %s: presence %q, want %q (the estate is not what the model sees)\n%s", n.addr, gn.Presence, want, e.specText())
		}
		body := p.get("/api/detail?addr=" + queryEscape(n.addr))
		var d Detail
		if err := json.Unmarshal(body, &d); err != nil {
			t.Fatal(err)
		}
		c.scan("soundness/detail", n.addr, "/api/detail", body, hide)
		seen := map[string]rpSeen{}
		for i := range d.Attrs {
			rpFlatten(d.Attrs[i].Name, &d.Attrs[i], seen)
		}
		if !n.schemaless() {
			if want, have := rpCountWithheld(d.Attrs), gn.Sensitive; want != have {
				class := "consistency/count"
				if n.onlyMarks() {
					class = "consistency/count-marks-only"
				}
				c.fail(class, n.addr, "", "the graph counts %d withheld values, the detail withholds %d", have, want)
			}
		}
		for _, l := range n.leaves {
			s, found := rpFind(seen, l.path)
			shown := found && !s.withheld && !s.null && !s.unknown
			if l.why == "" {
				rpStats["leaves-public"]++
			} else {
				rpStats["leaves-withheld"]++
			}
			switch {
			case l.why == "" && !shown:
				c.fail("precision/withheld("+s.why+")@"+n.typ, n.addr, l.path, "a public value is not shown (%+v): %q", s, l.text)
			case l.why == "" && s.text != l.text:
				c.fail("precision/changed", n.addr, l.path, "shown as %q, the record holds %q", s.text, l.text)
			case l.why != "" && shown:
				c.fail("soundness/shown("+l.why+")@"+n.typ, n.addr, l.path, "a value that the model withholds (%s) is shown: %q", l.why, l.text)
			}
			if has := c.fieldsHas(n.addr, l.path, l.text); has != shown {
				c.fail("consistency/fields", n.addr, l.path, "detail shows=%v, /api/fields has a row=%v (model: %q) for %q", shown, has, l.why, l.text)
			}
		}
		if n.declared && !n.data {
			c.checkRefs(n)
		}
		for _, h := range d.History {
			for _, ac := range h.Attrs {
				rpStats["history-changes"]++
				for _, l := range n.leaves {
					if l.path == ac.Path && rpFieldLevel(l.why) {
						rpStats["history-withheld-changes"]++
					}
					if l.path == ac.Path && rpFieldLevel(l.why) && (ac.Before != nil || ac.After != nil) {
						c.fail("soundness/history", n.addr, l.path, "the history shows %v -> %v for a value the model withholds (%s)", ac.Before, ac.After, l.why)
					}
				}
			}
		}
		if n.declared {
			srcBody, code := rpGetCode(p, "/api/source?addr="+queryEscape(n.addr))
			if code == 200 {
				var secrets []string
				// a literal assigned to a key that is named like a secret is masked in the lines of the node
				for _, k := range n.tagKeys {
					if n.guarded[k] {
						secrets = append(secrets, n.tags[k].text)
					}
				}
				for _, m := range append([]*rpNode{n}, e.closure(n)...) {
					for _, s := range m.typed {
						if len(s) >= 6 && !m.coincident[s] {
							secrets = append(secrets, s)
						}
					}
					secrets = append(secrets, m.made...)
				}
				rpStats["source-bodies"]++
				if strings.Contains(string(srcBody), "••••••••") {
					rpStats["source-bodies-masked"]++
				}
				c.scan("soundness/source", n.addr, "/api/source", srcBody, secrets)
			}
		}
	}

	// the same estate with a saved plan that updates every managed resource: a second viewer is dear, so a run of all the
	// seeds does it for the odd ones, and the replay of one seed always does
	pp := p
	if e.seed%2 == 1 || os.Getenv("RP_SEED") != "" {
		pp = rpBuild(t, e, true)
	}
	for _, n := range e.nodes {
		if !n.declared || n.data || pp == p {
			continue
		}
		body := pp.get("/api/detail?addr=" + queryEscape(n.addr))
		var d Detail
		if err := json.Unmarshal(body, &d); err != nil {
			t.Fatal(err)
		}
		c.scan("soundness/plan-detail", n.addr, "/api/detail (plan)", body, hide)
		c.checkPlan(n, d.Change)
	}

	for _, o := range e.outputs {
		go1, ok := gouts[o.name]
		if !ok {
			t.Fatalf("the graph has no output %s", o.name)
		}
		body := p.get("/api/detail?addr=" + queryEscape(o.name))
		var d Detail
		if err := json.Unmarshal(body, &d); err != nil {
			t.Fatal(err)
		}
		c.scan("soundness/detail", o.name, "/api/detail", body, hide)
		seen := map[string]rpSeen{}
		for i := range d.Attrs {
			rpFlatten(d.Attrs[i].Name, &d.Attrs[i], seen)
		}
		s, found := rpFind(seen, "value")
		detailShown := found && !s.withheld && !s.null && !s.unknown
		graphShown := go1.Redacted == "" && !go1.Unknown && go1.Value != nil
		if want := o.why == ""; graphShown != want {
			c.fail("verdict/output-graph", o.name, "value", "the graph shows=%v (redacted %q), the model says shown=%v: %q", graphShown, go1.Redacted, want, o.val.text)
		}
		if want := o.why == ""; detailShown != want {
			c.fail("verdict/output-detail", o.name, "value", "the detail shows=%v, the model says shown=%v: %q", detailShown, want, o.val.text)
		}
		if graphShown != detailShown {
			c.fail("consistency/output", o.name, "value", "the graph shows=%v, the detail shows=%v for %q", graphShown, detailShown, o.val.text)
		}
		if graphShown && fmt.Sprint(go1.Value) != o.val.text {
			c.fail("precision/changed", o.name, "value", "the graph shows %q, the output is %q", fmt.Sprint(go1.Value), o.val.text)
		}
		if gn, ok := gnodes[o.name]; ok {
			if want := map[bool]int{true: 0, false: 1}[o.why == ""]; gn.Sensitive != want {
				c.fail("consistency/count-output", o.name, "value", "the graph counts %d withheld values for the output, the model withholds %d", gn.Sensitive, want)
			}
		}
		if has := c.fieldsHas(o.name, "value", o.val.text); has != detailShown {
			c.fail("consistency/fields", o.name, "value", "detail shows=%v, /api/fields has a row=%v for %q", detailShown, has, o.val.text)
		}
	}
	return c.fails
}

// checkRefs reads the picker of the template wizard (/api/templates/refs), which offers the string attributes of the managed
// resources with the value the state holds "as the Inventory shows it", and compares it with the verdict of the model.
func (c *rpCheck) checkRefs(n *rpNode) {
	c.t.Helper()
	body := c.p.get("/api/templates/refs?kind=string&q=" + url.QueryEscape(n.addr+"."))
	c.scan("soundness/refs", n.addr, "/api/templates/refs", body, c.e.mustHide())
	var refs TemplateRefs
	if err := json.Unmarshal(body, &refs); err != nil {
		c.t.Fatal(err)
	}
	by := map[string]TemplateRef{}
	for _, r := range refs.Refs {
		if r.Addr == n.addr {
			by[r.Attr] = r
		}
	}
	for _, l := range n.leaves {
		if l.path != l.field || n.f[l.field] == nil {
			continue
		}
		rpStats["refs-compared"]++
		ref, listed := by[l.field]
		if rpAttrMark(n.typ, l.field) {
			if listed {
				c.fail("soundness/refs", n.addr, l.path, "the picker offers the sensitive attribute %s", ref.Expr)
			}
			continue
		}
		switch {
		case !listed:
			c.fail("consistency/refs", n.addr, l.path, "the picker does not offer %s.%s", n.addr, l.field)
		case l.why == "" && (ref.ValueState != "known" || (len(strconv.Quote(l.text)) <= 120 && ref.Value != strconv.Quote(l.text))):
			c.fail("precision/refs("+ref.ValueState+")", n.addr, l.path, "the picker gives %q (%s) for a public value %q", ref.Value, ref.ValueState, l.text)
		case l.why != "" && (ref.ValueState != "withheld" || ref.Value != ""):
			c.fail("soundness/refs("+l.why+")", n.addr, l.path, "the picker gives %q (%s) for a value the model withholds: %q", ref.Value, ref.ValueState, l.text)
		}
	}
}

// checkPlan compares the attribute changes of the saved plan, as the detail of the node lays them over the record, with the
// verdict of the model: the change of a public value shows both sides, the change of a withheld field shows neither.
func (c *rpCheck) checkPlan(n *rpNode, ch *Change) {
	c.t.Helper()
	if ch == nil {
		c.fail("consistency/plan", n.addr, "", "the detail has no change from the saved plan")
		return
	}
	find := func(path string) (AttrChange, bool) {
		for p := path; ; {
			for _, ac := range ch.Attrs {
				if ac.Path == p {
					return ac, true
				}
			}
			i := strings.LastIndex(p, ".")
			if i < 0 {
				return AttrChange{}, false
			}
			p = p[:i]
		}
	}
	for _, l := range n.leaves {
		if l.why != "" && !rpFieldLevel(l.why) {
			continue
		}
		if _, ok := n.f[l.field]; !ok && l.field != "tags" {
			continue
		}
		ac, ok := find(l.path)
		rpStats["plan-changes-compared"]++
		switch {
		case !ok:
			c.fail("consistency/plan-missing", n.addr, l.path, "the plan changes this value and the detail lists no change (%q)", l.why)
		case l.why == "" && (ac.BeforeRedacted || ac.AfterRedacted || fmt.Sprint(ac.Before) != l.text || fmt.Sprint(ac.After) != l.text+"~plan"):
			c.fail("precision/plan", n.addr, l.path, "the plan changes a public value %q and the detail gives %+v", l.text, ac)
		case l.why != "" && (ac.Before != nil || ac.After != nil):
			c.fail("soundness/plan("+l.why+")", n.addr, l.path, "the plan changes a value the model withholds (%s) and the detail gives %+v", l.why, ac)
		}
	}
}

func rpGetCode(p *project, path string) ([]byte, int) {
	resp, body := p.do("GET", path, nil)
	return body, resp.StatusCode
}

// ---------------------------------------------------------------------------------------------------------------------
// The tests.

func rpSeeds() []int64 {
	if s := os.Getenv("RP_SEED"); s != "" {
		n, err := strconv.ParseInt(s, 10, 64)
		if err == nil {
			return []int64{n}
		}
	}
	count := rpDefaultSeeds
	if s := os.Getenv("RP_SEEDS"); s != "" {
		if n, err := strconv.Atoi(s); err == nil && n > 0 {
			count = n
		}
	}
	out := make([]int64, count)
	for i := range out {
		out[i] = int64(i + 1)
	}
	return out
}

func (e *rpEstate) dump() string {
	var b strings.Builder
	b.WriteString("--- specification ---\n" + e.specText() + "--- records ---\n" + e.recordsText() + "--- verdicts of the model ---\n")
	for _, n := range e.nodes {
		for _, l := range n.leaves {
			fmt.Fprintf(&b, "%-4s %-14s %-10s %q\n", n.addr, l.path, l.why, l.text)
		}
	}
	for _, o := range e.outputs {
		fmt.Fprintf(&b, "%-4s %-14s %-10s %q\n", o.name, "value", o.why, o.val.text)
	}
	return b.String()
}

func TestRPRedactionProperty(t *testing.T) {
	verbose := os.Getenv("RP_VERBOSE") != ""
	single := os.Getenv("RP_SEED") != ""
	classes := map[string][]string{}
	dumped, ran := 0, 0
	seeds := rpSeeds()
	for _, seed := range seeds {
		e := rpGenerate(seed)
		t.Run(fmt.Sprintf("seed%d", seed), func(t *testing.T) {
			ran++
			fails := rpRun(t, e)
			if len(fails) == 0 {
				return
			}
			var b strings.Builder
			fmt.Fprintf(&b, "seed %d: %d violations (replay: RP_SEED=%d go test -count=1 -run TestRPRedactionProperty ./internal/serve)\n", seed, len(fails), seed)
			// the first violation of each class; all of them when one seed is replayed
			first := map[string]bool{}
			for _, f := range fails {
				classes[f.class] = append(classes[f.class], fmt.Sprintf("seed %d %s %s", seed, f.node, f.path))
				if single || verbose || !first[f.class] {
					fmt.Fprintf(&b, "  [%s] %s %s: %s\n", f.class, f.node, f.path, f.msg)
				}
				first[f.class] = true
			}
			if single || verbose || dumped < 5 {
				dumped++
				b.WriteString(e.dump())
			}
			t.Error(b.String())
		})
	}
	t.Logf("the oracle compared: %v", rpStats)
	if !single && ran == len(seeds) {
		for _, k := range []string{"leaves-public", "leaves-withheld", "fields-queries", "history-withheld-changes", "refs-compared", "source-bodies", "plan-changes-compared"} {
			if rpStats[k] == 0 {
				t.Errorf("the oracle never compared %s: the property passes without looking", k)
			}
		}
	}
	if len(classes) > 0 {
		var names []string
		for k := range classes {
			names = append(names, k)
		}
		sort.Strings(names)
		var b strings.Builder
		b.WriteString("failure classes:\n")
		for _, k := range names {
			ex := classes[k]
			if len(ex) > 3 {
				ex = ex[:3]
			}
			fmt.Fprintf(&b, "  %-26s %4d  e.g. %s\n", k, len(classes[k]), strings.Join(ex, "; "))
		}
		t.Log(b.String())
	}
}

// The generator must produce what the property is about, in a fair share of the seeds: otherwise the test passes by never
// meeting the cases.
func TestRPGeneratorSelfCheck(t *testing.T) {
	const seeds = 150
	var derivedByRef, hidden, hiddenTyped, hiddenMade, coincident, ghosts, undeclHit, outDerived, outShown, guarded, noSchema, sizeBad int
	var sameSeedDiffers bool
	byWhy := map[string]int{}
	sizeSum, sizeMin, sizeMax := 0, 100, 0
	for seed := int64(1); seed <= seeds; seed++ {
		e := rpGenerate(seed)
		if rpGenerate(seed).specText() != e.specText() || rpGenerate(seed).recordsText() != e.recordsText() {
			sameSeedDiffers = true
		}
		n := len(e.nodes)
		if n < 4 || n > 9 {
			sizeBad++
		}
		sizeSum += n
		sizeMin, sizeMax = min(sizeMin, n), max(sizeMax, n)
		var d, h, ht, hm, co, gh, uh, gu, ns bool
		for _, n := range e.nodes {
			if !n.declared {
				gh = true
			}
			if n.schemaless() {
				ns = true
			}
			for _, l := range n.leaves {
				byWhy[l.why]++
				if l.why == "derived" {
					d = true
				}
				if l.why == "name" {
					gu = true
				}
				if l.why == "undeclared" {
					uh = true
				}
				if n.hiddenRoute && n.data && (l.why == "world" || l.why == "made") {
					h = true
					if rpContainsAny(l.text, e.madeAll) {
						hm = true
					} else {
						ht = true
					}
				}
				if l.why == "" {
					for _, m := range e.nodes {
						if m == n || !m.declared {
							continue
						}
						for typed := range m.coincident {
							if strings.Contains(l.text, typed) && !strings.HasPrefix(l.text, "echo") {
								co = true
							}
						}
					}
				}
			}
		}
		for _, o := range e.outputs {
			if o.why != "" {
				outDerived++
			} else {
				outShown++
			}
		}
		for _, f := range []struct {
			b   bool
			ctr *int
		}{{d, &derivedByRef}, {h, &hidden}, {ht, &hiddenTyped}, {hm, &hiddenMade}, {co, &coincident}, {gh, &ghosts}, {uh, &undeclHit}, {gu, &guarded}, {ns, &noSchema}} {
			if f.b {
				*f.ctr++
			}
		}
	}
	t.Logf("of %d seeds: derived-by-reference %d, hidden-route data source %d (typed %d, made %d), coincident public value %d, undeclared record %d (text hit %d), name-guarded %d, no schema %d, outputs derived %d shown %d",
		seeds, derivedByRef, hidden, hiddenTyped, hiddenMade, coincident, ghosts, undeclHit, guarded, noSchema, outDerived, outShown)
	t.Logf("verdicts of the model over the leaves: %v", byWhy)
	for _, why := range []string{"", "mark", "name", "schema", "derived", "made", "world", "undeclared"} {
		if byWhy[why] == 0 {
			t.Errorf("no value has the verdict %q", why)
		}
	}
	if sameSeedDiffers {
		t.Error("the generator is not deterministic: one seed gives two estates")
	}
	if sizeBad > 0 {
		t.Errorf("%d estates have fewer than 4 or more than 9 nodes", sizeBad)
	}
	t.Logf("nodes of an estate (declared and not): min %d, max %d, mean %.1f", sizeMin, sizeMax, float64(sizeSum)/seeds)
	for name, v := range map[string]int{
		"derived-by-reference fields": derivedByRef,
		"hidden-route data sources":   hidden,
		"hidden route with a typed":   hiddenTyped,
		"hidden route with a made":    hiddenMade,
		"coincident public values":    coincident,
		"undeclared records":          ghosts,
		"name-guarded map entries":    guarded,
		"derived outputs":             outDerived,
		"shown outputs":               outShown,
	} {
		if v == 0 {
			t.Errorf("no seed produced %s", name)
		}
	}
	for name, v := range map[string]int{"derived-by-reference": derivedByRef, "hidden-route": hidden, "coincident": coincident} {
		if v*100 < seeds*20 {
			t.Errorf("%s appears in only %d of %d seeds", name, v, seeds)
		}
	}
}
