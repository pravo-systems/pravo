package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/pravo-systems/pravo/internal/config"
	"github.com/pravo-systems/pravo/internal/engine"
	"github.com/pravo-systems/pravo/internal/serve"
	"github.com/pravo-systems/pravo/pravo"
)

// boardOptions turn on the writes of the drawing board of "pravo serve --edit": the viewer saves a drawing as a new
// file, and asks for plans, which run here as "pravo plan" runs them, one at a time. With apply (--apply) a plan of the
// files as they are is kept in a plan file, and the board can apply it as "pravo apply <planfile>" would. close removes
// the plan files that were kept.
func boardOptions(rt *runContext, apply bool) (opts *serve.BoardOptions, closeFn func()) {
	actor := rt.flags.Actor
	if actor == "" {
		if u, err := user.Current(); err == nil {
			actor = u.Username
		}
	}
	var mu sync.Mutex
	kept := &boardKept{}
	opts = &serve.BoardOptions{
		Actor: actor,
		Plan: func(ctx context.Context, req serve.BoardPlanRequest, step func(serve.BoardStep)) (*serve.BoardPlanResult, error) {
			mu.Lock()
			defer mu.Unlock()
			return boardPlan(ctx, rt, req, step, kept)
		},
	}
	if apply {
		opts.Apply = func(ctx context.Context, ticket string, step func(serve.BoardStep)) (*serve.BoardApplyResult, error) {
			mu.Lock()
			defer mu.Unlock()
			k, err := kept.take(ticket)
			if err != nil {
				return nil, err
			}
			defer os.Remove(k.path)
			return boardApply(ctx, rt, k, step)
		}
	}
	return opts, kept.close
}

// boardKept is the one plan of the board that can be applied: the last that was made of the files as they are, in a
// plan file in a directory of its own that only this user can read. A ticket is used once, and is good for a while.
type boardKept struct {
	dir string
	cur *boardTicket
}

type boardTicket struct {
	id, path string
	at       time.Time
}

const boardTicketLife = 15 * time.Minute

// keep saves the plan, in place of the one kept before, and names it.
func (k *boardKept) keep(eng *engine.Engine, plan *engine.Plan, tags []string, sysTags bool) (string, error) {
	if k.dir == "" {
		dir, err := os.MkdirTemp("", "pravo-board-")
		if err != nil {
			return "", err
		}
		k.dir = dir
	}
	k.drop()
	var b [12]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	id := "bp-" + hex.EncodeToString(b[:])
	path := filepath.Join(k.dir, id+".plan")
	if err := eng.SavePlan(path, plan, tags, sysTags); err != nil {
		return "", err
	}
	k.cur = &boardTicket{id: id, path: path, at: time.Now()}
	return id, nil
}

// take hands out the kept plan that the ticket names, once.
func (k *boardKept) take(ticket string) (*boardTicket, error) {
	t := k.cur
	if t == nil || t.id != ticket {
		return nil, errors.New("this plan is not the last plan of the board: plan again, and apply what that plan shows")
	}
	k.cur = nil
	if time.Since(t.at) > boardTicketLife {
		_ = os.Remove(t.path)
		return nil, errors.New("the plan is older than 15 minutes: plan again, and apply what that plan shows")
	}
	return t, nil
}

func (k *boardKept) drop() {
	if k.cur != nil {
		_ = os.Remove(k.cur.path)
		k.cur = nil
	}
}

func (k *boardKept) close() {
	if k.dir != "" {
		_ = os.RemoveAll(k.dir)
	}
}

// boardPlan makes the plan that "pravo plan" would make of the specification with the drawing laid over it, and keeps
// of it only the action of each node, the counts and what the policies say.
func boardPlan(ctx context.Context, rt *runContext, req serve.BoardPlanRequest, step func(serve.BoardStep), kept *boardKept) (*serve.BoardPlanResult, error) {
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
	from := map[string]string{}
	for _, c := range plan.Changes {
		if c.Action == engine.ActionMove && !c.Applied {
			out.Move++
			from[c.Addr] = c.MovedFrom
		}
	}
	for _, c := range view.Changes {
		out.Changes = append(out.Changes, serve.BoardChange{Addr: c.Addr, Action: string(c.Action), From: from[c.Addr]})
	}
	sort.Slice(out.Changes, func(i, j int) bool { return out.Changes[i].Addr < out.Changes[j].Addr })
	if perr := eng.JudgePlan(ctx, plan); perr != nil {
		out.Refused = perr.Error()
	}
	if req.Keep {
		out.Ticket, out.Held = boardKeep(ctx, kept, eng, plan, out, req)
	}
	return out, nil
}

// boardKeep keeps a plan to be applied from the board, or says why it cannot be: what "pravo apply" would refuse, or ask
// a person at a terminal for, is not applied from a page.
func boardKeep(ctx context.Context, kept *boardKept, eng *engine.Engine, plan *engine.Plan, out *serve.BoardPlanResult, req serve.BoardPlanRequest) (ticket, held string) {
	kept.drop()
	switch {
	case out.Refused != "":
		return "", "A policy refuses the plan."
	case !plan.HasWrites():
		return "", ""
	case len(plan.GuardedDestroys()) > 0:
		return "", "It destroys " + strings.Join(plan.GuardedDestroys(), ", ") + ", which carried $lifecycle.prevent_destroy: apply it from a terminal, with --allow-destroy."
	}
	if err := eng.CheckAdmission(ctx, plan); err != nil {
		return "", err.Error()
	}
	id, err := kept.keep(eng, plan, req.Tags, req.SysTags)
	if err != nil {
		return "", "The plan could not be kept to apply: " + err.Error()
	}
	return id, ""
}

// boardApply applies a plan of the board as "pravo apply <planfile>" applies a plan file: no question, since the person
// saw the plan and pressed Apply, and every change is checked against the planned one.
func boardApply(ctx context.Context, rt *runContext, t *boardTicket, step func(serve.BoardStep)) (*serve.BoardApplyResult, error) {
	prt := *rt
	prt.flags.JSON, prt.flags.Markdown = false, false
	if prt.flags.Reason == "" {
		prt.flags.Reason = "applied from the drawing board of pravo serve"
	}
	pf, err := readPlanFile(ctx, &prt, t.path)
	if err != nil {
		return nil, err
	}
	// the plan carries the tags that it was made with, as a plan file does for "pravo apply <planfile>"
	prt.flags.Tags, prt.flags.SysTags = pf.Tags, pf.SysTags
	s, err := newSession(ctx, &prt, sessionOptions{install: true, schemas: true, loadState: true})
	if err != nil {
		return nil, err
	}
	defer s.close()
	pf.SealWith(s.state.Sealer())
	eng := s.engine(&prt, engine.Options{
		Refresh:  engine.RefreshNone,
		Progress: func(string, ...any) {},
		Event: func(ev engine.Event) {
			if ev.Addr != "" || ev.Kind == "apply" {
				step(serve.BoardStep{Addr: ev.Addr, Kind: ev.Kind, Phase: ev.Phase})
			}
		},
	})
	eng.SetPlan("", pf.Digest)
	moves := 0
	for _, c := range pf.Changes {
		if c.Action == engine.ActionMove && !c.Applied {
			moves++
		}
	}
	sa, err := pravo.ApplySavedPlan(ctx, eng, pf, pravo.SavedApplyOptions{Name: "the plan of the board", Again: "pravo plan", Sink: fileSink{path: t.path, pf: pf}})
	var stale *engine.ErrStalePlan
	if errors.As(err, &stale) {
		return nil, fmt.Errorf("the state changed since this plan was made: plan again (%s)", strings.Join(stale.Reasons, "; "))
	}
	if err != nil {
		if sa != nil && sa.Partial != nil && sa.Partial.Count > 0 {
			return nil, fmt.Errorf("%w (%d changes were applied before it and are recorded in the state: plan again to see what is left)", err, sa.Partial.Count)
		}
		return nil, err
	}
	out := &serve.BoardApplyResult{}
	if sa.Result != nil {
		out.Added, out.Changed, out.Destroyed, out.Txn, out.Deferred = sa.Result.Added, sa.Result.Changed, sa.Result.Destroyed, sa.Result.Txn, sa.Result.Deferred
		// an apply that returns no error made every change of the plan, the moves first
		out.Moved = moves
	}
	return out, nil
}
