package statetest

import (
	"context"
	"time"

	"github.com/pravo-systems/pravo/internal/state"
)

// Latency wraps a Backend and makes every call take at least Delay, as a call
// to a bucket over a network does. A call is not held back by another: two calls
// at the same time take Delay together. The backend is not on this machine, so
// a lock is judged by the time it was last renewed and not by its process.
type Latency struct {
	state.Backend
	Delay time.Duration
}

// NewLatency returns a Latency backend over b.
func NewLatency(b state.Backend, delay time.Duration) *Latency {
	return &Latency{Backend: b, Delay: delay}
}

// wait returns when the delay has passed, or when ctx is done.
func (l *Latency) wait(ctx context.Context) error {
	if l.Delay <= 0 {
		return nil
	}
	t := time.NewTimer(l.Delay)
	defer t.Stop()
	select {
	case <-t.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Local reports false: a backend with latency is a remote one.
func (l *Latency) Local() bool { return false }

func (l *Latency) Get(ctx context.Context, key string) ([]byte, error) {
	if err := l.wait(ctx); err != nil {
		return nil, err
	}
	return l.Backend.Get(ctx, key)
}

func (l *Latency) Put(ctx context.Context, key string, data []byte) error {
	if err := l.wait(ctx); err != nil {
		return err
	}
	return l.Backend.Put(ctx, key, data)
}

func (l *Latency) PutIfAbsent(ctx context.Context, key string, data []byte) (bool, error) {
	if err := l.wait(ctx); err != nil {
		return false, err
	}
	return l.Backend.PutIfAbsent(ctx, key, data)
}

func (l *Latency) Delete(ctx context.Context, key string) error {
	if err := l.wait(ctx); err != nil {
		return err
	}
	return l.Backend.Delete(ctx, key)
}

// DeleteIf takes two calls: the read of the object and the delete.
func (l *Latency) DeleteIf(ctx context.Context, key string, match func(current []byte) bool) (bool, error) {
	for range 2 {
		if err := l.wait(ctx); err != nil {
			return false, err
		}
	}
	return l.Backend.DeleteIf(ctx, key, match)
}

// ReplaceIf takes two calls: the read of the object and the write.
func (l *Latency) ReplaceIf(ctx context.Context, key string, update func(current []byte) ([]byte, bool)) (bool, error) {
	for range 2 {
		if err := l.wait(ctx); err != nil {
			return false, err
		}
	}
	return l.Backend.ReplaceIf(ctx, key, update)
}

func (l *Latency) List(ctx context.Context, prefix string) ([]string, error) {
	if err := l.wait(ctx); err != nil {
		return nil, err
	}
	return l.Backend.List(ctx, prefix)
}
