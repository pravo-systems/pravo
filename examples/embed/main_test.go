package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pravo-systems/pravo/internal/schema"
)

// The program plans and applies a specification through the library alone.
func TestEmbedPlansAndApplies(t *testing.T) {
	dir := t.TempDir()
	for name, content := range map[string]string{
		"cue.mod/module.cue": "module: \"pravo.local/test\"\nlanguage: version: \"v0.14.0\"\n",
		"main.cue":           "package estate\n\nimport \"pravo.dev/pravo\"\n\ngreeting: pravo.#Output & {value: \"hello embed\"}\n",
		"cue.mod/gen/pravo.dev/pravo/settings_gen.cue": schema.SettingsPackage,
	} {
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	var out bytes.Buffer
	if err := run(context.Background(), &out, dir, true); err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(out.Bytes(), &doc); err != nil {
		t.Fatalf("the plan is not JSON: %v\n%s", err, out.String())
	}
	if !strings.Contains(out.String(), "hello embed") {
		t.Errorf("the plan does not show the output:\n%s", out.String())
	}
	if _, err := os.Stat(filepath.Join(dir, "pravo.state")); err != nil {
		t.Errorf("the state was not written: %v", err)
	}
}
