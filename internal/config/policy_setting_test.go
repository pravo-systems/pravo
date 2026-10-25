package config

import (
	"strings"
	"testing"
)

// Policies are nodes of the specification, built from pravo.#Policy; the settings have
// no field for them.
func TestSettingsHasNoPolicies(t *testing.T) {
	_, err := loadSpec(t, `$settings: norms: plan: summary: blast_radius: <=5`)
	if err == nil || !strings.Contains(err.Error(), "is not a setting") || !strings.Contains(err.Error(), "state and refresh") {
		t.Fatalf("expected an unknown-setting error, got %v", err)
	}
	if _, err := loadSpec(t, `$settings: policy: summary: blast_radius: <=5`); err == nil {
		t.Fatal("a policy setting should be refused too")
	}
}
