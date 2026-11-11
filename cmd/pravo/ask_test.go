package main

import (
	"io"
	"os"
	"testing"

	"github.com/pravo-systems/pravo/internal/ui"
)

// Two questions in one run, both answered from one piped input: the second must read its
// own line, not the end of the input.
func TestTwoQuestionsReadTwoLinesOfOnePipe(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	old := os.Stdin
	os.Stdin = r
	t.Cleanup(func() { os.Stdin = old; r.Close() })
	go func() { w.WriteString("yes\nyes\n"); w.Close() }()
	rt := &runContext{out: ui.NewPrinter(io.Discard, true)}
	for i := 1; i <= 2; i++ {
		if ok, noInput := confirmApply(rt, "Do you want to perform these actions?"); !ok || noInput {
			t.Fatalf("question %d: ok=%v noInput=%v, want a yes", i, ok, noInput)
		}
	}
	if ok, noInput := confirmApply(rt, "a third?"); ok || !noInput {
		t.Fatalf("an empty input must be no input, got ok=%v noInput=%v", ok, noInput)
	}
}
