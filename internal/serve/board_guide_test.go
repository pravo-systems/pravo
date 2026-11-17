package serve

import (
	"bufio"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/pravo-systems/pravo/internal/chat"
)

// askGuide sends a question to the guide and reads its stream.
func askGuide(t *testing.T, p *project, req BoardGuideRequest) (int, []guideEvent) {
	t.Helper()
	body, _ := json.Marshal(req)
	r, _ := http.NewRequest(http.MethodPost, p.base+"/api/board/guide", strings.NewReader(string(body)))
	r.Header.Set("Authorization", "Bearer "+p.srv.Token())
	r.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(r)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var evs []guideEvent
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 1<<20), 8<<20)
	for sc.Scan() {
		if line := sc.Text(); strings.HasPrefix(line, "data: ") {
			var e guideEvent
			if json.Unmarshal([]byte(line[6:]), &e) == nil {
				evs = append(evs, e)
			}
		}
	}
	return resp.StatusCode, evs
}

func eventOf(evs []guideEvent, typ string) *guideEvent {
	for i := range evs {
		if evs[i].Type == typ {
			return &evs[i]
		}
	}
	return nil
}

// The reads of the guide are GETs of the page that write nothing.
func TestBoardGuideReadsOnlyThePagesReads(t *testing.T) {
	readPaths := map[string]bool{"/api/graph": true, "/api/detail": true, "/api/source": true, "/api/board/types": true, "/api/board/type": true, "/api/board/edit": true}
	for _, tl := range guideTools {
		if !readPaths[tl.path] {
			t.Errorf("tool %s reads %s, which is not a read path of the page", tl.name, tl.path)
		}
	}
}

// The guide reads, draws a proposal that the board checks, fixes what the check says and answers: nothing is written,
// and the tools that would save, plan or apply do not exist for it.
func TestBoardGuideDrawsAProposalAndWritesNothing(t *testing.T) {
	p := penProject(t, &BoardOptions{Actor: "ada"})
	script := &chat.Script{Replies: []chat.Response{
		{Calls: []chat.Call{call("1", "types", `{"q":"database"}`), call("2", "form", `{"type":"fake_db","provider":"$fake"}`)}},
		// first a drawing that misses its name and sets a secret, then one that checks
		{Calls: []chat.Call{call("3", "draw", `{"items":[{"label":"orders","type":"fake_db","provider":"$fake","values":{"blob":{"text":"hunter2"}}}]}`)}},
		{Calls: []chat.Call{call("4", "draw", `{"items":[{"label":"orders","type":"fake_db","provider":"$fake","values":{"name":{"text":"orders"},"port":{"wire":{"to":"cache","read":"port"}}}}],"edits":[{"addr":"cache","set":{"api_token":{"text":"t"}}}]}`)}},
		{Calls: []chat.Call{call("5", "save", `{}`), call("6", "plan", `{}`), call("7", "apply", `{}`)}},
		{Text: "I drew `orders`, wired to the port of `cache`."},
	}}
	p.srv.opts.Chat = &ChatOptions{Provider: script, Name: "fake", Model: "m", Host: "example.test"}
	before := digestTree(t, p.dir)

	mine := ordersDraft()
	mine.Items[0].Label = "billing"
	status, evs := askGuide(t, p, BoardGuideRequest{Mode: "draw", Wish: "a database for orders", Draft: mine})
	if status != http.StatusOK {
		t.Fatalf("guide: %d %+v", status, evs)
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
		t.Errorf("an edit of the guide is made against the file as it is: %+v", e)
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
		if (s.Tool == "save" || s.Tool == "plan" || s.Tool == "apply") && strings.HasPrefix(s.Error, "no such tool") {
			refused++
		}
	}
	if refused != 3 {
		t.Errorf("save, plan and apply: %+v", done.Sent)
	}
	if first := done.Sent[2]; first.Tool != "draw" || !strings.Contains(first.Body, "sensitive field takes no typed value") {
		t.Errorf("the first drawing: %+v", first)
	}
	if digestTree(t, p.dir) != before {
		t.Fatal("the guide changed the project directory")
	}
	req := script.Requests[0]
	if req.System != guideSystem || !strings.Contains(req.Messages[0].Text, "a database for orders") || !strings.Contains(req.Messages[0].Text, `"label":"billing"`) {
		t.Errorf("what the guide was asked: %q / %q", req.System[:40], req.Messages[0].Text)
	}
	var names []string
	for _, d := range req.Tools {
		names = append(names, d.Name)
	}
	if strings.Join(names, ",") != "overview,node,source,types,form,editable,draw" {
		t.Errorf("tools: %v", names)
	}
}

// Review and explain read and answer: they have no tool that draws, and the guide is not there without --chat.
func TestBoardGuideReviewsAndExplainsAndIsOffWithoutAModel(t *testing.T) {
	p := penProject(t, nil)
	if status, _ := askGuide(t, p, BoardGuideRequest{Mode: "draw", Wish: "x"}); status != http.StatusNotFound {
		t.Errorf("the guide without --chat: %d", status)
	}
	var cfg BoardConfig
	if err := json.Unmarshal(p.get("/api/board"), &cfg); err != nil || cfg.Guide != "" {
		t.Fatalf("config without --chat: %+v %v", cfg, err)
	}
	script := &chat.Script{Replies: []chat.Response{
		{Calls: []chat.Call{call("1", "draw", `{"items":[]}`), call("2", "node", `{"addr":"shared"}`)}},
		{Text: "It adds a database that reads the port of `shared`."},
		{Text: "`cache` is a database."},
	}}
	p.srv.opts.Chat = &ChatOptions{Provider: script, Name: "fake", Model: "m", Host: "example.test"}
	if err := json.Unmarshal(p.get("/api/board"), &cfg); err != nil || cfg.Guide != "fake m" {
		t.Fatalf("config with --chat: %+v %v", cfg, err)
	}
	status, evs := askGuide(t, p, BoardGuideRequest{Mode: "review", Draft: ordersDraft()})
	if status != http.StatusOK || eventOf(evs, "proposal") != nil || eventOf(evs, "text") == nil {
		t.Fatalf("review: %d %+v", status, evs)
	}
	if s := eventOf(evs, "done").Sent; s[0].Error != "no such tool: draw" || s[1].Error != "" {
		t.Errorf("review tools: %+v", s)
	}
	if !strings.Contains(script.Requests[0].Messages[0].Text, "What the check of the drawing says") {
		t.Errorf("review question: %q", script.Requests[0].Messages[0].Text)
	}
	if status, evs := askGuide(t, p, BoardGuideRequest{Mode: "explain", Addr: "cache"}); status != http.StatusOK || eventOf(evs, "text").Text != "`cache` is a database." {
		t.Fatalf("explain: %d %+v", status, evs)
	}
	for _, bad := range []BoardGuideRequest{{Mode: "draw"}, {Mode: "review"}, {Mode: "explain"}, {Mode: "apply", Wish: "x"}, {Mode: "draw", Wish: strings.Repeat("x", maxGuideWish+1)}} {
		if status, _ := askGuide(t, p, bad); status != http.StatusBadRequest {
			t.Errorf("%+v: %d", bad.Mode, status)
		}
	}
}
