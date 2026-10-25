package serve

import (
	"encoding/json"
	"regexp"
	"sort"
	"strconv"

	"github.com/pravo-systems/pravo/internal/protect"
	"github.com/pravo-systems/pravo/internal/state"
)

const (
	// maxPolicyText bounds the text of a rule that is sent.
	maxPolicyText = 64 << 10
	// maxJudgedRuns bounds the transactions listed under a version.
	maxJudgedRuns = 8
)

var pctRE = regexp.MustCompile(`%([0-9A-F]{4})`)

// policyDisplay is the name of a policy as its address reads: the state keeps it
// encoded as an object name.
func policyDisplay(name string) string {
	return pctRE.ReplaceAllStringFunc(name, func(s string) string {
		n, err := strconv.ParseInt(s[1:], 16, 32)
		if err != nil {
			return s
		}
		return string(rune(n))
	})
}

// buildPolicies reads the versions of every policy and sets them against what the
// specification declares and the transactions that were judged by them. It reads the
// state and asks no provider.
func (m *model) buildPolicies(rev string) {
	doc := &Policies{Version: APIVersion, Revision: rev, SpecOK: m.spec.root != nil, Policies: []PolicyInfo{}, Command: "pravo apply"}
	var readers []protect.Reader
	if f := m.srv.opts.PolicyReaders; f != nil {
		readers, doc.Checked = f(m.store)
	}
	history, err := m.store.PolicyHistory(m.store.Context(), readers)
	if err != nil {
		doc.Problem = m.scrubText(headline(err.Error()))
		history = map[string][]state.PolicyVersion{}
	}
	judged := map[string][]JudgedRun{}
	for _, t := range m.transactions() {
		for _, ref := range t.Policies {
			judged[ref] = append(judged[ref], JudgedRun{ID: t.ID, Kind: t.Kind, Status: t.Status, Time: t.Started})
		}
	}
	declared := map[string]string{}
	if root := m.spec.root; root != nil {
		for _, p := range root.Policies {
			declared[state.PolicyName(p.Name)] = p.Rule
		}
	}
	names := map[string]bool{}
	for n := range history {
		names[n] = true
	}
	for n := range declared {
		names[n] = true
	}
	order := make([]string, 0, len(names))
	for n := range names {
		order = append(order, n)
	}
	sort.Strings(order)
	for _, name := range order {
		vs := history[name]
		info := PolicyInfo{Name: m.clipText(policyDisplay(name)), Versions: []PolicyVersionInfo{}, Source: m.spec.polRefs[name]}
		rule, isDeclared := declared[name]
		info.Declared = isDeclared
		var head *state.PolicyVersion
		if len(vs) > 0 {
			head = &vs[len(vs)-1]
			info.Head = head.Seq
		}
		live := head != nil && !head.Tombstone()
		switch {
		case !doc.SpecOK && live:
			info.Status = "in-force"
		case live && isDeclared && head.Text == rule:
			info.Status = "in-force"
		case live && isDeclared:
			info.Status = "pending"
		case live:
			info.Status = "removing"
		case isDeclared:
			info.Status = "new"
		default:
			info.Status = "removed"
		}
		if isDeclared && info.Status != "in-force" {
			info.Rule = m.scrubText(clip(rule, maxPolicyText))
		}
		if live {
			doc.InForce++
		}
		if info.Status == "pending" || info.Status == "new" || info.Status == "removing" {
			doc.Pending++
		}
		for i := len(vs) - 1; i >= 0; i-- {
			v := vs[i]
			vi := PolicyVersionInfo{Seq: v.Seq, Ref: v.Ref(), Digest: v.Digest(), At: v.At, Removal: v.Tombstone()}
			if !v.Tombstone() {
				vi.Text = m.scrubText(clip(v.Text, maxPolicyText))
			}
			if doc.Checked {
				vi.Signature = v.Status.Key()
				if v.Status == protect.SigVerified {
					vi.Signer = m.clipText(v.Signer)
				}
			}
			runs := judged[v.Ref()]
			vi.Judged = len(runs)
			info.Judged += vi.Judged
			if len(runs) > maxJudgedRuns {
				runs = runs[:maxJudgedRuns]
			}
			vi.Runs = runs
			info.Versions = append(info.Versions, vi)
		}
		doc.Policies = append(doc.Policies, info)
	}
	m.pol = doc
	m.polJSON, _ = json.Marshal(doc)
}

