package pravo

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pravo-systems/pravo/internal/state"
	"github.com/pravo-systems/pravo/internal/state/statetest"
)

// A workspace keeps its state in the Backend of its Config, and writes nothing to
// the location that the specification names.
func TestAWorkspaceKeepsItsStateInTheBackendOfItsConfig(t *testing.T) {
	dir := specDir(t, greeting)
	b := NewMemoryBackend("test")
	applyOn := func(mode StateMode) *Workspace {
		ws, err := Open(context.Background(), Config{Dir: dir, Env: EnvMap(map[string]string{"PRAVO_TEST_GREETEE": "gopher"}), Schemas: true, State: mode, Backend: b, Args: []string{"apply"}})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(ws.Close)
		return ws
	}
	ws := applyOn(StateWrite)
	planned, err := ws.Plan(context.Background(), PlanOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := planned.Apply(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "pravo.state")); err == nil {
		t.Error("the state was also written to the location of the specification")
	}
	// A second workspace on the same backend reads what the first applied.
	again := applyOn(StateRead)
	if out := again.State().Outputs()["greeting"]; !strings.Contains(string(out), "hello gopher") {
		t.Errorf("output read back from the backend = %s", out)
	}
	if keys, _ := b.List(context.Background(), "txn"); len(keys) == 0 {
		t.Error("the backend holds no transaction of the apply")
	}
}

// A sealed state is encrypted before the backend sees it, and a backend that
// changes an object is found out: the backend is trusted with availability only.
func TestASealedStateOnACustomBackendIsEncryptedAndTamperEvident(t *testing.T) {
	dir := specDir(t, greeting)
	b := state.NewMemBackend("sealed")
	id := statetest.Identity(t, "alice")
	statetest.Seal(t, b, id).Close()

	cfg := Config{
		Dir: dir, Env: EnvMap(map[string]string{"PRAVO_TEST_GREETEE": "secret-marker"}), Schemas: true,
		State: StateWrite, Backend: b, Args: []string{"apply"},
		Identity: func([]ReaderPublic) (*Identity, error) { return id, nil },
	}
	ws, err := Open(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	planned, err := ws.Plan(context.Background(), PlanOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := planned.Apply(context.Background()); err != nil {
		t.Fatal(err)
	}
	ws.Close()

	// Nothing the backend holds is readable.
	ctx := context.Background()
	if len(b.Keys()) == 0 {
		t.Fatal("the backend holds nothing")
	}
	for _, k := range b.Keys() {
		data, _ := b.Get(ctx, k)
		if bytes.Contains(data, []byte("secret-marker")) {
			t.Fatalf("%s holds the value in plain text: %.80q", k, data)
		}
	}

	// A backend that flips a byte in the outputs is found out.
	data, err := b.Get(ctx, "outputs.json")
	if err != nil {
		t.Fatal(err)
	}
	data[len(data)-5] ^= 0x01
	_ = b.Put(ctx, "outputs.json", data)
	ws, err = Open(context.Background(), cfg)
	if err == nil {
		defer ws.Close()
		_, err = ws.Plan(context.Background(), PlanOptions{})
	}
	if err == nil {
		t.Error("a state with a changed object was accepted")
	}
	t.Logf("refused with: %v", err)
}
