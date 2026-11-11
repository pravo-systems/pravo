package state

import (
	"context"
	"testing"
)

// Every kind of the list is copied and, when it counts as content, makes a state that is not
// empty: the two used to be lists of their own.
func TestEveryStateKindIsCopiedAndCounted(t *testing.T) {
	ctx := context.Background()
	for _, kind := range stateKinds {
		src, dst := NewMemBackend("src"), NewMemBackend("dst")
		key := kind.prefix + "/x"
		if err := src.Put(ctx, key, []byte("{}")); err != nil {
			t.Fatal(err)
		}
		empty, err := holdsNothing(ctx, src)
		if err != nil {
			t.Fatal(err)
		}
		if empty == kind.content {
			t.Errorf("%s: holdsNothing = %v with an object of the kind, content = %v", kind.prefix, empty, kind.content)
		}
		if kind.prefix == secretDir || kind.copy == copySecretVersions {
			// A secret object is parsed when it differs; the plain copy of a new one needs no parse.
			continue
		}
		if _, err := Copy(ctx, src, dst); err != nil {
			t.Fatalf("%s: %v", kind.prefix, err)
		}
		if _, err := dst.Get(ctx, key); err != nil {
			t.Errorf("%s: the object was not copied: %v", kind.prefix, err)
		}
	}
}

// A version of a history that the two states hold with other bytes is refused.
func TestCopyRefusesADifferingVersionOfAHistory(t *testing.T) {
	ctx := context.Background()
	src, dst := NewMemBackend("src"), NewMemBackend("dst")
	_ = src.Put(ctx, policyDir+"/p@00000001.json", []byte(`{"a":1}`))
	_ = dst.Put(ctx, policyDir+"/p@00000001.json", []byte(`{"a":2}`))
	if _, err := Copy(ctx, src, dst); err == nil {
		t.Fatal("a version of a history that differs must be refused")
	}
}
