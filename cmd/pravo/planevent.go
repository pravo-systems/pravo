package main

import (
	"context"
	"time"

	"github.com/pravo-systems/pravo/internal/engine"
	"github.com/pravo-systems/pravo/pravo"
)

// Kinds of planEvent. A plan moves through them in this order; refused and
// drifted can come at any point before it is applied.
const (
	eventDrifted  = "plan.drifted"  // drift was found and a plan that puts it right was made, but not stored
	eventStored   = "plan.stored"   // the plan is in the state, to be approved and applied by its id
	eventApproved = "plan.approved" // someone signed an approval of the plan
	eventApplied  = "plan.applied"  // the plan was applied, all of it or the part --only chose
	eventRefused  = "plan.refused"  // a policy refused the plan, or the apply of it stopped
)

// planEvent is the one document that the commands with --webhook POST about a
// plan. Whatever the kind, the fields are the same; the ones that do not apply
// are left out.
type planEvent struct {
	Event string    `json:"event"`
	Time  time.Time `json:"time"`
	Root  string    `json:"root"`
	Actor string    `json:"actor,omitempty"`
	// Plan is the stored plan; it is left out of plan.drifted, which has none.
	Plan *planEventPlan `json:"plan,omitempty"`
	// Drifted are the resources that changed outside pravo (plan.stored, plan.drifted
	// and plan.refused of a drift run).
	Drifted []pravo.DriftItem `json:"drifted,omitempty"`
	// By is who approved the plan (plan.approved).
	By string `json:"by,omitempty"`
	// Reason says why a plan was refused.
	Reason string `json:"reason,omitempty"`
}

type planEventPlan struct {
	ID           string            `json:"id"`
	Expires      *time.Time        `json:"expires,omitempty"`
	ApplyCommand string            `json:"apply_command,omitempty"`
	Summary      engine.PlanCounts `json:"summary"`
}

func storedEventPlan(m *engine.PlanMeta) *planEventPlan {
	return &planEventPlan{ID: m.ID, Expires: &m.Expires, ApplyCommand: m.ApplyCommand(), Summary: m.Summary}
}

// sendPlanEvent POSTs the event to the webhook, if there is one. The event is
// stamped here.
func sendPlanEvent(ctx context.Context, webhook, root string, ev planEvent) error {
	if webhook == "" {
		return nil
	}
	ev.Time, ev.Root = time.Now().UTC(), root
	if ev.Actor == "" {
		ev.Actor = flags.Actor
	}
	return postJSON(ctx, webhook, ev)
}

// driftProposal is what "pravo drift --plan --json" prints: the drift report, the
// plan that puts it right, and where the plan is stored.
type driftProposal struct {
	*pravo.DriftReport
	// Plan is the plan that reverts the drift; it changes nothing until it is applied.
	Plan   *engine.View `json:"plan,omitempty"`
	Stored *storedInfo  `json:"stored,omitempty"`
	// Refused is the verdict of a policy that refused the plan, so it was not stored.
	Refused string `json:"refused,omitempty"`

	stored *storedPlan
	event  string
}

// proposeFix makes the proposal for a drift run: it judges the plan with the
// policies, stores it if asked, and sends the one event that says what became of it.
func proposeFix(ctx context.Context, s *session, rep *pravo.DriftReport, planned *pravo.Planned, store bool, ttl time.Duration, webhook string) (*driftProposal, error) {
	p := &driftProposal{DriftReport: rep, event: eventDrifted}
	plan := planned.Plan()
	if len(rep.Drifted) == 0 || !planWorth(plan) {
		return p, nil // nothing drifted, so there is nothing to propose and nothing to tell
	}
	p.Plan = planned.View()
	ev := planEvent{Actor: rep.Actor, Drifted: rep.Drifted}
	if err := planned.CheckPolicy(ctx); err != nil {
		p.Refused, p.event = err.Error(), eventRefused
		ev.Event, ev.Reason = eventRefused, err.Error()
		return p, sendPlanEvent(ctx, webhook, rep.Root, ev)
	}
	if store {
		sp, err := planned.Store(ctx, pravo.StorePlanOptions{TTL: ttl})
		if err != nil {
			return nil, err
		}
		p.stored, p.event = sp, eventStored
		p.Stored = storedInfoOf(sp)
		ev.Plan = storedEventPlan(sp.Meta)
	}
	ev.Event = p.event
	return p, sendPlanEvent(ctx, webhook, rep.Root, ev)
}

// print says what became of the plan, after the drift itself is listed.
func (p *driftProposal) print() {
	switch {
	case p.Plan == nil:
	case p.Refused != "":
		out.Println()
		out.Printf("A policy refused the plan that puts this right, so it was not stored: %s\n", p.Refused)
	case p.stored != nil:
		printStored(p.stored)
	default:
		out.Println()
		sum := p.Plan.Summary
		out.Printf("Putting it right adds %d, changes %d and destroys %d. Run \"pravo drift --plan --store\" to keep that plan for review.\n", sum.Add, sum.Change, sum.Destroy)
	}
}
