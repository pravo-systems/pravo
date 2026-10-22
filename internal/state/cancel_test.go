package state

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"testing"
)

// cancelling counts the calls to a backend, cancels a context when the count
// reaches a limit, and fails a call made with a context that is cancelled, as a
// client of a bucket does.
type cancelling struct {
	Backend
	mu     sync.Mutex
	calls  int
	limit  int
	cancel context.CancelFunc
}

func (b *cancelling) count(ctx context.Context) error {
	b.mu.Lock()
	b.calls++
	if b.limit > 0 && b.calls == b.limit {
		b.cancel()
	}
	b.mu.Unlock()
	return ctx.Err()
}

func (b *cancelling) total() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.calls
}

func (b *cancelling) Get(ctx context.Context, key string) ([]byte, error) {
	if err := b.count(ctx); err != nil {
		return nil, err
	}
	return b.Backend.Get(ctx, key)
}

func (b *cancelling) Put(ctx context.Context, key string, data []byte) error {
	if err := b.count(ctx); err != nil {
		return err
	}
	return b.Backend.Put(ctx, key, data)
}

func (b *cancelling) PutIfAbsent(ctx context.Context, key string, data []byte) (bool, error) {
	if err := b.count(ctx); err != nil {
		return false, err
	}
	return b.Backend.PutIfAbsent(ctx, key, data)
}

func (b *cancelling) ReplaceIf(ctx context.Context, key string, update func([]byte) ([]byte, bool)) (bool, error) {
	if err := b.count(ctx); err != nil {
		return false, err
	}
	return b.Backend.ReplaceIf(ctx, key, update)
}

func (b *cancelling) DeleteIf(ctx context.Context, key string, match func([]byte) bool) (bool, error) {
	if err := b.count(ctx); err != nil {
		return false, err
	}
	return b.Backend.DeleteIf(ctx, key, match)
}

func (b *cancelling) Delete(ctx context.Context, key string) error {
	if err := b.count(ctx); err != nil {
		return err
	}
	return b.Backend.Delete(ctx, key)
}

func (b *cancelling) List(ctx context.Context, prefix string) ([]string, error) {
	if err := b.count(ctx); err != nil {
		return nil, err
	}
	return b.Backend.List(ctx, prefix)
}

// attributesOf reads every record of the store as a person who holds the key.
func attributesOf(t *testing.T, s *Store) map[string]string {
	t.Helper()
	out := map[string]string{}
	for _, r := range s.List() {
		var attrs bytes.Buffer
		if err := json.Compact(&attrs, r.Attributes); err != nil {
			t.Fatal(err)
		}
		out[r.Address()] = attrs.String() + "|" + string(r.Private) + "|" + r.ConfigHash
	}
	return out
}

// seedMany makes a store of n resources that carry the secret, some of them with
// a second revision, some of them removed.
func seedMany(t *testing.T, s *Store, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		r := &Resource{Mode: "managed", Type: "db", Name: fmt.Sprintf("r%02d", i), Provider: "p",
			Attributes: json.RawMessage(fmt.Sprintf(`{"id":"db-%d","password":%q}`, i, secretValue)), Private: []byte("private:" + secretValue), ConfigHash: "c0ffee"}
		if i%3 == 0 {
			r.Dependencies = []string{"db.r00"}
		}
		if i == 0 {
			r.Dependencies = nil
		}
		mustSet(t, s, r)
		if i%4 == 0 {
			again := *s.Get(r.Address())
			again.Attributes = json.RawMessage(fmt.Sprintf(`{"id":"db-%d","password":%q,"rev":2}`, i, secretValue))
			mustSet(t, s, &again)
		}
	}
	if err := s.SetOutputs(map[string]json.RawMessage{"password": json.RawMessage(`{"$sensitive":"` + secretValue + `"}`)}); err != nil {
		t.Fatal(err)
	}
}

// 'pravo protect init' that is interrupted must leave a store that the command
// finishes when it is run again, as it does one that was killed. The context of
// the upgrade is cancelled after n calls to the backend, for several n; the
// upgrade is then run again with a context that is not.
func TestSealInterruptedByCancellationIsResumable(t *testing.T) {
	skipWhenSealed(t)
	for _, from := range []int{Format, FormatImmutable} {
		for _, bk := range testBackends() {
			t.Run(fmt.Sprintf("%s from format %d", bk.name, from), func(t *testing.T) {
				ada := testIdentity(t, "ada")
				// The number of calls an uninterrupted upgrade makes.
				dry := func() int {
					cb := &cancelling{Backend: bk.make(t)}
					s, err := OpenBackendWith(context.Background(), cb, Options{NewFormat: from, Identity: ada})
					if err != nil {
						t.Fatal(err)
					}
					seedMany(t, s, 9)
					before := cb.total()
					if _, err := s.Upgrade(context.Background(), UpgradeOptions{To: FormatSealed, Readers: readersOf(ada), Identity: ada}); err != nil {
						t.Fatal(err)
					}
					return cb.total() - before
				}()
				step := 3
				if bk.name != "local" {
					step = 7
				}
				interrupted, midway := 0, 0
				defer func() {
					if interrupted == 0 || midway == 0 {
						t.Errorf("of the upgrades cancelled in %d calls, %d were interrupted and %d left records to convert", dry, interrupted, midway)
					}
				}()
				for n := 1; n <= dry; n += step {
					cb := &cancelling{Backend: bk.make(t)}
					s, err := OpenBackendWith(context.Background(), cb, Options{NewFormat: from, Identity: ada})
					if err != nil {
						t.Fatal(err)
					}
					seedMany(t, s, 9)
					want := attributesOf(t, s)
					outputs, _ := s.ReloadOutputs()

					ctx, cancel := context.WithCancel(context.Background())
					cb.mu.Lock()
					cb.limit, cb.cancel = cb.calls+n, cancel
					cb.mu.Unlock()
					_, err = s.Upgrade(ctx, UpgradeOptions{To: FormatSealed, Readers: readersOf(ada), Identity: ada})
					cancel()
					if err != nil && !errors.Is(err, context.Canceled) {
						t.Fatalf("n=%d: Upgrade = %v, want nil or a cancellation", n, err)
					}
					upErr := err
					if upErr != nil {
						interrupted++
					}

					// Whatever was left, a store opened now reads every record, and the
					// upgrade run again finishes.
					mid, err := OpenBackendWith(context.Background(), cb.Backend, Options{Identity: ada})
					if err != nil {
						t.Fatalf("n=%d: Open after the interrupted upgrade = %v", n, err)
					}
					if got := attributesOf(t, mid); !equalMaps(got, want) {
						t.Fatalf("n=%d: the interrupted store reads %v, want %v", n, got, want)
					}
					if p := mid.Protection(); upErr != nil && p.Plain > 0 && p.Plain < p.Records {
						midway++
					}
					if _, err := mid.Upgrade(context.Background(), UpgradeOptions{To: FormatSealed, Readers: readersOf(ada), Identity: ada}); err != nil {
						t.Fatalf("n=%d: Upgrade run again = %v", n, err)
					}

					done := openIdentity(t, cb.Backend, ada)
					if p := done.Protection(); p.State != "sealed" || p.Plain != 0 || p.Migration != nil {
						t.Fatalf("n=%d: after the second run the state is %+v", n, p)
					}
					if got := attributesOf(t, done); !equalMaps(got, want) {
						t.Fatalf("n=%d: the sealed store reads %v, want %v", n, got, want)
					}
					if got, _ := done.ReloadOutputs(); outputsJSON(got) != outputsJSON(outputs) {
						t.Fatalf("n=%d: the outputs are %s, want %s", n, outputsJSON(got), outputsJSON(outputs))
					}
					ver, err := done.VerifyAll(context.Background())
					if err != nil || len(ver.Bad) > 0 {
						t.Fatalf("n=%d: VerifyAll = %+v, %v", n, ver, err)
					}
					assertNoSecret(t, cb.Backend, secretValue)
				}
			})
		}
	}
}

func equalMaps(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}
