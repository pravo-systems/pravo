package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pravo-systems/pravo/internal/schema"
)

// The tools that read the state answer from inside the server, as the commands
// do, and refuse the way they do.
func TestInprocessStateTools(t *testing.T) {
	dir := t.TempDir()
	for name, content := range map[string]string{
		"cue.mod/module.cue": "module: \"pravo.local/test\"\nlanguage: version: \"v0.14.0\"\n",
		"main.cue":           "package estate\n\nimport \"pravo.dev/pravo\"\n\ngreeting: pravo.#Output & {value: \"hello\"}\n",
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
	t.Chdir(dir)
	root := newRootCmd()
	root.SetArgs(normaliseArgs([]string{"pravo", "apply", "-auto-approve", "--no-color"})[1:])
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}

	byName := map[string]mcpTool{}
	for _, tool := range mcpTools() {
		byName[tool.Name] = tool
	}
	for _, name := range []string{"pravo_state_query", "pravo_dependents"} {
		if byName[name].run == nil {
			t.Errorf("%s does not run in the server", name)
		}
	}
	ctx := context.Background()
	text, code := inprocess(ctx, map[string]any{"expression": "outputs.greeting"}, byName["pravo_state_query"].run)
	if code != 0 || !strings.Contains(text, "hello") {
		t.Errorf("state query: %d %q", code, text)
	}
	text, code = inprocess(ctx, map[string]any{"expression": "outputs.greeting +"}, byName["pravo_state_query"].run)
	if code == 0 || !strings.Contains(text, `"kind":"error"`) {
		t.Errorf("a bad expression: %d %q", code, text)
	}
	text, code = inprocess(ctx, map[string]any{"addr": "nothing.here"}, byName["pravo_dependents"].run)
	if code == 0 {
		t.Errorf("an unknown address was accepted: %q", text)
	}
	if flagTags != nil {
		t.Errorf("the tags of a call were left set: %v", flagTags)
	}
}
