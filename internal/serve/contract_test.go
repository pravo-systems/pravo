package serve

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The fixtures of the interface (web/dev/fixtures) are what the page is built
// and tested against. They must say what the server says: every field a fixture
// has is a field of the types of api.go, which win when the two differ.
func TestInterfaceFixturesMatchTheContract(t *testing.T) {
	dir := filepath.Join("web", "dev", "fixtures")
	files, err := filepath.Glob(filepath.Join(dir, "*.json"))
	if err != nil || len(files) == 0 {
		t.Skipf("no fixtures in %s", dir)
	}
	for _, f := range files {
		name := filepath.Base(f)
		var into any
		switch kind, _, _ := strings.Cut(name, "."); kind {
		case "graph":
			into = &Graph{}
		case "detail":
			into = &Detail{}
		case "source":
			into = &Source{}
		case "search":
			into = &Search{}
		case "event":
			into = &Event{}
		default:
			t.Errorf("%s: a fixture is named graph.*, detail.*, source.*, search.* or event.*", name)
			continue
		}
		data, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		dec := json.NewDecoder(bytes.NewReader(data))
		dec.DisallowUnknownFields()
		if err := dec.Decode(into); err != nil {
			t.Errorf("%s does not match the types of internal/serve/api.go (the Go types win; change the fixture, or ask for the field to be added there): %v", name, err)
		}
	}
}
