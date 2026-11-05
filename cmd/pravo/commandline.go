package main

import (
	"context"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/pravo-systems/pravo/internal/state"
)

// runningCommand is the command that runs. The root command sets it when the
// command is known, with the flags that were given parsed.
var runningCommand *cobra.Command

// commandContext is the context of the command that runs, which carries the
// interrupt of the process: a Ctrl-C reaches what a command does with it. Outside
// a command (a test that calls a loader directly) it is a background context.
func commandContext() context.Context {
	if runningCommand != nil && runningCommand.Context() != nil {
		return runningCommand.Context()
	}
	return context.Background()
}

// The state seals the value of a flag that takes one, in the form --flag value as in
// --flag=value, and it cannot tell such a flag from a boolean one. The command tree
// can, for the command that runs.
func init() { state.LookupFlag = commandFlag }

// commandFlag says what the flag called name is for the command that runs: a long
// name, or the letter of a shorthand. A flag takes the next argument as its value
// unless it can be given without one, as a boolean flag can.
func commandFlag(name string, short bool) (state.FlagKind, bool) {
	if runningCommand == nil {
		return state.FlagKind{}, false
	}
	fs := runningCommand.Flags()
	var f *pflag.Flag
	if short {
		if len(name) != 1 {
			return state.FlagKind{}, false
		}
		f = fs.ShorthandLookup(name)
	} else {
		f = fs.Lookup(name)
	}
	if f == nil {
		return state.FlagKind{}, false
	}
	return state.FlagKind{Long: f.Name, TakesValue: f.NoOptDefVal == ""}, true
}
