package main

import "testing"

func TestNotInStateMessage(t *testing.T) {
	if got := notInState("a").Error(); got != "a is not in state" {
		t.Fatalf("notInState = %q", got)
	}
}
