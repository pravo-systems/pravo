package main

import (
	"context"
	"os/user"
	"sort"
	"sync"

	"github.com/pravo-systems/pravo/internal/config"
	"github.com/pravo-systems/pravo/internal/engine"
	"github.com/pravo-systems/pravo/internal/serve"
)

// boardOptions turn on the writes of the drawing board of "pravo serve --edit": the viewer saves a drawing as a new
// file, and asks for plans, which run here as "pravo plan" runs them, one at a time.
func boardOptions(rt *runContext) *serve.BoardOptions {
	actor := rt.flags.Actor
	if actor == "" {
		if u, err := user.Current(); err == nil {
			actor = u.Username
		}
	}
	var mu sync.Mutex
	return &serve.BoardOptions{
		Actor: actor,
		Plan: func(ctx context.Context, req serve.BoardPlanRequest, step func(serve.BoardStep)) (*serve.BoardPlanResult, error) {
			mu.Lock()
			defer mu.Unlock()
			return boardPlan(ctx, rt, req, step)
		},
	}
}

// boardPlan makes the plan that "pravo plan" would make of the specification with the drawing laid over it, and keeps
// of it only the action of each node, the counts and what the policies say.
func boardPlan(ctx context.Context, rt *runContext, req serve.BoardPlanRequest, step func(serve.BoardStep)) (*serve.BoardPlanResult, error) {
	// The plan reads with the tags that the page reads with, and prints nothing of its own: its steps go to the page.
	prt := *rt
	prt.flags.Tags, prt.flags.SysTags, prt.flags.JSON, prt.flags.Markdown = req.Tags, req.SysTags, false, false
	step(serve.BoardStep{Kind: "load", Phase: "start"})
	root, err := config.Load(".", config.LoadOptions{Tags: req.Tags, SysTags: req.SysTags, Context: ctx, Env: environment(), Extra: req.Extra})
	if err != nil {
		return nil, engine.WithCode(engine.ExitInvalid, err)
	}
	step(serve.BoardStep{Kind: "load", Phase: "done"})
	s, err := newSession(ctx, &prt, sessionOptions{install: true, schemas: true, loadState: true, planning: true, root: root})
	if err != nil {
		return nil, err
	}
	defer s.close()
	eng := s.engine(&prt, engine.Options{
		Progress: func(string, ...any) {},
		Event: func(ev engine.Event) {
			if ev.Addr != "" || ev.Kind == "plan" {
				step(serve.BoardStep{Addr: ev.Addr, Kind: ev.Kind, Phase: ev.Phase})
			}
		},
	})
	plan, err := eng.Plan(ctx)
	if err != nil {
		return nil, err
	}
	view := eng.NewView(plan, engine.ViewOptions{Redact: true})
	out := &serve.BoardPlanResult{Changes: []serve.BoardChange{}, Add: view.Summary.Add, Change: view.Summary.Change, Destroy: view.Summary.Destroy}
	for _, c := range view.Changes {
		out.Changes = append(out.Changes, serve.BoardChange{Addr: c.Addr, Action: string(c.Action)})
	}
	sort.Slice(out.Changes, func(i, j int) bool { return out.Changes[i].Addr < out.Changes[j].Addr })
	if perr := eng.JudgePlan(ctx, plan); perr != nil {
		out.Refused = perr.Error()
	}
	return out, nil
}
