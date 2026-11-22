package serve

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/pravo-systems/pravo/internal/schema"
	"github.com/pravo-systems/pravo/internal/state"
)

// kindsProject is the test project with a data source in the provider struct, and the package pravo as pravo init
// writes it.
func kindsProject(t *testing.T) *project {
	t.Helper()
	dir := t.TempDir()
	data := "\t#Data: fake_zones: {\n\t\t$kind:   \"data\"\n\t\t$type:   \"fake_zones\"\n\t\t$source: \"example.test/x/fake\"\n\t\t...\n\t}\n\t#Resource: {"
	writeSpec(t, dir, strings.Replace(specCUE, "\t#Resource: {", data, 1))
	pkg := filepath.Join(dir, "cue.mod", "gen", "pravo.dev", "pravo", "settings_gen.cue")
	if err := os.MkdirAll(filepath.Dir(pkg), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(pkg, []byte(schema.SettingsPackage), 0o644); err != nil {
		t.Fatal(err)
	}
	st, err := state.Open(filepath.Join(dir, "pravo.state"))
	if err != nil {
		t.Fatal(err)
	}
	seed(t, st)
	return start(t, dir, st, Options{Board: &BoardOptions{Actor: "ada"}})
}

func firstHit(t *testing.T, p *project, q string) BoardTypeHit {
	t.Helper()
	var types BoardTypes
	if err := json.Unmarshal(p.get("/api/board/types?q="+q), &types); err != nil {
		t.Fatal(err)
	}
	if len(types.Hits) == 0 {
		t.Fatalf("%s: no hit", q)
	}
	return types.Hits[0]
}

// Every kind is drawn: a data source through #Data of its provider, an output and an environment variable from the
// package pravo, written in one new file with the import it needs. What each reads draws its edges.
func TestBoardDrawsEveryKind(t *testing.T) {
	p := kindsProject(t)
	for q, want := range map[string]string{"data+zones": "data fake_zones", "output": "output output", "env": "env env", "secret+version": "secret_version secret_version"} {
		if h := firstHit(t, p, q); h.Kind+" "+h.Type != want {
			t.Errorf("%s: %+v, want %s", q, h, want)
		}
	}
	var form BoardType
	if err := json.Unmarshal(p.get("/api/board/type?kind=data&type=fake_zones&provider=$fake"), &form); err != nil {
		t.Fatal(err)
	}
	if form.Kind != "data" || len(form.Fields) != 1 || form.Fields[0].Name != "state" {
		t.Fatalf("form of a data source: %+v", form)
	}
	if err := json.Unmarshal(p.get("/api/board/type?kind=env"), &form); err != nil {
		t.Fatal(err)
	}
	if form.Kind != "env" || len(form.Fields) != 2 || !form.Fields[0].Required || !slices.Equal(form.Reads, []string{"value"}) {
		t.Fatalf("form of an environment variable: %+v", form)
	}

	d := BoardDraft{Items: []BoardItem{
		{Label: "zones", Kind: "data", Type: "fake_zones", Values: map[string]BoardValue{"state": {Text: strp("up")}}},
		{Label: "region", Kind: "env", Values: map[string]BoardValue{"name": {Text: strp("DEMO_REGION")}, "default": {Text: strp("eu")}}},
		{Label: "where", Kind: "output", Values: map[string]BoardValue{"value": {Expr: `"\(region.value) \(zones.state)"`}, "sensitive": {Expr: "false"}}},
	}}
	chk := boardCheckOf(t, p, d)
	if chk.State != "valid" {
		t.Fatalf("check: %s %+v\n%s", chk.State, chk.Problems, chk.Code)
	}
	for _, want := range []string{"import \"pravo.dev/pravo\"\n", "zones: $fake.#Data.fake_zones & {\n\tstate: \"up\"\n}", "region: pravo.#Env & {\n\tname:    \"DEMO_REGION\"\n\tdefault: \"eu\"\n}", "where: pravo.#Output & {"} {
		if !strings.Contains(chk.Code, want) {
			t.Errorf("code without %q:\n%s", want, chk.Code)
		}
	}
	if got := strings.Join(chk.Reads["where"], " "); got != "region zones" {
		t.Errorf("the output reads %q", got)
	}

	// a data source does not adopt, and a kind that is not one is said so
	bad := BoardDraft{Items: []BoardItem{{Label: "x", Kind: "data", Type: "fake_zones", Adopt: &BoardAdopt{Discovery: "d", Key: "k"}}, {Label: "y", Kind: "policy"}}}
	if chk := boardCheckOf(t, p, bad); chk.State != "invalid" || len(chk.Problems) != 2 {
		t.Fatalf("check of what cannot be drawn: %s %+v", chk.State, chk.Problems)
	}

	if resp, body := p.post("/api/board/save", d); resp.StatusCode != http.StatusOK {
		t.Fatalf("save: %d %s", resp.StatusCode, body)
	}
	waitFor(t, "the saved file", func() bool { g := p.graph(); return hasNode(g, "zones") && hasNode(g, "where") })
	for addr, kind := range map[string]string{"zones": "data", "region": "env", "where": "output", "host": "output"} {
		if e := editableOf(t, p, addr); e.Kind != kind || e.Reason != "" || e.Form == nil {
			t.Errorf("%s: %+v", addr, e)
		}
	}
}

// An output, a data source and an environment variable of the specification are changed and deleted where they are
// written, as a resource is; an import that only the deleted one used goes with it.
func TestBoardEditsEveryKind(t *testing.T) {
	p := kindsProject(t)
	extra := "package demo\n\nimport \"pravo.dev/pravo\"\n\nzones: $fake.#Data.fake_zones & {state: \"up\"}\n\nregion: pravo.#Env & {name: \"DEMO_REGION\", default: \"eu\"}\n"
	if err := os.WriteFile(filepath.Join(p.dir, "extra.cue"), []byte(extra), 0o644); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "extra.cue", func() bool { return hasNode(p.graph(), "zones") })
	host, zones, region := editableOf(t, p, "host"), editableOf(t, p, "zones"), editableOf(t, p, "region")
	if v := host.Values["value"]; v.Text == nil || *v.Text != "db.example" {
		t.Fatalf("the value of host: %+v", host)
	}
	d := BoardDraft{Edits: []BoardEdit{
		{Addr: "host", Base: host.Base, Set: map[string]BoardValue{"description": {Text: strp("The database host")}}},
		{Addr: "zones", Base: zones.Base, Set: map[string]BoardValue{"state": {Text: strp("down")}}},
		{Addr: "region", Base: region.Base, Remove: true},
	}}
	chk := boardCheckOf(t, p, d)
	if chk.State != "valid" || len(chk.Files) != 2 || !slices.Contains(chk.Removes, "region") {
		t.Fatalf("check: %s %+v %+v", chk.State, chk.Problems, chk.Files)
	}
	text := map[string]string{}
	for _, f := range chk.Files {
		text[f.File] = f.Text
	}
	if !strings.Contains(text["main.cue"], `description: "The database host"`) || !strings.Contains(text["extra.cue"], `state: "down"`) || strings.Contains(text["extra.cue"], "region") ||
		strings.Contains(text["extra.cue"], "pravo.dev/pravo") {
		t.Fatalf("files: %+v", text)
	}
}
