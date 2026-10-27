package serve

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"cuelang.org/go/cue/parser"
)

func linesOf(b SourceBlock) string {
	var sb strings.Builder
	for _, l := range b.Lines {
		sb.WriteString(l.Text)
		sb.WriteByte('\n')
	}
	return sb.String()
}

func TestSourceOfAResourceFromADefinition(t *testing.T) {
	p := newProject(t)
	raw := p.get("/api/source?addr=services.a.db")
	noSecrets(t, "/api/source", raw)
	s := p.source("services.a.db")
	if s.Addr != "services.a.db" || len(s.Blocks) != 2 {
		t.Fatalf("source %+v", s)
	}
	def, inst := s.Blocks[0], s.Blocks[1]
	if def.Role != "definition" || def.File != "main.cue" || def.Kind != "file" || !strings.Contains(def.Label, "#Service") {
		t.Errorf("definition block %+v", def)
	}
	if def.StartLine != def.Lines[0].No || def.EndLine != def.Lines[len(def.Lines)-1].No || def.Modified == nil {
		t.Errorf("definition lines %d-%d of %d", def.StartLine, def.EndLine, len(def.Lines))
	}
	text := linesOf(def)
	if !strings.Contains(text, `blob:    "`+mask+`"`) || strings.Contains(text, secretBlob) {
		t.Errorf("the literal of a sensitive attribute is not masked:\n%s", text)
	}
	if !strings.Contains(text, "db: $fake.#Resource.fake_db") {
		t.Errorf("the definition does not show its own header:\n%s", text)
	}
	for _, l := range def.Lines {
		sensitive := strings.Contains(l.Text, "blob:")
		if (l.Mark == "sensitive") != sensitive {
			t.Errorf("line %d %q is marked %q", l.No, l.Text, l.Mark)
		}
		if !sensitive && l.Mark != "focus" {
			t.Errorf("line %d is marked %q, want focus", l.No, l.Mark)
		}
	}
	if inst.Role != "instance" || inst.Label != "services.a of #Service" || !strings.Contains(linesOf(inst), `a: #Service & {name: "a"}`) {
		t.Errorf("instance block %+v", inst)
	}
	if s.Generated != 2 {
		t.Errorf("generated %d, want 2 (services.a.db and services.b.db)", s.Generated)
	}
	kinds := map[string]string{}
	for _, c := range s.Contributions {
		kinds[c.Kind+" "+c.Where] = c.Note
	}
	if len(s.Contributions) < 3 || kinds["file pravo.lock.cue"] == "" {
		t.Errorf("contributions %+v", s.Contributions)
	}
	if shared := p.source("shared"); shared.Generated != 1 || len(shared.Blocks) != 1 {
		t.Errorf("shared: generated %d blocks %d", shared.Generated, len(shared.Blocks))
	}
}

func TestSourceOfAComprehension(t *testing.T) {
	p := newProject(t)
	s := p.source("hosts.x")
	if len(s.Blocks) != 1 || s.Generated != 2 {
		t.Fatalf("source %+v", s)
	}
	b := s.Blocks[0]
	var dim, focus int
	for _, l := range b.Lines {
		if l.Dim {
			dim++
		}
		if l.Mark == "focus" {
			focus++
		}
	}
	if !strings.Contains(b.Lines[0].Text, "for n in") || dim != 2 || focus != 1 {
		t.Errorf("the comprehension is shown as:\n%s(dim %d, focus %d)", linesOf(b), dim, focus)
	}
}

func TestSourceOfAnOrphanAndOfBadAddresses(t *testing.T) {
	p := newProject(t)
	s := p.source("old.thing")
	if len(s.Blocks) != 0 || len(s.Notes) == 0 {
		t.Errorf("the source of a record the specification does not declare: %+v", s)
	}
	os.MkdirAll(filepath.Join(p.dir, "sub"), 0o755)
	os.WriteFile(filepath.Join(p.dir, "sub", "notes.cue"), []byte("package other\n\nTOPSECRETFILE: 1\n"), 0o644)
	for _, addr := range []string{"main.cue", "sub/notes.cue", "../../etc/passwd", "/etc/passwd", "..", "services.a.db/../../main.cue", "services.a.db\x00", "services.a", "pravo.lock.cue", "C:\\Windows\\win.ini"} {
		resp, body := p.do("GET", "/api/source?addr="+queryEscape(addr), nil)
		if resp.StatusCode != http.StatusNotFound && resp.StatusCode != http.StatusBadRequest {
			t.Errorf("addr %q: %d", addr, resp.StatusCode)
		}
		if strings.Contains(string(body), "package demo") || strings.Contains(string(body), "TOPSECRETFILE") || strings.Contains(string(body), "root:") {
			t.Errorf("addr %q returned a file: %s", addr, body)
		}
	}
	resp, _ := p.do("GET", "/api/source?addr="+strings.Repeat("a", 3000), nil)
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("a very long address: %d", resp.StatusCode)
	}
}

// Only files that the evaluation loaded are read.
func TestOnlyLoadedFilesAreRead(t *testing.T) {
	p := newProject(t)
	m, err := p.srv.current()
	if err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "outside.cue")
	os.WriteFile(outside, []byte("package demo\n\nx: 1\n"), 0o644)
	ss := m.spec
	ss.abs["outside.cue"] = outside
	r := &blockReader{ss: ss}
	if pf := r.load("outside.cue"); pf.err == nil || pf.data != nil {
		t.Errorf("a file that was not loaded was read: %+v", pf)
	}
	if pf := r.load("nothing.cue"); pf.err == nil {
		t.Errorf("a name that is not a display path was read")
	}
	if pf := r.load("main.cue"); pf.err != nil || pf.file == nil {
		t.Errorf("a loaded file was not read: %v", pf.err)
	}
}

func TestMask(t *testing.T) {
	for name, c := range map[string]struct {
		text   string
		masked []string // lines (1-based within the text) that are masked
		gone   []string
		keep   []string
	}{
		"literal":       {"f: {\n\tblob: \"abc-literal\"\n\tregion: \"eu\"\n}\n", []string{"2"}, []string{"abc-literal"}, []string{`region: "eu"`}},
		"name":          {"f: {\n\tapi_token: \"abc-token\"\n\tname: \"n\"\n}\n", []string{"2"}, []string{"abc-token"}, []string{`name: "n"`}},
		"interpolation": {"f: {\n\tblob: \"pre-\\(name)-post\"\n}\n", []string{"2"}, []string{"pre-", "-post"}, nil},
		"multiline":     {"f: {\n\tblob: \"\"\"\n\t\tline one\n\t\tline two\n\t\t\"\"\"\n\tafter: 1\n}\n", []string{"2", "3", "4", "5"}, []string{"line one", "line two"}, []string{"after: 1"}},
		"list":          {"f: {\n\tblob: [\"p1\", \"p2\"]\n}\n", []string{"2"}, []string{"p1", "p2"}, nil},
		"derived":       {"f: {\n\tcontent: \"x " + secretBlob + " y\"\n}\n", []string{"2"}, []string{secretBlob}, nil},
		"crlf":          {"f: {\r\n\tblob: \"abc-literal\"\r\n\tregion: \"eu\"\r\n}\r\n", []string{"2"}, []string{"abc-literal"}, []string{`region: "eu"`}},
	} {
		data := []byte(c.text)
		file, err := parser.ParseFile("t.cue", data, parser.ParseComments)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		field, _ := findField(file, &Ref{Line: 1, Col: 1})
		if field == nil {
			t.Fatalf("%s: no field", name)
		}
		secrets := newSecretSet()
		secrets.add(secretBlob)
		r := &blockReader{sens: map[string]bool{"blob": true}, secrets: secrets}
		text := strings.ReplaceAll(c.text, "\r\n", "\n")
		out, touched := r.mask(&parsedFile{data: data, file: file}, field, text)
		if strings.Count(out, "\n") != strings.Count(text, "\n") {
			t.Errorf("%s: the masked text has %d lines, the text %d", name, strings.Count(out, "\n"), strings.Count(text, "\n"))
		}
		for _, g := range c.gone {
			if strings.Contains(out, g) {
				t.Errorf("%s: %q is still in\n%s", name, g, out)
			}
		}
		for _, k := range c.keep {
			if !strings.Contains(out, k) {
				t.Errorf("%s: %q was lost in\n%s", name, k, out)
			}
		}
		var got []string
		for l := range touched {
			got = append(got, strconvItoa(l))
		}
		for _, w := range c.masked {
			if !touched[atoiOr0(w)] {
				t.Errorf("%s: line %s is not marked (%v)", name, w, got)
			}
		}
		if len(touched) != len(c.masked) {
			t.Errorf("%s: lines %v are marked, want %v", name, got, c.masked)
		}
	}
}

func TestTagContributions(t *testing.T) {
	dir := t.TempDir()
	writeSpec(t, dir, specCUE+`
env:       *"dev" | string @tag(env)
api_token: *"none" | string @tag(api_token)
tagged:    $fake.#Resource.fake_db & {name: "t-\(env)", api_token: api_token}
plain:     $fake.#Resource.fake_db & {name: "plain"}
`)
	p := start(t, dir, nil, Options{Tags: []string{"env=prod", "api_token=zzz-tag-secret"}})
	s := p.source("tagged")
	var tags []string
	for _, c := range s.Contributions {
		if c.Kind == "tag" {
			tags = append(tags, c.Where)
		}
	}
	if len(tags) != 2 || tags[0] != "-t api_token=(withheld)" || tags[1] != "-t env=prod" {
		t.Errorf("tags of tagged: %v", tags)
	}
	for _, c := range p.source("plain").Contributions {
		if c.Kind == "tag" {
			t.Errorf("the tag %s is listed for a node that does not name it", c.Where)
		}
	}
	noSecrets(t, "source", p.get("/api/source?addr=tagged"), "zzz-tag-secret")
	noSecrets(t, "graph", p.get("/api/graph"), "zzz-tag-secret")
}
