package serve

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pravo-systems/pravo/internal/state"
)

// boardProject is the project of the tests with the board's writes on or off.
func boardProject(t *testing.T, board *BoardOptions) *project {
	t.Helper()
	dir := t.TempDir()
	writeSpec(t, dir, specCUE)
	st, err := state.Open(filepath.Join(dir, "pravo.state"))
	if err != nil {
		t.Fatal(err)
	}
	seed(t, st)
	return start(t, dir, st, Options{Board: board})
}

// post sends a drawing to the board.
func (p *project) post(path string, v any) (*http.Response, []byte) {
	p.t.Helper()
	body, err := json.Marshal(v)
	if err != nil {
		p.t.Fatal(err)
	}
	req, err := http.NewRequest(http.MethodPost, p.base+path, bytes.NewReader(body))
	if err != nil {
		p.t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+p.srv.Token())
	req.Header.Set("Content-Type", "application/json")
	return p.send(req)
}

func strp(s string) *string { return &s }

// orders is a database drawn on the board: its name typed, its port wired to the shared database.
func ordersDraft() BoardDraft {
	return BoardDraft{Items: []BoardItem{{Label: "orders", Type: "fake_db", Values: map[string]BoardValue{
		"name": {Text: strp("orders")},
		"port": {Wire: &BoardRef{To: "shared", Read: "port"}},
	}}}}
}

func boardCheckOf(t *testing.T, p *project, d BoardDraft) BoardCheck {
	t.Helper()
	resp, body := p.post("/api/board/check", d)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("check: %d %s", resp.StatusCode, body)
	}
	var out BoardCheck
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestBoardDrawsAndChecksWithoutWriting(t *testing.T) {
	p := boardProject(t, nil)
	before := digestTree(t, p.dir)

	var cfg BoardConfig
	if err := json.Unmarshal(p.get("/api/board"), &cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.Edit || len(cfg.Providers) != 1 || cfg.Providers[0].Label != "$fake" || cfg.Providers[0].Types != 2 || cfg.File != "board.cue" || cfg.Problem != "" {
		t.Fatalf("config: %+v", cfg)
	}

	var types BoardTypes
	if err := json.Unmarshal(p.get("/api/board/types?q=db"), &types); err != nil {
		t.Fatal(err)
	}
	if len(types.Hits) == 0 || types.Hits[0].Type != "fake_db" || types.Hits[0].Provider != "$fake" {
		t.Fatalf("types: %+v", types)
	}

	var form BoardType
	if err := json.Unmarshal(p.get("/api/board/type?type=fake_db&provider=$fake"), &form); err != nil {
		t.Fatal(err)
	}
	if len(form.Fields) == 0 || form.Fields[0].Name != "name" || !form.Fields[0].Required || form.Fields[0].Kind != "text" {
		t.Fatalf("form: %+v", form.Fields)
	}
	for _, f := range form.Fields {
		if f.Name == "pin_code" && !f.Sensitive {
			t.Errorf("pin_code is sensitive: %+v", f)
		}
	}

	chk := boardCheckOf(t, p, ordersDraft())
	if chk.State != "valid" || len(chk.Problems) != 0 {
		t.Fatalf("check: %s %+v", chk.State, chk.Problems)
	}
	want := "orders: $fake.#Resource.fake_db & {\n\tname: \"orders\"\n\tport: shared.port\n}"
	if !strings.Contains(chk.Code, want) || !strings.HasPrefix(chk.Code, "package demo\n") {
		t.Fatalf("code:\n%s", chk.Code)
	}
	if l := chk.Lines["orders"]; l[0] == 0 || l[1] < l[0]+3 {
		t.Errorf("lines: %+v", chk.Lines)
	}
	if len(chk.Creates) != 1 || chk.Creates[0].Address != "orders" {
		t.Errorf("creates: %+v", chk.Creates)
	}

	// without --edit the board writes nothing and plans nothing
	for _, path := range []string{"/api/board/save", "/api/board/plan"} {
		if resp, body := p.post(path, ordersDraft()); resp.StatusCode != http.StatusForbidden {
			t.Errorf("%s: %d %s", path, resp.StatusCode, body)
		}
	}
	if resp, _ := p.do("GET", "/api/board/check", nil); resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("GET of a check: %d", resp.StatusCode)
	}
	if after := digestTree(t, p.dir); after != before {
		t.Fatal("the board changed the project directory without --edit")
	}
}

func TestBoardSaysWhatIsMissingAndWrong(t *testing.T) {
	p := boardProject(t, nil)
	d := ordersDraft()
	delete(d.Items[0].Values, "name")
	if chk := boardCheckOf(t, p, d); chk.State != "incomplete" || len(chk.Problems) != 1 || chk.Problems[0].Item != "orders" || chk.Problems[0].Field != "name" {
		t.Errorf("missing name: %s %+v", chk.State, chk.Problems)
	}
	d = ordersDraft()
	d.Items[0].Values["port"] = BoardValue{Expr: "1 +"}
	if chk := boardCheckOf(t, p, d); chk.State != "invalid" || len(chk.Problems) == 0 || chk.Problems[0].Field != "port" {
		t.Errorf("bad expression: %s %+v", chk.State, chk.Problems)
	}
	d = ordersDraft()
	d.Items[0].Values["port"] = BoardValue{Text: strp("not a number")}
	if chk := boardCheckOf(t, p, d); chk.State != "invalid" || len(chk.Problems) == 0 || chk.Problems[0].Item != "orders" {
		t.Errorf("a text for a number: %s %+v", chk.State, chk.Problems)
	}
	for _, label := range []string{"shared", "services", "string", "Orders", "9x", "has-dash"} {
		d = ordersDraft()
		d.Items[0].Label = label
		if chk := boardCheckOf(t, p, d); chk.State != "invalid" || len(chk.Problems) == 0 || chk.Problems[0].Field != "@label" {
			t.Errorf("label %q: %s %+v", label, chk.State, chk.Problems)
		}
	}
	d = ordersDraft()
	d.Items[0].Values["port"] = BoardValue{Wire: &BoardRef{To: "nowhere", Read: "port"}}
	if chk := boardCheckOf(t, p, d); chk.State != "invalid" || len(chk.Problems) == 0 || chk.Problems[0].Field != "port" {
		t.Errorf("a wire to nothing: %s %+v", chk.State, chk.Problems)
	}
	d = ordersDraft()
	d.File = "../escape.cue"
	if chk := boardCheckOf(t, p, d); chk.State != "invalid" || chk.Problems[0].Field != "@file" {
		t.Errorf("a file outside: %s %+v", chk.State, chk.Problems)
	}
}

func TestBoardWiresBetweenThingsDrawnAndAvoidsShadowing(t *testing.T) {
	p := boardProject(t, nil)
	// the twin reads the name of the database named "name", and sets a field "name" of its own: the reference goes
	// through an alias, or it would read that field
	d := BoardDraft{Items: []BoardItem{
		{Label: "name", Type: "fake_db", Values: map[string]BoardValue{"name": {Text: strp("first")}}},
		{Label: "twin", Type: "fake_db", Values: map[string]BoardValue{"name": {Wire: &BoardRef{To: "name", Read: "name"}}}},
		{Label: "note", Type: "fake_file", Values: map[string]BoardValue{
			"path":    {Text: strp("/tmp/note")},
			"content": {Wire: &BoardRef{To: "name", Read: "name"}},
		}},
		{Label: "copy", Type: "fake_file", Values: map[string]BoardValue{
			"path":    {Wire: &BoardRef{To: "note", Read: "path"}},
			"content": {Wire: &BoardRef{To: "services.a.file", Read: "content"}},
		}},
	}}
	chk := boardCheckOf(t, p, d)
	if chk.State != "valid" {
		t.Fatalf("%s %+v\n%s", chk.State, chk.Problems, chk.Code)
	}
	for _, want := range []string{"let ref_name = name", "\tname: ref_name.name", "content: name.name", "path:    note.path", "content: services.a.file.content"} {
		if !strings.Contains(chk.Code, want) {
			t.Errorf("no %q in\n%s", want, chk.Code)
		}
	}
	// what each reads, wired or typed, is what the graph draws as its edges
	d.Items[0].Values["name"] = BoardValue{Expr: `"db-\(services.a.file.id)"`}
	chk = boardCheckOf(t, p, d)
	want := map[string][]string{"name": {"services.a.file"}, "twin": {"name"}, "note": {"name"}, "copy": {"note", "services.a.file"}}
	if chk.State != "valid" || fmt.Sprint(chk.Reads) != fmt.Sprint(want) {
		t.Errorf("reads: %s %v, want %v", chk.State, chk.Reads, want)
	}
}

func TestBoardEditWritesOneNewFileAndNeverAnother(t *testing.T) {
	var planned BoardPlanRequest
	p := boardProject(t, &BoardOptions{Actor: "ada", Plan: func(ctx context.Context, req BoardPlanRequest, step func(BoardStep)) (*BoardPlanResult, error) {
		planned = req
		step(BoardStep{Addr: "orders", Kind: "plan", Phase: "start"})
		return &BoardPlanResult{Changes: []BoardChange{{Addr: "orders", Action: "create"}}, Add: 1}, nil
	}})
	var cfg BoardConfig
	if err := json.Unmarshal(p.get("/api/board"), &cfg); err != nil {
		t.Fatal(err)
	}
	if !cfg.Edit || cfg.Actor != "ada" {
		t.Fatalf("config: %+v", cfg)
	}

	// a plan of the drawing before it is saved lays the file over the specification and writes nothing
	before := digestTree(t, p.dir)
	resp, body := p.post("/api/board/plan", ordersDraft())
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), `"step":{"addr":"orders"`) || !strings.Contains(string(body), `"done":{"changes":[{"addr":"orders","action":"create"}]`) {
		t.Fatalf("plan: %d %s", resp.StatusCode, body)
	}
	if !strings.Contains(planned.Extra["board.cue"], "orders: $fake.#Resource.fake_db") {
		t.Errorf("the plan was not given the drawing: %+v", planned.Extra)
	}
	if digestTree(t, p.dir) != before {
		t.Fatal("a plan of the board changed the project directory")
	}

	// an incomplete drawing is not saved
	d := ordersDraft()
	delete(d.Items[0].Values, "name")
	if resp, _ := p.post("/api/board/save", d); resp.StatusCode != http.StatusConflict {
		t.Errorf("an incomplete drawing was saved: %d", resp.StatusCode)
	}

	resp, body = p.post("/api/board/save", ordersDraft())
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("save: %d %s", resp.StatusCode, body)
	}
	var saved BoardSaved
	if err := json.Unmarshal(body, &saved); err != nil {
		t.Fatal(err)
	}
	written, err := os.ReadFile(filepath.Join(p.dir, "board.cue"))
	if err != nil || saved.File != "board.cue" || !strings.Contains(string(written), "orders: $fake.#Resource.fake_db") || saved.Commands[1] != "pravo apply" {
		t.Fatalf("saved %+v: %s %v", saved, written, err)
	}

	// the name is taken now: a second save of the same file is refused, and nothing else in the directory changed
	if resp, body := p.post("/api/board/save", BoardDraft{File: "board.cue", Items: []BoardItem{{Label: "other", Type: "fake_file", Values: map[string]BoardValue{"path": {Text: strp("/x")}}}}}); resp.StatusCode != http.StatusConflict {
		t.Errorf("a second save of board.cue: %d %s", resp.StatusCode, body)
	}
	if got, _ := os.ReadFile(filepath.Join(p.dir, "board.cue")); !bytes.Equal(got, written) {
		t.Error("board.cue was written over")
	}
	var cfg2 BoardConfig
	if err := json.Unmarshal(p.get("/api/board"), &cfg2); err != nil {
		t.Fatal(err)
	}
	if cfg2.File != "board-2.cue" {
		t.Errorf("the next file: %q", cfg2.File)
	}
}
