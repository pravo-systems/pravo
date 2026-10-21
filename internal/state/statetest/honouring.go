package statetest

import (
	"context"

	"github.com/pravo-systems/pravo/internal/state"
)

// Honouring wraps a Backend and refuses every call made with a context that is
// done, as the S3 client does. The local backend ignores its context, so a test of
// what a command does after a signal needs this to behave like a remote state.
type Honouring struct {
	state.Backend
}

// NewHonouring returns a Honouring backend over b.
func NewHonouring(b state.Backend) *Honouring { return &Honouring{Backend: b} }

func (h *Honouring) Get(ctx context.Context, key string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return h.Backend.Get(ctx, key)
}

func (h *Honouring) Put(ctx context.Context, key string, data []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return h.Backend.Put(ctx, key, data)
}

func (h *Honouring) PutIfAbsent(ctx context.Context, key string, data []byte) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	return h.Backend.PutIfAbsent(ctx, key, data)
}

func (h *Honouring) DeleteIf(ctx context.Context, key string, match func(current []byte) bool) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	return h.Backend.DeleteIf(ctx, key, match)
}

func (h *Honouring) ReplaceIf(ctx context.Context, key string, update func(current []byte) ([]byte, bool)) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	return h.Backend.ReplaceIf(ctx, key, update)
}

func (h *Honouring) Delete(ctx context.Context, key string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return h.Backend.Delete(ctx, key)
}

func (h *Honouring) List(ctx context.Context, prefix string) ([]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return h.Backend.List(ctx, prefix)
}
