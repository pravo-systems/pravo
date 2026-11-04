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

// A format raised from 3 up (the first policy, secret, record of an environment, pin of
// roles) only adds objects: a store that is open goes on writing its records and holds the
// new format. A seal that appears, or a format below 3, still stops it (F57).
func TestStoreOpenWhenTheFormatIsRaisedKeepsWriting(t *testing.T) {
	ctx := context.Background()
	ada := testIdentity(t, "ada")
	b, _ := NewLocalBackend(t.TempDir())
	first, err := OpenBackendWith(ctx, b, Options{Identity: ada})
	if err != nil {
		t.Fatal(err)
	}
	mustSet(t, first, node("a"))
	if _, err := first.Upgrade(ctx, UpgradeOptions{To: FormatSealed, Readers: readersOf(ada), Identity: ada}); err != nil {
		t.Fatal(err)
	}
	running, err := OpenBackendWith(ctx, b, Options{Identity: ada})
	if err != nil {
		t.Fatal(err)
	}
	if running.Format() != FormatSealed {
		t.Fatalf("the format is %d, want %d", running.Format(), FormatSealed)
	}
	other, err := OpenBackendWith(ctx, b, Options{Identity: ada})
	if err != nil {
		t.Fatal(err)
	}
	if err := other.setFormat(FormatSecret); err != nil {
		t.Fatal(err)
	}
	running.mu.Lock()
	running.metaChecked = running.metaChecked.Add(-2 * metaRecheck)
	running.mu.Unlock()
	if err := running.Set(node("b"), ""); err != nil {
		t.Fatalf("Set after another process raised the format: %v", err)
	}
	if running.Format() != FormatSecret {
		t.Errorf("the format is %d after the check, want %d", running.Format(), FormatSecret)
	}
}

// Asking whether the format can be raised does not raise it, and a key that may not put
// meta.json is told so (F59).
func TestCanRaiseFormatChangesNothing(t *testing.T) {
	ctx := context.Background()
	ada := testIdentity(t, "ada")
	b, _ := NewLocalBackend(t.TempDir())
	s, err := OpenBackendWith(ctx, b, Options{Identity: ada})
	if err != nil {
		t.Fatal(err)
	}
	mustSet(t, s, node("a"))
	if _, err := s.Upgrade(ctx, UpgradeOptions{To: FormatSealed, Readers: readersOf(ada), Identity: ada}); err != nil {
		t.Fatal(err)
	}
	before, err := b.Get(ctx, "meta.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.CanRaiseFormat(FormatSecret); err != nil {
		t.Fatal(err)
	}
	after, _ := b.Get(ctx, "meta.json")
	if string(before) != string(after) || s.Format() != FormatSealed {
		t.Errorf("the probe changed the state: format %d\nbefore: %s\nafter:  %s", s.Format(), before, after)
	}
	denied, err := OpenBackendWith(ctx, metaPutDenied{b}, Options{Identity: ada})
	if err != nil {
		t.Fatal(err)
	}
	if err := denied.CanRaiseFormat(FormatSecret); err == nil {
		t.Error("a key that cannot put meta.json can raise the format")
	}
	if err := denied.CanRaiseFormat(FormatSealed); err != nil {
		t.Errorf("a state already at the format needs no put: %v", err)
	}
}

type metaPutDenied struct{ Backend }

func (metaPutDenied) ReplaceIf(ctx context.Context, key string, update func([]byte) ([]byte, bool)) (bool, error) {
	return false, errors.New("AccessDenied")
}
