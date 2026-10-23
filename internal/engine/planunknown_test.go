package engine

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/pravo-systems/pravo/internal/state"
)

// unknownBackend answers a conditional write of one key with
// state.ErrUnknownOutcome, as a bucket does that lost the answer of a write and
// could not read the object to see whether the write was made. With made set the
// write is made first.
type unknownBackend struct {
	state.Backend
	key  string
	op   string
	made bool
	hits int
}

func (u *unknownBackend) unknown(op, key string) error {
	if op != u.op || key != u.key {
		return nil
	}
	u.hits++
	return fmt.Errorf("s3 conditional put %s: %w", key, state.ErrUnknownOutcome)
}

func (u *unknownBackend) PutIfAbsent(ctx context.Context, key string, data []byte) (bool, error) {
	err := u.unknown("PutIfAbsent", key)
	if err == nil {
		return u.Backend.PutIfAbsent(ctx, key, data)
	}
	if u.made {
		_, _ = u.Backend.PutIfAbsent(ctx, key, data)
	}
	return false, err
}

func (u *unknownBackend) ReplaceIf(ctx context.Context, key string, update func([]byte) ([]byte, bool)) (bool, error) {
	err := u.unknown("ReplaceIf", key)
	if err == nil {
		return u.Backend.ReplaceIf(ctx, key, update)
	}
	if u.made {
		_, _ = u.Backend.ReplaceIf(ctx, key, update)
	}
	return false, err
}

// storing returns the plan store and a plan to put in it, with the key that Put
// writes the plan to.
func unknownPlan(t *testing.T, ps *PlanStore) (data []byte, meta *PlanMeta, id string) {
	t.Helper()
	pf := &PlanFile{
		Format: PlanFileFormat, Created: t0, Command: "pravo plan --store", Lineage: "lineage-1", ConfigFingerprint: "fp-1",
		Providers: map[string]string{"test": "1.0.0"}, Changes: []savedChange{change("a", ActionCreate, 0)},
	}
	data, err := indentJSON(pf)
	if err != nil {
		t.Fatal(err)
	}
	meta = newPlanMeta(pf, "alice", time.Hour, len(data))
	return data, meta, digestID(data, planIDHex)
}

func wantAdvice(t *testing.T, what string, err error) {
	t.Helper()
	if !errors.Is(err, state.ErrUnknownOutcome) || !strings.Contains(err.Error(), "run the command again") {
		t.Errorf("%s: %v, want ErrUnknownOutcome and the advice to run the command again", what, err)
	}
}

// A plan that is stored by a write of unknown outcome is read, as it is after a
// refusal: the plan that is there is the same plan, or it is not there and the
// command is told to run again.
func TestStoringAPlanWhoseWriteIsOfUnknownOutcome(t *testing.T) {
	ctx := context.Background()
	eachBackend(t, func(t *testing.T, ps *PlanStore, clock *time.Time) {
		data, meta, id := unknownPlan(t, ps)

		t.Run("made", func(t *testing.T) {
			u := &unknownBackend{Backend: ps.B, key: planKey(id), op: "PutIfAbsent", made: true}
			hooked := &PlanStore{B: u, Now: ps.Now}
			if err := hooked.Put(ctx, data, meta); err != nil {
				t.Fatalf("Put = %v, want the plan that is there to be taken for this one", err)
			}
			if u.hits != 1 {
				t.Errorf("the unknown write was made %d times", u.hits)
			}
			if _, err := ps.B.Get(ctx, metaKey2(id)); err != nil {
				t.Errorf("the summary of the plan was not stored: %v", err)
			}
		})
	})
	eachBackend(t, func(t *testing.T, ps *PlanStore, clock *time.Time) {
		data, meta, id := unknownPlan(t, ps)
		t.Run("not made", func(t *testing.T) {
			u := &unknownBackend{Backend: ps.B, key: planKey(id), op: "PutIfAbsent"}
			err := (&PlanStore{B: u, Now: ps.Now}).Put(ctx, data, meta)
			wantAdvice(t, "Put", err)
		})
	})
	eachBackend(t, func(t *testing.T, ps *PlanStore, clock *time.Time) {
		data, meta, id := unknownPlan(t, ps)
		t.Run("summary", func(t *testing.T) {
			u := &unknownBackend{Backend: ps.B, key: metaKey2(id), op: "PutIfAbsent"}
			err := (&PlanStore{B: u, Now: ps.Now}).Put(ctx, data, meta)
			wantAdvice(t, "Put", err)
		})
	})
}

// The progress of a plan is updated by a read and a conditional write. A write of
// unknown outcome is an error that says to run the command again, and not a
// success or a refusal, because the edit is not necessarily one to make twice.
func TestProgressWhoseWriteIsOfUnknownOutcome(t *testing.T) {
	ctx := context.Background()
	for _, op := range []string{"PutIfAbsent", "ReplaceIf"} {
		eachBackend(t, func(t *testing.T, ps *PlanStore, clock *time.Time) {
			t.Run(op, func(t *testing.T) {
				m := putPlan(t, ps, t0, "alice", time.Hour, change("a", ActionCreate, 0))
				// The first apply creates the progress, the second replaces it.
				if op == "ReplaceIf" {
					if err := ps.BeginApply(ctx, m.ID, "bob#1"); err != nil {
						t.Fatal(err)
					}
				} else {
					_ = ps.B.Delete(ctx, progressKey(m.ID))
				}
				u := &unknownBackend{Backend: ps.B, key: progressKey(m.ID), op: op, made: true}
				err := (&PlanStore{B: u, Now: ps.Now}).BeginApply(ctx, m.ID, "alice#1")
				if u.hits == 0 {
					t.Fatalf("the store did not write the progress with %s", op)
				}
				wantAdvice(t, "BeginApply", err)
			})
		})
	}
}
