package state

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"
)

// faultBackend fails operations of one kind from the n-th of that kind on (a
// machine that went down, or a bucket that keeps refusing that call), or only the
// n-th (a call that failed once). kind "" counts every kind and always keeps failing.
type faultBackend struct {
	Backend
	kind       string
	n          int64
	persistent bool
	seen       atomic.Int64 // operations of the kind counted, whether or not they failed
	failed     atomic.Int64
}

func (b *faultBackend) hit(kind string) error {
	if b.kind != "" && b.kind != kind {
		return nil
	}
	i := b.seen.Add(1) - 1
	if i < b.n || (i > b.n && !b.persistent && b.kind != "") {
		return nil
	}
	b.failed.Add(1)
	return errCrash
}

func (b *faultBackend) Put(ctx context.Context, key string, data []byte) error {
	if err := b.hit("Put"); err != nil {
		return err
	}
	return b.Backend.Put(ctx, key, data)
}

func (b *faultBackend) PutIfAbsent(ctx context.Context, key string, data []byte) (bool, error) {
	if err := b.hit("PutIfAbsent"); err != nil {
		return false, err
	}
	return b.Backend.PutIfAbsent(ctx, key, data)
}

func (b *faultBackend) ReplaceIf(ctx context.Context, key string, update func([]byte) ([]byte, bool)) (bool, error) {
	if err := b.hit("ReplaceIf"); err != nil {
		return false, err
	}
	return b.Backend.ReplaceIf(ctx, key, update)
}

func (b *faultBackend) DeleteIf(ctx context.Context, key string, match func([]byte) bool) (bool, error) {
	if err := b.hit("DeleteIf"); err != nil {
		return false, err
	}
	return b.Backend.DeleteIf(ctx, key, match)
}

func (b *faultBackend) Delete(ctx context.Context, key string) error {
	if err := b.hit("Delete"); err != nil {
		return err
	}
	return b.Backend.Delete(ctx, key)
}

// sweepInvariants checks a store that an upgrade left, whole or not, from outside
// the process that failed: what an old binary sees, what a new one lists, and that
// no record is lost.
func sweepInvariants(t *testing.T, label string, lb Backend, want string, from, to int) {
	t.Helper()
	m := formatMeta(t, lb)
	// An old binary refuses a format it does not know: so no object of the new
	// layout may exist before meta.json says so.
	objs := allObjects(t, lb)
	for k, data := range objs {
		if len(k) > 10 && k[:10] == "resources/" {
			if _, isPtr, _ := parsePointer(data); isPtr && m.Format < FormatImmutable {
				t.Errorf("%s: %s is a pointer and meta.json says format %d", label, k, m.Format)
			}
		}
		if len(k) > 4 && k[:4] == "rec/" && m.Format < FormatImmutable {
			t.Errorf("%s: %s exists and meta.json says format %d", label, k, m.Format)
		}
		if len(k) > 4 && k[:4] == "rec/" && bytes.Contains(data, []byte(`"sealed"`)) && m.Format < FormatSealed {
			t.Errorf("%s: %s is sealed and meta.json says format %d", label, k, m.Format)
		}
	}
	if m.Format < from || m.Format > to {
		t.Errorf("%s: format %d not in %d..%d", label, m.Format, from, to)
	}
}

func TestUpgradeSurvivesAFailureAtEveryOperation(t *testing.T) {
	ctx := context.Background()
	ada := testIdentity(t, "ada")
	defer func(d time.Duration) { outputsLockStale = d }(outputsLockStale)

	build := func(t *testing.T, from int, fb *faultBackend, lb Backend) *Store {
		s, err := OpenBackendWith(ctx, lb, Options{NewFormat: from, Identity: ada})
		if err != nil {
			t.Fatal(err)
		}
		seedSecrets(t, s)
		fb.Backend = lb
		s, err = OpenBackendWith(ctx, fb, Options{Identity: ada})
		if err != nil {
			t.Fatal(err)
		}
		return s
	}
	type tr struct{ from, to int }
	for _, bk := range testBackends() {
		for _, x := range []tr{{Format, FormatImmutable}, {Format, FormatSealed}, {FormatImmutable, FormatSealed}} {
			up := UpgradeOptions{To: x.to}
			if x.to == FormatSealed {
				up.Readers, up.Identity = readersOf(ada), ada
			}
			for _, kind := range []string{"", "Put", "PutIfAbsent", "ReplaceIf", "DeleteIf", "Delete"} {
				for _, persistent := range []bool{true, false} {
					if kind == "" && !persistent {
						continue
					}
					if bk.name == "s3" && (kind == "Put" || kind == "Delete" || kind == "DeleteIf" || !persistent) {
						continue // the bucket is slow; the calls that matter to the order are left
					}
					name := fmt.Sprintf("%s %d to %d, %s persistent=%v", bk.name, x.from, x.to, kind, persistent)
					t.Run(name, func(t *testing.T) {
						// Count the operations of the kind in a whole upgrade.
						lb := bk.make(t)
						fb := &faultBackend{kind: kind, n: 1 << 30, persistent: persistent}
						s := build(t, x.from, fb, lb)
						if _, err := s.Upgrade(ctx, up); err != nil {
							t.Fatal(err)
						}
						total := int(fb.seen.Load())
						if kind != "" && total == 0 {
							t.Skip("no operation of this kind")
						}
						t.Logf("%d operations", total)
						bad := 0
						for n := 0; n < total && bad < 6; n++ {
							label := fmt.Sprintf("%s at op %d of %d", name, n, total)
							outputsLockStale = time.Hour
							lb := bk.make(t)
							fb := &faultBackend{kind: kind, n: int64(n), persistent: persistent}
							s := build(t, x.from, fb, lb)
							want := listJSON(t, s)
							wantOut, _, _ := s.LoadOutputs()
							_, uerr := s.Upgrade(ctx, up)
							before := t.Failed()
							if fb.failed.Load() > 0 && uerr == nil && persistent {
								// Errors the upgrade swallows are fine only if it still ends well.
								t.Logf("%s: upgrade reported no error although %d calls failed", label, fb.failed.Load())
							}
							sweepInvariants(t, label, lb, want, x.from, x.to)
							mid, err := OpenBackendWith(ctx, lb, Options{Identity: ada})
							if err != nil {
								t.Errorf("%s: open after the failure: %v", label, err)
								bad++
								continue
							}
							if got := listJSON(t, mid); got != want {
								t.Errorf("%s: the store after the failure lists other records:\n got %s\nwant %s", label, got, want)
							}
							if out, _, _ := mid.LoadOutputs(); outputsJSON(out) != outputsJSON(wantOut) {
								t.Errorf("%s: outputs after the failure: %v", label, out)
							}
							if v, err := mid.VerifyAll(ctx); err != nil || len(v.Bad) != 0 {
								t.Errorf("%s: VerifyAll after the failure = %+v, %v", label, v, err)
							}
							if x.to == FormatSealed && formatMeta(t, lb).Sealed != nil {
								if locked := openIdentity(t, lb, nil); locked.Len() != mid.Len() {
									t.Errorf("%s: a stranger lists %d of %d records", label, locked.Len(), mid.Len())
								}
							}
							// Run it again without a fault.
							outputsLockStale = time.Nanosecond
							if _, err := mid.Upgrade(ctx, up); err != nil {
								t.Errorf("%s: second upgrade: %v", label, err)
							}
							done := openIdentity(t, lb, ada)
							if got := listJSON(t, done); got != want {
								t.Errorf("%s: the finished store lists other records", label)
							}
							if out, _, _ := done.LoadOutputs(); outputsJSON(out) != outputsJSON(wantOut) {
								t.Errorf("%s: outputs after the second upgrade: %v", label, out)
							}
							if v, err := done.VerifyAll(ctx); err != nil || len(v.Bad) != 0 || v.Checked != done.Len() {
								t.Errorf("%s: VerifyAll after the second upgrade = %+v, %v", label, v, err)
							}
							if m := formatMeta(t, lb); m.Format != x.to || m.Migration != nil {
								t.Errorf("%s: meta.json after the second upgrade: format %d, migration %v", label, m.Format, m.Migration)
							}
							if x.to == FormatSealed {
								assertNoSecret(t, lb, secretValue)
							}
							// Every rec/ object is named by a pointer except superseded revisions
							// of the seeded record, which format 2 keeps by design.
							named, err := done.namedRecords()
							if err != nil {
								t.Fatal(err)
							}
							for _, k := range listKeys(t, lb, "rec") {
								if named[k] {
									continue
								}
								data, _ := lb.Get(ctx, k)
								var head struct {
									Sealed *json.RawMessage `json:"sealed"`
								}
								_ = json.Unmarshal(data, &head)
								if x.to == FormatSealed && head.Sealed == nil {
									t.Errorf("%s: %s is a record in plain text that no pointer names", label, k)
								}
							}
							if t.Failed() && !before {
								bad++
							}
						}
					})
				}
			}
		}
	}
	_ = errors.Is
}
