package engine

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/pravo-systems/pravo/internal/protect"
	"github.com/pravo-systems/pravo/internal/state"
)

// ReceiptDelta is the margin of the omission rule: the clocks of the signer of a version
// and of the applier are signed and not checked, so a version dated within this span of
// the start of an apply is not held against the list.
const ReceiptDelta = 10 * time.Minute

// Receipt is what VerifyReceipt found out about an apply.
type Receipt struct {
	Txn, Signer, Version string
	// PlanID is the stored plan the apply ran from, if it was one; Digest is the SHA-256
	// of the plan file it judged.
	PlanID, Digest string
	// Policies are the versions the start lists, and Unknown how many of them were signed
	// by a key that is not in the readers file.
	Policies []string
	Unknown  int
	// Judged is false for an apply that changed no resource, which no policy judges.
	Judged bool
}

func untrusted(format string, args ...any) error {
	return WithCode(ExitUntrusted, fmt.Errorf("%w: %s", state.ErrUntrusted, fmt.Sprintf(format, args...)))
}

// VerifyReceipt checks the receipt of an apply: the signed start of its transaction. The
// plan file must be the one the start names by its digest. The versions the start lists
// must be the text a key signed, the list must omit no version that was in force at the
// start less ReceiptDelta, and the versions must hold for the plan. A failure to check
// is an error of exit 1, a bad signature, digest or list one of exit 7, a rule that does
// not hold one of exit 5.
func (e *Engine) VerifyReceipt(ctx context.Context, t *state.Transaction, pf *PlanFile) (*Receipt, error) {
	readers := e.Opts.Readers
	if e.State.Sealer() == nil {
		return nil, errors.New("the state is not sealed: its transactions carry no signature to check")
	}
	if len(readers) == 0 {
		return nil, errors.New("the readers file is missing or empty: there is nothing to check the signatures against")
	}
	verdict := state.VerifyTransaction(e.State.Lineage(), t, readers)
	switch verdict.Overall() {
	case protect.SigInvalid:
		return nil, untrusted("the signature of transaction %s does not verify", t.ID)
	case protect.SigUnsigned:
		return nil, fmt.Errorf("transaction %s has no signature", t.ID)
	}
	rc := &Receipt{Txn: t.ID, Signer: verdict.Signer, Version: t.Version, PlanID: t.PlanID, Digest: t.PlanDigest}
	if rc.Signer == "" {
		rc.Signer = verdict.Claimed + " (key not in the readers file)"
	}
	if pf.Digest != t.PlanDigest {
		return nil, untrusted("the plan has digest %s and the transaction names %s", pf.Digest, t.PlanDigest)
	}
	// A tripwire and no more: what the transaction changed must be a row of the plan.
	rows := map[string]Action{}
	for _, sc := range pf.Changes {
		rows[sc.Addr] = sc.Action
	}
	for _, c := range t.Changes {
		switch Action(c.Action) {
		case ActionCreate, ActionUpdate, ActionReplace, ActionDelete:
			if rows[c.Addr] != Action(c.Action) {
				return nil, untrusted("transaction %s %sd %s, which the plan does not say", t.ID, c.Action, c.Addr)
			}
		}
	}
	set, err := e.policySet(ctx)
	if err != nil {
		return nil, err
	}
	listed := map[string]int{}
	var versions []state.PolicyVersion
	for _, ref := range t.Policies {
		name, seq, digest, err := parseRef(ref)
		if err != nil {
			return nil, untrusted("transaction %s lists %q", t.ID, ref)
		}
		v, err := e.State.PolicyVersion(ctx, readers, name, seq)
		if err != nil {
			return nil, err
		}
		if v.Digest() != digest {
			return nil, untrusted("%s holds other text than the transaction lists", ref)
		}
		if v.Status == protect.SigUnknownKey {
			rc.Unknown++
		}
		listed[name] = seq
		versions = append(versions, v)
		rc.Policies = append(rc.Policies, ref)
	}
	// A finished apply was judged if it changed a resource; one that was killed, if its
	// plan has such a row.
	changed := pf.changesResources()
	if t.Finished != nil {
		changed = false
		for _, c := range t.Changes {
			switch Action(c.Action) {
			case ActionCreate, ActionUpdate, ActionReplace, ActionDelete:
				changed = true
			}
		}
	}
	if !changed {
		return rc, nil
	}
	rc.Judged = true
	if err := e.omitted(ctx, set, t, listed); err != nil {
		return nil, err
	}
	doc, err := e.judgedDoc(pf, t.Started)
	if err != nil {
		return nil, err
	}
	_, err = judgeVersions(e, doc, versions)
	return rc, err
}

// omitted applies the omission rule: per name, the latest version dated at or before the
// start less the margin must be listed, or a later one, unless that version is a
// tombstone, or a tombstone above it is dated no later than the start plus the margin.
func (e *Engine) omitted(ctx context.Context, set *state.PolicySet, t *state.Transaction, listed map[string]int) error {
	before, after := t.Started.Add(-ReceiptDelta), t.Started.Add(ReceiptDelta)
	for _, name := range set.Names() {
		head, _ := set.Head(name)
		versions := map[int]state.PolicyVersion{head.Seq: head}
		at := func(seq int) (state.PolicyVersion, error) {
			if v, ok := versions[seq]; ok {
				return v, nil
			}
			v, err := e.State.PolicyVersion(ctx, e.Opts.Readers, name, seq)
			versions[seq] = v
			return v, err
		}
		m := 0
		for seq := head.Seq; seq >= 1; seq-- {
			v, err := at(seq)
			if err != nil {
				return err
			}
			if !v.At.After(before) {
				m = seq
				break
			}
		}
		if m == 0 {
			continue
		}
		base, _ := at(m)
		if base.Tombstone() || listed[name] >= m && listed[name] > 0 {
			continue
		}
		retired := false
		for seq := m + 1; seq <= head.Seq; seq++ {
			v, err := at(seq)
			if err != nil {
				return err
			}
			if v.Tombstone() && !v.At.After(after) {
				retired = true
				break
			}
		}
		if !retired {
			return untrusted("the policy %s was in force at %s (version %d) and transaction %s does not list it", name, t.Started.UTC().Format(time.RFC3339), m, t.ID)
		}
	}
	return nil
}

// parseRef splits NAME@SEQ:SHA-256.
func parseRef(ref string) (name string, seq int, digest string, err error) {
	at := strings.IndexByte(ref, '@')
	colon := strings.IndexByte(ref, ':')
	if at <= 0 || colon < at {
		return "", 0, "", errors.New("not a policy version")
	}
	seq, err = strconv.Atoi(ref[at+1 : colon])
	if err != nil || seq < 1 {
		return "", 0, "", errors.New("not a policy version")
	}
	return ref[:at], seq, ref[colon+1:], nil
}
