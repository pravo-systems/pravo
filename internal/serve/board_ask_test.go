package serve

import (
	"bufio"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/pravo-systems/pravo/internal/chat"
)

// askBoard asks the chat a question with the board open and reads its stream.
func askBoard(t *testing.T, p *project, q string, b *ChatBoard) (int, []chatEvent) {
	t.Helper()
	body, _ := json.Marshal(ChatRequest{Messages: []ChatTurn{{Role: "user", Text: q}}, Board: b})
	r, _ := http.NewRequest(http.MethodPost, p.base+"/api/chat", strings.NewReader(string(body)))
	r.Header.Set("Authorization", "Bearer "+p.srv.Token())
	r.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(r)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var evs []chatEvent
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 1<<20), 8<<20)
	for sc.Scan() {
		if line := sc.Text(); strings.HasPrefix(line, "data: ") {
			var e chatEvent
			if json.Unmarshal([]byte(line[6:]), &e) == nil {
				evs = append(evs, e)
			}
		}
	}
	return resp.StatusCode, evs
}

func eventOf(evs []chatEvent, typ string) *chatEvent {
	for i := range evs {
		if evs[i].Type == typ {
			return &evs[i]
		}
	}
	return nil
}

func toolNames(defs []chat.ToolDef) string {
	var names []string
	for _, d := range defs {
		names = append(names, d.Name)
	}
	return strings.Join(names, ",")
}

// The reads that the board adds to the chat are GETs of the page that write nothing.
func TestBoardAskReadsOnlyThePagesReads(t *testing.T) {
	readPaths := map[string]bool{"/api/board/types": true, "/api/board/type": true, "/api/board/edit": true}
	for _, tl := range boardChatReads {
		if !readPaths[tl.path] {
			t.Errorf("tool %s reads %s, which is not a read path of the page", tl.name, tl.path)
		}
	}
}

// With the board open, the chat reads, draws a proposal that the board checks, fixes what the check says and answers:
// nothing is written, and the tools that would save, approve or apply do not exist for it (plan is the read of a stored plan).
func TestBoardAskDrawsAProposalAndWritesNothing(t *testing.T) {
	p := penProject(t, &BoardOptions{Actor: "ada"})
	script := &chat.Script{Replies: []chat.Response{
		{Calls: []chat.Call{call("1", "types", `{"q":"database"}`), call("2", "form", `{"type":"fake_db","provider":"$fake"}`)}},
		// first a drawing that misses its name and sets a secret, then one that checks
		{Calls: []chat.Call{call("3", "draw", `{"items":[{"label":"orders","type":"fake_db","provider":"$fake","values":{"blob":{"text":"hunter2"}}}]}`)}},
		{Calls: []chat.Call{call("4", "draw", `{"items":[{"label":"orders","type":"fake_db","provider":"$fake","values":{"name":{"text":"orders"},"port":{"wire":{"to":"cache","read":"port"}}}}],"edits":[{"addr":"cache","set":{"api_token":{"text":"t"}}}]}`)}},
		{Calls: []chat.Call{call("5", "save", `{}`), call("6", "approve", `{}`), call("7", "apply", `{}`)}},
		{Text: "I drew `orders`, wired to the port of `cache`."},
	}}
	p.srv.opts.Chat = &ChatOptions{Provider: script, Name: "fake", Model: "m", Host: "example.test"}
	before := digestTree(t, p.dir)

	mine := ordersDraft()
	mine.Items[0].Label = "billing"
	status, evs := askBoard(t, p, "a database for orders", &ChatBoard{Draft: mine})
	if status != http.StatusOK {
		t.Fatalf("ask: %d %+v", status, evs)
	}
	pr := eventOf(evs, "proposal")
	if pr == nil || pr.Proposal == nil || len(pr.Proposal.Items) != 1 || pr.Proposal.Items[0].Label != "orders" {
		t.Fatalf("proposal: %+v", evs)
	}
	// the proposal is checked with the person's drawing: billing and orders, and the change of cache
	if c := pr.Proposal.Check; c.State != "valid" || !strings.Contains(c.Code, "billing:") || !strings.Contains(c.Code, "orders:") || len(c.Files) != 1 {
		t.Errorf("the check of the proposal: %+v", c)
	}
	if e := pr.Proposal.Edits; len(e) != 1 || e[0].Base == "" {
		t.Errorf("an edit of the model is made against the file as it is: %+v", e)
	}
	done := eventOf(evs, "done")
	if done == nil || eventOf(evs, "text") == nil {
		t.Fatalf("no answer: %+v", evs)
	}
	refused := 0
	for _, s := range done.Sent {
		if s.Tool == "draw" && strings.Contains(s.Body, "hunter2") {
			t.Error("a typed secret came back to the model")
		}
		if (s.Tool == "save" || s.Tool == "approve" || s.Tool == "apply") && strings.HasPrefix(s.Error, "no such tool") {
			refused++
		}
	}
	if refused != 3 {
		t.Errorf("save, approve and apply: %+v", done.Sent)
	}
	if first := done.Sent[2]; first.Tool != "draw" || !strings.Contains(first.Body, "sensitive field takes no typed value") {
		t.Errorf("the first drawing: %+v", first)
	}
	if digestTree(t, p.dir) != before {
		t.Fatal("the chat changed the project directory")
	}
	req := script.Requests[0]
	q := req.Messages[0].Text
	if req.System != boardSystem || !strings.HasPrefix(q, "a database for orders") || !strings.Contains(q, `"label":"billing"`) || !strings.Contains(q, "What the check of the drawing says") {
		t.Errorf("what the model was asked: %q / %q", req.System[:40], q)
	}
	if strings.Contains(req.System, "You can only read") || !strings.Contains(req.System, "random_password") {
		t.Errorf("the instruction with the board says that it draws, and how a secret is drawn: %q", req.System)
	}
	want := strings.Join(chatToolNames(), ",") + ",types,form,editable,draw"
	if got := toolNames(req.Tools); got != want {
		t.Errorf("tools: %s, want %s", got, want)
	}
}

// Without the board the chat has no tool that draws; with it, a pending proposal is given to the model to revise; and a
// drawing too large to ask about is refused.
func TestBoardAskOnlyWithTheBoardOpen(t *testing.T) {
	p := penProject(t, nil)
	if status, _ := askBoard(t, p, "x", &ChatBoard{}); status != http.StatusNotFound {
		t.Errorf("the chat without --chat: %d", status)
	}
	var cfg BoardConfig
	if err := json.Unmarshal(p.get("/api/board"), &cfg); err != nil || cfg.Ask != "" {
		t.Fatalf("config without --chat: %+v %v", cfg, err)
	}
	script := &chat.Script{Replies: []chat.Response{
		{Calls: []chat.Call{call("1", "draw", `{"items":[]}`), call("2", "node", `{"addr":"shared"}`)}},
		{Text: "`shared` is a database."},
		{Text: "Made it larger."},
	}}
	p.srv.opts.Chat = &ChatOptions{Provider: script, Name: "fake", Model: "m", Host: "example.test"}
	if err := json.Unmarshal(p.get("/api/board"), &cfg); err != nil || cfg.Ask != "fake m" {
		t.Fatalf("config with --chat: %+v %v", cfg, err)
	}
	status, evs := askBoard(t, p, "what is shared?", nil)
	if status != http.StatusOK || eventOf(evs, "proposal") != nil || eventOf(evs, "text") == nil {
		t.Fatalf("without the board: %d %+v", status, evs)
	}
	if s := eventOf(evs, "done").Sent; s[0].Error != "no such tool: draw" || s[1].Error != "" {
		t.Errorf("tools without the board: %+v", s)
	}
	if script.Requests[0].System != chat.System || strings.Contains(toolNames(script.Requests[0].Tools), "draw") {
		t.Errorf("without the board the chat is as it was: %+v", script.Requests[0].Tools)
	}
	pending := &BoardProposal{Items: ordersDraft().Items}
	if status, _ := askBoard(t, p, "make it larger", &ChatBoard{Pending: pending}); status != http.StatusOK {
		t.Fatalf("with a pending proposal: %d", status)
	}
	if q := script.Requests[2].Messages[0].Text; !strings.Contains(q, "Nothing is drawn on the drawing board yet") || !strings.Contains(q, "Your last proposal") || !strings.Contains(q, `"label":"orders"`) {
		t.Errorf("the pending proposal was not given: %q", q)
	}
	big := &BoardProposal{Items: make([]BoardItem, maxDrawItems+1)}
	if status, _ := askBoard(t, p, "x", &ChatBoard{Pending: big}); status != http.StatusBadRequest {
		t.Errorf("a pending proposal too large: %d", status)
	}
}
