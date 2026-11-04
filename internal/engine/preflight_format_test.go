package engine

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/pravo-systems/pravo/internal/state"
)

// metaDenied is a bucket whose policy lets a writer put every object but meta.json.
type metaDenied struct{ state.Backend }

var errDenied = errors.New("s3 conditional put meta.json: AccessDenied")

func (b metaDenied) ReplaceIf(ctx context.Context, key string, update func([]byte) ([]byte, bool)) (bool, error) {
	if key == "meta.json" {
		return false, errDenied
	}
	return b.Backend.ReplaceIf(ctx, key, update)
}

func (b metaDenied) Put(ctx context.Context, key string, data []byte) error {
	if key == "meta.json" {
		return errDenied
	}
	return b.Backend.Put(ctx, key, data)
}

// The first policy of a state raises meta.json. A key that may not put it is refused before
// the apply changes anything, with exit 5 and not the error of the write (F56).
func TestFirstPolicyOfAKeyThatCannotPutMetaIsRefusedBeforeAnyChange(t *testing.T) {
	b := newBoundHarness(t)
	plan := &Plan{Policies: []PolicyEntry{{Name: "no_destroy"}}}

	writer, err := state.OpenBackendWith(context.Background(), metaDenied{b.backend}, state.Options{Identity: b.id})
	if err != nil {
		t.Fatal(err)
	}
	if writer.Sealer() == nil || writer.Format() >= state.FormatPolicy {
		t.Fatalf("the state is at format %d, sealed %v; want a sealed state below the policies", writer.Format(), writer.Sealer() != nil)
	}
	e := &Engine{State: writer}
	err = e.preflightPolicies(plan)
	if exitCodeOf(err) != ExitRefused || err == nil || !strings.Contains(err.Error(), "does not yet hold policies") || strings.HasPrefix(err.Error(), "s3") {
		t.Fatalf("writer: %v, exit code %d; want exit code 5 and the refusal", err, exitCodeOf(err))
	}
	if err := (&Engine{State: writer}).preflightPolicies(&Plan{}); err != nil {
		t.Errorf("a plan with no policy is refused: %v", err)
	}

	// The key that may put meta.json raises the format, and the next writer finds it done.
	e = &Engine{State: b.state}
	if err := e.preflightPolicies(plan); err != nil {
		t.Fatalf("admin: %v", err)
	}
	if b.state.Format() < state.FormatPolicy {
		t.Errorf("the format is %d after the admin's check", b.state.Format())
	}
	writer2, err := state.OpenBackendWith(context.Background(), metaDenied{b.backend}, state.Options{Identity: b.id})
	if err != nil {
		t.Fatal(err)
	}
	if err := (&Engine{State: writer2}).preflightPolicies(plan); err != nil {
		t.Errorf("writer after the admin: %v", err)
	}
}
