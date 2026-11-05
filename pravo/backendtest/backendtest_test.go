package backendtest_test

import (
	"testing"

	"github.com/pravo-systems/pravo/internal/state"
	"github.com/pravo-systems/pravo/pravo"
	"github.com/pravo-systems/pravo/pravo/backendtest"
)

func TestMemoryBackendHonoursTheContract(t *testing.T) {
	backendtest.Run(t, func(t *testing.T) pravo.Backend { return pravo.NewMemoryBackend("memory") })
}

func TestLocalBackendHonoursTheContract(t *testing.T) {
	backendtest.Run(t, func(t *testing.T) pravo.Backend {
		b, err := state.NewLocalBackend(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		return b
	})
}
