package pravo

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pravo-systems/pravo/internal/engine"
	"github.com/pravo-systems/pravo/internal/schema"
)

const greeting = `package estate

import "pravo.dev/pravo"

who: pravo.#Env & {name: "PRAVO_TEST_GREETEE", default: "world"}

greeting: pravo.#Output & {value: "hello \(who.value)"}
`

func specDir(t *testing.T, cue string) string {
	t.Helper()
	dir := t.TempDir()
	files := map[string]string{
		"cue.mod/module.cue": "module: \"pravo.local/test\"\nlanguage: version: \"v0.14.0\"\n",
		"main.cue":           cue,
		// The package that "pravo init" generates: #Env, #Output and the rest.
		"cue.mod/gen/pravo.dev/pravo/settings_gen.cue": schema.SettingsPackage,
	}
	for name, content := range files {
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func open(t *testing.T, dir string, e Env, mode StateMode) *Workspace {
	t.Helper()
	ws, err := Open(context.Background(), Config{Dir: dir, Env: e, Schemas: true, State: mode, Args: []string{"plan"}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(ws.Close)
	return ws
}

// A workspace plans and applies with the environment it is given, and does not
// see the environment of the process.
func TestPlanAndApplyUseTheEnvironmentOfTheConfig(t *testing.T) {
	t.Setenv("PRAVO_TEST_GREETEE", "the process")
	dir := specDir(t, greeting)
	ws := open(t, dir, EnvMap(map[string]string{"PRAVO_TEST_GREETEE": "gopher"}), StateWrite)

	planned, err := ws.Plan(context.Background(), PlanOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err := planned.CheckPolicy(context.Background()); err != nil {
		t.Fatal(err)
	}
	doc, err := json.Marshal(planned.View())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(doc), "hello gopher") || strings.Contains(string(doc), "the process") {
		t.Errorf("the plan does not show the value of the environment of the config: %s", doc)
	}
	if _, err := planned.Apply(context.Background()); err != nil {
		t.Fatal(err)
	}
	out := ws.State().Outputs()["greeting"]
	if !strings.Contains(string(out), "hello gopher") {
		t.Errorf("output = %s", out)
	}
}

// Without the variable in the environment of the config, the default is used,
// whatever the process has.
func TestAnEmptyEnvironmentGivesTheDefaults(t *testing.T) {
	t.Setenv("PRAVO_TEST_GREETEE", "the process")
	ws := open(t, specDir(t, greeting), Env{}, StateRead)
	planned, err := ws.Plan(context.Background(), PlanOptions{})
	if err != nil {
		t.Fatal(err)
	}
	doc, _ := json.Marshal(planned.View())
	if !strings.Contains(string(doc), "hello world") {
		t.Errorf("want the default: %s", doc)
	}
}

// A workspace opened for reading does not create the state.
func TestOpenForReadingCreatesNoState(t *testing.T) {
	dir := specDir(t, greeting)
	ws := open(t, dir, Env{}, StateRead)
	if ws.State() == nil {
		t.Fatal("no state")
	}
	if _, err := os.Stat(filepath.Join(dir, "pravo.state")); err == nil {
		t.Error("the state was created")
	}
}

func TestPlanNeedsTheStateAndTheSchemas(t *testing.T) {
	ws, err := Open(context.Background(), Config{Dir: specDir(t, greeting)})
	if err != nil {
		t.Fatal(err)
	}
	defer ws.Close()
	if _, err := ws.Plan(context.Background(), PlanOptions{}); err == nil || !strings.Contains(err.Error(), "needs the state") {
		t.Errorf("err = %v", err)
	}
}

func TestAnInvalidSpecificationIsRefusedWithTheCodeOfInvalid(t *testing.T) {
	_, err := Open(context.Background(), Config{Dir: specDir(t, "package estate\n\nx: 1 & 2\n")})
	var coded *engine.CodedError
	if !errors.As(err, &coded) || coded.Code != engine.ExitInvalid {
		t.Errorf("err = %v; want the code of an invalid specification", err)
	}
}
