package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pravo-systems/pravo/internal/ui"
)

// An agent draws through the MCP server with the pen of the drawing board: it finds a type, checks a drawing, plans
// it, saves it as a new file, reads a resource as the pen changes it and changes it where it is written. A change
// made against a file that changed since is refused, and nothing is applied.
func TestMCPDrawsWithThePenOfTheBoard(t *testing.T) {
	rt := &runContext{}
	dir := localRoot(t, rt, "")
	rt.out = ui.NewPrinter(io.Discard, false)
	t.Chdir(dir)
	d := &mcpDrawer{rt: rt}
	defer d.close()
	byName := map[string]mcpTool{}
	for _, tool := range mcpToolsWith(rt, d) {
		byName[tool.Name] = tool
	}
	ctx := context.Background()
	call := func(name string, in map[string]any) (string, int) {
		t.Helper()
		tool, ok := byName[name]
		if !ok || tool.run == nil {
			t.Fatalf("no tool %s that runs in the server", name)
		}
		if _, err := tool.args(in); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		return inprocess(ctx, rt, in, tool.run)
	}

	if text, code := call("pravo_draw_types", map[string]any{"q": "file"}); code != 0 || !strings.Contains(text, `"local_file"`) {
		t.Fatalf("types: %d %s", code, text)
	}
	if text, code := call("pravo_draw_form", map[string]any{"type": "local_file", "provider": "$local"}); code != 0 || !strings.Contains(text, `"filename"`) {
		t.Fatalf("form: %d %s", code, text)
	}
	drawing := map[string]any{"items": []any{map[string]any{"label": "notes", "type": "local_file", "provider": "$local", "values": map[string]any{
		"filename": map[string]any{"text": "notes.txt"},
		"content":  map[string]any{"wire": map[string]any{"to": "hello", "read": "content"}},
	}}}}
	text, code := call("pravo_draw_check", map[string]any{"drawing": drawing})
	if code != 0 || !strings.Contains(text, `"state":"valid"`) || !strings.Contains(text, "content:  hello.content") {
		t.Fatalf("check: %d %s", code, text)
	}
	if text, code := call("pravo_draw_check", map[string]any{"drawing": map[string]any{"items": []any{map[string]any{"label": "notes", "type": "local_file", "provider": "$local"}}}}); code != 0 || !strings.Contains(text, `"state":"incomplete"`) {
		t.Errorf("check of a drawing that misses a field: %d %s", code, text)
	}
	if text, code := call("pravo_draw_plan", map[string]any{"drawing": drawing}); code != 0 || !strings.Contains(text, `"add":2`) {
		t.Fatalf("plan: %d %s", code, text)
	}
	if exists(dir, "board.cue") || exists(dir, "notes.txt") {
		t.Fatal("a check or a plan wrote something")
	}
	if text, code := call("pravo_draw_save", map[string]any{"drawing": drawing}); code != 0 || !exists(dir, "board.cue") {
		t.Fatalf("save: %d %s", code, text)
	}
	text, code = call("pravo_draw_resource", map[string]any{"addr": "notes"})
	var res struct {
		File, Base string
	}
	if code != 0 || json.Unmarshal([]byte(text), &res) != nil || res.File != "board.cue" || res.Base == "" {
		t.Fatalf("the saved resource, as the pen reads it: %d %s", code, text)
	}
	edit := map[string]any{"edits": []any{map[string]any{"addr": "notes", "base": res.Base, "set": map[string]any{"file_permission": map[string]any{"text": "0640"}}}}}
	if text, code := call("pravo_draw_save", map[string]any{"drawing": edit}); code != 0 {
		t.Fatalf("save of a change: %d %s", code, text)
	}
	b, _ := os.ReadFile(filepath.Join(dir, "board.cue"))
	if !bytes.Contains(b, []byte(`file_permission: "0640"`)) {
		t.Errorf("the change is not in the file:\n%s", b)
	}
	if text, code := call("pravo_draw_save", map[string]any{"drawing": edit}); code == 0 || !strings.Contains(text, "changed") {
		t.Errorf("a change against a file that changed since: %d %s", code, text)
	}
	if exists(dir, "notes.txt") {
		t.Error("the drawing tools applied something")
	}
	if text, code := call("pravo_draw_check", map[string]any{"drawing": "notes"}); code == 0 {
		t.Errorf("a drawing that is not one: %s", text)
	}
}
