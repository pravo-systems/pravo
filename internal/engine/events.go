package engine

import (
	"fmt"
	"time"
)

// Event is one structured progress notification. With -json, the CLI
// streams events as newline-delimited JSON so an orchestrator can follow a
// run without parsing text.
type Event struct {
	Time    time.Time `json:"time"`
	Kind    string    `json:"kind"`           // refresh, read, open, call, create, update, replace, destroy, lock, note, plan, apply
	Addr    string    `json:"addr,omitempty"` // resource address, when the event concerns one
	Phase   string    `json:"phase"`          // start, done, skip, error
	Message string    `json:"message,omitempty"`
	ID      string    `json:"id,omitempty"` // provider id of the resource, when known
	Elapsed int64     `json:"elapsed_ms,omitempty"`
	Txn     string    `json:"txn,omitempty"`
	TraceID string    `json:"trace_id,omitempty"`
}

// emit sends an event to the configured sink, if any.
func (e *Engine) emit(ev Event) {
	if e.Opts.Event == nil {
		return
	}
	if ev.Time.IsZero() {
		ev.Time = time.Now().UTC()
	}
	ev.Txn = e.txnID()
	ev.TraceID = e.traceID
	e.Opts.Event(ev)
}

// step reports progress both as a human line and as an event.
func (e *Engine) step(kind, addr, phase, id string, start time.Time, format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	e.Opts.Progress("%s", msg)
	ev := Event{Kind: kind, Addr: addr, Phase: phase, Message: msg}
	if id != "" {
		ev.ID = id
	}
	if !start.IsZero() {
		ev.Elapsed = time.Since(start).Milliseconds()
	}
	e.emit(ev)
}

// ExitCode classifies failures for scripts and agents.
type ExitCode int

const (
	ExitOK      ExitCode = 0
	ExitError   ExitCode = 1 // any other error
	ExitChanges ExitCode = 2 // plan: changes pending (with -detailed-exitcode)
	ExitStale   ExitCode = 3 // a saved plan no longer applies
	ExitLocked  ExitCode = 4 // rejected because a resource is locked or reserved
	ExitRefused ExitCode = 5 // refused by a plan policy, or by a module that does not match its lock
	ExitInvalid ExitCode = 6 // the specification does not evaluate or validate
)

// CodedError carries an exit code with an error.
type CodedError struct {
	Code ExitCode
	Err  error
}

func (c *CodedError) Error() string { return c.Err.Error() }
func (c *CodedError) Unwrap() error { return c.Err }

// WithCode wraps err with an exit code.
func WithCode(code ExitCode, err error) error {
	if err == nil {
		return nil
	}
	return &CodedError{Code: code, Err: err}
}
