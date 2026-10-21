// Package statetest holds helpers for tests that run against a state store.
package statetest

import (
	"context"
	"strings"
	"sync"

	"github.com/pravo-systems/pravo/internal/state"
)

// Counting wraps a Backend and counts the calls made through it, by operation
// and by the first element of the key: resources, locks, txn, or the key
// itself for meta.json and outputs.json.
type Counting struct {
	state.Backend

	mu     sync.Mutex
	counts map[string]int
}

// NewCounting returns a Counting backend over b.
func NewCounting(b state.Backend) *Counting {
	return &Counting{Backend: b, counts: map[string]int{}}
}

func (c *Counting) note(op, key string) {
	prefix, _, _ := strings.Cut(key, "/")
	c.mu.Lock()
	c.counts[op]++
	c.counts[op+" "+prefix]++
	c.mu.Unlock()
}

// Count returns the number of calls of op (Get, Put, PutIfAbsent, Delete or
// List) since the last Reset. A conditional delete counts as a Get and a Delete,
// and a conditional replace as a Get and a Put: a store without conditions in the
// protocol reads the object and then writes it, and a store with them is asked
// for the same two things. With a prefix it counts the calls on keys below
// that prefix only: Count("Put", "resources").
func (c *Counting) Count(op string, prefix ...string) int {
	key := op
	if len(prefix) > 0 {
		key += " " + prefix[0]
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.counts[key]
}

// Writes returns the number of calls that change the store: Put, PutIfAbsent
// and Delete.
func (c *Counting) Writes() int {
	return c.Count("Put") + c.Count("PutIfAbsent") + c.Count("Delete")
}

// Reset sets every count to zero.
func (c *Counting) Reset() {
	c.mu.Lock()
	c.counts = map[string]int{}
	c.mu.Unlock()
}

func (c *Counting) Get(ctx context.Context, key string) ([]byte, error) {
	c.note("Get", key)
	return c.Backend.Get(ctx, key)
}

func (c *Counting) Put(ctx context.Context, key string, data []byte) error {
	c.note("Put", key)
	return c.Backend.Put(ctx, key, data)
}

func (c *Counting) PutIfAbsent(ctx context.Context, key string, data []byte) (bool, error) {
	c.note("PutIfAbsent", key)
	return c.Backend.PutIfAbsent(ctx, key, data)
}

func (c *Counting) Delete(ctx context.Context, key string) error {
	c.note("Delete", key)
	return c.Backend.Delete(ctx, key)
}

func (c *Counting) DeleteIf(ctx context.Context, key string, match func(current []byte) bool) (bool, error) {
	c.note("Get", key)
	c.note("Delete", key)
	return c.Backend.DeleteIf(ctx, key, match)
}

func (c *Counting) ReplaceIf(ctx context.Context, key string, update func(current []byte) ([]byte, bool)) (bool, error) {
	c.note("Get", key)
	c.note("Put", key)
	return c.Backend.ReplaceIf(ctx, key, update)
}

func (c *Counting) List(ctx context.Context, prefix string) ([]string, error) {
	c.note("List", prefix)
	return c.Backend.List(ctx, prefix)
}
