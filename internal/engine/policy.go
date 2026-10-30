package engine

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"sort"
	"strings"
	"time"

	"cuelang.org/go/cue"
	cueerrors "cuelang.org/go/cue/errors"
	cuejson "cuelang.org/go/encoding/json"
	"go.opentelemetry.io/otel/attribute"

	"github.com/pravo-systems/pravo/internal/protect"
	"github.com/pravo-systems/pravo/internal/state"
	"github.com/pravo-systems/pravo/internal/telemetry"
)

// Policies. A policy is a node of the specification, built from pravo.#Policy, that
// judges every plan: its rule is standalone CUE that is unified with the plan document
// and must make it concrete. The state keeps the versions of each policy (see
// state/policy.go); the policies in force are the latest version of each name that is
// not a tombstone. A plan lists what it changes in them as entries, and the apply
// writes the versions last. At apply, under the locks, the resource changes of the
// whole plan file are judged by the policies in force, and the signed start of the
// transaction lists the versions that judged them.

// PolicyEntry is a change that a plan makes to the policies: a new policy, a new
// text for one, or a removal.
type PolicyEntry struct {
	// Name is the policy as the state names it.
	Name string `json:"name"`
	// Base is the number of the latest version when the plan was made, 0 for a policy
	// the state has none of.
	Base int `json:"base"`
	// Digest is the SHA-256 of the new text in hex; empty for a removal. The text is in
	// the .cue files of the root, which the configuration fingerprint covers.
	Digest string `json:"digest,omitempty"`
}

func (p PolicyEntry) removal() bool { return p.Digest == "" }

// PolicyCounts says how many policies a plan adds, changes and removes.
func (p *Plan) PolicyCounts() (added, changed, removed int) { return CountPolicyEntries(p.Policies) }

// CountPolicyEntries says how many of the entries add a policy, change one and remove one.
func CountPolicyEntries(entries []PolicyEntry) (added, changed, removed int) {
	for _, en := range entries {
		switch {
		case en.removal():
			removed++
		case en.Base == 0:
			added++
		default:
			changed++
		}
	}
	return
}

// Kind says what the entry does to the policy: "new", "changed" or "removed".
func (p PolicyEntry) Kind() string {
	switch {
	case p.removal():
		return "removed"
	case p.Base == 0:
		return "new"
	}
	return "changed"
}

// policySet reads the policies of the state, checked against the readers file.
func (e *Engine) policySet(ctx context.Context) (*state.PolicySet, error) {
	return e.State.PolicySet(ctx, e.Opts.Readers)
}

// planPolicies compares the policies of the specification with those in force and
// returns what a plan changes in them.
func (e *Engine) planPolicies(ctx context.Context) ([]PolicyEntry, error) {
	if len(e.Root.Policies) > 0 && e.State.Sealer() == nil {
		return nil, refused("the root declares policies, and a policy version is signed: the state must be sealed (pravo protect init --encrypt)")
	}
	set, err := e.policySet(ctx)
	if err != nil {
		return nil, err
	}
	declared := map[string]string{}
	for _, p := range e.Root.Policies {
		declared[state.PolicyName(p.Name)] = p.Rule
	}
	var entries []PolicyEntry
	for _, name := range slices.Sorted(maps.Keys(declared)) {
		text := declared[name]
		head, ok := set.Head(name)
		if ok && !head.Tombstone() && head.Text == text {
			continue
		}
		base := 0
		if ok {
			base = head.Seq
		}
		if err := e.checkNewPolicy(name, text); err != nil {
			return nil, err
		}
		entries = append(entries, PolicyEntry{Name: name, Base: base, Digest: state.TextDigest(text)})
	}
	for _, v := range set.InForce() {
		if _, ok := declared[v.Name]; !ok {
			entries = append(entries, PolicyEntry{Name: v.Name, Base: v.Seq})
		}
	}
	return entries, nil
}

// checkPolicyRoles refuses the removal of a policy by a key that is not an admin in the
// readers file. A writer adds and edits policies; removing one is for an admin, since it
// is the one change that lets a plan through that the policy would have stopped.
func (e *Engine) checkPolicyRoles(plan *Plan) error {
	if !protect.RolesOn(e.Opts.Readers) {
		return nil
	}
	var removing []string
	for _, en := range plan.Policies {
		if en.removal() {
			removing = append(removing, en.Name)
		}
	}
	if len(removing) == 0 {
		return nil
	}
	role, ok := protect.RoleOf(e.Opts.Readers, e.State.SigningKey())
	if ok && role.Allows(protect.RoleAdmin) {
		return nil
	}
	return refused("removing the polic%s %s needs an admin, and your key is %s in the readers file", map[bool]string{true: "y", false: "ies"}[len(removing) == 1], strings.Join(removing, ", "), map[bool]string{true: "a " + role.String(), false: "not in it"}[ok])
}

// checkNewPolicy refuses a text that cannot be a policy: it compiles alone, imports
// only the standard library, and accepts the document of an empty plan. It catches a
// rule with a misspelt bound, and not one that is too strict.
func (e *Engine) checkNewPolicy(name, text string) error {
	if p := CheckRule(e.Root.Ctx, name, text); p != nil {
		return WithCode(ExitInvalid, errors.New(p.Message))
	}
	return nil
}

// judgedDoc is the document a policy is judged against, made from the plan file alone.
// It is the document of "plan --json" without what the file does not hold: the state
// gives it no dependents and no blast radius, the clock no time, the invocation no actor
// or reason, and the marks of an earlier stage are cleared.
func (e *Engine) judgedDoc(pf *PlanFile) (cue.Value, error) {
	data, _, err := e.PolicyDocument(pf)
	if err != nil {
		return cue.Value{}, err
	}
	return buildDoc(e.Root.Ctx, data)
}

// PolicyDocument is the JSON of the document that policies judge pf by, and the plan
// it was made from.
func (e *Engine) PolicyDocument(pf *PlanFile) ([]byte, *Plan, error) {
	plan, err := e.newPlan(pf)
	if err != nil {
		return nil, nil, err
	}
	data, err := documentOf(pf, plan)
	return data, plan, err
}

func documentOf(pf *PlanFile, plan *Plan) ([]byte, error) {
	v := &View{Format: ViewFormat, Created: pf.Created, Actor: pf.Actor, Reason: pf.Reason, GitRevision: pf.GitRevision,
		Destroy: pf.Destroy, Stats: pf.Stats, Changes: []ChangeView{}, Outputs: map[string]any{}, Reservation: pf.Reservation,
		Providers: map[string]string{}}
	for local, ver := range pf.Providers {
		v.Providers[local] = ver
	}
	v.Summary.Add, v.Summary.Change, v.Summary.Destroy, v.Summary.Read = plan.Counts()
	for _, ch := range plan.Changes {
		ch.Applied = false
		v.Changes = append(v.Changes, changeViewOf(ch))
	}
	addOutputs(v, plan)
	return marshalDoc(v)
}

// marshalDoc is the JSON of the document of v, without the number that only the state can give.
func marshalDoc(v *View) ([]byte, error) {
	data, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	// blast_radius is a number that only the state can give.
	var doc map[string]any
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	if err := dec.Decode(&doc); err != nil {
		return nil, err
	}
	if sum, ok := doc["summary"].(map[string]any); ok {
		delete(sum, "blast_radius")
	}
	return json.Marshal(doc)
}

// StateDoc is the document of a plan that would update every one of the records, each
// to the state it holds. It is what a policy would see of the state if every resource
// were in a plan, and answers which records a rule would refuse today. The changes are
// the ones given, in order.
func StateDoc(changes []ChangeView) (Doc, error) {
	v := &View{Format: ViewFormat, Changes: changes, Outputs: map[string]any{}, Providers: map[string]string{}}
	if v.Changes == nil {
		v.Changes = []ChangeView{}
	}
	v.Summary.Change = len(changes)
	data, err := marshalDoc(v)
	if err != nil {
		return Doc{}, err
	}
	d := Doc{JSON: data}
	for _, c := range changes {
		d.Addrs = append(d.Addrs, c.Addr)
	}
	return d, nil
}

// ProposedDoc is the document of a plan that would create every one of the changes, each
// with the values the specification gives it and none that a provider would fill in. It is
// an approximation of the plan that pasting a piece of specification would make, for a rule
// to be tried on before any provider is asked. The changes are the ones given, in order.
func ProposedDoc(changes []ChangeView) (Doc, error) {
	v := &View{Format: ViewFormat, Changes: changes, Outputs: map[string]any{}, Providers: map[string]string{}}
	if v.Changes == nil {
		v.Changes = []ChangeView{}
	}
	v.Summary.Add = len(changes)
	data, err := marshalDoc(v)
	if err != nil {
		return Doc{}, err
	}
	d := Doc{JSON: data}
	for _, c := range changes {
		d.Addrs = append(d.Addrs, c.Addr)
	}
	return d, nil
}

// buildDoc turns the JSON of a document into a value of ctx.
func buildDoc(ctx *cue.Context, data []byte) (cue.Value, error) {
	expr, err := cuejson.Extract("plan", data)
	if err != nil {
		return cue.Value{}, err
	}
	pv := ctx.BuildExpr(expr)
	return pv, pv.Err()
}

// changesResources reports whether a plan file has a row that creates, updates,
// replaces or deletes a resource. A plan without one is not judged.
func (pf *PlanFile) changesResources() bool {
	for _, sc := range pf.Changes {
		switch sc.Action {
		case ActionCreate, ActionUpdate, ActionReplace, ActionDelete:
			return true
		}
	}
	return false
}

// Judge judges the whole plan file, whatever has been applied from it and whatever an
// apply selects, by the policies in force in set. It returns the versions that judged,
// as a receipt lists them; none when no policy is in force or the file changes no
// resource. A policy that is violated is an error of exit code 5.
func (e *Engine) Judge(set *state.PolicySet, pf *PlanFile) ([]string, error) {
	force := set.InForce()
	if len(force) == 0 || !pf.changesResources() {
		return nil, nil
	}
	doc, err := e.judgedDoc(pf)
	if err != nil {
		return nil, err
	}
	return judgeVersions(e, doc, force)
}

// judgeVersions judges the document by the versions, each on its own: all must hold.
func judgeVersions(e *Engine, doc cue.Value, force []state.PolicyVersion) (refs []string, err error) {
	_, end := telemetry.Span(e.traceCtx(), "engine check policy", attribute.Int("pravo.policies", len(force)))
	defer func() { end(err) }()
	refs = make([]string, 0, len(force))
	var failed []string
	for _, v := range force {
		rule := e.Root.Ctx.CompileString(v.Text, cue.Filename(v.Name))
		if err := rule.Err(); err != nil {
			return nil, WithCode(ExitInvalid, fmt.Errorf("policy %s@%d does not compile: %s", v.Name, v.Seq, cueerrors.Details(err, nil)))
		}
		if err := rule.Unify(doc).Validate(cue.Concrete(true)); err != nil {
			failed = append(failed, fmt.Sprintf("the plan violates the policy %s@%d (sha256:%s):\n%s", v.Name, v.Seq, v.Digest()[:12], cueerrors.Details(err, nil)))
		}
		refs = append(refs, v.Ref())
	}
	if len(failed) > 0 {
		return refs, refused("%s", strings.Join(failed, "\n"))
	}
	return refs, nil
}

// JudgePlan judges a plan that has not been saved, for the feedback of "plan": the
// document is the one the plan file would give.
func (e *Engine) JudgePlan(ctx context.Context, plan *Plan) error {
	if plan.Destroy && len(plan.Changes) == 0 {
		return nil
	}
	set, err := e.policySet(ctx)
	if err != nil {
		return err
	}
	if len(set.InForce()) == 0 {
		return nil
	}
	pf, err := e.buildPlanFile(plan, nil, false)
	if err != nil {
		return err
	}
	refs, err := e.Judge(set, pf)
	if err != nil {
		return err
	}
	if len(refs) > 0 {
		e.Opts.Progress("Policies: %d satisfied.", len(refs))
	}
	return nil
}

// JudgeFile is JudgePlan for a plan file: the feedback of "show".
func (e *Engine) JudgeFile(ctx context.Context, pf *PlanFile) error {
	set, err := e.policySet(ctx)
	if err != nil {
		return err
	}
	_, err = e.Judge(set, pf)
	return err
}

// SetPlanFile gives the apply the whole plan file it runs from, so that it judges the
// file and not the part it applies in this stage.
func (e *Engine) SetPlanFile(pf *PlanFile) { e.planFile = pf }

// judgeApply is the judging of an apply, with the locks held: the policies are read
// again, the entries of the plan must still apply to them, and the whole plan file is
// judged. The versions that judged are signed into the start of the transaction.
func (e *Engine) judgeApply(ctx context.Context, plan *Plan, writes bool) (*state.PolicySet, error) {
	set, err := e.policySet(ctx)
	if err != nil {
		return nil, err
	}
	if err := e.checkPolicyRoles(plan); err != nil {
		return nil, err
	}
	var reasons []string
	for _, en := range plan.Policies {
		head, ok := set.Head(en.Name)
		seq := 0
		if ok {
			seq = head.Seq
		}
		retry := ok && seq == en.Base+1 && head.Tombstone() == en.removal() && (en.removal() || head.Digest() == en.Digest)
		if seq != en.Base && !retry {
			reasons = append(reasons, fmt.Sprintf("the policy %s changed since the plan was made (the plan is from version %d, the state has %d)", en.Name, en.Base, seq))
		}
	}
	if len(reasons) > 0 {
		return nil, WithCode(ExitStale, &ErrStalePlan{Reasons: reasons})
	}
	if need := policyChangeNeed(set, plan); need != nil {
		if err := e.checkApprovals(ctx, need); err != nil {
			return nil, err
		}
	}
	if !writes || len(set.InForce()) == 0 {
		return set, nil
	}
	pf := e.planFile
	if pf == nil {
		if pf, err = e.buildPlanFile(plan, nil, false); err != nil {
			return nil, err
		}
	}
	if !pf.changesResources() {
		return set, nil
	}
	digest := e.planDigest
	if digest == "" {
		data, err := indentJSON(pf)
		if err != nil {
			return nil, err
		}
		digest = PlanDigest(data)
		e.txn.SetPlan(e.planID, digest)
	}
	if e.Opts.Judged != nil {
		e.Opts.Judged()
	}
	refs, err := e.Judge(set, pf)
	e.txn.SetPolicies(refs)
	if err != nil {
		return set, err
	}
	doc, err := e.judgedDoc(pf)
	if err != nil {
		return nil, err
	}
	need, err := approvalNeed(e, doc, set.InForce())
	if err != nil {
		return nil, err
	}
	return set, e.checkApprovals(ctx, need)
}

// writePolicies writes the versions of the plan's entries, puts and edits before
// tombstones. A version that the state holds already, put by an earlier attempt, is
// not written again.
func (e *Engine) writePolicies(ctx context.Context, set *state.PolicySet, plan *Plan) error {
	texts := map[string]string{}
	for _, p := range e.Root.Policies {
		texts[state.PolicyName(p.Name)] = p.Rule
	}
	entries := append([]PolicyEntry(nil), plan.Policies...)
	sort.SliceStable(entries, func(i, j int) bool {
		if entries[i].removal() != entries[j].removal() {
			return !entries[i].removal()
		}
		return entries[i].Name < entries[j].Name
	})
	for _, en := range entries {
		head, ok := set.Head(en.Name)
		if ok && head.Seq == en.Base+1 {
			continue
		}
		text := texts[en.Name]
		if en.removal() {
			text = ""
		} else if state.TextDigest(text) != en.Digest {
			return fmt.Errorf("the policy %s is not the text the plan was made with", en.Name)
		}
		// The signer's clock, read once; a version is not dated before the one it follows,
		// so a slow clock blocks no one.
		at := time.Now().UTC()
		if ok && head.At.After(at) {
			at = head.At
		}
		if err := e.State.PutPolicyVersion(ctx, en.Name, en.Base+1, text, at); err != nil {
			if errors.Is(err, state.ErrPolicyChanged) {
				return WithCode(ExitStale, err)
			}
			return err
		}
	}
	return nil
}

func refused(format string, args ...any) error {
	return WithCode(ExitRefused, fmt.Errorf(format, args...))
}
