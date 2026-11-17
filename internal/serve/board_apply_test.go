package serve

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
)

// Without --apply the board applies nothing, and its plans are not kept to apply.
func TestBoardAppliesNothingWithoutApply(t *testing.T) {
	var planned BoardPlanRequest
	p := boardProject(t, &BoardOptions{Plan: func(_ context.Context, req BoardPlanRequest, _ func(BoardStep)) (*BoardPlanResult, error) {
		planned = req
		return &BoardPlanResult{}, nil
	}})
	var cfg BoardConfig
	if err := json.Unmarshal(p.get("/api/board"), &cfg); err != nil || !cfg.Edit || cfg.Apply {
		t.Fatalf("config with --edit: %+v %v", cfg, err)
	}
	if resp, _ := p.post("/api/board/plan", BoardDraft{}); resp.StatusCode != http.StatusOK || planned.Keep {
		t.Errorf("a plan with --edit is kept to apply: %d %+v", resp.StatusCode, planned)
	}
	if resp, body := p.post("/api/board/apply", map[string]string{"ticket": "bp-1"}); resp.StatusCode != http.StatusForbidden {
		t.Errorf("apply with --edit: %d %s", resp.StatusCode, body)
	}
	q := boardProject(t, nil)
	if resp, _ := q.post("/api/board/apply", map[string]string{"ticket": "bp-1"}); resp.StatusCode != http.StatusForbidden {
		t.Errorf("apply without --edit: %d", resp.StatusCode)
	}
}

// With --apply a plan of the files as they are is kept and named by a ticket, and the ticket is what an apply sends: the
// apply streams its steps and what it did. A plan of a drawing that is not saved is never kept.
func TestBoardAppliesTheTicketOfAPlanOfTheFiles(t *testing.T) {
	var planned []BoardPlanRequest
	var applied []string
	p := boardProject(t, &BoardOptions{
		Actor: "ada",
		Plan: func(_ context.Context, req BoardPlanRequest, _ func(BoardStep)) (*BoardPlanResult, error) {
			planned = append(planned, req)
			res := &BoardPlanResult{Changes: []BoardChange{{Addr: "orders", Action: "create"}}, Add: 1}
			if req.Keep {
				res.Ticket = "bp-1"
			}
			return res, nil
		},
		Apply: func(_ context.Context, ticket string, step func(BoardStep)) (*BoardApplyResult, error) {
			applied = append(applied, ticket)
			if ticket != "bp-1" {
				return nil, errors.New("this plan is not the last plan of the board")
			}
			step(BoardStep{Addr: "orders", Kind: "apply", Phase: "start"})
			return &BoardApplyResult{Added: 1, Txn: "txn-1"}, nil
		},
	})
	var cfg BoardConfig
	if err := json.Unmarshal(p.get("/api/board"), &cfg); err != nil || !cfg.Edit || !cfg.Apply {
		t.Fatalf("config with --apply: %+v %v", cfg, err)
	}
	if resp, body := p.post("/api/board/plan", ordersDraft()); resp.StatusCode != http.StatusOK || strings.Contains(string(body), "ticket") {
		t.Fatalf("plan of a drawing: %d %s", resp.StatusCode, body)
	}
	resp, body := p.post("/api/board/plan", BoardDraft{})
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), `"ticket":"bp-1"`) {
		t.Fatalf("plan of the files: %d %s", resp.StatusCode, body)
	}
	if len(planned) != 2 || planned[0].Keep || planned[0].Extra == nil || !planned[1].Keep || planned[1].Extra != nil {
		t.Errorf("what was planned: %+v", planned)
	}

	for _, bad := range []any{map[string]string{}, map[string]string{"ticket": strings.Repeat("x", 65)}, "bp-1"} {
		if resp, body := p.post("/api/board/apply", bad); resp.StatusCode != http.StatusBadRequest {
			t.Errorf("apply of %v: %d %s", bad, resp.StatusCode, body)
		}
	}
	resp, body = p.post("/api/board/apply", map[string]string{"ticket": "bp-1"})
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), `"step":{"addr":"orders","kind":"apply","phase":"start"}`) ||
		!strings.Contains(string(body), `"done":{"added":1,"changed":0,"destroyed":0,"txn":"txn-1"}`) {
		t.Fatalf("apply: %d %s", resp.StatusCode, body)
	}
	resp, body = p.post("/api/board/apply", map[string]string{"ticket": "bp-2"})
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), `"error":"this plan is not the last plan of the board"`) {
		t.Errorf("apply of another ticket: %d %s", resp.StatusCode, body)
	}
	if strings.Join(applied, ",") != "bp-1,bp-2" {
		t.Errorf("applied: %v", applied)
	}
}
