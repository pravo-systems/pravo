package main

import "github.com/pravo-systems/pravo/internal/state"

// The state records the version of the pravo that writes each transaction, which
// `pravo protect check` reads to say whether every writer is recent enough.
func init() { state.PravoVersion = Version }
