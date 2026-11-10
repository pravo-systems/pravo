package engine

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// Every node that a test of this package leaves alone on the word of the plan
// memo is converted in full as well, whatever the test is about, and the two
// are compared (memoShadow). The harness gives each of its engines a memo, so
// the plans of all the tests are the plans of a command that has one.

var memoAudit struct {
	mu       sync.Mutex
	failures []string
}

func init() {
	memoShadow = func(e *Engine, problem string) {
		if e.Root.Dir != "" {
			if text, err := os.ReadFile(filepath.Join(e.Root.Dir, "main.cue")); err == nil {
				problem += "\n  specification:\n" + string(text)
			}
		}
		memoAudit.mu.Lock()
		defer memoAudit.mu.Unlock()
		memoAudit.failures = append(memoAudit.failures, problem)
	}
}

func memoAuditReport(code int) int {
	memoAudit.mu.Lock()
	defer memoAudit.mu.Unlock()
	if testing.Verbose() || len(memoAudit.failures) > 0 {
		fmt.Printf("memo audit: %d nodes left alone by the memo and converted as well, %d differ\n", memoChecks.Load(), len(memoAudit.failures))
	}
	if len(memoAudit.failures) == 0 {
		return code
	}
	shown := memoAudit.failures
	if len(shown) > 10 {
		shown = shown[:10]
	}
	fmt.Printf("memo audit: %d failures\n%s\n", len(memoAudit.failures), strings.Join(shown, "\n"))
	if code == 0 {
		code = 1
	}
	return code
}
