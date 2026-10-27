package pravo

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
)

// Several roots plan and apply in one process at the same time, each with its own
// environment, and none sees the state or the environment of another. Run with
// -race.
func TestRootsRunInParallelInOneProcess(t *testing.T) {
	const roots = 6
	var wg sync.WaitGroup
	errs := make([]error, roots)
	outs := make([]string, roots)
	for i := range roots {
		dir := specDir(t, greeting)
		wg.Add(1)
		go func() {
			defer wg.Done()
			ctx := context.Background()
			ws, err := Open(ctx, Config{Dir: dir, Schemas: true, State: StateWrite, Args: []string{"apply"},
				Env: EnvMap(map[string]string{"PRAVO_TEST_GREETEE": fmt.Sprintf("root%d", i)})})
			if err != nil {
				errs[i] = err
				return
			}
			defer ws.Close()
			planned, err := ws.Plan(ctx, PlanOptions{})
			if err != nil {
				errs[i] = err
				return
			}
			if _, err := planned.Apply(ctx); err != nil {
				errs[i] = err
				return
			}
			outs[i] = string(ws.State().Outputs()["greeting"])
		}()
	}
	wg.Wait()
	for i := range roots {
		if errs[i] != nil {
			t.Errorf("root %d: %v", i, errs[i])
			continue
		}
		if want := fmt.Sprintf("hello root%d", i); !strings.Contains(outs[i], want) {
			t.Errorf("root %d: output %s, want %s", i, outs[i], want)
		}
	}
}

// A workspace is opened from a relative directory without the process changing
// its working directory.
func TestARelativeDirectoryIsResolvedAgainstTheProcessDirectoryOnce(t *testing.T) {
	dir := specDir(t, greeting)
	t.Chdir(dir)
	ws, err := Open(context.Background(), Config{Schemas: true, State: StateRead})
	if err != nil {
		t.Fatal(err)
	}
	defer ws.Close()
	if !strings.HasPrefix(ws.Root().Dir, "/") && !strings.Contains(ws.Root().Dir, ":") {
		t.Errorf("root dir = %q: not absolute", ws.Root().Dir)
	}
	t.Chdir(t.TempDir())
	if _, err := ws.Plan(context.Background(), PlanOptions{}); err != nil {
		t.Errorf("a plan after the process changed directory: %v", err)
	}
}
