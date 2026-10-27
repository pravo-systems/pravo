package state

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"testing"

	"github.com/pravo-systems/pravo/internal/protect"
)

// Two commands seal the same state at the same moment: one begins, the other is
// refused or goes on from where the first is, and the state ends sealed, whole and
// without a value in plain text, whichever way they interleave.
func TestTwoSealsAtOnce(t *testing.T) {
	for _, bk := range testBackends() {
		for _, from := range []int{Format, FormatImmutable} {
			t.Run(bk.name, func(t *testing.T) {
				ctx := context.Background()
				ada, dan := testIdentity(t, "ada"), testIdentity(t, "dan")
				b := bk.make(t)
				seed, err := OpenBackendWith(ctx, b, Options{NewFormat: from})
				if err != nil {
					t.Fatal(err)
				}
				seedSecrets(t, seed)
				want := listJSON(t, seed)
				for round := range 4 {
					var wg sync.WaitGroup
					errs := make([]error, 2)
					for i, id := range []*protect.Identity{ada, dan} {
						s := openIdentity(t, b, id)
						wg.Add(1)
						go func() {
							defer wg.Done()
							_, errs[i] = s.Upgrade(ctx, UpgradeOptions{To: FormatSealed, Readers: readersOf(ada, dan), Identity: id})
						}()
					}
					wg.Wait()
					t.Logf("round %d: %v | %v", round, errs[0], errs[1])
					if errs[0] == nil || errs[1] == nil || round == 3 {
						// Finish whatever is left.
						if m := formatMeta(t, b); m.Migration != nil {
							if _, err := openIdentity(t, b, ada).Upgrade(ctx, UpgradeOptions{To: FormatSealed, Readers: readersOf(ada, dan), Identity: ada}); err != nil {
								t.Fatalf("finish: %v", err)
							}
						}
						break
					}
				}
				done := openIdentity(t, b, ada)
				if listJSON(t, done) != want {
					t.Errorf("the sealed store lists other records")
				}
				if v, err := done.VerifyAll(ctx); err != nil || len(v.Bad) != 0 {
					t.Errorf("VerifyAll = %+v, %v", v, err)
				}
				if p := done.Protection(); p.State != "sealed" || p.Plain != 0 {
					t.Errorf("Protection = %+v", p)
				}
				assertNoSecret(t, b, secretValue)
			})
		}
	}
}

// Two commands finish an interrupted seal at the same moment.
func TestTwoResumesAtOnce(t *testing.T) {
	ctx := context.Background()
	ada, dan := testIdentity(t, "ada"), testIdentity(t, "dan")
	up := func(id *protect.Identity) UpgradeOptions {
		return UpgradeOptions{To: FormatSealed, Readers: readersOf(ada, dan), Identity: id}
	}
	defer func(d time.Duration) { outputsLockStale = d }(outputsLockStale)
	for _, bk := range testBackends() {
		for _, crashAt := range []int{3, 6, 9, 12} {
			t.Run(fmt.Sprintf("%s after %d", bk.name, crashAt), func(t *testing.T) {
				outputsLockStale = time.Hour
				b := bk.make(t)
				seed, err := OpenBackendWith(ctx, b, Options{NewFormat: FormatImmutable, Identity: ada})
				if err != nil {
					t.Fatal(err)
				}
				seedSecrets(t, seed)
				want := listJSON(t, seed)
				fb := &faultBackend{Backend: b, n: int64(crashAt)}
				s, err := OpenBackendWith(ctx, fb, Options{Identity: ada})
				if err != nil {
					t.Fatal(err)
				}
				if _, err := s.Upgrade(ctx, up(ada)); !errors.Is(err, errCrash) {
					t.Fatalf("crash: %v", err)
				}
				outputsLockStale = time.Nanosecond
				var wg sync.WaitGroup
				errs := make([]error, 2)
				for i, id := range []*protect.Identity{ada, dan} {
					r := openIdentity(t, b, id)
					wg.Add(1)
					go func() {
						defer wg.Done()
						_, errs[i] = r.Upgrade(ctx, up(id))
					}()
				}
				wg.Wait()
				t.Logf("resumes: %v | %v", errs[0], errs[1])
				// Whatever they said, one more run finishes it.
				if _, err := openIdentity(t, b, ada).Upgrade(ctx, up(ada)); err != nil {
					t.Fatalf("finish: %v", err)
				}
				done := openIdentity(t, b, ada)
				if listJSON(t, done) != want {
					t.Errorf("the sealed store lists other records")
				}
				if v, err := done.VerifyAll(ctx); err != nil || len(v.Bad) != 0 {
					t.Errorf("VerifyAll = %+v, %v", v, err)
				}
				if p := done.Protection(); p.State != "sealed" || p.Plain != 0 {
					t.Errorf("Protection = %+v", p)
				}
				assertNoSecret(t, b, secretValue)
			})
		}
	}
}
