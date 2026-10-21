package engine

import (
	"encoding/json"
	"testing"
)

func TestWrapOutput(t *testing.T) {
	v := json.RawMessage(`"s3cret"`)
	if string(WrapOutput(v, false)) != `"s3cret"` {
		t.Error("a plain output must not be wrapped")
	}
	w := WrapOutput(v, true)
	got, sensitive := UnwrapOutput(w)
	if !sensitive || string(got) != `"s3cret"` {
		t.Errorf("unwrap = %s, %v", got, sensitive)
	}
	if _, sensitive := UnwrapOutput(json.RawMessage(`{"a":1}`)); sensitive {
		t.Error("an ordinary object is not sensitive")
	}
}
