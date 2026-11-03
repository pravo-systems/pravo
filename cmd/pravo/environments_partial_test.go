package main

import (
	"strings"
	"testing"

	"github.com/pravo-systems/pravo/internal/config"
)

// An environment whose apply deferred changes runs the release in part: its row says so, and
// the next step applies it there again rather than promoting past it.
func TestPartlyAppliedEnvironment(t *testing.T) {
	r := envRow{j: environmentJSON{Name: "dev", After: []string{}, Sealed: true, State: "state/dev", Release: "partial", Runs: &runJSON{Deferred: 2}}}
	if got := status(r); !strings.Contains(got, "in part: 2 changes deferred to the next plan") {
		t.Fatalf("status: %q", got)
	}
	root := &config.Root{Environments: []config.Environment{{Name: "dev"}, {Name: "prod", After: []string{"dev"}}}}
	rows := []envRow{r, {j: environmentJSON{Name: "prod", After: []string{"dev"}, Sealed: true, State: "state/prod"}}}
	if got := nextStep(root, rows); !strings.Contains(got, "apply") || !strings.Contains(got, "dev") {
		t.Fatalf("next step %q, want the apply in dev", got)
	}
}
