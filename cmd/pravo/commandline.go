package main

import (
	"strings"

	"github.com/pravo-systems/pravo/internal/engine"
	"github.com/pravo-systems/pravo/internal/state"
)

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
