package serve

import (
	"context"
	"testing"

	"github.com/pravo-systems/pravo/internal/state"
)

type lister struct{ state.Backend }

func (lister) ListVersions(context.Context, string) ([]state.ObjectVersion, error) { return nil, nil }

// The viewer reads a state through its snapshot when the backend lists versions, and a backend
// that does not is read as before.
func TestTheViewersReadOnlyBackendPassesVersionsOn(t *testing.T) {
	lb, err := state.NewLocalBackend(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ro, ok := ReadOnly(lister{lb}).(interface{ ListsVersions() bool })
	if !ok || !ro.ListsVersions() {
		t.Fatal("the viewer's backend hides the versions of the backend under it")
	}
	if ro, ok := ReadOnly(lb).(interface{ ListsVersions() bool }); !ok || ro.ListsVersions() {
		t.Fatal("the viewer's backend claims versions that the backend under it does not list")
	}
}
