package main

import (
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
