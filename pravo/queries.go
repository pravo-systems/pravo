package pravo

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"cuelang.org/go/cue"
	"cuelang.org/go/cue/cuecontext"
	"cuelang.org/go/cue/format"
	cuejson "cuelang.org/go/encoding/json"

	"github.com/pravo-systems/pravo/internal/engine"
)

// ParseRefresh reads a refresh mode as "-refresh" writes it: age (the default),
// expired, false (or none) and all (or true).
func ParseRefresh(v string) (RefreshMode, error) {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "", "age":
		return engine.RefreshAge, nil
	case "false", "none", "no", "off":
		return engine.RefreshNone, nil
	case "true", "all", "force", "yes":
		return engine.RefreshAll, nil
	case "expired":
		return engine.RefreshExpired, nil
	}
	return 0, fmt.Errorf("invalid -refresh value %q: want age, expired, false or all", v)
}

func (w *Workspace) needPlanning() error {
	if w.state == nil || w.schemas == nil {
		return errors.New("pravo: this needs the state and the schemas of the providers: open the workspace with Config.State and Config.Schemas set")
	}
	return nil
}

// quiet returns an engine that reports no progress.
func (w *Workspace) quiet(opts EngineOptions) *Engine {
	opts.Progress = func(string, ...any) {}
	return w.Engine(opts)
}

// DriftItem is a resource that changed outside pravo.
type DriftItem struct {
	Addr    string   `json:"addr"`
	Action  string   `json:"action"`
	Reasons []string `json:"reasons"`
	// Gone says that the object no longer exists.
	Gone bool `json:"gone,omitempty"`
}

// DriftReport is what Drift found. The JSON is the report "pravo drift --json" prints.
type DriftReport struct {
	Time    time.Time      `json:"time"`
	Root    string         `json:"root"`
	Actor   string         `json:"actor,omitempty"`
	Drifted []DriftItem    `json:"drifted"`
	Pending int            `json:"pending_changes"`
	Summary engine.Summary `json:"summary"`
	// Refreshed is the number of resources that were read.
	Refreshed int `json:"-"`
}

// Drift reads the real state of every resource, ignoring refresh ages, and
// reports the resources whose real state differs from the state and the ones
// that no longer exist. What it reads is written back to the state, with a mark
// on every record that differs or is gone, so that the next plan acts on it.
func (w *Workspace) Drift(ctx context.Context) (*DriftReport, error) {
	if err := w.needPlanning(); err != nil {
		return nil, err
	}
	eng := w.quiet(EngineOptions{Refresh: engine.RefreshAll})
	plan, err := eng.Plan(ctx)
	if err != nil {
		return nil, err
	}
	view := eng.NewView(plan, engine.ViewOptions{})
	rep := &DriftReport{Time: time.Now().UTC(), Root: w.root.Dir, Actor: w.cfg.Actor, Drifted: []DriftItem{}, Summary: view.Summary, Refreshed: view.Stats.Refreshed}
	for _, c := range view.Changes {
		if c.Drifted {
			reasons := c.Reasons
			if reasons == nil {
				reasons = []string{}
			}
			rep.Drifted = append(rep.Drifted, DriftItem{Addr: c.Addr, Action: string(c.Action), Reasons: reasons, Gone: c.Action == engine.ActionCreate})
		}
		if c.Action != engine.ActionNoop && c.Action != engine.ActionRead && c.Action != engine.ActionOpen && c.Action != engine.ActionCall {
			rep.Pending++
		}
	}
	sort.Slice(rep.Drifted, func(i, j int) bool { return rep.Drifted[i].Addr < rep.Drifted[j].Addr })
	return rep, nil
}

// DueDriftReport is what DriftDue found.
type DueDriftReport struct {
	Time  time.Time `json:"time"`
	Root  string    `json:"root"`
	Actor string    `json:"actor,omitempty"`
	*engine.DueReport
}

// DriftDue visits every record and reads only the ones that are older than their
// refresh age, the oldest first, at most limit of them (zero is no limit). It
// plans nothing, so a scheduled run costs what it reads.
func (w *Workspace) DriftDue(ctx context.Context, limit int) (*DueDriftReport, error) {
	if err := w.needPlanning(); err != nil {
		return nil, err
	}
	rep, err := w.quiet(EngineOptions{}).DriftDue(ctx, limit)
	if err != nil {
		return nil, err
	}
	return &DueDriftReport{Time: time.Now().UTC(), Root: w.root.Dir, Actor: w.cfg.Actor, DueReport: rep}, nil
}

// Explanation says why a plan would do what it does to a resource.
type Explanation struct {
	Addr       string   `json:"addr"`
	Action     string   `json:"action"`
	Reasons    []string `json:"reasons"`
	Dependents []string `json:"dependents,omitempty"`
	// Found is false for an address that is neither declared nor in the state.
	Found bool `json:"found"`
}

// Why plans, with the given refresh mode, and explains what the plan does to each
// of the addresses and what each change reaches.
func (w *Workspace) Why(ctx context.Context, refresh RefreshMode, addrs ...string) ([]Explanation, error) {
	if err := w.needPlanning(); err != nil {
		return nil, err
	}
	eng := w.quiet(EngineOptions{Refresh: refresh})
	plan, err := eng.Plan(ctx)
	if err != nil {
		return nil, err
	}
	view := eng.NewView(plan, engine.ViewOptions{Skipped: true, Full: true})
	byAddr := map[string]engine.ChangeView{}
	for _, c := range view.Changes {
		byAddr[c.Addr] = c
	}
	var all []Explanation
	for _, a := range addrs {
		c, ok := byAddr[a]
		ex := Explanation{Addr: a, Found: ok}
		if ok {
			ex.Action, ex.Reasons, ex.Dependents = string(c.Action), c.Reasons, c.Dependents
			if len(ex.Reasons) == 0 {
				ex.Reasons = []string{"nothing to report"}
			}
		} else {
			ex.Reasons = []string{"not declared in the specification and not in the state"}
		}
		all = append(all, ex)
	}
	return all, nil
}

// Dependents lists everything a change to the resource can reach.
func (w *Workspace) Dependents(addr string) ([]string, error) {
	if w.state == nil {
		return nil, errors.New("pravo: this needs the state: open the workspace with Config.State set")
	}
	return Dependents(w.state, addr)
}

// Dependents lists everything a change to the resource of the state can reach.
func Dependents(st *Store, addr string) ([]string, error) {
	if st.Get(addr) == nil {
		return nil, fmt.Errorf("%s is not in state", addr)
	}
	return st.Reach(addr), nil
}

// QueryResult is the value of a CUE expression over the state.
type QueryResult struct{ v cue.Value }

// JSON is the value as JSON.
func (q *QueryResult) JSON() ([]byte, error) { return q.v.MarshalJSON() }

// CUE is the value as CUE source.
func (q *QueryResult) CUE() ([]byte, error) {
	node := q.v.Syntax(cue.Final(), cue.Concrete(false), cue.Docs(false))
	return format.Node(node)
}

// QueryState evaluates a CUE expression with the state graph in scope:
//
//	resources: {<addr>: {mode, type, name, provider, attributes, dependencies, revision, refreshed_at, ...}}
//	edges:     [{from: <addr>, to: <addr>}]
//	outputs:   {<name>: value}
//
// The workspace must be opened with the values of the state readable.
func (w *Workspace) QueryState(expr string) (*QueryResult, error) {
	if w.state == nil {
		return nil, errors.New("pravo: this needs the state: open the workspace with Config.State set")
	}
	return QueryState(w.state, expr)
}

// QueryState is Workspace.QueryState for a state that is open.
func QueryState(st *Store, expr string) (*QueryResult, error) {
	data, err := json.Marshal(st.Snapshot())
	if err != nil {
		return nil, err
	}
	ctx := cuecontext.New()
	e, err := cuejson.Extract("state", data)
	if err != nil {
		return nil, err
	}
	scope := ctx.BuildExpr(e)
	if err := scope.Err(); err != nil {
		return nil, err
	}
	v := ctx.CompileString(expr, cue.Scope(scope))
	if err := v.Err(); err != nil {
		return nil, err
	}
	return &QueryResult{v: v}, nil
}
