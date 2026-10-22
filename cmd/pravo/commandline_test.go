package main

import (
	"testing"

	"github.com/pravo-systems/pravo/internal/engine"
	"github.com/pravo-systems/pravo/internal/state"
)

func TestSetCommandKeepsTheLineAsTypedInAnUnsealedState(t *testing.T) {
	st, err := state.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	args := []string{"apply", "-t", "env=prod", "--auto-approve", "plan-2f9c1a4e"}
	for name, st := range map[string]*state.Store{"a state": st, "no state": nil} {
		var opts engine.Options
		setCommand(st, args, &opts)
		if want := "pravo apply -t env=prod --auto-approve plan-2f9c1a4e"; opts.Command != want {
			t.Errorf("%s: command %q, want %q", name, opts.Command, want)
		}
		if len(opts.Args) != len(args) || opts.Args[2] != "env=prod" {
			t.Errorf("%s: args %q", name, opts.Args)
		}
	}
}
