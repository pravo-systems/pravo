package engine

import (
	"encoding/json"
	"testing"
)

func raw(s string) json.RawMessage { return json.RawMessage(s) }

// Two applies that overlap in time each compute every output from the state
// they started with. The one that ends last must not write back the stale value
// of an output it did not change.
func TestMergeOutputsKeepsWhatAnotherApplyRecorded(t *testing.T) {
	baseline := map[string]json.RawMessage{"a": raw(`"a0"`), "b": raw(`"b0"`)}
	// The other apply changed a to a1 and finished first.
	recorded := map[string]json.RawMessage{"a": raw(`"a1"`), "b": raw(`"b0"`)}
	// This apply changed b to b1; it computed a from its old snapshot.
	computed := map[string]json.RawMessage{"a": raw(`"a0"`), "b": raw(`"b1"`)}

	got := mergeOutputs(recorded, baseline, computed, []string{"a", "b"})
	if string(got["a"]) != `"a1"` {
		t.Errorf("a = %s; the other apply's value must survive", got["a"])
	}
	if string(got["b"]) != `"b1"` {
		t.Errorf("b = %s; this apply's change must be written", got["b"])
	}
}

func TestMergeOutputsDropsUndeclaredAndKeepsSkipped(t *testing.T) {
	baseline := map[string]json.RawMessage{"old": raw(`1`), "kept": raw(`2`)}
	recorded := map[string]json.RawMessage{"old": raw(`1`), "kept": raw(`2`)}
	computed := map[string]json.RawMessage{"new": raw(`3`)} // a partial apply: kept was skipped
	got := mergeOutputs(recorded, baseline, computed, []string{"kept", "new"})
	if _, ok := got["old"]; ok {
		t.Error("an output that is no longer declared is dropped")
	}
	if string(got["kept"]) != `2` || string(got["new"]) != `3` {
		t.Errorf("got %v", got)
	}
}

func TestMergeOutputsWritesAnOutputThatIsNotRecorded(t *testing.T) {
	got := mergeOutputs(nil, map[string]json.RawMessage{"x": raw(`1`)}, map[string]json.RawMessage{"x": raw(`1`)}, []string{"x"})
	if string(got["x"]) != `1` {
		t.Errorf("got %v", got)
	}
}
