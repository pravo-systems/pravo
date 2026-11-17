package serve

import (
	"context"
	"encoding/json"
	"io"
	"mime"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// maxBoardState is the largest Terraform state the board takes from the page: a state of thousands of resources.
const maxBoardState = 64 << 20

// BoardStartRequest is a Terraform state to start the board from: a place the command reads, a file on this machine
// or an s3:// object, or the text of a state that the person chose on the page.
type BoardStartRequest struct {
	Place string `json:"place,omitempty"`
	State string `json:"state,omitempty"`
}

// BoardStartResult is what a start from a Terraform state wrote: the file of the specification, the records of the
// objects, and whether the plan of it changes nothing. It carries no value of a resource.
type BoardStartResult struct {
	File      string `json:"file"`
	From      string `json:"from"`
	Resources int    `json:"resources"`
	Modules   int    `json:"modules"`
	Lines     int    `json:"lines"`
	// Verified says that the plan of the written specification changes nothing; Wants lists what it changes otherwise.
	Verified bool     `json:"verified"`
	Wants    []string `json:"wants,omitempty"`
	// LeftOut counts the attributes the provider derives that are not written; Secrets the sensitive values that are
	// not written, or that the vault keeps.
	LeftOut int `json:"left_out"`
	Secrets int `json:"secrets"`
	// Guarded says that Terraform still owns the objects: an apply that would change one is refused until
	// "pravo shadow take-over".
	Guarded bool     `json:"guarded"`
	Notes   []string `json:"notes,omitempty"`
}

// boardStartHandler starts the board from a Terraform state, as "pravo shadow" does, into a project whose state
// holds nothing yet. It writes a new file of the specification and the records of the objects, and applies nothing.
// It streams lines of JSON: a step while it runs, then the result or the error.
func (s *Server) boardStartHandler(w http.ResponseWriter, r *http.Request, m *model) {
	if s.opts.Board == nil || s.opts.Board.Start == nil {
		writeError(w, http.StatusForbidden, "the board starts from a Terraform state only when pravo serve was started with --edit")
		return
	}
	if mt, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type")); mt != "application/json" {
		writeError(w, http.StatusUnsupportedMediaType, "the state is sent as application/json")
		return
	}
	var req BoardStartRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, maxBoardState+4<<10)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "send the place of a Terraform state, or its text")
		return
	}
	req.Place = strings.TrimSpace(req.Place)
	switch {
	case (req.Place == "") == (req.State == ""):
		writeError(w, http.StatusBadRequest, "send the place of a Terraform state, or its text: one of them")
		return
	case len(req.State) > maxBoardState:
		writeError(w, http.StatusRequestEntityTooLarge, "the state is larger than 64 MB: run pravo shadow in a terminal")
		return
	case len(req.Place) > 1024 || req.Place == "-" || strings.ContainsAny(req.Place, "\x00\n\r"):
		writeError(w, http.StatusBadRequest, "the place is a path of this machine or an s3:// address")
		return
	}
	if n := m.recorded(); n > 0 {
		writeError(w, http.StatusConflict, "the state of this project holds "+strconv.Itoa(n)+plural(n, " resource", " resources")+": the board starts from a Terraform state only into an empty one")
		return
	}
	if !s.boardPlanning.CompareAndSwap(false, true) {
		writeError(w, http.StatusConflict, "a run of the board is running already")
		return
	}
	defer s.boardPlanning.Store(false)
	send := ndjsonWriter(w)
	send(map[string]any{"step": BoardStep{Kind: "start", Phase: "begin"}})
	// a start that began writes the file and the records together: a page that goes away does not stop it halfway
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 2*time.Hour)
	defer cancel()
	began := time.Now()
	res, err := s.opts.Board.Start(ctx, req)
	if err != nil {
		send(map[string]any{"error": s.boardText(m, err.Error()), "took_ms": time.Since(began).Milliseconds()})
		return
	}
	for i, n := range res.Notes {
		res.Notes[i] = s.boardText(m, n)
	}
	for i, n := range res.Wants {
		res.Wants[i] = s.boardText(m, n)
	}
	send(map[string]any{"done": res, "took_ms": time.Since(began).Milliseconds()})
}

// recorded counts the resources the state of the model holds records of.
func (m *model) recorded() int {
	n := 0
	for _, x := range m.nodes {
		if x.rec != nil {
			n++
		}
	}
	return n
}
