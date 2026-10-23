package statetest

import (
	"fmt"
	"os"
	"strconv"

	"github.com/pravo-systems/pravo/internal/protect"
	"github.com/pravo-systems/pravo/internal/state"
)

// FormatEnv is the environment variable that makes the tests that import this
// package create their stores in another format. PRAVO_TEST_FORMAT=2 runs a
// suite over stores of immutable records, and 3 over stores that are sealed to
// one identity; it is for checking that the layout changes nothing a caller can see. A test that asserts the layout itself
// (how many objects a write makes, the keys) skips itself under it.
const FormatEnv = "PRAVO_TEST_FORMAT"

func init() {
	v := os.Getenv(FormatEnv)
	if v == "" {
		return
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < state.Format || n > state.FormatSealed {
		panic(fmt.Sprintf("%s=%q: want %d, %d or %d", FormatEnv, v, state.Format, state.FormatImmutable, state.FormatSealed))
	}
	state.NewStoreFormat = n
	if n == state.FormatSealed {
		// Every store is sealed to one identity that every open uses.
		id, err := protect.NewIdentity("test", "")
		if err != nil {
			panic(err)
		}
		state.NewStoreIdentity = id
	}
}

// LayoutIsDefault reports whether new stores are created in the default format.
// A test that depends on the layout of format 1 calls it and skips if it is false.
func LayoutIsDefault() bool { return state.NewStoreFormat == state.Format }
