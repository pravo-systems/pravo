package serve

import (
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"cuelang.org/go/cue/format"

	"github.com/pravo-systems/pravo/internal/state"
)

// cacheCUE is a file of the project with a resource written on several lines, with comments around it.
const cacheCUE = `package demo

// The cache of the shop.
cache: $fake.#Resource.fake_db & {
	name: "cache" // what the shop calls it
	port: 6379
	blob: "s3cret"

	// the zones it runs in
	zones: ["a", "b"]
}

// The apps, with a field that is not a resource beside them.
apps: {
	shared: "not the database"
	web: $fake.#Resource.fake_file & {
		path: "/srv/web"
	}
}

// The log of the cache.
log: $fake.#Resource.fake_file & {
	path:    "/var/log/cache"
	content: cache.name
}
`

// penProject is the project of the board tests with cache.cue beside main.cue.
func penProject(t *testing.T, board *BoardOptions) *project {
	t.Helper()
	dir := t.TempDir()
	writeSpec(t, dir, specCUE)
	if err := os.WriteFile(filepath.Join(dir, "cache.cue"), []byte(cacheCUE), 0o640); err != nil {
		t.Fatal(err)
	}
	st, err := state.Open(filepath.Join(dir, "pravo.state"))
	if err != nil {
		t.Fatal(err)
	}
	seed(t, st)
	return start(t, dir, st, Options{Board: board})
}

func editableOf(t *testing.T, p *project, addr string) BoardEditable {
	t.Helper()
	var out BoardEditable
	if err := json.Unmarshal(p.get("/api/board/edit?addr="+url.QueryEscape(addr)), &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestBoardReadsAResourceAsThePenCanChangeIt(t *testing.T) {
	p := penProject(t, nil)
	e := editableOf(t, p, "cache")
	if e.Reason != "" || e.File != "cache.cue" || e.Line != 4 || e.Base == "" || e.Form == nil {
		t.Fatalf("cache: %+v", e)
	}
	if v := e.Values["name"]; v.Text == nil || *v.Text != "cache" {
		t.Errorf("name: %+v", v)
	}
	if v := e.Values["port"]; v.Expr != "6379" {
		t.Errorf("port: %+v", v)
	}
	if v := e.Values["zones"]; v.Expr != `["a", "b"]` {
		t.Errorf("zones: %+v", v)
	}
	// the secret typed into the code is not sent
	if _, sent := e.Values["blob"]; sent || len(e.Withheld) != 1 || e.Withheld[0] != "blob" {
		t.Errorf("blob: %+v %v", e.Values["blob"], e.Withheld)
	}
	if v := editableOf(t, p, "log").Values["content"]; v.Wire == nil || v.Wire.To != "cache" || v.Wire.Read != "name" {
		t.Errorf("a reference is a wire: %+v", v)
	}
	if v := editableOf(t, p, "shared").Values["port"]; v.Wire == nil || v.Wire.To != "services.a.db" || v.Wire.Read != "port" {
		t.Errorf("a reference into a struct is a wire: %+v", v)
	}
	// what a template or a loop makes is changed where that is written
	for _, addr := range []string{"services.a.db", "hosts.x"} {
		if e := editableOf(t, p, addr); e.Reason == "" || e.Base != "" || !strings.Contains(e.Reason, "template, a loop or a condition") {
			t.Errorf("%s: %+v", addr, e)
		}
	}
	if e := editableOf(t, p, "nothing"); !strings.Contains(e.Reason, "no node") {
		t.Errorf("nothing: %+v", e)
	}
}

func TestBoardChangesAResourceWhereItIsWritten(t *testing.T) {
	p := penProject(t, &BoardOptions{Actor: "ada"})
	base := editableOf(t, p, "cache").Base
	d := BoardDraft{Edits: []BoardEdit{{Addr: "cache", Base: base,
		Set:   map[string]BoardValue{"name": {Text: strp("cache-2")}, "api_token": {Wire: &BoardRef{To: "shared", Read: "name"}}},
		Unset: []string{"port"},
	}}}
	chk := boardCheckOf(t, p, d)
	if chk.State != "valid" || len(chk.Files) != 1 || chk.Code != "" {
		t.Fatalf("check: %+v", chk)
	}
	fc := chk.Files[0]
	for _, want := range []string{"// The cache of the shop.", `name: "cache-2" // what the shop calls it`, "api_token: shared.name", "// the zones it runs in", `blob: "••••••••"`, "content: cache.name"} {
		if !strings.Contains(fc.Text, want) {
			t.Errorf("the new cache.cue has no %q:\n%s", want, fc.Text)
		}
	}
	if f, err := format.Source([]byte(fc.Text)); err != nil || string(f) != fc.Text {
		t.Errorf("a formatted file is formatted after the change: %v", err)
	}
	// the page sees the change with the secret typed into the code masked, in the text and in the lines of the change
	if raw, _ := json.Marshal(chk); strings.Contains(string(raw), "s3cret") {
		t.Errorf("the check sent a secret typed into the code: %s", raw)
	}
	if !slices.Contains(fc.Diff, BoardDiffLine{Op: " ", Line: 6, Text: "\tblob: \"••••••••\""}) {
		t.Errorf("the lines of the change: %+v", fc.Diff)
	}
	if strings.Contains(fc.Text, "6379") {
		t.Errorf("port was not unset:\n%s", fc.Text)
	}
	if fc.File != "cache.cue" || fc.Base != base || fc.Lines["cache"] != [2]int{4, 11} {
		t.Errorf("file change: %s %s %v", fc.File, fc.Base, fc.Lines)
	}
	if !slices.Contains(fc.Diff, BoardDiffLine{Op: "-", Line: 6, Text: "\tport: 6379"}) || !slices.Contains(fc.Diff, BoardDiffLine{Op: "+", Line: 10, Text: "\tapi_token: shared.name"}) {
		t.Errorf("diff: %+v", fc.Diff)
	}

	// a check writes nothing; a save writes the file, keeping its mode
	if got, _ := os.ReadFile(filepath.Join(p.dir, "cache.cue")); string(got) != cacheCUE {
		t.Fatal("a check changed cache.cue")
	}
	resp, body := p.post("/api/board/save", d)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("save: %d %s", resp.StatusCode, body)
	}
	var saved BoardSaved
	if err := json.Unmarshal(body, &saved); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(p.dir, "cache.cue"))
	if err != nil || string(got) != strings.Replace(fc.Text, `"••••••••"`, `"s3cret"`, 1) || saved.File != "" || len(saved.Changed) != 1 || saved.Changed[0] != "cache.cue" {
		t.Fatalf("saved %+v: %v\n%s", saved, err, got)
	}
	if fi, _ := os.Stat(filepath.Join(p.dir, "cache.cue")); runtime.GOOS != "windows" && fi.Mode().Perm() != 0o640 { // Windows keeps no Unix mode
		t.Errorf("mode: %v", fi.Mode())
	}
	if _, err := os.Lstat(filepath.Join(p.dir, "board.cue")); err == nil {
		t.Error("a save of changes alone wrote board.cue")
	}
	entries, _ := os.ReadDir(p.dir)
	for _, en := range entries {
		if strings.HasPrefix(en.Name(), ".cache.cue.board-") {
			t.Errorf("a file of the write was left: %s", en.Name())
		}
	}

	// the same change again is made against a file that changed since
	resp, body = p.post("/api/board/save", d)
	if resp.StatusCode != http.StatusConflict || !strings.Contains(string(body), "changed since") {
		t.Errorf("a change against an old base: %d %s", resp.StatusCode, body)
	}
}

func TestBoardChangesAStructOnOneLineAndRemovesAResource(t *testing.T) {
	p := penProject(t, &BoardOptions{Actor: "ada"})
	mainBase := editableOf(t, p, "shared").Base
	cacheBase := editableOf(t, p, "cache").Base
	// log reads cache, so cache cannot go alone
	only := boardCheckOf(t, p, BoardDraft{Edits: []BoardEdit{{Addr: "cache", Base: cacheBase, Remove: true}}})
	if only.State != "invalid" || len(only.Problems) != 1 || only.Problems[0].Item != "cache" || only.Problems[0].Message != "log reads cache: remove it too, or change it to read something else." {
		t.Errorf("removing what log reads: %+v", only)
	}
	d := BoardDraft{Edits: []BoardEdit{
		{Addr: "shared", Base: mainBase, Set: map[string]BoardValue{"api_token": {Text: strp("t-1")}}},
		{Addr: "log", Base: cacheBase, Remove: true},
		{Addr: "cache", Base: cacheBase, Remove: true},
	}}
	chk := boardCheckOf(t, p, d)
	if chk.State != "valid" || len(chk.Files) != 2 || strings.Join(chk.Removes, ",") != "log,cache" {
		t.Fatalf("check: %+v", chk)
	}
	cache, main := chk.Files[0], chk.Files[1]
	if strings.Contains(cache.Text, "cache:") || strings.Contains(cache.Text, "log:") || !strings.HasPrefix(cache.Text, "package demo") || cache.Lines["cache"] != [2]int{} {
		t.Errorf("cache.cue after removing both:\n%s", cache.Text)
	}
	if !strings.Contains(main.Text, `shared: $fake.#Resource.fake_db & {name: "shared", port: services.a.db.port, api_token: "••••••••"}`) || !strings.Contains(main.Text, "// A reusable unit.") {
		t.Errorf("main.cue:\n%s", main.Text)
	}
	if len(chk.Creates) != 0 {
		t.Errorf("creates: %+v", chk.Creates)
	}
}

func TestBoardRefusesWhatThePenCannotWriteExactly(t *testing.T) {
	p := penProject(t, nil)
	base := editableOf(t, p, "cache").Base
	cases := []struct {
		name  string
		edit  BoardEdit
		field string
		says  string
	}{
		{"a typed secret", BoardEdit{Addr: "cache", Base: base, Set: map[string]BoardValue{"blob": {Text: strp("x")}}}, "blob", "sensitive"},
		{"an unknown field", BoardEdit{Addr: "cache", Base: base, Set: map[string]BoardValue{"nope": {Text: strp("x")}}}, "nope", "no field"},
		{"an old base", BoardEdit{Addr: "cache", Base: "0000", Set: map[string]BoardValue{"name": {Text: strp("x")}}}, "@base", "read it again"},
		{"a template's resource", BoardEdit{Addr: "services.a.db", Base: base, Remove: true}, "", "template"},
		{"set and unset", BoardEdit{Addr: "cache", Base: base, Set: map[string]BoardValue{"name": {Text: strp("x")}}, Unset: []string{"name"}}, "name", "not both"},
		{"a wire to itself", BoardEdit{Addr: "cache", Base: base, Set: map[string]BoardValue{"api_token": {Wire: &BoardRef{To: "cache", Read: "name"}}}}, "api_token", ""},
		{"a wire a field would hide", BoardEdit{Addr: "apps.web", Base: base, Set: map[string]BoardValue{"content": {Wire: &BoardRef{To: "shared", Read: "name"}}}}, "content", "around the resource"},
	}
	for _, c := range cases {
		chk := boardCheckOf(t, p, BoardDraft{Edits: []BoardEdit{c.edit}})
		if chk.State != "invalid" {
			t.Errorf("%s: %+v", c.name, chk)
			continue
		}
		if c.says == "" {
			continue
		}
		found := false
		for _, pr := range chk.Problems {
			if pr.Item == c.edit.Addr && pr.Field == c.field && strings.Contains(pr.Message, c.says) {
				found = true
			}
		}
		if !found {
			t.Errorf("%s: problems %+v", c.name, chk.Problems)
		}
	}
	// a drawn thing does not take the name of a resource the drawing changes
	d := ordersDraft()
	d.Items[0].Label = "cache"
	d.Edits = []BoardEdit{{Addr: "cache", Base: base, Set: map[string]BoardValue{"name": {Text: strp("x")}}}}
	if chk := boardCheckOf(t, p, d); chk.State == "valid" {
		t.Errorf("a drawn thing named like a changed resource: %+v", chk)
	}
}

func TestBoardWritesNoChangeWithoutEdit(t *testing.T) {
	p := penProject(t, nil)
	before := digestTree(t, p.dir)
	d := BoardDraft{Edits: []BoardEdit{{Addr: "cache", Base: editableOf(t, p, "cache").Base, Set: map[string]BoardValue{"name": {Text: strp("x")}}}}}
	if chk := boardCheckOf(t, p, d); chk.State != "valid" {
		t.Fatalf("check: %+v", chk)
	}
	if resp, _ := p.post("/api/board/save", d); resp.StatusCode != http.StatusForbidden {
		t.Errorf("save without --edit: %d", resp.StatusCode)
	}
	if digestTree(t, p.dir) != before {
		t.Fatal("the board changed the project directory without --edit")
	}
}
