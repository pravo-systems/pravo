package engine

import (
	"context"
	"strings"
	"testing"

	"github.com/pravo-systems/pravo/internal/schema"
	"github.com/pravo-systems/pravo/internal/state"
)

// A record written under a newer schema version is refused before the provider is asked.
func TestUpgradeRefusesStateFromNewerSchema(t *testing.T) {
	e := &Engine{}
	_, err := e.upgrade(context.Background(), nil, &state.Resource{SchemaVersion: 3}, &schema.Schema{Version: 1}, "x.y")
	if err == nil || !strings.Contains(err.Error(), "newer provider version") {
		t.Fatalf("want a refusal, got %v", err)
	}
}
