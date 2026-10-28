package serve

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The bar of the page fits a phone: with seven sections the word beside the mark goes.
func TestTopBarFitsAPhone(t *testing.T) {
	css, err := os.ReadFile(filepath.Join("web", "assets", "app.css"))
	if err != nil {
		t.Skip("the interface is not in the tree")
	}
	i := strings.Index(string(css), "@media (max-width: 40em)")
	if i < 0 || !strings.Contains(string(css)[i:], ".brand .word { display: none; }") {
		t.Error("app.css does not make room for the seven sections on a phone")
	}
}

// A reader name stays whole beside the keys when the page has three panes: the cell
// of a name may break anywhere, and has a width that keeps it from breaking into letters.
func TestReaderNamesDoNotBreakIntoLetters(t *testing.T) {
	css, err := os.ReadFile(filepath.Join("web", "assets", "access.css"))
	if err != nil {
		t.Skip("the interface is not in the tree")
	}
	if !strings.Contains(string(css), ".ac-table tbody th { font-weight: 500; min-width: 4rem; }") {
		t.Error("access.css lets the name of a reader shrink to one letter")
	}
}

// The badge of a sealed record gives up its word beside another badge, so that the
// narrow status column of the table does not cut it.
func TestSealedBadgeGivesWayInTheTable(t *testing.T) {
	js, err := os.ReadFile(filepath.Join("web", "assets", "table.js"))
	if err != nil {
		t.Skip("the interface is not in the tree")
	}
	if !strings.Contains(string(js), "stat.length ? null : 'sealed'") {
		t.Error("table.js writes the word sealed beside other badges")
	}
}
