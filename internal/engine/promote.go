package engine

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"

	"cuelang.org/go/cue"
	"cuelang.org/go/cue/ast"
	"cuelang.org/go/cue/parser"

	"github.com/pravo-systems/pravo/internal/config"
	"github.com/pravo-systems/pravo/internal/protect"
	"github.com/pravo-systems/pravo/internal/state"
)

// Promotion. A policy rule may set the field promote of the document it judges, and a plan that
// comes out with it set is applied only when the state it names (the one before this in the
// chain of environments) holds a signed attestation that it runs the same specification:
//
//	rule: promote: {
//		from:    state: path: "../staging/pravo.state"
//		signers: ["SHA256:…"]
//		vary:    ["env"]
//	}
//
// The state of an environment holds the policies its root declares. A root that declares this
// policy only when it is the production environment (if env == "prod" { … }) gates only that one.
// The attestation is the object promotions/<fingerprint>.<key>.json that "pravo promote attest"
// writes into the state before (see state/promotion.go). The specification is the same when the
// fingerprint of the root is the same and every tag that does not vary has the same value; the
// tags are compared by digest, so the object shows none of them. The signature must be by a key
// whose fingerprint the policy pins: the state that holds the object does not decide whom to trust.

// PromoteSpec is the field promote of a judged document.
type PromoteSpec struct {
	// Signers are the fingerprints of the keys whose attestation counts.
	Signers []string `json:"signers"`
	// Lineage, when set, is the lineage the state before must have.
	Lineage string `json:"lineage"`
	// Vary lists the tags whose value differs between the environments, such as env.
	Vary []string `json:"vary"`
	// MinAge is how long an attestation must have stood before it counts; MaxAge how long it counts.
	MinAge string `json:"min_age"`
	MaxAge string `json:"max_age"`
}

// PromotionNeed is what one policy version asks of a plan.
type PromotionNeed struct {
	PromoteSpec
	// From names the state before, as a person knows it: the directory or the bucket.
	From string
	// By names the policy version that asks.
	By string

	backend state.Backend
	minAge  time.Duration
	maxAge  time.Duration
}

// PromotionCheck is how one need stands against the state before it.
type PromotionCheck struct {
	Need PromotionNeed
	// Met is set when an attestation counts.
	Met bool
	// Reason says, when it is not met, what is missing.
	Reason string
	// Signer, At and Txn describe the attestation that counted, or the nearest that did not.
	Signer string
	At     time.Time
	Txn    string
	// WaitUntil is when an attestation that is too young starts to count.
	WaitUntil time.Time
}

// ParseDuration reads a duration as the settings spell it: "90m", "24h" or "7d".
func ParseDuration(s string) (time.Duration, error) {
	if n := strings.TrimSuffix(s, "d"); n != s {
		d, err := time.ParseDuration(n + "h")
		return d * 24, err
	}
	return time.ParseDuration(s)
}

// promotionNeeds reads the requirements that the versions set on the document, each rule
// unified with it.
func promotionNeeds(e *Engine, doc cue.Value, force []state.PolicyVersion) ([]PromotionNeed, error) {
	var needs []PromotionNeed
	for _, v := range force {
		rule := e.Root.Ctx.CompileString(v.Text, cue.Filename(v.Name))
		if rule.Err() != nil {
			continue // judgeVersions reports it
		}
		a := rule.Unify(doc).LookupPath(cue.ParsePath("promote"))
		if !a.Exists() {
			continue
		}
		by := fmt.Sprintf("%s@%d", v.Name, v.Seq)
		bad := func(format string, args ...any) error {
			return WithCode(ExitInvalid, fmt.Errorf("policy %s sets promote %s", by, fmt.Sprintf(format, args...)))
		}
		var spec PromoteSpec
		if err := a.Decode(&spec); err != nil {
			return nil, bad("that is not concrete: %v", err)
		}
		need := PromotionNeed{PromoteSpec: spec, By: by}
		if len(spec.Signers) == 0 {
			return nil, bad("with no signers: name the fingerprints of the keys whose attestation counts (\"pravo id show\" prints one)")
		}
		for _, f := range spec.Signers {
			if !strings.HasPrefix(f, "SHA256:") {
				return nil, bad("with the signer %q: want a fingerprint such as SHA256:…, as \"pravo id show\" prints it", f)
			}
		}
		var err error
		if spec.MinAge != "" {
			if need.minAge, err = ParseDuration(spec.MinAge); err != nil || need.minAge < 0 {
				return nil, bad("with min_age %q: want a duration such as 24h or 2d", spec.MinAge)
			}
		}
		if spec.MaxAge != "" {
			if need.maxAge, err = ParseDuration(spec.MaxAge); err != nil || need.maxAge <= 0 {
				return nil, bad("with max_age %q: want a duration such as 72h or 7d", spec.MaxAge)
			}
		}
		st, dir, err := config.ParseStateLocation(a.LookupPath(cue.ParsePath("from.state")), "promote.from.state", e.Root.Dir)
		if err != nil {
			return nil, bad("whose from is wrong: %v", err)
		}
		if st.Remote() {
			if need.backend, err = RemoteBackend(context.Background(), st, e.Opts.Env); err != nil {
				return nil, bad("whose from cannot be opened: %v", err)
			}
		} else {
			need.backend = &state.LocalBackend{Dir: dir}
		}
		need.From = need.backend.Name()
		if rel, err := filepath.Rel(e.Root.Dir, need.From); err == nil && !st.Remote() && !strings.HasPrefix(rel, "..") {
			need.From = rel
		}
		needs = append(needs, need)
	}
	return needs, nil
}

// SpecOf is the identity a plan file gives the specification: its fingerprint and the digest of each tag.
func SpecOf(pf *PlanFile) (string, map[string]string) {
	return pf.ConfigFingerprint, state.SpecTagDigests(pf.Tags)
}

// Check reads the state before and says whether an attestation of the specification counts. A
// state that cannot be read is a reason and not an error: the apply is refused and says why.
func (n PromotionNeed) Check(ctx context.Context, fingerprint string, tags map[string]string, now time.Time) PromotionCheck {
	c := PromotionCheck{Need: n}
	fail := func(format string, args ...any) PromotionCheck {
		c.Reason = fmt.Sprintf(format, args...)
		return c
	}
	lineage, err := state.ReadLineage(ctx, n.backend)
	if err != nil {
		return fail("%s cannot be read: %v", n.From, err)
	}
	if n.Lineage != "" && n.Lineage != lineage {
		return fail("%s is a state of another lineage than the policy pins (it is %s)", n.From, lineage)
	}
	all, err := state.ReadPromotions(ctx, n.backend, "")
	if err != nil {
		return fail("%s cannot be read: %v", n.From, err)
	}
	var mine []state.PromotionObject
	for _, o := range all {
		if o.Promotion != nil && o.Fingerprint == fingerprint {
			mine = append(mine, o)
		}
	}
	if len(mine) == 0 {
		msg := fmt.Sprintf("it holds no attestation of this specification (fingerprint %s)", short(fingerprint))
		var newest *state.Promotion
		for _, o := range all {
			if o.Promotion != nil && (newest == nil || o.At.After(newest.At)) {
				newest = o.Promotion
			}
		}
		if newest != nil {
			msg += fmt.Sprintf("; the newest it holds is of %s, signed %s", short(newest.Fingerprint), newest.At.UTC().Format("2006-01-02 15:04"))
		}
		return fail("%s", msg)
	}
	// Each attestation of this specification is judged, and the reason of the nearest is the one
	// reported. Nearer means: signed by a pinned key, then right in its tags, then right in its age.
	rank := -1
	for _, o := range mine {
		p := o.Promotion
		reason, level := n.judge(p, lineage, tags, now)
		if reason == "" {
			c.Met, c.Signer, c.At, c.Txn, c.Reason = true, p.Signature.Signer, p.At, p.Txn, ""
			return c
		}
		if level > rank {
			rank, c.Reason, c.Signer, c.At, c.Txn = level, reason, p.Signature.Signer, p.At, p.Txn
			c.WaitUntil = time.Time{}
			if level == 3 && n.minAge > 0 && now.Sub(p.At) < n.minAge {
				c.WaitUntil = p.At.Add(n.minAge)
			}
		}
	}
	return c
}

// judge says why an attestation does not count, or "" when it does, with how far it got.
func (n PromotionNeed) judge(p *state.Promotion, lineage string, tags map[string]string, now time.Time) (string, int) {
	switch p.Verify(lineage, n.Signers) {
	case protect.SigUnknownKey:
		return fmt.Sprintf("the attestation of %s is signed by %s, which the policy does not pin", p.Signature.Signer, p.Signature.Key), 0
	case protect.SigVerified:
	default:
		return fmt.Sprintf("the attestation of %s does not verify", p.Signature.Signer), 0
	}
	if diff := n.tagDiff(p.Tags, tags); len(diff) > 0 {
		return "the specification is the same, but " + strings.Join(diff, "; "), 2
	}
	age := now.Sub(p.At)
	if n.minAge > 0 && age < n.minAge {
		return fmt.Sprintf("the attestation is %s old and the policy wants it to stand for %s first", roundAge(age), n.MinAge), 3
	}
	if n.maxAge > 0 && age > n.maxAge {
		return fmt.Sprintf("the attestation is %s old, and the policy counts one for %s", roundAge(age), n.MaxAge), 3
	}
	return "", 4
}

// tagDiff lists the tags that must agree and do not.
func (n PromotionNeed) tagDiff(there, here map[string]string) []string {
	names := map[string]bool{}
	for k := range there {
		names[k] = true
	}
	for k := range here {
		names[k] = true
	}
	var out []string
	for _, name := range sortedNames(names) {
		if slices.Contains(n.Vary, name) {
			continue
		}
		t, ok1 := there[name]
		h, ok2 := here[name]
		switch {
		case ok1 && !ok2:
			out = append(out, fmt.Sprintf("the tag %s was set when it was applied before and is not set now", name))
		case !ok1 && ok2:
			out = append(out, fmt.Sprintf("the tag %s is set now and was not when it was applied before", name))
		case t != h:
			hint := ""
			if name == "env" {
				hint = " (the tag that names the environment varies: add it to vary)"
			}
			out = append(out, fmt.Sprintf("the tag %s has another value than when it was applied before%s", name, hint))
		}
	}
	return out
}

func sortedNames(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func short(fp string) string {
	if len(fp) > 12 {
		return fp[:12]
	}
	return fp
}

func roundAge(d time.Duration) string {
	switch {
	case d < time.Minute:
		return "less than a minute"
	case d < time.Hour:
		return fmt.Sprintf("%d minutes", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%d hours", int(d.Hours()))
	}
	return fmt.Sprintf("%d days", int(d.Hours()/24))
}

// checkPromotion refuses an apply whose plan the policies ask a promotion for when the state
// before holds no attestation that counts. An admin is not exempt: the gate says which specification
// may reach this state, and an admin who must have another changes the policy, which takes an
// approval by another admin.
func (e *Engine) checkPromotion(ctx context.Context, needs []PromotionNeed, pf *PlanFile) error {
	if len(needs) == 0 {
		return nil
	}
	fp, tags := SpecOf(pf)
	for _, n := range needs {
		c := n.Check(ctx, fp, tags, time.Now())
		if c.Met {
			continue
		}
		return refused("the policy %s asks %s for an attestation of this specification, and %s. Vouch for it there with \"pravo promote attest\", after the apply that put the specification there", n.By, n.From, c.Reason)
	}
	return nil
}

// PromotionChecks is what the policies in force ask of a plan file by way of promotion, and how each
// stands against the state before. The viewer and "pravo promote check" show it.
func (e *Engine) PromotionChecks(ctx context.Context, set *state.PolicySet, pf *PlanFile) ([]PromotionCheck, error) {
	force := set.InForce()
	if len(force) == 0 || !pf.changesResources() {
		return nil, nil
	}
	doc, err := e.judgedDoc(pf)
	if err != nil {
		return nil, err
	}
	if _, err := judgeVersions(e, doc, force); err != nil {
		var coded *CodedError
		if errors.As(err, &coded) && coded.Code == ExitRefused {
			return nil, nil // the rules refuse the plan; the apply stops there
		}
		return nil, err
	}
	return e.promotionChecksOf(ctx, doc, force, pf)
}

func (e *Engine) promotionChecksOf(ctx context.Context, doc cue.Value, force []state.PolicyVersion, pf *PlanFile) ([]PromotionCheck, error) {
	needs, err := promotionNeeds(e, doc, force)
	if err != nil {
		return nil, err
	}
	fp, tags := SpecOf(pf)
	out := make([]PromotionCheck, 0, len(needs))
	for _, n := range needs {
		out = append(out, n.Check(ctx, fp, tags, time.Now()))
	}
	return out, nil
}

// Line is the check as "plan" reports it.
func (c PromotionCheck) Line() string {
	if c.Met {
		return fmt.Sprintf("Promotion: %s holds an attestation by %s, signed %s ago, so this specification may reach this state.", c.Need.From, c.Signer, roundAge(time.Since(c.At)))
	}
	return fmt.Sprintf("Promotion: not yet. The policy %s asks %s for an attestation, and %s. An apply is refused until then.", c.Need.By, c.Need.From, c.Reason)
}

// asksForPromotion reports whether the text of a rule sets the field promote anywhere.
func asksForPromotion(text string) bool {
	f, err := parser.ParseFile("policy", text)
	if err != nil {
		return false
	}
	found := false
	ast.Walk(f, func(n ast.Node) bool {
		if fl, ok := n.(*ast.Field); ok {
			if name, _, err := ast.LabelName(fl.Label); err == nil && name == "promote" {
				found = true
			}
		}
		return !found
	}, nil)
	return found
}
