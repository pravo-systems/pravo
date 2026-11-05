package main

import (
	"context"
	"strings"

	"github.com/pravo-systems/pravo/internal/state"
	"github.com/pravo-systems/pravo/pravo"
)

// The tools that only read the state run inside the server, through the
// library, and not as a subprocess: they need no isolation and no plan, and
// the library is what a product embedding pravo would call. Their results are
// what the commands print with --json. Calls are served one at a time, so the
// tags can be set in the flag variable that loadRoot reads.

// inprocess runs the tool inside the server and returns its text and exit
// code in the form of runSelf.
func inprocess(ctx context.Context, rt *runContext, in map[string]any, run func(ctx context.Context, in map[string]any) (string, error)) (string, int) {
	saved := rt.flags.Tags
	rt.flags.Tags = listArg(in, "tags")
	defer func() { rt.flags.Tags = saved }()
	text, err := run(ctx, in)
	if err != nil {
		code := exitCodeOf(err)
		line, _ := jsonText(map[string]any{"kind": "error", "code": code, "message": err.Error()}, false)
		return strings.TrimSpace(string(line)) + "\n\nError: " + err.Error(), code
	}
	return strings.TrimSpace(text), 0
}

func readStateValues(ctx context.Context, rt *runContext) (*state.Store, error) {
	root, err := loadRoot(rt)
	if err != nil {
		return nil, err
	}
	return openStateValues(ctx, rt, root)
}

func runStateQuery(ctx context.Context, rt *runContext, in map[string]any) (string, error) {
	st, err := readStateValues(ctx, rt)
	if err != nil {
		return "", err
	}
	// An agent reads the state as the viewer shows it: sensitive values withheld.
	snap, _, err := redactedSnapshot(ctx, rt, st)
	if err != nil {
		return "", err
	}
	res, err := pravo.QuerySnapshot(snap, strArg(in, "expression"))
	if err != nil {
		return "", err
	}
	b, err := res.JSON()
	return string(b), err
}

func runDependents(ctx context.Context, rt *runContext, in map[string]any) (string, error) {
	st, err := readStateValues(ctx, rt)
	if err != nil {
		return "", err
	}
	reach, err := pravo.Dependents(st, strArg(in, "addr"))
	if err != nil {
		return "", err
	}
	return strings.Join(reach, "\n"), nil
}
