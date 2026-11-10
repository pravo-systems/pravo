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
		if json.Unmarshal(cur, &old) == nil && old.Signature != nil && old.Signature.Key == sig.Key && ps.isMine(old, digest) {
			if bytes.Equal(cur, enc) {
				return obj, nil
			}
			return &old, nil // approved before; the first approval stays
		}
		// What is stored under this key is not an approval that it made: a file put there
		// would keep this key from approving the plan, with a success to say it had.
		if err := ps.B.Put(ctx, key, enc); err != nil {
			return nil, fmt.Errorf("approving the plan: %w", state.Advise(err))
		}
	}
	return obj, nil
}

// isMine reports whether old is the approval that this identity made of the plan with
// this digest at the time it names. A signature of ed25519 is the same every time it is
// made, so the identity signs it again and the two are compared.
func (ps *PlanStore) isMine(old PlanSignature, digest string) bool {
	again, ok, err := ps.st.SignMessage(protect.DomainPlanApproval, planSigParts(digest, old.At)...)
	return err == nil && ok && again.Key == old.Signature.Key && bytes.Equal(again.Sig, old.Signature.Sig)
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

// Why says why an approval does not count toward need, or "" when it does. storers are as
// for Counted. An approval that is not verified is reported by its own status.
func (n *ApprovalNeed) Why(a Approval, storers []string) string {
	switch {
	case a.Status != protect.SigVerified:
		return "its key is not in the readers file or its signature does not verify"
	case !a.Role.Allows(n.Role):
		return fmt.Sprintf("the policy needs %s and %s is %s", roleWords(n.Role), a.Signer, withArticle(a.Role))
	case !n.Self && slices.Contains(storers, a.Key):
		return a.Signer + " stored this plan, and the policy does not let the key that stored it approve"
	}
	return ""
}

func withArticle(r protect.Role) string {
	if r == protect.RoleAdmin {
		return "an admin"
	}
	return "a " + r.String()
}

// WordsOf is the text of a policy version that asks, "policy admin_approves, version 1".
func WordsOf(by []string) string { return byWords(by) }

// Ask is what the need asks for, in words: "1 approval by an admin (policy x, version 1)".
func (n *ApprovalNeed) Ask() string {
	return fmt.Sprintf("%d approval%s by %s (%s)", n.Min, map[bool]string{true: "", false: "s"}[n.Min == 1], roleWords(n.Role), byWords(n.By))
}

// judgingReaders are the readers whose roles judge the key that applies and the keys that
// approve. When the state holds a roles chain that the readers file does not pin, the roles
// are the lower of the file and the chain: a local file that dropped the pin line, or an old
// branch from before it, must not make a writer an admin. fromState says that the chain
// lowered the roles of the file.
func (e *Engine) judgingReaders(ctx context.Context) (readers []protect.Reader, fromState bool) {
	rs := e.Opts.Readers
	if e.Opts.RolesPin != "" || len(rs) == 0 || e.State.Sealer() == nil {
		return rs, false
	}
	_, st, err := e.State.RolesFor(ctx, rs, "")
	if err != nil || !st.Unpinned {
		return rs, false
	}
	return protect.CapRoles(rs, st.Stored), true
}

// checkApprovals refuses an apply of a plan that the policies ask approvals for and has too few. A plan that is
// not stored has nowhere to keep approvals.
func (e *Engine) checkApprovals(ctx context.Context, need *ApprovalNeed) error {
	if need == nil {
		return nil
	}
	// An admin needs no approval, and with no roles in the readers file every key is an admin: a
	// state with one member applies as it did before.
	readers, fromState := e.judgingReaders(ctx)
	role, known := protect.RoleOf(readers, e.State.SigningKey())
	if known && role.Allows(protect.RoleAdmin) {
		return nil
	}
	ask := fmt.Sprintf("%d approval%s by %s (%s)", need.Min, map[bool]string{true: "", false: "s"}[need.Min == 1], roleWords(need.Role), byWords(need.By))
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
	stored, err := ps.SignaturesOf(ctx, e.planID, digest, readers)
	if err != nil {
		return err
	}
	var storers []string
	for _, v := range stored {
		if v.Status == protect.SigVerified {
			storers = append(storers, v.Key)
		}
	}
	approvals, err := ps.Approvals(ctx, e.planID, digest, readers)
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
	msg += fmt.Sprintf(" Ask an admin to review it (pravo plan show %s) and run pravo plan approve %s; then apply again.", e.planID, e.planID)
	if fromState {
		msg += fmt.Sprintf(" Your key is %s in the roles stored in the state; %s does not pin those roles, so the roles in the state decide: commit the %s line that pravo protect roles printed.", map[bool]string{true: "a " + role.String(), false: "not listed"}[known], e.Opts.ReadersFile, protect.RolesGenesisKey)
	}
	return refused("%s", msg)
}

// byWords writes the policy versions that ask as words: "policy admin_approves, version 1".
func byWords(by []string) string {
	words := make([]string, len(by))
	for i, b := range by {
		prefix := ""
		if rest, ok := strings.CutPrefix(b, "a change to "); ok {
			prefix, b = "a change to ", rest
		}
		if name, seq, ok := strings.Cut(b, "@"); ok {
			b = "policy " + name + ", version " + seq
		}
		words[i] = prefix + b
	}
	return strings.Join(words, "; ")
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
		if !ok || head.Tombstone() || head.Seq != en.Base || !asksForApprovals(head.Text) {
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
	if _, err := judgeVersions(e, doc, force, nil); err != nil {
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
