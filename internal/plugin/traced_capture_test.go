package plugin

import (
	"context"
	"testing"

	"github.com/pravo-systems/pravo/internal/plugin/netcap"
)

// The network calls of a provider go under the RPC that is running, the newest of several, and under none after it ended.
func TestCaptureParentIsTheRunningRPC(t *testing.T) {
	t.Setenv("PRAVO_TRACES", "on")
	c, err := netcap.New()
	if err != nil {
		t.Fatal(err)
	}
	tr := &Traced{}
	AttachCapture(tr, c)
	if c.Parent == nil || tr.parent() != nil {
		t.Fatal("no parent expected before an RPC")
	}
	type key struct{}
	a, b := context.WithValue(context.Background(), key{}, "a"), context.WithValue(context.Background(), key{}, "b")
	tr.push(a)
	tr.push(b)
	if tr.parent() != b {
		t.Error("the newest RPC is the parent")
	}
	tr.pop(b)
	if tr.parent() != a {
		t.Error("the one that is left is the parent")
	}
	tr.pop(a)
	if tr.parent() != nil {
		t.Error("no parent after the last RPC")
	}
	// a provider that is not traced closes the capture at once
	c2, _ := netcap.New()
	AttachCapture(nil, c2)
}
