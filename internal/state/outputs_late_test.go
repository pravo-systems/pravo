package state_test

import (
	"bytes"
	"context"
	"encoding/json"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/pravo-systems/pravo/internal/state"
)

// lateOutputs delivers the first write of outputs.json late, as a store with
// conditional writes sees it: the object is read, and the write that names what
// was read reaches the store after delay (a computer that is suspended while the
// request is on its way, or a connection that hangs and is sent again).
type lateOutputs struct {
	state.Backend
	delay time.Duration
	done  atomic.Bool
}

func (b *lateOutputs) Put(ctx context.Context, key string, data []byte) error {
	if key == "outputs.json" && b.done.CompareAndSwap(false, true) {
		time.Sleep(b.delay)
	}
	return b.Backend.Put(ctx, key, data)
}

func (b *lateOutputs) ReplaceIf(ctx context.Context, key string, update func([]byte) ([]byte, bool)) (bool, error) {
	if key != "outputs.json" || !b.done.CompareAndSwap(false, true) {
		return b.Backend.ReplaceIf(ctx, key, update)
	}
	read, err := b.Get(ctx, key)
	if err != nil {
		return false, err
	}
	next, ok := update(read)
	if !ok {
		return false, nil
	}
	time.Sleep(b.delay)
	return b.Backend.ReplaceIf(ctx, key, func(current []byte) ([]byte, bool) {
		return next, bytes.Equal(current, read)
	})
}

// Two commands record an output each. The write of the first arrives 61 seconds
// late; the lock on the outputs is stale after 60 and is not renewed, so the
// second takes it, reads the outputs and writes its own. The late write of the
// first is refused, because outputs.json is not what the first read, and the
// first merges again: both outputs are recorded. Found by the exploration of
// the interleavings of the lock protocol.
func TestAnOutputsWriteThatLandsLateLosesNothing(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		mem := state.NewMemBackend("mem")
		ctx := context.Background()
		seed, err := state.OpenBackend(ctx, mem)
		if err != nil {
			t.Fatal(err)
		}
		if err := seed.SetOutputs(map[string]json.RawMessage{"seed": json.RawMessage(`"v"`)}); err != nil {
			t.Fatal(err)
		}
		sa, err := state.OpenBackend(ctx, &lateOutputs{Backend: mem, delay: 61 * time.Second})
		if err != nil {
			t.Fatal(err)
		}
		sb, err := state.OpenBackend(ctx, mem)
		if err != nil {
			t.Fatal(err)
		}
		add := func(name string) func(map[string]json.RawMessage, bool) (map[string]json.RawMessage, bool, error) {
			return func(rec map[string]json.RawMessage, _ bool) (map[string]json.RawMessage, bool, error) {
				rec[name] = json.RawMessage(`"v"`)
				return rec, true, nil
			}
		}
		var wg sync.WaitGroup
		var errA, errB error
		wg.Go(func() { errA = sa.UpdateOutputs(add("a")) })
		time.Sleep(time.Second)
		wg.Go(func() { errB = sb.UpdateOutputs(add("b")) })
		wg.Wait()
		if errA != nil || errB != nil {
			t.Fatalf("A: %v, B: %v", errA, errB)
		}
		data, _ := mem.Get(ctx, "outputs.json")
		var out map[string]json.RawMessage
		_ = json.Unmarshal(data, &out)
		if out["seed"] == nil || out["a"] == nil || out["b"] == nil {
			t.Fatalf("both commands report that they recorded their output, and outputs.json holds %s", data)
		}
		if got := sa.Outputs(); got["a"] == nil || got["b"] == nil {
			t.Fatalf("the command whose write was refused holds %v", got)
		}
	})
}

// The first outputs of a state are created, not replaced: of two commands that
// both found no outputs.json, the second merges into what the first created.
func TestTheFirstOutputsAreCreatedOnce(t *testing.T) {
	mem := state.NewMemBackend("mem")
	ctx := context.Background()
	sa, err := state.OpenBackend(ctx, mem)
	if err != nil {
		t.Fatal(err)
	}
	sb, err := state.OpenBackend(ctx, mem)
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	err = sa.UpdateOutputs(func(rec map[string]json.RawMessage, present bool) (map[string]json.RawMessage, bool, error) {
		if calls++; calls == 1 {
			if present {
				t.Fatal("outputs.json is there before any command wrote it")
			}
			// Another command records its outputs between the read and the write.
			if err := sb.SetOutputs(map[string]json.RawMessage{"b": json.RawMessage(`"v"`)}); err != nil {
				t.Fatal(err)
			}
		}
		rec["a"] = json.RawMessage(`"v"`)
		return rec, true, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	out, _, err := sb.LoadOutputs()
	if err != nil {
		t.Fatal(err)
	}
	if calls != 2 || out["a"] == nil || out["b"] == nil {
		t.Fatalf("after %d merges the outputs are %v", calls, out)
	}
}
