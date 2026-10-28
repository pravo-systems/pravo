package pravo

import (
	"errors"
	"strings"

	"github.com/pravo-systems/pravo/internal/engine"
	"github.com/pravo-systems/pravo/internal/protect"
	"github.com/pravo-systems/pravo/internal/state"
)

// ExitCode is the code a command ends with, which classifies a failure for a
// script, an agent or a program built on this package. CodeOf reads it from an
// error.
type ExitCode = engine.ExitCode

// The exit codes, as the reference lists them.
const (
	ExitOK        = engine.ExitOK
	ExitError     = engine.ExitError     // any other error
	ExitChanges   = engine.ExitChanges   // a plan with changes, when asked to say so
	ExitStale     = engine.ExitStale     // a stored or saved plan that no longer applies
	ExitLocked    = engine.ExitLocked    // a resource is locked or reserved by someone else
	ExitRefused   = engine.ExitRefused   // refused: a policy, a missing approval, a role or a readers file
	ExitInvalid   = engine.ExitInvalid   // the specification does not evaluate or validate
	ExitUntrusted = engine.ExitUntrusted // the state failed verification
)

// ErrStalePlan is the error of a plan that no longer applies: the state moved on
// under it. It says which changes conflict.
type ErrStalePlan = engine.ErrStalePlan

// CodeOf is the exit code that the command line gives for err, which is what a
// program built on this package maps its errors to instead of reading their text.
// A nil error is ExitOK.
func CodeOf(err error) ExitCode {
	if err == nil {
		return ExitOK
	}
	var coded *engine.CodedError
	if errors.As(err, &coded) {
		return coded.Code
	}
	var stale *ErrStalePlan
	if errors.As(err, &stale) {
		return ExitStale
	}
	switch {
	case errors.Is(err, state.ErrUntrusted):
		return ExitUntrusted
	case errors.Is(err, state.ErrNotReader), errors.Is(err, protect.ErrNotReader):
		return ExitRefused
	case errors.Is(err, state.ErrSealing):
		return ExitLocked
	}
	var held *state.ErrLocked
	if errors.As(err, &held) {
		return ExitLocked
	}
	if strings.Contains(err.Error(), "is locked by") || strings.Contains(err.Error(), "reserved by") {
		return ExitLocked
	}
	return ExitError
}
