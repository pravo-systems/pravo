package serve

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pravo-systems/pravo/internal/state"
)

// With --edit, a board whose state holds nothing starts from a Terraform state: the page sends the place of one or
// its text, never both, and the result of the command comes back as lines of JSON. A state that holds anything is
// not started into, and without --edit nothing is.
func TestBoardStartsFromATerraformState(t *testing.T) {
	var got []BoardStartRequest
	opts := &BoardOptions{
		Plan: func(context.Context, BoardPlanRequest, func(BoardStep)) (*BoardPlanResult, error) {
			return &BoardPlanResult{}, nil
		},
		Start: func(_ context.Context, req BoardStartRequest) (*BoardStartResult, error) {
			got = append(got, req)
			if req.Place == "missing.tfstate" {
				return nil, errors.New("open missing.tfstate: no such file or directory")
			}
			return &BoardStartResult{File: "shadow.cue", From: req.Place, Resources: 3, Verified: true, Guarded: true}, nil
		},
	}
	dir := t.TempDir()
	writeSpec(t, dir, specCUE)
	st, err := state.Open(filepath.Join(dir, "pravo.state"))
	if err != nil {
		t.Fatal(err)
	}
	p := start(t, dir, st, Options{Board: opts})
	var cfg BoardConfig
	if err := json.Unmarshal(p.get("/api/board"), &cfg); err != nil || !cfg.Start {
		t.Fatalf("config of an empty state with --edit: %+v %v", cfg, err)
	}
	for _, bad := range []any{map[string]string{}, map[string]string{"place": "a.tfstate", "state": "{}"}, map[string]string{"place": "-"}, map[string]string{"place": "a\nb"}, "terraform.tfstate"} {
		if resp, body := p.post("/api/board/start", bad); resp.StatusCode != http.StatusBadRequest {
			t.Errorf("start of %v: %d %s", bad, resp.StatusCode, body)
		}
	}
	resp, body := p.post("/api/board/start", BoardStartRequest{Place: " terraform.tfstate "})
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), `"step":{"kind":"start","phase":"begin"}`) ||
		!strings.Contains(string(body), `"done":{"file":"shadow.cue","from":"terraform.tfstate","resources":3`) {
		t.Fatalf("start from a place: %d %s", resp.StatusCode, body)
	}
	if resp, body := p.post("/api/board/start", BoardStartRequest{State: `{"version": 4}`}); resp.StatusCode != http.StatusOK || !strings.Contains(string(body), `"done"`) {
		t.Errorf("start from the text of a state: %d %s", resp.StatusCode, body)
	}
	if resp, body := p.post("/api/board/start", BoardStartRequest{Place: "missing.tfstate"}); resp.StatusCode != http.StatusOK || !strings.Contains(string(body), `"error":"open missing.tfstate: no such file or directory"`) {
		t.Errorf("start from a place that is not there: %d %s", resp.StatusCode, body)
	}
	if len(got) != 3 || got[0].Place != "terraform.tfstate" || got[1].State != `{"version": 4}` || got[1].Place != "" {
		t.Errorf("what was started: %+v", got)
	}

	q := boardProject(t, opts)
	var held BoardConfig
	if err := json.Unmarshal(q.get("/api/board"), &held); err != nil || held.Start {
		t.Errorf("config of a state that holds records: %+v %v", held, err)
	}
	if resp, body := q.post("/api/board/start", BoardStartRequest{Place: "terraform.tfstate"}); resp.StatusCode != http.StatusConflict || !strings.Contains(string(body), "only into an empty one") {
		t.Errorf("start into a state that holds records: %d %s", resp.StatusCode, body)
	}
	if resp, _ := boardProject(t, nil).post("/api/board/start", BoardStartRequest{Place: "terraform.tfstate"}); resp.StatusCode != http.StatusForbidden {
		t.Errorf("start without --edit: %d", resp.StatusCode)
	}
	if len(got) != 3 {
		t.Errorf("a refused start reached the command: %+v", got)
	}
}
