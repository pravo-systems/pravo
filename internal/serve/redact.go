package serve

import (
	"bytes"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"maps"
	"net/url"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/hashicorp/terraform-plugin-go/tftypes"

	"github.com/pravo-systems/pravo/internal/schema"
)

// nameGuard is the second guard of README rule 5: a value whose attribute name
// says it is a secret is withheld whatever the schema says.
var nameGuard = schema.NameGuard

const (
	// maxChildren bounds the elements of a list, set, map or object that are sent.
	maxChildren = 100
	// maxText bounds the text of a string value.
	maxText = 4096
	// maxDepth bounds how deep a value is followed.
	maxDepth = 12
	// minSecret is the shortest sensitive string that is looked for in other
	// values. A shorter one matches too much to say anything.
	minSecret = 6
	// maxDescription bounds the description of an attribute.
	maxDescription = 300
	// mask replaces a literal in source lines.
	mask = "••••••••"
)

// secretSet holds the strings that the state keeps in sensitive attributes. The
// viewer withholds a value that is not sensitive by its schema but contains one
// of them: a file whose content embeds a password has no sensitive flag, and the
// password is in it all the same. The set is filled while the model is built and
// read by every request after; it is not changed then.
//
// Each string remembers the nodes it was found in. A value of the state carries
// its sensitivity along the references that read it, so a reader of node X is
// judged against the secrets of X and of what X reads, directly or not
// (scopedTo), and not against a string that happens to be the same in a node
// that has nothing to do with X: the region that an SSM parameter keeps in a
// sensitive attribute says nothing of a bucket that is in the same region.
type secretSet struct {
	set  map[string]struct{}
	list []string
	// from lists the nodes that a string was found in. A string with no node
	// ("") is judged for every reader.
	from map[string][]string
	// by lists the strings that were found in a node, by node, and pos is where
	// a string is in list.
	by  map[string][]string
	pos map[string]int
	// made lists, by node, the strings that the node's own provider produced: a
	// sensitive attribute that is not set in the specification. A node is judged
	// against these in its own attributes and not against what a user typed into
	// it, which a provider has no reason to echo: the value of an SSM parameter
	// says nothing of its ARN.
	made map[string]map[string]bool
	// generated lists, by node, the strings of an attribute that only the provider fills in: no specification field
	// can have been the source of them. made is wider (an attribute the specification may set, left empty, is made).
	generated map[string]map[string]bool
}

func newSecretSet() *secretSet {
	return &secretSet{set: map[string]struct{}{}, from: map[string][]string{}, by: map[string][]string{}, pos: map[string]int{}, made: map[string]map[string]bool{}, generated: map[string]map[string]bool{}}
}

// add records the strings of a withheld value, and the forms in which another
// attribute is likely to embed them: percent-encoded in a connection string,
// escaped inside a JSON document, base64 in user data, hex.
func (s *secretSet) add(v any) { s.addFrom("", v) }

// addFrom is add for a value found in the node at addr.
func (s *secretSet) addFrom(addr string, v any) {
	switch x := v.(type) {
	case string:
		if len(x) >= minSecret && len(x) <= 1<<16 {
			s.put(x, addr)
			for _, form := range embeddedForms(x) {
				s.put(form, addr)
			}
		}
	case json.Number:
		// A sensitive number, a PIN or a code, is a secret as a string is.
		s.addFrom(addr, string(x))
	case []any:
		for _, e := range x {
			s.addFrom(addr, e)
		}
	case map[string]any:
		for _, e := range x {
			s.addFrom(addr, e)
		}
	}
}

// scopedTo returns the strings that were found in one of the nodes of scope, and
// those that were found in no node in particular.
func (s *secretSet) scopedTo(scope map[string]bool) *secretSet {
	if s == nil {
		return nil
	}
	var found []string
	seen := map[string]bool{}
	take := func(o string) {
		for _, x := range s.by[o] {
			if !seen[x] {
				seen[x] = true
				found = append(found, x)
			}
		}
	}
	take("")
	// The scope is mostly larger than what a node holds, and a node holds little.
	if len(scope) <= len(s.by) {
		for o := range scope {
			take(o)
		}
	} else {
		for o := range s.by {
			if scope[o] {
				take(o)
			}
		}
	}
	// In the order of the set: a secret that holds another is replaced first.
	sort.Slice(found, func(i, j int) bool { return s.pos[found[i]] < s.pos[found[j]] })
	out := newSecretSet()
	for _, x := range found {
		for _, o := range s.from[x] {
			if o == "" || scope[o] {
				out.put(x, o)
			}
		}
	}
	return out
}

// clone returns a set that holds the same strings and that adding to does not
// change this one.
func (s *secretSet) clone() *secretSet {
	c := &secretSet{set: make(map[string]struct{}, len(s.set)), list: append([]string(nil), s.list...), from: make(map[string][]string, len(s.from)), by: make(map[string][]string, len(s.by)), pos: make(map[string]int, len(s.pos)), made: make(map[string]map[string]bool, len(s.made)), generated: make(map[string]map[string]bool, len(s.generated))}
	for k := range s.set {
		c.set[k] = struct{}{}
	}
	for k, v := range s.from {
		c.from[k] = append([]string(nil), v...)
	}
	for k, v := range s.by {
		c.by[k] = append([]string(nil), v...)
	}
	for k, v := range s.pos {
		c.pos[k] = v
	}
	for k, v := range s.made {
		c.made[k] = maps.Clone(v)
	}
	for k, v := range s.generated {
		c.generated[k] = maps.Clone(v)
	}
	return c
}

// addGenerated is addFrom for a value of an attribute that only the provider fills in.
func (s *secretSet) addGenerated(addr string, v any) {
	t := newSecretSet()
	t.addFrom(addr, v)
	if len(t.list) == 0 {
		return
	}
	if s.generated[addr] == nil {
		s.generated[addr] = map[string]bool{}
	}
	for _, x := range t.list {
		s.generated[addr][x] = true
	}
}

// addMade is addFrom for a value that the provider of the node made.
func (s *secretSet) addMade(addr string, v any) {
	t := newSecretSet()
	t.addFrom(addr, v)
	if len(t.list) == 0 {
		return
	}
	if s.made[addr] == nil {
		s.made[addr] = map[string]bool{}
	}
	for _, x := range t.list {
		s.made[addr][x] = true
	}
}

// ownTo is what a declared node is judged against in its own attributes: the
// strings it made, and those found in no node in particular.
func (s *secretSet) ownTo(addr string) *secretSet {
	if s == nil {
		return nil
	}
	out := newSecretSet()
	for _, x := range s.list {
		for _, o := range s.from[x] {
			if o == "" || o == addr && s.made[addr][x] {
				out.put(x, o)
			}
		}
	}
	return out
}

// withMade is the strings found in the nodes of scope and those that any provider generated: a sensitive attribute that
// only the provider fills in. A node that reads from outside the graph can hold what a provider generated, which no reference
// leads to; what a person typed into the specification reaches it only along a reference, which scope follows.
func (s *secretSet) withMade(scope map[string]bool) *secretSet {
	out := s.scopedTo(scope)
	if s == nil {
		return out
	}
	for addr, strs := range s.generated {
		for x := range strs {
			out.put(x, addr)
			if out.generated[addr] == nil {
				out.generated[addr] = map[string]bool{}
			}
			out.generated[addr][x] = true
		}
	}
	return out
}

func (s *secretSet) put(x, addr string) {
	if _, ok := s.set[x]; !ok {
		s.set[x] = struct{}{}
		s.pos[x] = len(s.list)
		s.list = append(s.list, x)
	}
	if !slices.Contains(s.from[x], addr) {
		s.from[x] = append(s.from[x], addr)
		s.by[addr] = append(s.by[addr], x)
	}
}

// minForm is the shortest encoded form that is looked for.
const minForm = 8

// embeddedForms lists the encodings of a secret that a specification is likely
// to put into another attribute: the secret itself is looked for by the caller.
// It is bounded: a secret of many kilobytes (a key) is looked for as it is, and
// in base64, which is how a key is embedded.
func embeddedForms(x string) []string {
	var out []string
	add := func(f string) {
		if f != x && len(f) >= minForm {
			out = append(out, f)
		}
	}
	if len(x) <= 4096 {
		add(url.QueryEscape(x))
		add(url.PathEscape(x))
		add(jsonInner(x, true))
		add(jsonInner(x, false))
		add(hex.EncodeToString([]byte(x)))
		add(strings.ToUpper(hex.EncodeToString([]byte(x))))
	}
	for _, enc := range []*base64.Encoding{base64.StdEncoding, base64.URLEncoding} {
		out = append(out, base64Forms(enc, []byte(x))...)
	}
	return out
}

// jsonInner is the secret as it reads inside a JSON string.
func jsonInner(x string, escapeHTML bool) string {
	var b strings.Builder
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(escapeHTML)
	if enc.Encode(x) != nil {
		return x
	}
	out := strings.TrimSpace(b.String())
	if len(out) < 2 {
		return x
	}
	return out[1 : len(out)-1]
}

// base64Forms returns what the bytes become when they are part of a longer text
// that is base64-encoded: the encoding depends on where in a group of three they
// start, and the first and last characters of the encoding depend on the bytes
// around them, so those are left out.
func base64Forms(enc *base64.Encoding, secret []byte) []string {
	var out []string
	for lead := range 3 {
		text := strings.TrimRight(enc.EncodeToString(append(make([]byte, lead), secret...)), "=")
		// The characters that the lead bytes take part in are not the secret's.
		drop := [3]int{0, 2, 3}[lead]
		if drop >= len(text) {
			continue
		}
		text = text[drop:]
		// A group that the secret ends inside has a last character that depends
		// on what follows.
		if (lead+len(secret))%3 != 0 && len(text) > 0 {
			text = text[:len(text)-1]
		}
		if len(text) >= minSecret {
			out = append(out, text)
		}
	}
	return out
}

// scrub replaces, in the text, every sensitive string of the state with the
// mask, and reports whether it replaced any.
func (s *secretSet) scrub(text string) (string, bool) {
	if s == nil || len(text) < minSecret {
		return text, false
	}
	changed := false
	for _, sec := range s.list {
		if len(sec) <= len(text) && strings.Contains(text, sec) {
			text = strings.ReplaceAll(text, sec, mask)
			changed = true
		}
	}
	return text, changed
}

// leaks reports whether text contains a sensitive string of the state.
func (s *secretSet) leaks(text string) bool {
	if s == nil || len(text) < minSecret {
		return false
	}
	for _, sec := range s.list {
		if len(sec) <= len(text) && strings.Contains(text, sec) {
			return true
		}
	}
	return false
}

// leaksIn reports whether any string inside the JSON value contains one.
func (s *secretSet) leaksIn(v any) bool {
	if s == nil || len(s.list) == 0 {
		return false
	}
	switch x := v.(type) {
	case string:
		return s.leaks(x)
	case []any:
		for _, e := range x {
			if s.leaksIn(e) {
				return true
			}
		}
	case map[string]any:
		for _, e := range x {
			if s.leaksIn(e) {
				return true
			}
		}
	case json.RawMessage:
		var d any
		dec := json.NewDecoder(strings.NewReader(string(x)))
		dec.UseNumber()
		if dec.Decode(&d) == nil {
			return s.leaksIn(d)
		}
	}
	return false
}

// walk renders the attributes of a record against the schema of its type.
type walk struct {
	// secrets are the strings to withhold wherever they show. It is nil while the
	// model is built and the set is not complete.
	secrets *secretSet
	// sink, when set, collects the strings of every value that is withheld, as
	// found in the node at origin.
	sink   *secretSet
	origin string
	// taint lists the top-level fields that read a withheld attribute of another
	// node: they are withheld as derived, whatever their text.
	taint map[string]bool
	// build makes the walk return the attributes. A walk that only counts and
	// collects leaves them out.
	build bool
	// withheld counts the values that were withheld for what they are: by the
	// schema, by the name or because the schema does not know them. derived counts
	// those withheld for what they contain.
	withheld, derived int
}

// decodeAttrs decodes the attributes of a record. UseNumber keeps a number as it
// is written.
func decodeAttrs(raw json.RawMessage) (map[string]any, error) {
	if len(raw) == 0 {
		return map[string]any{}, nil
	}
	if jsonDepth(raw) > maxRawDepth {
		return nil, errors.New("the values are nested too deeply")
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var m map[string]any
	if err := dec.Decode(&m); err != nil {
		return nil, err
	}
	if m == nil {
		m = map[string]any{}
	}
	return m, nil
}

// maxRawDepth is the deepest nesting of a record that is decoded. A provider
// schema nests a few levels; decoding a record that nests thousands costs
// seconds and shows nothing.
const maxRawDepth = 200

// jsonDepth returns the deepest nesting of arrays and objects in JSON text,
// without decoding it.
func jsonDepth(raw []byte) int {
	depth, deepest := 0, 0
	inString, escaped := false, false
	for _, c := range raw {
		switch {
		case inString:
			switch {
			case escaped:
				escaped = false
			case c == '\\':
				escaped = true
			case c == '"':
				inString = false
			}
		case c == '"':
			inString = true
		case c == '{' || c == '[':
			depth++
			if depth > deepest {
				deepest = depth
			}
		case c == '}' || c == ']':
			depth--
		}
	}
	return deepest
}

// withholdReason says why the value of an attribute is not sent, "" when it is.
func withholdReason(name string, sensitive, writeOnly bool) string {
	switch {
	case sensitive:
		return "sensitive"
	case writeOnly:
		return "write-only"
	case nameGuard.MatchString(name):
		return "name"
	}
	return ""
}

// block renders the attributes and nested blocks of b over the values of a
// record. An attribute the record holds and the schema does not know is
// withheld: nothing says whether it is sensitive.
func (w *walk) block(b *schema.Block, rec map[string]any, depth int) []Attr {
	var out []Attr
	seen := make(map[string]bool, len(b.Attributes)+len(b.BlockTypes))
	for _, a := range b.Attributes {
		seen[a.Name] = true
		at := w.attribute(a, rec[a.Name], depth)
		if w.build {
			out = append(out, at)
		}
	}
	for _, nb := range b.BlockTypes {
		seen[nb.TypeName] = true
		at := w.blockAttr(nb, rec[nb.TypeName], depth)
		if w.build {
			out = append(out, at)
		}
	}
	return append(out, w.extra(rec, seen)...)
}

// extra renders the values of a record that the schema has no attribute for.
func (w *walk) extra(rec map[string]any, seen map[string]bool) []Attr {
	var names []string
	for k := range rec {
		if !seen[k] {
			names = append(names, k)
		}
	}
	sort.Strings(names)
	var out []Attr
	for _, k := range names {
		at := Attr{Name: k}
		if rec[k] == nil {
			at.Null = true
		} else {
			w.withhold(&at, rec[k], "schema")
		}
		if w.build {
			out = append(out, at)
		}
	}
	return out
}

func (w *walk) attribute(a *schema.Attribute, v any, depth int) Attr {
	at := Attr{Name: a.Name, Kind: kindOf(a), Sensitive: a.Sensitive, Deprecated: a.Deprecated}
	if w.build {
		at.Type = attrType(a)
		at.Description = clip(a.Description, maxDescription)
	}
	if v == nil {
		at.Null = true
		return at
	}
	if why := withholdReason(a.Name, a.Sensitive, a.WriteOnly); why != "" {
		w.withhold(&at, v, why)
		return at
	}
	if depth == 0 && w.taint[a.Name] {
		at.Redacted = "derived"
		w.derived++
		return at
	}
	if a.NestedType != nil {
		w.nestedObject(&at, a.NestedType, v, depth)
		return at
	}
	w.typed(&at, a.Type(), v, depth)
	return at
}

// nestedObject renders a nested attribute type: the nesting says how the
// objects are arranged, and each attribute of an object has its own flags.
func (w *walk) nestedObject(at *Attr, o *schema.Object, v any, depth int) {
	switch o.Nesting {
	case schema.NestingList, schema.NestingSet:
		list, ok := v.([]any)
		if !ok {
			w.free(at, v, depth, objectNames(o))
			return
		}
		w.elements(at, list, func(i int, e any) Attr {
			return w.objectAttr(strconv.Itoa(i), o, e, depth+1)
		})
	case schema.NestingMap:
		m, ok := v.(map[string]any)
		if !ok {
			w.free(at, v, depth, objectNames(o))
			return
		}
		w.entries(at, m, func(k string, e any) Attr { return w.objectAttr(k, o, e, depth+1) })
	default:
		m, ok := v.(map[string]any)
		if !ok {
			w.free(at, v, depth, objectNames(o))
			return
		}
		at.Children = w.attrsOf(o.Attributes, m, depth+1)
	}
}

// objectAttr renders one object of a nested attribute type.
func (w *walk) objectAttr(name string, o *schema.Object, v any, depth int) Attr {
	at := Attr{Name: name}
	if w.build {
		at.Type = "object"
	}
	if v == nil {
		at.Null = true
		return at
	}
	if guarded(name) {
		w.withhold(&at, v, "name")
		return at
	}
	m, ok := v.(map[string]any)
	if !ok {
		w.free(&at, v, depth, objectNames(o))
		return at
	}
	at.Children = w.attrsOf(o.Attributes, m, depth+1)
	return at
}

// attrsOf renders the attributes of a nested object.
func (w *walk) attrsOf(attrs []*schema.Attribute, m map[string]any, depth int) []Attr {
	if depth > maxDepth {
		return nil
	}
	var out []Attr
	seen := make(map[string]bool, len(attrs))
	for _, a := range attrs {
		seen[a.Name] = true
		at := w.attribute(a, m[a.Name], depth)
		if w.build {
			out = append(out, at)
		}
	}
	return append(out, w.extra(m, seen)...)
}

// blockAttr renders a nested block type.
func (w *walk) blockAttr(nb *schema.NestedBlock, v any, depth int) Attr {
	at := Attr{Name: nb.TypeName}
	if nb.MinItems > 0 {
		at.Kind = "required"
	}
	if w.build {
		at.Type = blockType(nb)
		at.Deprecated = nb.Block.Deprecated
		at.Description = clip(nb.Block.Description, maxDescription)
	}
	if v == nil {
		at.Null = true
		return at
	}
	if guarded(nb.TypeName) {
		w.withhold(&at, v, "name")
		return at
	}
	if depth == 0 && w.taint[nb.TypeName] {
		at.Redacted = "derived"
		w.derived++
		return at
	}
	one := func(name string, e any) Attr {
		c := Attr{Name: name}
		if w.build {
			c.Type = "block"
		}
		m, ok := e.(map[string]any)
		switch {
		case e == nil:
			c.Null = true
		case !ok:
			w.free(&c, e, depth+1, blockNames(nb.Block))
		case depth+1 <= maxDepth:
			c.Children = w.block(nb.Block, m, depth+1)
		}
		return c
	}
	switch nb.Nesting {
	case schema.NestingList, schema.NestingSet:
		list, ok := v.([]any)
		if !ok {
			w.free(&at, v, depth, blockNames(nb.Block))
			return at
		}
		w.elements(&at, list, func(i int, e any) Attr { return one(strconv.Itoa(i), e) })
	case schema.NestingMap:
		m, ok := v.(map[string]any)
		if !ok {
			w.free(&at, v, depth, blockNames(nb.Block))
			return at
		}
		w.entries(&at, m, one)
	default:
		m, ok := v.(map[string]any)
		if !ok {
			w.free(&at, v, depth, blockNames(nb.Block))
			return at
		}
		if depth+1 <= maxDepth {
			at.Children = w.block(nb.Block, m, depth+1)
		}
	}
	return at
}

// typed renders a value of a known type: its elements follow the element type.
func (w *walk) typed(at *Attr, t tftypes.Type, v any, depth int) {
	if depth > maxDepth {
		return
	}
	switch {
	case t == nil, t.Is(tftypes.DynamicPseudoType):
		w.free(at, v, depth, nil)
	case t.Is(tftypes.List{}), t.Is(tftypes.Set{}):
		list, ok := v.([]any)
		if !ok {
			w.free(at, v, depth, nil)
			return
		}
		elem := elementType(t)
		w.elements(at, list, func(i int, e any) Attr { return w.child(strconv.Itoa(i), elem, e, depth+1, false) })
	case t.Is(tftypes.Tuple{}):
		list, ok := v.([]any)
		if !ok {
			w.free(at, v, depth, nil)
			return
		}
		types, _ := t.(tftypes.Tuple)
		w.elements(at, list, func(i int, e any) Attr {
			var et tftypes.Type
			if i < len(types.ElementTypes) {
				et = types.ElementTypes[i]
			}
			return w.child(strconv.Itoa(i), et, e, depth+1, false)
		})
	case t.Is(tftypes.Map{}):
		m, ok := v.(map[string]any)
		if !ok {
			w.free(at, v, depth, nil)
			return
		}
		elem := elementType(t)
		w.entries(at, m, func(k string, e any) Attr { return w.child(k, elem, e, depth+1, true) })
	case t.Is(tftypes.Object{}):
		m, ok := v.(map[string]any)
		if !ok {
			w.free(at, v, depth, nil)
			return
		}
		obj, _ := t.(tftypes.Object)
		w.entries(at, m, func(k string, e any) Attr { return w.child(k, obj.AttributeTypes[k], e, depth+1, true) })
	default:
		w.scalar(at, v)
	}
}

// child renders an element of a collection. named says that its name is a key
// or an attribute name and not an index, so the name guard applies to it.
func (w *walk) child(name string, t tftypes.Type, v any, depth int, named bool) Attr {
	at := Attr{Name: name}
	if w.build && t != nil {
		at.Type = typeText(t)
	}
	if v == nil {
		at.Null = true
		return at
	}
	if named && guarded(name) {
		w.withhold(&at, v, "name")
		return at
	}
	w.typed(&at, t, v, depth)
	return at
}

// free renders a value whose shape the schema does not say: the shape of the
// JSON is followed, and a key that looks like a secret is withheld. Where the
// schema says that something below is sensitive (names), a key of that name is
// withheld too: a record whose shape is not the schema's, one written by another
// version of the provider, is not a way round it.
func (w *walk) free(at *Attr, v any, depth int, names map[string]bool) {
	if depth > maxDepth {
		return
	}
	switch x := v.(type) {
	case []any:
		w.elements(at, x, func(i int, e any) Attr { return w.freeChild(strconv.Itoa(i), e, depth+1, false, names) })
	case map[string]any:
		w.entries(at, x, func(k string, e any) Attr { return w.freeChild(k, e, depth+1, true, names) })
	default:
		w.scalar(at, v)
	}
}

func (w *walk) freeChild(name string, v any, depth int, named bool, names map[string]bool) Attr {
	at := Attr{Name: name}
	if v == nil {
		at.Null = true
		return at
	}
	if named && (guarded(name) || names[name]) {
		why := "name"
		if !guarded(name) {
			why = "schema"
		}
		w.withhold(&at, v, why)
		return at
	}
	w.free(&at, v, depth, names)
	return at
}

// scalar sets the value of a string, number or bool.
func (w *walk) scalar(at *Attr, v any) {
	switch x := v.(type) {
	case string:
		if w.secrets.leaks(x) {
			at.Redacted = "derived"
			w.derived++
			return
		}
		if !w.build {
			return
		}
		if len(x) > maxText {
			cut := maxText
			for cut > 0 && !utf8.RuneStart(x[cut]) {
				cut--
			}
			at.Truncated = len(x) - cut
			x = x[:cut]
		}
		at.Value = x
	case json.Number, bool, float64:
		if w.build {
			at.Value = x
		}
	default:
		at.Redacted = "schema"
		w.withheld++
	}
}

// elements renders the elements of a list or set, up to the limit.
func (w *walk) elements(at *Attr, list []any, f func(i int, e any) Attr) {
	for i, e := range list {
		if i >= maxChildren {
			at.Truncated = len(list) - maxChildren
			if !w.build {
				// A walk that only counts still has to see the rest: a sensitive
				// value does not leave the count because the list is long.
				for j := i; j < len(list); j++ {
					f(j, list[j])
				}
			}
			break
		}
		c := f(i, e)
		if w.build {
			at.Children = append(at.Children, c)
		}
	}
}

// entries renders the entries of a map or object in the order of their keys.
func (w *walk) entries(at *Attr, m map[string]any, f func(k string, e any) Attr) {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for i, k := range keys {
		if i >= maxChildren {
			at.Truncated = len(keys) - maxChildren
			if !w.build {
				for _, k2 := range keys[i:] {
					f(k2, m[k2])
				}
			}
			break
		}
		c := f(k, m[k])
		if w.build {
			at.Children = append(at.Children, c)
		}
	}
}

// withhold marks a value as not sent for what the attribute is, and keeps its
// strings for the check on other values.
func (w *walk) withhold(at *Attr, v any, why string) {
	at.Redacted = why
	at.Value, at.Children = nil, nil
	w.withheld++
	if w.sink != nil {
		w.sink.addFrom(w.origin, v)
		if at.Kind != "required" && at.Kind != "optional" {
			w.sink.addMade(w.origin, v)
		}
		if at.Kind == "computed" {
			w.sink.addGenerated(w.origin, v)
		}
	}
}

func guarded(name string) bool { return nameGuard.MatchString(name) }

// words splits a command line into its words and the white space between them.
var words = regexp.MustCompile(`\S+|\s+`)

// scrubCommand withholds what the command line of a recorded run may carry
// that is a secret: the value of every tag given with -t (a tag has no schema
// that says whether it is sensitive, and a tag is how a secret reaches a
// specification), the value of a flag or a key=value word whose name says
// secret. The command
// is recorded with its arguments joined by spaces, so a value of several words
// has lost its quotes: the words that follow a value that is withheld, up to the
// next flag, are withheld with it.
func scrubCommand(cmd string) string {
	var b strings.Builder
	var prev string // the previous word that was not white space
	inValue := false
	for _, w := range words.FindAllString(cmd, -1) {
		if strings.TrimSpace(w) == "" {
			b.WriteString(w)
			continue
		}
		out := w
		bare := strings.Trim(w, `"'`)
		switch {
		case inValue && !isFlag(bare):
			out = mask
		case prev == "-t" || prev == "--tag" || prev == "-tag" || clusterEndT.MatchString(strings.Trim(prev, `"'`)):
			out, inValue = tagWithoutValue(bare), true
		case clusterT.MatchString(bare) && strings.Contains(clusterT.FindStringSubmatch(bare)[2], "="):
			m := clusterT.FindStringSubmatch(bare)
			out, inValue = m[1]+"t"+tagWithoutValue(m[2]), true
		case strings.HasPrefix(bare, "--tag="):
			out, inValue = "--tag="+tagWithoutValue(strings.TrimPrefix(bare, "--tag=")), true
		case strings.HasPrefix(bare, "-t") && strings.Contains(bare, "=") && !strings.HasPrefix(bare, "--"):
			out, inValue = "-t"+tagWithoutValue(strings.TrimPrefix(bare, "-t")), true
		case isFlag(prev) && !strings.Contains(prev, "=") && guarded(prev) && !isFlag(bare):
			out, inValue = mask, true
		case strings.Contains(bare, "="):
			k, _, _ := strings.Cut(bare, "=")
			if guarded(k) {
				out, inValue = k+"="+mask, true
			}
		default:
			inValue = false
		}
		if inValue && isFlag(bare) && out == w && !strings.Contains(bare, "=") {
			inValue = false
		}
		b.WriteString(out)
		prev = w
	}
	return b.String()
}

func isFlag(w string) bool { return strings.HasPrefix(w, "-") }

// A short flag may be clustered with others that take no argument, and the last
// of the cluster then takes the next word, or the rest of this one: -vt k=v,
// -Tt k=v, -vtk=v. The flags that take no argument and have a short name are -v,
// -T, -c and -O. clusterT reads a word that carries the tag in it, clusterEndT one
// that ends in t, so that the next word is the tag.
var (
	clusterT    = regexp.MustCompile(`^(-[vTcO]+)t(.*)$`)
	clusterEndT = regexp.MustCompile(`^-[vTcO]+t$`)
)

// tagWithoutValue writes a tag, name=value, without the value.
func tagWithoutValue(tag string) string {
	k, v, ok := strings.Cut(tag, "=")
	if !ok || v == "" {
		return tag
	}
	return k + "=" + mask
}

func kindOf(a *schema.Attribute) string {
	switch {
	case a.Required:
		return "required"
	case a.Optional && a.Computed:
		return "optional,computed"
	case a.Optional:
		return "optional"
	case a.Computed:
		return "computed"
	}
	return ""
}

func attrType(a *schema.Attribute) string {
	if o := a.NestedType; o != nil {
		switch o.Nesting {
		case schema.NestingList:
			return "list(object)"
		case schema.NestingSet:
			return "set(object)"
		case schema.NestingMap:
			return "map(object)"
		}
		return "object"
	}
	return typeText(a.Type())
}

func blockType(nb *schema.NestedBlock) string {
	switch nb.Nesting {
	case schema.NestingList:
		return "list(block)"
	case schema.NestingSet:
		return "set(block)"
	case schema.NestingMap:
		return "map(block)"
	}
	return "block"
}

func elementType(t tftypes.Type) tftypes.Type {
	switch x := t.(type) {
	case tftypes.List:
		return x.ElementType
	case tftypes.Set:
		return x.ElementType
	case tftypes.Map:
		return x.ElementType
	}
	return nil
}

// typeText writes a type the way a specification does: list(string).
func typeText(t tftypes.Type) string {
	switch {
	case t == nil, t.Is(tftypes.DynamicPseudoType):
		return "dynamic"
	case t.Is(tftypes.String):
		return "string"
	case t.Is(tftypes.Number):
		return "number"
	case t.Is(tftypes.Bool):
		return "bool"
	case t.Is(tftypes.List{}):
		return "list(" + typeText(elementType(t)) + ")"
	case t.Is(tftypes.Set{}):
		return "set(" + typeText(elementType(t)) + ")"
	case t.Is(tftypes.Map{}):
		return "map(" + typeText(elementType(t)) + ")"
	case t.Is(tftypes.Tuple{}):
		return "tuple"
	case t.Is(tftypes.Object{}):
		return "object"
	}
	return "dynamic"
}

// clip shortens text to n runes.
func clip(s string, n int) string {
	s = strings.TrimSpace(s)
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	r := []rune(s)
	return strings.TrimSpace(string(r[:n])) + "…"
}

// objectNames and blockNames list the sensitive names below a nested attribute
// type and a nested block.
func objectNames(o *schema.Object) map[string]bool {
	if o == nil {
		return nil
	}
	return sensitiveNames(&schema.Block{Attributes: o.Attributes})
}

func blockNames(b *schema.Block) map[string]bool { return sensitiveNames(b) }

// sensitiveNames lists the names of the attributes and blocks of b, at any
// depth, whose values the schema withholds.
func sensitiveNames(b *schema.Block) map[string]bool {
	out := map[string]bool{}
	var block func(b *schema.Block, depth int)
	var attrs func(as []*schema.Attribute, depth int)
	attrs = func(as []*schema.Attribute, depth int) {
		for _, a := range as {
			if a.Sensitive || a.WriteOnly {
				out[a.Name] = true
			}
			if a.NestedType != nil && depth < maxDepth {
				attrs(a.NestedType.Attributes, depth+1)
			}
		}
	}
	block = func(b *schema.Block, depth int) {
		if b == nil || depth > maxDepth {
			return
		}
		attrs(b.Attributes, depth)
		for _, nb := range b.BlockTypes {
			block(nb.Block, depth+1)
		}
	}
	block(b, 0)
	return out
}
