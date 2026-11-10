package main

import (
	"runtime/debug"
	"testing"

	"github.com/pravo-systems/pravo/internal/env"
)

// A command runs with the collector set for a run that ends, unless GOGC says
// otherwise.
func TestTheCollectorIsSetUnlessGOGCIs(t *testing.T) {
	was := debug.SetGCPercent(100)
	defer debug.SetGCPercent(was)
	applyCollector(env.New([]string{"GOGC=50"}))
	if got := debug.SetGCPercent(100); got != 100 {
		t.Errorf("with GOGC set the collector was set to %d", got)
	}
	applyCollector(env.New(nil))
	if got := debug.SetGCPercent(100); got != collectorPercent {
		t.Errorf("without GOGC the collector is at %d, want %d", got, collectorPercent)
	}
}
