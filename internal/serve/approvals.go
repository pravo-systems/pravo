package serve

import (
	"context"

	"github.com/pravo-systems/pravo/internal/engine"
	"github.com/pravo-systems/pravo/internal/protect"
)

// approvalsOf reads the approvals of a stored plan against the readers file, and what the
// policies in force ask of the plan before it is applied, counted as "pravo apply" counts
// them. It is nil without a readers file, and for a plan that nobody approved and no policy
// asks approvals for. The caller holds the CUE lock of the specification: the needs decode
// the plan.
func (s *Server) approvalsOf(ctx context.Context, m *model, e *engine.PlanEntry, pf *engine.PlanFile) *PlanApprovals {
	if s.opts.PolicyReaders == nil {
		return nil
	}
	readers, ok := s.opts.PolicyReaders(m.store)
	if !ok {
		return nil
	}
	ps := engine.NewPlanStoreFor(m.store)
	approvals, err := ps.Approvals(ctx, e.Meta.ID, pf.Digest, readers)
	if err != nil {
		return &PlanApprovals{Items: []PlanApproval{}, Note: "not checked: the approvals could not be read"}
	}
	out := &PlanApprovals{Items: []PlanApproval{}}
	for _, a := range approvals {
		it := PlanApproval{Status: a.Status.Key(), At: a.At}
		if a.Status == protect.SigVerified {
			it.Signer, it.Role = a.Signer, a.Role.String()
		} else {
			it.Claimed = a.Claimed
		}
		out.Items = append(out.Items, it)
	}
	// A plan that cannot be applied any more asks for nothing.
	if !e.Status.Open() {
		if len(approvals) == 0 {
			return nil
		}
		return out
	}
	needs, note := s.approvalNeeds(ctx, m, pf, readers)
	if note != "" {
		out.Note = note
		return out
	}
	if len(needs) == 0 {
		if len(approvals) == 0 {
			return nil
		}
		return out
	}
	// The key that stored the plan counts only when every need lets it.
	var storers []string
	sigs, err := ps.SignaturesOf(ctx, e.Meta.ID, pf.Digest, readers)
	if err != nil {
		out.Note = "not checked: the signatures could not be read"
		return out
	}
	for _, v := range sigs {
		if v.Status == protect.SigVerified {
			storers = append(storers, v.Key)
		}
	}
	for _, n := range needs {
		counted := n.Counted(approvals, storers)
		out.Needs = append(out.Needs, PlanApprovalNeed{Min: n.Min, Role: n.Role.String(), Self: n.Self, By: n.By, Have: len(counted), Met: len(counted) >= n.Min})
	}
	return out
}

// approvalNeeds is what the policies in force ask of a plan, or why that could not be
// worked out.
func (s *Server) approvalNeeds(ctx context.Context, m *model, pf *engine.PlanFile, readers []protect.Reader) ([]engine.ApprovalNeed, string) {
	if m.spec.root == nil {
		return nil, "not checked: the specification does not load"
	}
	set, err := m.store.PolicySet(ctx, readers)
	if err != nil {
		return nil, "not checked: the policies could not be read"
	}
	eng, err := s.planEngine(m, pf)
	if err != nil {
		return nil, "not checked: the plan cannot be decoded"
	}
	needs, _, err := eng.ApprovalNeeds(set, pf)
	if err != nil {
		return nil, "not checked: " + m.scrubText(headline(err.Error()))
	}
	return needs, ""
}
