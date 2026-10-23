package state

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestReadOutputs(t *testing.T) {
	skipWhenSealed(t)
	dir := t.TempDir()
	b := &LocalBackend{Dir: dir}
	ctx := context.Background()
	if _, err := ReadOutputs(ctx, b); err == nil || !strings.Contains(err.Error(), "apply that root first") {
		t.Fatalf("a root with no outputs should say so, got %v", err)
	}
	st, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetOutputs(map[string]json.RawMessage{"vpc_id": json.RawMessage(`"vpc-1"`)}); err != nil {
		t.Fatal(err)
	}
	got, err := ReadOutputs(ctx, b)
	if err != nil || string(got["vpc_id"]) != `"vpc-1"` {
		t.Fatalf("ReadOutputs = %v, %v", got, err)
	}
}
