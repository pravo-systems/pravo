package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/pravo-systems/pravo/internal/engine"
	"github.com/pravo-systems/pravo/internal/ui"
	"github.com/pravo-systems/pravo/pravo"
)

// maxFollowUps is how many plans an apply makes after itself for the changes it deferred,
// before it leaves the rest to the next plan.
const maxFollowUps = 5

// followUp plans again after an apply that deferred changes to the next plan: what they
// waited for is known now. It returns the engine and the plan, or a nil plan when the new
// plan makes no progress: it defers as many changes as the apply did and writes nothing.
// The plan is printed, or sent as a JSON line.
func followUp(ctx context.Context, rt *runContext, s *session, opts engine.Options, deferred int) (*engine.Engine, *engine.Plan, error) {
	opts.Replace = nil // a replacement asked for was made
	eng := s.engine(rt, opts)
	plan, err := eng.Plan(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("planning the deferred changes again: %w", err)
	}
	if !plan.HasWrites() && plan.DeferredCount() >= deferred {
		if !rt.flags.JSON {
			rt.out.Println()
			rt.out.Printf("The next plan still defers %s and has nothing else to apply. They wait for something that no apply here makes known: run \"pravo plan\" to see why.\n", plural(plan.DeferredCount(), "change", "changes"))
		}
		return eng, nil, nil
	}
	if rt.flags.JSON {
		emitLine(map[string]any{"kind": "plan", "phase": "done", "follow_up": true, "plan": eng.NewView(plan, engine.ViewOptions{Redact: rt.flags.Withhold})})
	} else {
		rt.out.Println()
		rt.out.Println(rt.out.C(ui.Bold, fmt.Sprintf("Planned again for the %s that the apply deferred:", plural(deferred, "change", "changes"))))
		rt.out.RenderPlan(plan)
	}
	return eng, plan, nil
}

// storeFollowUp stores the follow-up plan of an apply of the stored plan id that deferred
// changes, and says how to apply it. The new plan is judged by the policies in force first,
// as one that "pravo plan --store" makes is.
func storeFollowUp(ctx context.Context, rt *runContext, s *session, id string, deferred int) error {
	eng, plan, err := followUp(ctx, rt, s, engine.Options{Refresh: engine.RefreshAge, Reason: "follows " + id + ": the changes it deferred"}, deferred)
	if err != nil || plan == nil || !planWorth(plan) {
		return err
	}
	if err := eng.JudgePlan(ctx, plan); err != nil {
		return err
	}
	stored, err := s.ws.StorePlan(ctx, eng, plan, pravo.StorePlanOptions{})
	if err != nil {
		return err
	}
	if rt.flags.JSON {
		emitLine(map[string]any{"kind": "follow_up", "plan": stored.Meta.ID, "after": id})
		return nil
	}
	printStored(rt, stored)
	return nil
}

// saveFollowUp writes the follow-up plan of an apply of the plan file path that deferred
// changes next to it, and says how to apply it.
func saveFollowUp(ctx context.Context, rt *runContext, s *session, path string, deferred int) error {
	eng, plan, err := followUp(ctx, rt, s, engine.Options{Refresh: engine.RefreshAge}, deferred)
	if err != nil || plan == nil || !planWorth(plan) {
		return err
	}
	if err := eng.JudgePlan(ctx, plan); err != nil {
		return err
	}
	next := nextPlanPath(path)
	if err := eng.SavePlan(next, plan, rt.flags.Tags, rt.flags.SysTags); err != nil {
		return err
	}
	if rt.flags.JSON {
		emitLine(map[string]any{"kind": "follow_up", "planfile": next, "after": path})
		return nil
	}
	rt.out.Println()
	rt.out.Printf("Saved the plan to %s. To apply exactly these actions, run:\n  pravo apply %s\n", next, next)
	return nil
}

// nextPlanPath is the file of the follow-up plan of the plan file path: change.plan gives
// change.next.plan, and change.next.plan gives change.next.next.plan.
func nextPlanPath(path string) string {
	if base, ok := strings.CutSuffix(path, ".plan"); ok {
		return base + ".next.plan"
	}
	return path + ".next"
}
