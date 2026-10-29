package engine

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"cuelang.org/go/cue"
	"cuelang.org/go/cue/ast"
	"cuelang.org/go/cue/parser"

	"github.com/pravo-systems/pravo/internal/protect"
	"github.com/pravo-systems/pravo/internal/state"
)

// Approvals. A policy rule may set the field approvals of the document it judges, and
// a plan that comes out with it set is applied only when enough keys have approved the
// plan by its id:
//
//	big_destroys: pravo.#Policy & {rule: {if summary.destroy > 2 {approvals: {min: 2, role: "admin"}}}}
//
// An approval is a signature by a key of the readers file over the digest of the stored
// plan, in a domain of its own, and an object beside the plan:
//
//	plans/<id>.ok-<key>.json   (the shape of a signature of the storing, see plansign.go)
//
// It is created and never replaced, so an approval stays what the key signed. It binds to
// the digest, so it is worth nothing for another plan. It carries no time limit: a plan
// has its own lifetime. Whether the signer may approve is decided at apply from the
// readers file, so a key that was demoted or dropped since stops counting.

// ApprovalNeed is what the policies in force ask of a plan: the strongest of the
// requirements their rules set.
type ApprovalNeed struct {
	// Min is the number of distinct keys that must have approved.
	Min int
	// Role is the weakest role that counts as an approver.
	Role protect.Role
	// Self says whether the key that stored the plan may approve it. It may only when no
	// rule that asks for approvals forbids it.
	Self bool
	// By names the policy versions that ask.
	By []string
}

// approvalSpec is the field approvals of a judged document.
type approvalSpec struct {
	Min  int    `json:"min"`
	Role string `json:"role"`
	Self bool   `json:"self"`
}

// approvalNeed reads the requirement that the versions set on the document, each rule
// unified with it. It returns nil when none asks for approvals.
func approvalNeed(e *Engine, doc cue.Value, force []state.PolicyVersion) (*ApprovalNeed, error) {
	var need *ApprovalNeed
	for _, v := range force {
		rule := e.Root.Ctx.CompileString(v.Text, cue.Filename(v.Name))
		if rule.Err() != nil {
			continue // judgeVersions reports it
		}
		a := rule.Unify(doc).LookupPath(cue.ParsePath("approvals"))
		if !a.Exists() {
			continue
		}
		var spec approvalSpec
		if err := a.Decode(&spec); err != nil {
			return nil, WithCode(ExitInvalid, fmt.Errorf("policy %s@%d sets approvals that are not concrete: %w", v.Name, v.Seq, err))
		}
		// The rule is judged standalone, without the schema of the specification, so the
		// default of the schema, a writer, is supplied here.
		if spec.Role == "" {
			spec.Role = "writer"
		}
		role, err := protect.ParseRole("role:" + spec.Role)
		if err != nil || role == protect.RoleReader || spec.Min < 1 {
			return nil, WithCode(ExitInvalid, fmt.Errorf("policy %s@%d sets approvals min %d, role %q: want min of 1 or more and role writer or admin", v.Name, v.Seq, spec.Min, spec.Role))
		}
		if need == nil {
			need = &ApprovalNeed{Self: true, Role: role}
		}
		need.Min = max(need.Min, spec.Min)
		need.Role = max(need.Role, role)
		need.Self = need.Self && spec.Self
		need.By = append(need.By, fmt.Sprintf("%s@%d", v.Name, v.Seq))
	}
	return need, nil
}

// Approval is one approval object of a stored plan, as a verifier reads it.
type Approval struct {
	PlanVerdict
	// Role is the role the readers file gives the signing key, when it is in the file.
	Role protect.Role
}

// Approve signs the approval of the stored plan id with the identity of the store. The
// state must be sealed, since an approval is a signature.
func (ps *PlanStore) Approve(ctx context.Context, id string) (*PlanSignature, error) {
	if ps.st == nil {
		return nil, errors.New("approving a plan: the state is not sealed, and an approval is a signature")
	}
	digest, err := ps.PlanDigest(ctx, id)
	if err != nil {
		return nil, err
	}
	at := ps.now()
	sig, ok, err := ps.st.SignMessage(protect.DomainPlanApproval, planSigParts(digest, at)...)
	if err != nil {
		return nil, fmt.Errorf("approving the plan: %w", err)
	}
	if !ok {
		return nil, errors.New("approving a plan: this identity has no signing key")
	}
	obj := &PlanSignature{V: sigVersion, ID: id, At: at, Signature: &sig}
	enc, err := indentJSON(obj)
	if err != nil {
		return nil, err
	}
	key := okKey(id, signatureID(sig.Key))
	put, err := ps.B.PutIfAbsent(ctx, key, enc)
	if err != nil {
		return nil, fmt.Errorf("approving the plan: %w", state.Advise(err))
	}
	if !put {
		cur, err := ps.B.Get(ctx, key)
		if err != nil {
			return nil, fmt.Errorf("approving the plan: %w", err)
		}
		var old PlanSignature
		if json.Unmarshal(cur, &old) == nil && old.Signature != nil && old.Signature.Key == sig.Key && !bytes.Equal(cur, enc) {
			return &old, nil // approved before; the first approval stays
		}
	}
	return obj, nil
}

// Approvals reads and checks the approvals of a stored plan against the readers. The
// digest is the one of the plan file as a reader opens it.
func (ps *PlanStore) Approvals(ctx context.Context, id, digest string, readers []protect.Reader) ([]Approval, error) {
	vs, err := ps.verdicts(ctx, id, digest, readers, ".ok-", protect.DomainPlanApproval, okKey)
	if err != nil {
		return nil, err
	}
	out := make([]Approval, 0, len(vs))
	for _, v := range vs {
		a := Approval{PlanVerdict: v}
		if v.Status == protect.SigVerified {
			a.Role, _ = protect.RoleOf(readers, v.Key)
		}
		out = append(out, a)
	}
	// The objects are listed by the id of the key that signed them, which says nothing
	// to a reader. Give them in the order the approvals were made, and the objects that
	// are not approvals last.
	slices.SortStableFunc(out, func(a, b Approval) int {
		if a.At.IsZero() != b.At.IsZero() {
			if a.At.IsZero() {
				return 1
			}
			return -1
		}
		if c := a.At.Compare(b.At); c != 0 {
			return c
		}
		return strings.Compare(a.Signer+a.Key, b.Signer+b.Key)
	})
	return out, nil
}

// Counted reports which approvals count toward need, by the key that signed: verified,
// of a role that suffices, and not the key that stored the plan unless need allows it.
// storers are the keys whose signature of the storing verified.
func (n *ApprovalNeed) Counted(approvals []Approval, storers []string) (counted []Approval) {
	seen := map[string]bool{}
	for _, a := range approvals {
		if a.Status != protect.SigVerified || seen[a.Key] || !a.Role.Allows(n.Role) {
			continue
		}
		if !n.Self && slices.Contains(storers, a.Key) {
			continue
		}
		seen[a.Key] = true
		counted = append(counted, a)
	}
	return counted
}

// checkApprovals refuses an apply of a plan that the policies ask approvals for and has too few. A plan that is
// not stored has nowhere to keep approvals.
func (e *Engine) checkApprovals(ctx context.Context, need *ApprovalNeed) error {
	if need == nil {
		return nil
	}
	// An admin needs no approval, and with no roles in the readers file every key is an admin: a
	// state with one member applies as it did before.
	if role, ok := protect.RoleOf(e.Opts.Readers, e.State.SigningKey()); ok && role.Allows(protect.RoleAdmin) {
		return nil
	}
	ask := fmt.Sprintf("%d approval%s by %s (%s)", need.Min, map[bool]string{true: "", false: "s"}[need.Min == 1], roleWords(need.Role), strings.Join(need.By, ", "))
	if e.planID == "" {
		return refused("the plan needs %s, and approvals belong to a stored plan: make it with plan --store, have it approved with \"pravo plan approve\" and apply it by its id", ask)
	}
	if e.State.Sealer() == nil || len(e.Opts.Readers) == 0 {
		return refused("the plan needs %s, and approvals are signatures: the state must be sealed with a readers file", ask)
	}
	ps := NewPlanStoreFor(e.State)
	digest, err := ps.PlanDigest(ctx, e.planID)
	if err != nil {
		return err
	}
	stored, err := ps.SignaturesOf(ctx, e.planID, digest, e.Opts.Readers)
	if err != nil {
		return err
	}
	var storers []string
	for _, v := range stored {
		if v.Status == protect.SigVerified {
			storers = append(storers, v.Key)
		}
	}
	approvals, err := ps.Approvals(ctx, e.planID, digest, e.Opts.Readers)
	if err != nil {
		return err
	}
	counted := need.Counted(approvals, storers)
	if len(counted) >= need.Min {
		return nil
	}
	var have []string
	for _, a := range counted {
		have = append(have, fmt.Sprintf("%s (%s)", a.Signer, a.Role))
	}
	msg := fmt.Sprintf("the plan %s needs %s and has %d", e.planID, ask, len(counted))
	if len(have) > 0 {
		msg += ": " + strings.Join(have, ", ")
	}
	if !need.Self {
		msg += ". The key that stored the plan does not count."
	}
	return refused("%s", msg)
}

func roleWords(r protect.Role) string {
	if r == protect.RoleAdmin {
		return "an admin"
	}
	return "a writer or an admin"
}

// asksForApprovals reports whether the text of a rule sets the field approvals anywhere, in
// any branch of a disjunction.
func asksForApprovals(text string) bool {
	f, err := parser.ParseFile("policy", text)
	if err != nil {
		return false
	}
	found := false
	ast.Walk(f, func(n ast.Node) bool {
		if fl, ok := n.(*ast.Field); ok {
			if name, _, err := ast.LabelName(fl.Label); err == nil && name == "approvals" {
				found = true
			}
		}
		return !found
	}, nil)
	return found
}

// policyChangeNeed is what a plan needs when it edits or removes a policy in force that asks
// for approvals: one admin, not the key that stored the plan. Without it a writer could end
// the requirement with a plan that no rule judges. A policy that is new, or asks for none, is
// free; a policy-only apply is not judged by the rules, so this is its only gate.
func policyChangeNeed(set *state.PolicySet, plan *Plan) *ApprovalNeed {
	var need *ApprovalNeed
	for _, en := range plan.Policies {
		if en.Base == 0 {
			continue
		}
		head, ok := set.Head(en.Name)
		if !ok || head.Tombstone() || head.Seq != en.Base || !(asksForApprovals(head.Text) || asksForPromotion(head.Text)) {
			continue
		}
		if need == nil {
			need = &ApprovalNeed{Min: 1, Role: protect.RoleAdmin}
		}
		need.By = append(need.By, fmt.Sprintf("a change to %s@%d", head.Name, head.Seq))
	}
	return need
}

// ApprovalNeeds is what a plan must have approved before an apply lets it through, as
// the apply counts it, each need on its own: the admin that a change to a policy in
// force that asks for approvals needs, and what the rules of the policies in force set
// on the document of the plan when it changes a resource. A plan that the rules refuse
// gets no need from them, and refused says so: the apply stops at the refusal. An admin
// applies without approvals. The viewer shows the needs beside the approvals a stored
// plan has.
func (e *Engine) ApprovalNeeds(set *state.PolicySet, pf *PlanFile) (needs []ApprovalNeed, refused bool, err error) {
	data, plan, err := e.PolicyDocument(pf)
	if err != nil {
		return nil, false, err
	}
	if need := policyChangeNeed(set, plan); need != nil {
		needs = append(needs, *need)
	}
	force := set.InForce()
	if len(force) == 0 || !pf.changesResources() {
		return needs, false, nil
	}
	doc, err := buildDoc(e.Root.Ctx, data)
	if err != nil {
		return nil, false, err
	}
	if _, err := judgeVersions(e, doc, force); err != nil {
		var coded *CodedError
		if errors.As(err, &coded) && coded.Code == ExitRefused {
			return needs, true, nil
		}
		return nil, false, err
	}
	need, err := approvalNeed(e, doc, force)
	if err != nil {
		return nil, false, err
	}
	if need != nil {
		needs = append(needs, *need)
	}
	return needs, false, nil
}
