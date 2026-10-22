package state

import (
	"fmt"
	"os"
	"strconv"
	"testing"

	"github.com/pravo-systems/pravo/internal/protect"
)

// TestMain lets PRAVO_TEST_FORMAT=2 run this suite over stores of format 2, and 3
// over stores sealed to one identity, as statetest does for the packages that
// import it.
func TestMain(m *testing.M) {
	if v := os.Getenv("PRAVO_TEST_FORMAT"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < Format || n > FormatSealed {
			fmt.Fprintf(os.Stderr, "PRAVO_TEST_FORMAT=%q: want %d, %d or %d\n", v, Format, FormatImmutable, FormatSealed)
			os.Exit(2)
		}
		NewStoreFormat = n
		if n == FormatSealed {
			id, err := protect.NewIdentity("test", "")
			if err != nil {
				fmt.Fprintln(os.Stderr, err)
				os.Exit(2)
			}
			NewStoreIdentity = id
		}
	}
	os.Exit(m.Run())
}

// skipWhenSealed skips a test that depends on the stores it makes being unsealed
// or on opening a sealed one with no identity, when the suite runs over sealed
// stores (PRAVO_TEST_FORMAT=3).
func skipWhenSealed(t *testing.T) {
	t.Helper()
	if NewStoreIdentity != nil {
		t.Skip("the suite runs over sealed stores, and this test needs one that is not")
	}
}
