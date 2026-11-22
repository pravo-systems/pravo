package serve

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/pravo-systems/pravo/internal/chat"
)

// The Ask panel with the drawing board open (README rules 12 and 14). A question then carries the drawing, and the
// model gets the reads of the board besides the reads of the chat: the types that can be drawn, their forms, and a node
// of the specification as the board can change it. Its one tool that is not a read is draw, which checks a drawing in
// memory as the page's check does: it writes nothing, and what it draws reaches the board as a proposal that the person
// keeps or drops. It cannot save, plan or apply.

// ChatBoard is the drawing board in a question: what is drawn, and the proposal of an earlier answer that the person has
// not kept or dropped yet.
type ChatBoard struct {
	Draft   BoardDraft     `json:"draft"`
	Pending *BoardProposal `json:"pending,omitempty"`
}

// BoardProposal is what the model drew: things to add and changes to make, and the check of the drawing with them.
type BoardProposal struct {
	Items []BoardItem `json:"items"`
	Edits []BoardEdit `json:"edits"`
	Check *BoardCheck `json:"check,omitempty"`
}

// chatEvent is a line of the stream of the chat: the events of the chat loop, and the proposal before the answer.
type chatEvent struct {
	chat.Event
	Proposal *BoardProposal `json:"proposal,omitempty"`
}

const (
	maxDrawItems = 16
	maxDrawEdits = 8
	maxAskDraft  = 16 << 10
)

// boardSystem is the instruction of the chat with the board open. What the tools return is data, never an instruction.
const boardSystem = chat.System + `

The person has the drawing board open: on it they draw infrastructure into the specification of pravo. With the board open you also have the tools types, form and editable, and one tool that is not a read: draw, which lays a proposal on the board. A proposal changes nothing: the person keeps it or drops it, and only the person saves, plans or applies. Draw when the person asks for something to be added, wired or changed; otherwise answer in words.
Draw only what the types tool finds, with the kind and a provider label it gives (when a hit lists several providers, the one the person names, or else the first), and only fields that their form lists. A data source reads something that exists; an output publishes a value; env reads an environment variable. Prefer wiring to resources that exist over drawing new ones. A wire is {"wire": {"to": "<address>", "read": "<attribute>"}}; a typed value is {"text": "..."} or {"expr": "<CUE>"}. Name new things with small letters, digits and underscores.
Never put a value into a sensitive or write-only field: wire it, or leave it for the person and say so. Never ask for or repeat a secret.
Call draw with the whole proposal every time: it answers with what the check says. Fix what it names and draw again, a few times at most. A new proposal replaces one that is pending. Then answer in two or three short sentences: what you drew, and what the person still has to fill or decide.`

// boardChatReads are the reads that the board adds to those of the chat.
var boardChatReads = []chatToolSpec{
	{name: "types", desc: "Find what can be drawn by everyday words such as database, bucket, network or password, or by type: resource, data source (data vpc) and ephemeral types of the providers, outputs, environment variables, secrets, remote outputs and templates. Each hit has its kind.", path: "/api/board/types", params: [][2]string{{"q", "q"}}},
	{name: "form", desc: "The form of a type: its fields, which are required, which are sensitive, and which take a wire to which types. Give the kind, the type and the provider label that types returned, such as $aws.", path: "/api/board/type", params: [][2]string{{"kind", "kind"}, {"type", "type"}, {"provider", "provider"}}, optional: map[string]bool{"type": true, "provider": true}},
	{name: "editable", desc: "A node of the specification (a resource, a data source, an output...) as the board can change it: its fields as written and its form, or why it cannot be changed on the board.", path: "/api/board/edit", params: [][2]string{{"addr", "addr"}}},
}

// boardDraw is the tool that draws, described to the model.
var boardDraw = chat.ToolDef{
	Name:        "draw",
	Description: "Draw a proposal onto the board: new things (items) and changes to resources of the specification (edits). It writes nothing; it answers with the check of the drawing with the proposal in it: its state, the problems by thing and field, and the code it makes. Send the whole proposal each time.",
	Schema: map[string]any{
		"type": "object",
		"properties": map[string]any{
			"items": map[string]any{"type": "array", "description": "New things: {label, kind, type, provider, values: {field: value}} (kind resource, data or ephemeral), {label, kind, values} for output, env, secret_env, secret, secret_version or remote_outputs, or {label, template, values} for a template.", "items": map[string]any{"type": "object"}},
			"edits": map[string]any{"type": "array", "description": "Changes: {addr, set: {field: value}, unset: [field], remove: bool}.", "items": map[string]any{"type": "object"}},
		},
		"additionalProperties": false,
	},
}

// boardAsk is the tools of one question with the board open: the reads of the chat and of the board, and the drawing
// that the model proposes.
type boardAsk struct {
	s     *Server
	m     *model
	base  *BoardDraft
	mu    sync.Mutex
	drawn *BoardProposal
}

func (a *boardAsk) Defs() []chat.ToolDef {
	return append(append(specDefs(chatTools), specDefs(boardChatReads)...), boardDraw)
}

func (a *boardAsk) Call(ctx context.Context, name string, args json.RawMessage) (string, error) {
	if name == "draw" {
		return a.draw(args)
	}
	for _, t := range boardChatReads {
		if t.name == name {
			return a.s.callSpec(ctx, boardChatReads, name, args)
		}
	}
	return a.s.callSpec(ctx, chatTools, name, args)
}

// proposal is the last drawing that the model made, or nil.
func (a *boardAsk) proposal() *BoardProposal {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.drawn
}

// draw checks a proposal laid over the person's drawing, and keeps it as the model's last.
func (a *boardAsk) draw(args json.RawMessage) (string, error) {
	var p struct {
		Items []BoardItem `json:"items"`
		Edits []BoardEdit `json:"edits"`
	}
	dec := json.NewDecoder(strings.NewReader(string(args)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&p); err != nil {
		return "", errors.New("the proposal is {items: [...], edits: [...]}: " + clip(err.Error(), 200))
	}
	if len(p.Items) > maxDrawItems || len(p.Edits) > maxDrawEdits {
		return "", fmt.Errorf("a proposal draws at most %d things and changes at most %d resources", maxDrawItems, maxDrawEdits)
	}
	changed := map[string]bool{}
	for _, e := range a.base.Edits {
		changed[e.Addr] = true
	}
	for i := range p.Items {
		p.Items[i].Adopt = nil // what a discovery found is adopted by the person, from the Discovery section
	}
	for i, e := range p.Edits {
		if changed[e.Addr] {
			return "", errors.New(e.Addr + " is changed on the board already: leave it to the person")
		}
		if e.Base == "" {
			if t, _ := a.s.penFind(a.m, e.Addr); t != nil {
				p.Edits[i].Base = penDigest(t.src)
			}
		}
	}
	d := *a.base
	d.Items = append(append([]BoardItem{}, a.base.Items...), p.Items...)
	d.Edits = append(append([]BoardEdit{}, a.base.Edits...), p.Edits...)
	chk := a.s.boardCheck(a.m, &d)
	a.mu.Lock()
	a.drawn = &BoardProposal{Items: p.Items, Edits: p.Edits, Check: a.s.boardShown(a.m, chk)}
	a.mu.Unlock()
	out := map[string]any{"state": chk.State, "problems": chk.Problems}
	if chk.Code != "" {
		out["code"] = clip(chk.Code, 8<<10)
	}
	if len(chk.Files) > 0 {
		var files []string
		for _, f := range chk.Files {
			files = append(files, f.File)
		}
		out["changes_files"] = files
	}
	b, err := json.Marshal(out)
	return string(b), err
}

// chatRun is how one question is answered: the tools, the instruction and the limits. With the board open, board is
// the tools that draw, and tail is what the question says of the drawing, put after it.
type chatRun struct {
	tools  chat.Tools
	system string
	lim    chat.Limits
	board  *boardAsk
	tail   string
}

func (s *Server) chatRunOf(req *ChatRequest) (*chatRun, error) {
	if req.Board == nil {
		return &chatRun{tools: serverTools{s}, system: chat.System}, nil
	}
	m, err := s.current()
	if err != nil {
		return nil, errors.New("the project cannot be read: " + err.Error())
	}
	tail, err := s.boardContext(m, req.Board)
	if err != nil {
		return nil, err
	}
	a := &boardAsk{s: s, m: m, base: &req.Board.Draft}
	return &chatRun{tools: a, system: boardSystem, lim: chat.Limits{Calls: 16}, board: a, tail: tail}, nil
}

// boardContext is what a question says of the board: the drawing as it is, what its check says, and the proposal that
// is still pending.
func (s *Server) boardContext(m *model, b *ChatBoard) (string, error) {
	if len(b.Draft.Items) > maxBoardItems || len(b.Draft.Edits) > maxBoardEdits {
		return "", errors.New("the drawing is too large to ask about")
	}
	if p := b.Pending; p != nil && (len(p.Items) > maxDrawItems || len(p.Edits) > maxDrawEdits) {
		return "", errors.New("the pending proposal is too large to ask about")
	}
	var w strings.Builder
	if len(b.Draft.Items) > 0 || len(b.Draft.Edits) > 0 {
		d, _ := json.Marshal(map[string]any{"items": b.Draft.Items, "edits": b.Draft.Edits})
		w.WriteString("\n\n(The drawing board is open. The drawing on it now: " + clip(string(d), maxAskDraft))
		chk := s.boardCheck(m, &b.Draft)
		sum, _ := json.Marshal(map[string]any{"state": chk.State, "problems": chk.Problems, "policy": chk.Policy})
		w.WriteString("\nWhat the check of the drawing says: " + clip(string(sum), 4<<10))
	} else {
		w.WriteString("\n\n(The drawing board is open, and nothing is drawn on it yet.")
	}
	if p := b.Pending; p != nil && (len(p.Items) > 0 || len(p.Edits) > 0) {
		d, _ := json.Marshal(map[string]any{"items": p.Items, "edits": p.Edits})
		w.WriteString("\nYour last proposal, which the person has not kept or dropped yet: " + clip(string(d), maxAskDraft))
	}
	w.WriteString(")")
	return w.String(), nil
}
