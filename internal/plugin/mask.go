package plugin

import (
	"io"
	"strings"
	"sync"

	"github.com/pravo-systems/pravo/internal/sensitivity"
)

// minMasked is the shortest secret that is masked in the log of a provider. A value of one or two
// characters would turn the log into noise and hides nothing.
const minMasked = 4

var (
	maskMu      sync.RWMutex
	maskedBytes []string
)

// MaskSecret registers a value that a run read from the sealed state, so that the log of a
// provider (-v, -vv) shows (secret) where the value, or its URL-escaped or JSON-escaped form,
// would be. The docs promise that a secret is never written to a log.
func MaskSecret(v string) {
	if len(v) < minMasked {
		return
	}
	// the one list of forms that every surface looks for, with a lower floor than the viewer's
	forms := append([]string{v}, sensitivity.FormsAtLeast(v, minMasked)...)
	maskMu.Lock()
	defer maskMu.Unlock()
	for _, f := range forms {
		have := false
		for _, m := range maskedBytes {
			if m == f {
				have = true
				break
			}
		}
		if !have {
			maskedBytes = append(maskedBytes, f)
		}
	}
}

func maskLine(s string) string {
	maskMu.RLock()
	defer maskMu.RUnlock()
	for _, m := range maskedBytes {
		if strings.Contains(s, m) {
			s = strings.ReplaceAll(s, m, "(secret)")
		}
	}
	return s
}

type maskWriter struct{ w io.Writer }

func (m maskWriter) Write(p []byte) (int, error) {
	if _, err := io.WriteString(m.w, maskLine(string(p))); err != nil {
		return 0, err
	}
	return len(p), nil
}
