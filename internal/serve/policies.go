package serve

import (
	"encoding/json"
	"sort"

	"github.com/pravo-systems/pravo/internal/engine"
	"github.com/pravo-systems/pravo/internal/protect"
	"github.com/pravo-systems/pravo/internal/state"
)

const (
	// maxPolicyText bounds the text of a rule that is sent.
	maxPolicyText = 64 << 10
	// maxJudgedRuns bounds the transactions listed under a version.
	maxJudgedRuns = 8
	// maxPlanPolicies bounds the policy changes listed for a plan.
	maxPlanPolicies = 200
)

// policyDisplay is the name of a policy as its address reads.
func policyDisplay(name string) string { return state.PolicyDisplay(name) }

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
	described := map[string]string{}
	if root := m.spec.root; root != nil {
		for _, p := range root.Policies {
			declared[state.PolicyName(p.Name)] = p.Rule
			described[state.PolicyName(p.Name)] = p.Description
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
		info := PolicyInfo{Name: m.clipText(policyDisplay(name)), Versions: []PolicyVersionInfo{}, Description: m.clipText(described[name]), Source: m.spec.polRefs[name]}
		info.Written = m.policySource(info.Source)
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
			info.Rule = m.scrubText(policyDeclaration(info.Name, "", clip(rule, maxPolicyText)))
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
				vi.Text = m.scrubText(policyDeclaration(info.Name, "", clip(v.Text, maxPolicyText)))
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
	m.polText = map[string]polTexts{}
	for _, name := range order {
		pt := polTexts{declared: declared[name]}
		if vs := history[name]; len(vs) > 0 && !vs[len(vs)-1].Tombstone() {
			pt.inForce = vs[len(vs)-1].Text
		}
		m.polText[m.clipText(policyDisplay(name))] = pt
	}
	m.polJSON, _ = json.Marshal(doc)
}

// planPolicies lists what a plan does to the policies, by the names of the Policies section.
func (m *model) planPolicies(entries []engine.PolicyEntry) []PlanPolicy {
	var out []PlanPolicy
	for _, en := range entries {
		if len(out) == maxPlanPolicies {
			break
		}
		out = append(out, PlanPolicy{Name: m.clipText(policyDisplay(en.Name)), Change: en.Kind(), Base: en.Base})
	}
	return out
}

// policySource reads the lines of the specification that write a policy, masked as every other source is.
func (m *model) policySource(ref *Ref) *SourceBlock {
	if ref == nil || m.spec.root == nil {
		return nil
	}
	// The load of a model holds the lock of the CUE values.
	ss := m.spec
	var notes []string
	reader := &blockReader{ss: ss, sens: map[string]bool{}, secrets: m.secrets, notes: &notes}
	b := reader.read("definition", "", ref, false)
	if b == nil {
		return nil
	}
	for i := range b.Lines {
		b.Lines[i].Text = m.scrubText(b.Lines[i].Text)
	}
	return b
}
