package engine

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/pravo-systems/pravo/internal/config"
	"github.com/pravo-systems/pravo/internal/state"
)

// DiscoveryStatus says what a discovered object is to the specification and the
// state.
type DiscoveryStatus string

const (
	// Unmanaged is an object that no record, no $import and no acknowledgement
	// accounts for. It is the one that is shown as asking to be adopted.
	Unmanaged DiscoveryStatus = "unmanaged"
	// Acknowledged is an object that an acknowledgement covers: someone knows it is
	// unmanaged and means to leave it.
	Acknowledged DiscoveryStatus = "acknowledged"
	// Adopting is an object that a resource of the specification names in $import
	// and that has no record yet: the next apply brings it under management.
	Adopting DiscoveryStatus = "adopting"
	// Managed is an object that a record of the state is. It is counted and left
	// alone.
	Managed DiscoveryStatus = "managed"
)

// DiscoveryFreshness says whether the last run of a discovery is the run of the
// discovery as it is written now.
type DiscoveryFreshness string

const (
	// FreshCurrent: the last run listed in the scope that is written now.
	FreshCurrent DiscoveryFreshness = "current"
	// FreshStale: the scope, the limit, the type or the provider changed since the
	// last run, so what it found answers another question. Run the discovery again.
	FreshStale DiscoveryFreshness = "stale"
	// FreshNever: the discovery has not been run.
	FreshNever DiscoveryFreshness = "never"
	// FreshBroken: the scope cannot be listed with, so no run is possible.
	FreshBroken DiscoveryFreshness = "broken"
	// FreshUndeclared: the state holds a run of a discovery that the specification
	// no longer has.
	FreshUndeclared DiscoveryFreshness = "undeclared"
)

// DiscoveredItem is a listed object with what is known of it.
type DiscoveredItem struct {
	state.Listed
	Status DiscoveryStatus
	// By says what decided the status: the address of the record that is the object
	// (Managed), of the resource that adopts it (Adopting), or the id of the
	// acknowledgement that covers it (Acknowledged).
	By string
	// Reason is the reason of the acknowledgement.
	Reason string
}

// DiscoveryView is one discovery with the last run of it, seen against the state.
type DiscoveryView struct {
	Name        string
	Type        string
	Provider    string
	ProviderKey string
	Declared    bool
	Freshness   DiscoveryFreshness
	// ScopeErr is why a discovery that is Broken cannot run.
	ScopeErr string
	// Scope and Limit are the ones in the specification when it is declared, else the
	// ones of the last run.
	Scope map[string]any
	Limit int64
	Run   *state.DiscoveryRun
	// Items are the objects of the last run: unmanaged first, then adopting,
	// acknowledged and managed, each by display name and id.
	Items  []DiscoveredItem
	Counts map[DiscoveryStatus]int
	// Unreadable counts the records of the type that could not be read, so that the
	// objects they are may be listed as unmanaged.
	Unreadable int
	// Acks are the acknowledgements of the discovery.
	Acks []*state.Ack
}

// Unmanaged returns the items that ask to be adopted.
func (v *DiscoveryView) Unmanaged() []DiscoveredItem { return v.WithStatus(Unmanaged) }

// WithStatus returns the items that have the status, in the order of the view.
func (v *DiscoveryView) WithStatus(s DiscoveryStatus) []DiscoveredItem {
	var out []DiscoveredItem
	for _, it := range v.Items {
		if it.Status == s {
			out = append(out, it)
		}
	}
	return out
}

// DiscoveryReport is every discovery, and the acknowledgements of discoveries that
// are not declared.
type DiscoveryReport struct {
	Views []*DiscoveryView
	// OrphanAcks are acknowledgements of a discovery that the specification does
	// not have and the state holds no run of.
	OrphanAcks []*state.Ack
}

// View finds a discovery's view by name.
func (r *DiscoveryReport) View(name string) *DiscoveryView {
	for _, v := range r.Views {
		if v.Name == name {
			return v
		}
	}
	return nil
}

// DiscoveryInputs is what a report is made from. The caller reads each of them;
// nothing here reads the state or starts a provider.
type DiscoveryInputs struct {
	Root    *config.Root
	Runs    map[string]*state.DiscoveryRun
	Acks    []*state.Ack
	Records []*state.Resource
}

// BuildDiscoveryReport sets every discovery against the state. The discoveries of
// the specification come first, in the order they were found, then the ones that
// only the state holds a run of, by name.
func BuildDiscoveryReport(in DiscoveryInputs) *DiscoveryReport {
	// Only the records of a type that some run listed are read: the attributes of a
	// large state are decoded for nothing otherwise.
	listed := map[string]bool{}
	for _, run := range in.Runs {
		listed[run.Type] = true
	}
	for _, d := range in.Root.Discoveries {
		listed[d.Type] = true
	}
	idx := indexRecords(in.Records, listed)
	adopts := adoptions(in.Root)
	acksBy := map[string][]*state.Ack{}
	for _, a := range in.Acks {
		acksBy[a.Discovery] = append(acksBy[a.Discovery], a)
	}
	rep := &DiscoveryReport{}
	declared := map[string]bool{}
	for _, d := range in.Root.Discoveries {
		declared[d.Name] = true
		v := &DiscoveryView{Name: d.Name, Type: d.Type, Provider: d.Source.String(), ProviderKey: d.ProviderKey, Declared: true,
			Scope: d.Scope, Limit: d.Limit, Run: in.Runs[d.Name], Acks: acksBy[d.Name]}
		switch {
		case d.ScopeErr != nil:
			v.Freshness, v.ScopeErr = FreshBroken, d.ScopeErr.Error()
		case v.Run == nil:
			v.Freshness = FreshNever
		case v.Run.Digest != d.Digest:
			v.Freshness = FreshStale
		default:
			v.Freshness = FreshCurrent
		}
		v.fill(idx, adopts)
		rep.Views = append(rep.Views, v)
	}
	var extra []string
	for name := range in.Runs {
		if !declared[name] {
			extra = append(extra, name)
		}
	}
	sort.Strings(extra)
	for _, name := range extra {
		run := in.Runs[name]
		v := &DiscoveryView{Name: name, Type: run.Type, Provider: run.Provider, ProviderKey: run.ProviderKey, Freshness: FreshUndeclared,
			Scope: run.Scope, Limit: run.Limit, Run: run, Acks: acksBy[name]}
		v.fill(idx, adopts)
		rep.Views = append(rep.Views, v)
		declared[name] = true
	}
	for _, a := range in.Acks {
		if !declared[a.Discovery] {
			rep.OrphanAcks = append(rep.OrphanAcks, a)
		}
	}
	return rep
}

// fill sets the status of every object of the last run.
func (v *DiscoveryView) fill(idx map[string][]recordIdentity, adopts map[string][]adoption) {
	v.Counts = map[DiscoveryStatus]int{}
	recs := idx[v.Type]
	for _, r := range recs {
		if r.unreadable {
			v.Unreadable++
		}
	}
	if v.Run == nil {
		return
	}
	for _, l := range v.Run.Items {
		it := DiscoveredItem{Listed: l, Status: Unmanaged}
		if addr, ok := managedBy(l, recs); ok {
			it.Status, it.By = Managed, addr
		} else if addr, ok := adoptedBy(l, adopts[v.Type]); ok {
			it.Status, it.By = Adopting, addr
		} else if a := coveredBy(l, v.Acks); a != nil {
			it.Status, it.By, it.Reason = Acknowledged, a.ID, a.Reason
		}
		v.Counts[it.Status]++
		v.Items = append(v.Items, it)
	}
	rank := map[DiscoveryStatus]int{Unmanaged: 0, Adopting: 1, Acknowledged: 2, Managed: 3}
	sort.SliceStable(v.Items, func(i, j int) bool {
		a, b := v.Items[i], v.Items[j]
		if rank[a.Status] != rank[b.Status] {
			return rank[a.Status] < rank[b.Status]
		}
		if a.Name != b.Name {
			return a.Name < b.Name
		}
		return a.ID < b.ID
	})
}

func coveredBy(l state.Listed, acks []*state.Ack) *state.Ack {
	for _, a := range acks {
		if a.Covers(l) {
			return a
		}
	}
	return nil
}

// A record of the state, reduced to what an identity is compared with.
type recordIdentity struct {
	addr       string
	attrs      map[string]string
	unreadable bool
}

func indexRecords(records []*state.Resource, types map[string]bool) map[string][]recordIdentity {
	idx := map[string][]recordIdentity{}
	for _, r := range records {
		if r.Mode != "managed" || !types[r.Type] {
			continue
		}
		ri := recordIdentity{addr: r.Address(), attrs: map[string]string{}}
		var attrs map[string]any
		dec := json.NewDecoder(bytes.NewReader(r.Attributes))
		dec.UseNumber()
		if len(r.Attributes) == 0 || dec.Decode(&attrs) != nil {
			ri.unreadable = true
		}
		for k, v := range attrs {
			switch v.(type) {
			case string, json.Number, bool:
				ri.attrs[k] = scalarText(v)
			}
		}
		idx[r.Type] = append(idx[r.Type], ri)
	}
	return idx
}

func scalarText(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case json.Number:
		return x.String()
	}
	return fmt.Sprint(v)
}

// scoping names the identity attributes that say where an object is, not which one
// it is: two objects of one type in one region share them. An identity is matched
// to a record only if an attribute that is not one of these agrees as well.
var scoping = map[string]bool{
	"region": true, "account_id": true, "account": true, "project": true, "project_id": true,
	"subscription_id": true, "location": true, "zone": true, "tenant_id": true,
	"organization": true, "organization_id": true,
}

// managedBy finds the record that is the listed object. An identity matches a
// record of the same type when every attribute of the identity that the record
// also has is equal, and at least one of those says which object it is. A record
// that does not carry the attributes of the identity cannot be matched, and the
// object is then listed as unmanaged: a miss shows an object that is managed, a
// false match would hide one that is not.
func managedBy(l state.Listed, recs []recordIdentity) (string, bool) {
	for _, r := range recs {
		if r.unreadable {
			continue
		}
		hit, strong := 0, false
		ok := true
		for name, v := range l.Identity {
			if v == nil {
				continue
			}
			rv, has := r.attrs[name]
			if !has {
				continue
			}
			if rv != identityText(v) {
				ok = false
				break
			}
			hit++
			if !scoping[name] {
				strong = true
			}
		}
		if ok && hit > 0 && strong {
			return r.addr, true
		}
	}
	return "", false
}

func identityText(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprint(v)
	}
	return string(b)
}

type adoption struct{ addr, id string }

// adoptions lists, by type, the resources of the specification that name an
// object in $import.
func adoptions(root *config.Root) map[string][]adoption {
	out := map[string][]adoption{}
	addrs := make([]string, 0, len(root.Imports))
	for addr := range root.Imports {
		addrs = append(addrs, addr)
	}
	sort.Strings(addrs)
	for _, addr := range addrs {
		res := root.Resource(addr)
		if res == nil {
			continue
		}
		out[res.Type] = append(out[res.Type], adoption{addr: addr, id: root.Imports[addr].ID})
	}
	return out
}

func adoptedBy(l state.Listed, as []adoption) (string, bool) {
	for _, a := range as {
		if a.id == l.ImportID && a.id != "" || a.id == l.ID || a.id == l.Key {
			return a.addr, true
		}
	}
	return "", false
}

// SnippetName suggests the name a resource that adopts the object is given: the
// display name, else the id, as an identifier, that no resource or provider of the
// specification has.
func SnippetName(root *config.Root, l state.Listed) string {
	base := identifier(l.Name)
	if base == "" {
		base = identifier(l.ID)
	}
	if base == "" {
		base = "adopted"
	}
	taken := map[string]bool{}
	for _, r := range root.Resources {
		taken[r.Address()] = true
	}
	for _, d := range root.Discoveries {
		taken[d.Name] = true
	}
	for _, p := range root.ProviderDecls {
		taken[p.Path.String()] = true
	}
	name := base
	for i := 2; taken[name]; i++ {
		name = fmt.Sprintf("%s_%d", base, i)
	}
	return name
}

func identifier(s string) string {
	var b strings.Builder
	under := false
	for _, r := range strings.ToLower(s) {
		switch {
		case r >= 'a' && r <= 'z' || r >= '0' && r <= '9':
			b.WriteRune(r)
			under = false
		default:
			if b.Len() > 0 && !under {
				b.WriteByte('_')
				under = true
			}
		}
	}
	out := strings.TrimRight(b.String(), "_")
	if out == "" {
		return ""
	}
	if out[0] >= '0' && out[0] <= '9' {
		out = "r_" + out
	}
	switch out {
	case "package", "import", "for", "in", "if", "let", "true", "false", "null":
		out += "_"
	}
	if len(out) > 60 {
		out = strings.TrimRight(out[:60], "_")
	}
	return out
}

// AdoptSnippet is the CUE that brings a discovered object under management: a
// resource of the type, built through the provider the discovery lists through,
// that names the object in $import. body, when it is not empty, is the attributes
// the object has, as ReadAdoption renders them; without it the resource has only
// the import, and a plan says what it lacks.
func AdoptSnippet(root *config.Root, d *DiscoveryView, l state.Listed, body string) string {
	var b strings.Builder
	prov := "$provider"
	if p, ok := root.ProviderPath(d.ProviderKey); ok {
		prov = p.String()
	} else if len(root.ProviderDecls) == 1 {
		prov = root.ProviderDecls[0].Path.String()
	}
	label := d.Type + " " + quoteCUE(l.ID)
	if l.Name != "" {
		label += " (" + oneLine(l.Name) + ")"
	}
	fmt.Fprintf(&b, "// Adopts the %s that the discovery %s found.\n", label, oneLine(d.Name))
	fmt.Fprintf(&b, "%s: %s.#Resource.%s & {\n", SnippetName(root, l), prov, fieldPath(d.Type))
	if l.ImportID != "" {
		fmt.Fprintf(&b, "\t$import: %s\n", quoteCUE(l.ImportID))
	} else {
		fmt.Fprintf(&b, "\t// The provider lists this object by %s, which is not an id that $import takes.\n", oneLine(l.Key))
		fmt.Fprintf(&b, "\t// Use the id that the provider's documentation gives for importing a %s.\n", oneLine(d.Type))
		b.WriteString("\t$import: \"<id>\"\n")
	}
	b.WriteString(body)
	b.WriteString("}\n")
	return b.String()
}

func fieldPath(typ string) string {
	if identifier(typ) == typ {
		return typ
	}
	return quoteCUE(typ)
}

// PlainText makes a text that someone else wrote safe to put in a line of output or
// in a comment: a control character, which could end the line or move the cursor,
// and a character that changes the direction of the text around it, become a space.
func PlainText(s string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r < 0x20 || r == 0x7f || (r >= 0x80 && r < 0xa0):
			return ' '
		case r == '\u200e' || r == '\u200f' || (r >= 0x202a && r <= 0x202e) || (r >= 0x2066 && r <= 0x2069):
			return ' '
		}
		return r
	}, s)
}

func oneLine(s string) string {
	s = PlainText(s)
	if len(s) > 80 {
		s = s[:80] + "..."
	}
	return s
}

// quoteCUE writes s as a CUE string. The characters that change the direction of
// the text around them are written as escapes, so that a literal reads as it is.
func quoteCUE(s string) string {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(s)
	out := strings.TrimSuffix(b.String(), "\n")
	return bidiEscaper.Replace(out)
}

var bidiEscaper = func() *strings.Replacer {
	var pairs []string
	for _, r := range []rune{'\u200e', '\u200f', '\u202a', '\u202b', '\u202c', '\u202d', '\u202e', '\u2066', '\u2067', '\u2068', '\u2069'} {
		pairs = append(pairs, string(r), fmt.Sprintf("\\u%04x", r))
	}
	return strings.NewReplacer(pairs...)
}()
