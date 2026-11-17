package serve

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pravo-systems/pravo/internal/schema"
)

// A fold lifts resources into a template written in a new file, with an instance that takes their records with $from:
// what they read of other resources becomes an input that the instance gives, a value asked for becomes an input with
// it as the default, and what reads them reads them in the instance.
func TestBoardFoldsResourcesIntoATemplate(t *testing.T) {
	p := penProject(t, &BoardOptions{Actor: "ada"})
	pkg := filepath.Join(p.dir, "cue.mod", "gen", "pravo.dev", "pravo", "settings_gen.cue")
	if err := os.MkdirAll(filepath.Dir(pkg), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(pkg, []byte(schema.SettingsPackage), 0o644); err != nil {
		t.Fatal(err)
	}
	reader := "package demo\n\nreader: $fake.#Resource.fake_file & {\n\tpath:    \"/r\"\n\tcontent: log.path\n}\n"
	if err := os.WriteFile(filepath.Join(p.dir, "reader.cue"), []byte(reader), 0o644); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "reader.cue", func() bool { return hasNode(p.graph(), "reader") })
	d := BoardDraft{Fold: &BoardFold{Name: "store", Addrs: []string{"cache", "log", "shared"}, Ask: []string{"cache.port"}}}
	chk := boardCheckOf(t, p, d)
	if chk.State != "valid" || chk.Fold == nil {
		t.Fatalf("check: %+v", chk)
	}
	f := chk.Fold
	if f.Def != "#Store" || f.Label != "store" || f.File != "store.cue" || f.Moves["shared"] != "store.shared" || len(f.Inputs) != 2 ||
		f.Inputs[0] != (BoardFoldInput{Name: "services_a_db_port", From: "services.a.db.port"}) || f.Inputs[1] != (BoardFoldInput{Name: "cache_port", From: "cache.port", Asked: true}) {
		t.Fatalf("fold: %+v", f)
	}
	if strings.Join(f.Fields["cache"], ",") != "name,port,blob" {
		t.Errorf("the fields that can be asked for: %v", f.Fields)
	}
	files := map[string]BoardFileChange{}
	for _, fc := range chk.Files {
		files[fc.File] = fc
	}
	tpl := files["store.cue"]
	if !tpl.New || tpl.Base != "" || len(chk.Files) != 4 {
		t.Fatalf("files: %+v", chk.Files)
	}
	for _, want := range []string{"import \"pravo.dev/pravo\"", "#Store: pravo.#Template & {", "\t\tname:  \"store\"", "\tservices_a_db_port: number", "\tcache_port: *6379 | int",
		"\t// The cache of the shop.", "\t\tport: cache_port", "\t\tcontent: cache.name", "port: services_a_db_port", "store: #Store & {\n\tservices_a_db_port: services.a.db.port\n\tcache: $from:  \"cache\"",
		`blob: "••••••••"`} {
		if !strings.Contains(tpl.Text, want) {
			t.Errorf("store.cue has no %q:\n%s", want, tpl.Text)
			break
		}
	}
	if strings.Contains(files["cache.cue"].Text, "cache:") || strings.Contains(files["main.cue"].Text, "shared:") || !strings.Contains(files["reader.cue"].Text, "content: store.log.path") {
		t.Errorf("the files the resources leave and the one that reads them:\n%s\n%s\n%s", files["cache.cue"].Text, files["main.cue"].Text, files["reader.cue"].Text)
	}
	if len(chk.Creates) != 0 {
		t.Errorf("a fold creates nothing: %+v", chk.Creates)
	}

	// a save writes only what the page was shown
	if resp, body := p.post("/api/board/save", d); resp.StatusCode != http.StatusConflict || !strings.Contains(string(body), "not as the board showed it") {
		t.Fatalf("a save of a fold that was not shown: %d %s", resp.StatusCode, body)
	}
	d.Seen = map[string]string{}
	for _, fc := range chk.Files {
		d.Seen[fc.File] = fc.Base
	}
	resp, body := p.post("/api/board/save", d)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("save: %d %s", resp.StatusCode, body)
	}
	got, _ := os.ReadFile(filepath.Join(p.dir, "store.cue"))
	if string(got) != strings.Replace(tpl.Text, `"••••••••"`, `"s3cret"`, 1) {
		t.Errorf("store.cue as written:\n%s", got)
	}
	waitFor(t, "the fold", func() bool { return hasNode(p.graph(), "store.cache") })
	if g := p.graph(); !hasNode(g, "store.shared") || hasNode(g, "cache") && hasDeclared(g, "cache") {
		t.Errorf("the graph after the fold: %+v", g.Nodes)
	}
}

func hasNode(g Graph, addr string) bool {
	for _, n := range g.Nodes {
		if n.Addr == addr {
			return true
		}
	}
	return false
}

// hasDeclared says whether the specification declares a node of the graph (and not only the state records it).
func hasDeclared(g Graph, addr string) bool {
	for _, n := range g.Nodes {
		if n.Addr == addr && n.Presence != "state" {
			return true
		}
	}
	return false
}

// What the pen cannot lift as it is written is not folded: a resource inside a struct, a name that is taken, a value
// that is not typed as it is.
func TestBoardRefusesAFoldItCannotWriteExactly(t *testing.T) {
	p := penProject(t, &BoardOptions{Actor: "ada"})
	for _, c := range []struct {
		fold BoardFold
		want string
	}{
		{BoardFold{Name: "web", Addrs: []string{"apps.web"}}, "Only a resource written at the top of a file is folded"},
		{BoardFold{Name: "shared", Addrs: []string{"cache"}}, "shared is a name of the specification already."},
		{BoardFold{Name: "store", Addrs: []string{"cache"}, Ask: []string{"cache.zones"}}, "Only a value typed as it is"},
		{BoardFold{Name: "Store", Addrs: []string{"cache"}}, "Name the template with small letters"},
		{BoardFold{Name: "store", Addrs: []string{"services.a.db"}}, "not written as a field of its own"},
	} {
		chk := boardCheckOf(t, p, BoardDraft{Fold: &c.fold})
		raw, _ := json.Marshal(chk.Problems)
		if chk.State != "invalid" || !strings.Contains(string(raw), c.want) {
			t.Errorf("%+v: %s %s", c.fold, chk.State, raw)
		}
	}
}
