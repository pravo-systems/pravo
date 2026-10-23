package engine

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/pravo-systems/pravo/internal/protect"
	"github.com/pravo-systems/pravo/internal/state"
	"github.com/pravo-systems/pravo/internal/state/statetest"
)

// A real sealed state, with two readers, and a plan store for each of them.
// signedStores seals its own states, so it does not run in a suite that opens every state sealed.
func signedStores(t *testing.T) (ada, dan *PlanStore, readers []protect.Reader, b state.Backend) {
	t.Helper()
	if state.NewStoreIdentity != nil {
		t.Skip("the suite runs over sealed stores, and this test seals its own")
	}
	ia, id := statetest.Identity(t, "ada"), statetest.Identity(t, "dan")
	b, err := state.NewLocalBackend(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	sa := statetest.Seal(t, b, ia, id)
	sd, err := state.OpenBackendWith(context.Background(), b, state.Options{Identity: id})
	if err != nil {
		t.Fatal(err)
	}
	return NewPlanStoreFor(sa), NewPlanStoreFor(sd), statetest.Readers(ia, id), b
}

func TestAStoredPlanIsSignedByWhoStoredIt(t *testing.T) {
	ada, dan, readers, b := signedStores(t)
	ctx := context.Background()
	m := putSecret(t, ada, t0, change("a", ActionCreate, 0))
	vs, err := ada.Signatures(ctx, m.ID, readers)
	if err != nil || len(vs) != 1 || vs[0].Status != protect.SigVerified || vs[0].Signer != "ada" {
		t.Fatalf("signatures %+v %v", vs, err)
	}
	// Storing the same plan again adds a signature, and keeps the first.
	again := putSecret(t, dan, t0, change("a", ActionCreate, 0))
	if again.ID != m.ID {
		t.Fatalf("ids %s %s", m.ID, again.ID)
	}
	vs, _ = ada.Signatures(ctx, m.ID, readers)
	if len(vs) != 2 {
		t.Fatalf("signatures %+v", vs)
	}
	names := vs[0].Signer + "," + vs[1].Signer
	if !strings.Contains(names, "ada") || !strings.Contains(names, "dan") {
		t.Errorf("signers %s", names)
	}
	// A key that is not listed any more.
	vs, _ = ada.Signatures(ctx, m.ID, readers[:1])
	var unknown int
	for _, v := range vs {
		if v.Status == protect.SigUnknownKey {
			unknown++
		}
	}
	if unknown != 1 {
		t.Errorf("unknown %+v", vs)
	}
	// Nothing in the bucket holds the digest of the plan, or the plan.
	digest, err := ada.PlanDigest(ctx, m.ID)
	if err != nil || len(digest) != 64 {
		t.Fatalf("digest %q %v", digest, err)
	}
	for _, k := range listKeys(t, b, PlanDir) {
		data, _ := b.Get(ctx, k)
		if strings.Contains(string(data), digest) || strings.Contains(string(data), plantedTag) {
			t.Errorf("%s holds the digest or a value of the plan", k)
		}
	}
}

func TestATamperedPlanSignatureIsInvalid(t *testing.T) {
	ada, _, readers, b := signedStores(t)
	ctx := context.Background()
	m := putSecret(t, ada, t0, change("a", ActionCreate, 0))
	other := putSecret(t, ada, t0.Add(time.Minute), change("b", ActionCreate, 0))
	keys, _ := ada.sigObjects(ctx, m.ID)
	otherKeys, _ := ada.sigObjects(ctx, other.ID)
	// A signature of another plan, put under this plan's name.
	data, _ := b.Get(ctx, otherKeys[0])
	swapped := strings.Replace(string(data), other.ID, m.ID, 1)
	if err := b.Put(ctx, keys[0], []byte(swapped)); err != nil {
		t.Fatal(err)
	}
	vs, err := ada.Signatures(ctx, m.ID, readers)
	if err != nil || len(vs) != 1 || vs[0].Status != protect.SigInvalid {
		t.Fatalf("signatures %+v %v", vs, err)
	}
	// An object that is not a signature at all.
	if err := b.Put(ctx, keys[0], []byte("garbage")); err != nil {
		t.Fatal(err)
	}
	if vs, _ = ada.Signatures(ctx, m.ID, readers); len(vs) != 1 || vs[0].Status != protect.SigInvalid {
		t.Errorf("garbage %+v", vs)
	}
}

func TestAPlanWithoutAKeyIsNotSignedAndNotSealed(t *testing.T) {
	b, err := state.NewLocalBackend(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ps := NewPlanStore(b)
	m := putSecret(t, ps, t0, change("a", ActionCreate, 0))
	if keys, _ := ps.sigObjects(context.Background(), m.ID); len(keys) != 0 {
		t.Errorf("a plan of a state that is not sealed has signatures: %v", keys)
	}
}

func TestCollectingAPlanRemovesItsSignatures(t *testing.T) {
	ada, _, _, b := signedStores(t)
	ctx := context.Background()
	m := putSecret(t, ada, t0, change("a", ActionCreate, 0))
	if keys, _ := ada.sigObjects(ctx, m.ID); len(keys) != 1 {
		t.Fatalf("signatures %v", keys)
	}
	ada.Now = func() time.Time { return t0.Add(30 * 24 * time.Hour) }
	if _, err := ada.GC(ctx, GCOptions{Owner: "ada@host", OlderThan: time.Hour}); err != nil {
		t.Fatal(err)
	}
	for _, k := range listKeys(t, b, PlanDir) {
		if strings.Contains(k, m.ID) {
			t.Errorf("%s is left", k)
		}
	}
}
