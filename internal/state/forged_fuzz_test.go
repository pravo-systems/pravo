package state

import (
	"context"
	"sort"
	"testing"
)

// FuzzOpenWithOneForgedObject replaces one object of a small state with arbitrary bytes and
// opens and reads the state: whatever a writer of the bucket leaves there, a command reports an
// error and does not panic.
func FuzzOpenWithOneForgedObject(f *testing.F) {
	ctx := context.Background()
	seed := NewMemBackend("seed")
	s, err := openBackend(ctx, seed, Options{})
	if err != nil {
		f.Fatal(err)
	}
	mustSet(f, s, node("a"), node("b", "t.a"))
	if err := s.Lock("t.c", "apply", "T1"); err != nil {
		f.Fatal(err)
	}
	if err := s.NotePendingCreate("t.a", "id", nil, false, "T1", "apply"); err != nil {
		f.Fatal(err)
	}
	tx := s.Begin("apply", "pravo apply")
	_ = tx.Start()
	mustSet(f, s, node("d"))
	_ = tx.Finish("applied", "1 added", true)
	var keys []string
	seed.mu.Lock()
	for k := range seed.objects {
		keys = append(keys, k)
	}
	seed.mu.Unlock()
	sort.Strings(keys)
	for i, k := range keys {
		f.Add(uint8(i), seed.objects[k])
		f.Add(uint8(i), []byte(`{}`))
		f.Add(uint8(i), []byte(`null`))
		f.Add(uint8(i), []byte(`[]`))
	}
	f.Fuzz(func(t *testing.T, which uint8, data []byte) {
		b := NewMemBackend("fuzz")
		for k, v := range seed.objects {
			b.objects[k] = v
		}
		b.objects[keys[int(which)%len(keys)]] = data
		st, err := openBackend(ctx, b, Options{})
		if err != nil {
			return
		}
		_ = st.List()
		_, _ = st.Locks()
		_, _ = st.PendingCreates()
		_, _ = st.Suspects()
		_, _ = st.Transactions()
		_ = st.Outputs()
		_, _ = st.VerifyAll(ctx)
		_ = st.Snapshot()
		for _, r := range st.List() {
			_ = st.Dependents(r.Address())
			_ = r.ID()
		}
	})
}
