package backendtest_test

import (
	"testing"
	"time"

	"github.com/pravo-systems/pravo/internal/state"
	"github.com/pravo-systems/pravo/pravo"
	"github.com/pravo-systems/pravo/pravo/backendtest"
)

func TestMemoryBackendHonoursTheContract(t *testing.T) {
	backendtest.Run(t, func(t *testing.T) pravo.Backend { return pravo.NewMemoryBackend("memory") })
}

func TestMemoryBackendWithAClockHonoursTheContract(t *testing.T) {
	backendtest.Run(t, func(t *testing.T) pravo.Backend {
		b := state.NewMemBackend("memory")
		b.Clock = time.Now
		return b
	})
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
