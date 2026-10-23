package state

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
)

// A command that opened the store before it was sealed, and writes after, must not
// put a value in plain text into the sealed state.
func TestStoreOpenedBeforeASealWritesNothingInPlainText(t *testing.T) {
	for _, from := range []int{Format, FormatImmutable} {
		ctx := context.Background()
		ada := testIdentity(t, "ada")
		b, _ := NewLocalBackend(t.TempDir())
		first, err := OpenBackendWith(ctx, b, Options{NewFormat: from})
		if err != nil {
			t.Fatal(err)
		}
		mustSet(t, first, node("a"), node("b"))
		old, err := OpenBackendWith(ctx, b, Options{})
		if err != nil {
			t.Fatal(err)
		}
		sealer, err := OpenBackendWith(ctx, b, Options{Identity: ada})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := sealer.Upgrade(ctx, UpgradeOptions{To: FormatSealed, Readers: readersOf(ada), Identity: ada}); err != nil {
			t.Fatal(err)
		}
		// The store opened before the seal now writes.
		r := *old.Get("t.a")
		r.Attributes = json.RawMessage(`{"password":"` + secretValue + `"}`)
		if err := old.Set(&r, ""); !errors.Is(err, ErrUpgraded) {
			t.Errorf("format %d: Set from a store opened before the seal = %v, want ErrUpgraded", from, err)
		}
		assertNoSecret(t, b, secretValue)
	}
}
