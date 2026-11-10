package ui

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

// With Errors set the error goes there and nothing goes to Out.
func TestErrorGoesToTheErrorWriter(t *testing.T) {
	var out, errs bytes.Buffer
	p := NewPrinter(&out, true)
	p.Errors = &errs
	p.Error(errors.New("it broke"))
	if out.Len() != 0 || !strings.Contains(errs.String(), "Error: it broke") {
		t.Errorf("out %q, errors %q", out.String(), errs.String())
	}
	var only bytes.Buffer
	q := NewPrinter(&only, true)
	q.Error(errors.New("it broke"))
	if !strings.Contains(only.String(), "Error: it broke") {
		t.Errorf("without Errors the error is lost: %q", only.String())
	}
}
