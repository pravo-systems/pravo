package serve

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"unicode"

	"github.com/pravo-systems/pravo/internal/config"
	"github.com/pravo-systems/pravo/internal/engine"
	"github.com/pravo-systems/pravo/internal/state"
)

// What the discovery document keeps of a long list or a long text.
const (
	// maxDiscoveryItems bounds the objects of one discovery that are listed.
	maxDiscoveryItems = 2000
	// maxFields bounds the fields of an identity or a scope.
	maxFields = 16
	// maxDiscoveryText bounds each text that a provider or a person wrote.
	maxDiscoveryText = 300
	// maxAckPatternsShown bounds the patterns of an acknowledgement that are listed.
	maxAckPatternsShown = 50
)

// discoveryState is what the model holds of the discoveries: the document that
// GET /api/discovery sends and the report it was made from, which GET /api/adopt
// looks objects up in.
type discoveryState struct {
	doc    *Discovery
	json   []byte
	report *engine.DiscoveryReport
}

// buildDiscovery sets what the discoveries found against the records and the
// specification of this revision. It reads the state and asks no provider. It runs
// after the secrets of the state are known: every text that a provider or a person
// wrote is masked against them.
func (m *model) buildDiscovery(rev string) {
	var warnings []string
	runs, problems, err := m.store.Discoveries()
	if err != nil {
		warnings = append(warnings, "The discoveries could not be listed: "+err.Error())
	}
	for _, p := range problems {
		warnings = append(warnings, fmt.Sprintf("%s cannot be read: %s", p.Key, headline(p.Err.Error())))
	}
	var records []*state.Resource
	for _, a := range m.order {
		// A record that is sealed to this reader has no attributes: it is counted as
		// one that cannot be read.
		if mn := m.nodes[a]; mn.rec != nil {
			records = append(records, mn.rec)
		}
	}
	root := m.spec.root
	specOK := root != nil
	if root == nil {
		root = &config.Root{}
	}
	report := engine.BuildDiscoveryReport(engine.DiscoveryInputs{Root: root, Runs: runs, Records: records})
	doc := &Discovery{Version: APIVersion, Revision: rev, SpecOK: specOK, Discoveries: []DiscoveryInfo{}, Command: "pravo discover"}
	for _, v := range report.Views {
		info := m.discoveryInfo(v, specOK)
		doc.Unmanaged += info.Counts.Unmanaged
		doc.Discoveries = append(doc.Discoveries, info)
	}
	for i := range warnings {
		warnings[i] = m.scrubText(warnings[i])
	}
	doc.Warnings = warnings
	m.disc = &discoveryState{doc: doc, report: report}
	m.disc.json, _ = json.Marshal(doc)
}

func (m *model) clipText(s string) string {
	return clip(stripControl(m.scrubText(s)), maxDiscoveryText)
}

// stripControl removes the control and direction characters of a text that someone
// else wrote, so that it cannot move the text around it.
func stripControl(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || r == '‮' || r == '‭' || r == '⁦' || r == '⁧' || r == '⁨' || r == '⁩' || r == '‎' || r == '‏' {
			return -1
		}
		return r
	}, s)
}

func (m *model) fieldList(values map[string]any) []Field {
	names := make([]string, 0, len(values))
	for n := range values {
		names = append(names, n)
	}
	sort.Strings(names)
	out := make([]Field, 0, len(names))
	for _, n := range names {
		if len(out) == maxFields {
			break
		}
		v := values[n]
		if v == nil {
			continue
		}
		out = append(out, Field{Name: m.clipText(n), Value: m.clipText(state.IdentityValue(v))})
	}
	return out
}

func (m *model) discoveryInfo(v *engine.DiscoveryView, specOK bool) DiscoveryInfo {
	info := DiscoveryInfo{
		Name: m.clipText(v.Name), Type: v.Type, Provider: v.Provider, Instance: v.ProviderKey, Declared: v.Declared,
		Freshness: string(v.Freshness), Problem: m.clipText(v.ScopeErr), Scope: m.fieldList(v.Scope), Limit: v.Limit,
		Counts: DiscoveryCounts{
			Unmanaged: v.Counts[engine.Unmanaged], Adopting: v.Counts[engine.Adopting],
			Acknowledged: v.Counts[engine.Acknowledged], Managed: v.Counts[engine.Managed],
		},
		Unreadable: v.Unreadable, Items: []DiscoveredInfo{}, Command: runCommand(v.Name),
	}
	if !specOK {
		info.Freshness = "unknown"
	}
	if info.Scope == nil {
		info.Scope = []Field{}
	}
	if n := info.Counts.Unmanaged; n > 0 && n <= maxAckPatternsShown && v.Declared {
		ids := make([]string, 0, n)
		for _, it := range v.Unmanaged() {
			ids = append(ids, engine.EscapeGlob(it.ID))
		}
		info.AckAll = m.scrubText(engine.FormatCUE(engine.AckEntry(v.Name, ids, nil, "<why>")))
	}
	if ref := m.spec.discRefs[v.Name]; ref != nil && v.Declared {
		info.Source = ref
	}
	if r := v.Run; r != nil {
		ri := &RunInfo{Started: r.Started, Finished: r.Finished, Actor: m.clipText(r.Actor), Status: r.Status, Total: len(r.Items)}
		for _, w := range r.Warnings {
			if len(ri.Warnings) == 10 {
				break
			}
			ri.Warnings = append(ri.Warnings, m.clipText(w))
		}
		info.Run = ri
	}
	for _, it := range v.Items {
		if it.Status == engine.Managed {
			continue
		}
		if len(info.Items) == maxDiscoveryItems {
			info.ItemsOmitted++
			continue
		}
		info.Items = append(info.Items, DiscoveredInfo{
			Key: m.clipText(it.Key), ID: m.clipText(it.ID), Name: m.clipText(it.Name), Status: string(it.Status),
			By: m.clipText(it.By), Reason: m.clipText(it.Reason), Identity: m.fieldList(it.Identity), Importable: it.ImportID != "",
		})
	}
	for _, a := range v.Acks {
		info.Acks = append(info.Acks, m.ackInfo(a, v))
	}
	return info
}

func (m *model) ackInfo(a *config.Ack, v *engine.DiscoveryView) AckInfo {
	pats := func(in []string) []string {
		var out []string
		for i, p := range in {
			if i == maxAckPatternsShown {
				out = append(out, fmt.Sprintf("… and %d more", len(in)-i))
				break
			}
			out = append(out, m.clipText(p))
		}
		return out
	}
	info := AckInfo{Ref: a.Ref(), IDs: pats(a.IDs), Names: pats(a.Names), Reason: m.clipText(a.Reason), Covers: -1}
	if v != nil && v.Run != nil {
		info.Covers = 0
		for _, l := range v.Run.Items {
			if a.Covers(l.ID, l.Key, l.ImportID, l.Name) {
				info.Covers++
			}
		}
	}
	return info
}

// runCommand is the command that runs a discovery.
func runCommand(name string) string {
	if !safeArg(name) {
		return "pravo discover"
	}
	return "pravo discover " + engine.ShellWord(name)
}

// safeArg says whether a text can be put in a command that is shown for copying:
// it has no control or direction character and is not long.
func safeArg(s string) bool {
	if s == "" || len(s) > 512 || s != stripControl(s) {
		return false
	}
	return !strings.HasPrefix(s, "-")
}

// adoptFor answers GET /api/adopt: the snippet and the commands for one object of
// a discovery's last run.
func (m *model) adoptFor(name, key string) (*Adopt, bool) {
	if m.disc == nil {
		return nil, false
	}
	v := m.disc.report.View(name)
	if v == nil {
		return nil, false
	}
	var item *engine.DiscoveredItem
	for i := range v.Items {
		// The page names an object by the key it was sent, which the document
		// shows masked and without control characters.
		if v.Items[i].Status != engine.Managed && m.clipText(v.Items[i].Key) == key {
			item = &v.Items[i]
			break
		}
	}
	if item == nil {
		return nil, false
	}
	root := m.spec.root
	if root == nil {
		root = &config.Root{}
	}
	snippet := engine.FormatCUE(engine.AdoptSnippet(root, v, item.Listed, ""))
	out := &Adopt{Discovery: m.clipText(name), Key: m.clipText(item.Key), ID: m.clipText(item.ID), Status: string(item.Status), Snippet: m.scrubText(snippet)}
	if item.Status == engine.Unmanaged {
		out.Ack = m.scrubText(engine.FormatCUE(engine.AckEntry(name, []string{engine.EscapeGlob(item.ID)}, nil, "<why>")))
	}
	if item.Status == engine.Adopting {
		out.Steps = append(out.Steps, SuggestionStep{Kind: "note", Text: item.By + " already names this object in $import. The next plan reads it and an apply records it."})
		return out, true
	}
	if !safeArg(name) || !safeArg(item.ID) {
		out.Steps = append(out.Steps, SuggestionStep{Kind: "note", Text: "The name or the id of this object has characters that a command cannot carry safely, so none is suggested."})
		return out, true
	}
	n, id := engine.ShellWord(name), engine.ShellWord(item.ID)
	if item.ImportID != "" {
		out.Steps = append(out.Steps,
			SuggestionStep{Kind: "note", Text: "To write the resource with the attributes the object has, ask the provider for it:"},
			SuggestionStep{Kind: "command", Text: "pravo discover adopt " + n + " " + id + " --read"})
	} else {
		out.Steps = append(out.Steps,
			SuggestionStep{Kind: "note", Text: "The identity is not an id that $import takes. Give the id that the provider documents for importing a " + v.Type + ":"},
			SuggestionStep{Kind: "command", Text: "pravo discover adopt " + n + " " + id + " --read --import-id <id>", Placeholder: true})
	}
	return out, true
}

func (s *Server) adopt(w http.ResponseWriter, r *http.Request, m *model) {
	q := r.URL.Query()
	name, key := q.Get("discovery"), q.Get("key")
	if len(name) > 512 || len(key) > 2048 {
		writeError(w, http.StatusBadRequest, "the name or the key is too long")
		return
	}
	a, ok := m.adoptFor(name, key)
	if !ok {
		writeError(w, http.StatusNotFound, "no such object in the last run of that discovery")
		return
	}
	writeJSON(w, http.StatusOK, a)
}
