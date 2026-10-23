package main

import (
	"strings"

	"github.com/pravo-systems/pravo/internal/engine"
	"github.com/pravo-systems/pravo/internal/state"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// runningCommand is the command that runs. The root command sets it when the
// command is known, with the flags that were given parsed.
var runningCommand *cobra.Command

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

// setCommand sets the command line of opts from the arguments of the process.
// What the history of the state and the lock files show of it depends on the
// state: a sealed state shows the values of -t and a few other flags as tags (see
// state.Store.ShowCommand). Args keeps the arguments, so that the plan files and
// the stored plans can keep the line as it was typed.
func setCommand(st *state.Store, args []string, opts *engine.Options) {
	opts.Args = args
	opts.Command = "pravo " + strings.Join(args, " ")
	if st != nil {
		opts.Command, _ = st.ShowCommand(args)
	}
}
