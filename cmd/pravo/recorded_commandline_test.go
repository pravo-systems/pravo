package main

import (
	"strings"
	"testing"

	"github.com/pravo-systems/pravo/internal/serve"
)

func TestRecordedCommandLineWithholdsTagValues(t *testing.T) {
	got := serve.ScrubCommand(commandLine([]string{"pravo", "apply", "-t", "token=CANARY-X", "--tag=k=CANARY-Y", "--reason", "deploy"}))
	if strings.Contains(got, "CANARY") {
		t.Fatalf("a tag value is in the recorded command line: %s", got)
	}
}
