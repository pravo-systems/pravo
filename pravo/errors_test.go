package pravo

import (
	"errors"
	"fmt"
	"testing"

	"github.com/pravo-systems/pravo/internal/engine"
	"github.com/pravo-systems/pravo/internal/state"
)

// The code of an error is the one the command line ends with, wrapped or not.
func TestCodeOfReadsTheExitCodeOfAnError(t *testing.T) {
	cases := []struct {
		err  error
		want ExitCode
	}{
		{nil, ExitOK},
		{errors.New("a provider failed"), ExitError},
		{engine.WithCode(engine.ExitRefused, errors.New("the plan violates the policy no_destroy@1")), ExitRefused},
		{fmt.Errorf("applying: %w", engine.WithCode(engine.ExitInvalid, errors.New("does not evaluate"))), ExitInvalid},
		{&ErrStalePlan{}, ExitStale},
		{fmt.Errorf("opening: %w", state.ErrUntrusted), ExitUntrusted},
		{fmt.Errorf("opening: %w", state.ErrNotReader), ExitRefused},
		{errors.New("plan rejected: base is reserved by alice"), ExitLocked},
	}
	for _, c := range cases {
		if got := CodeOf(c.err); got != c.want {
			t.Errorf("CodeOf(%v) = %d, want %d", c.err, got, c.want)
		}
	}
}
