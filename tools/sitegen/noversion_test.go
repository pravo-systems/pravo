package main

import (
	"os"
	"strings"
	"testing"
)

// The landing page carries no pravo version number (a standing rule), also not
// one that a script writes into the page at load time.
func TestDownloadScriptShowsNoVersion(t *testing.T) {
	b, err := os.ReadFile("../../site/assets/download.js")
	if err != nil {
		t.Fatal(err)
	}
	for i, line := range strings.Split(string(b), "\n") {
		if strings.Contains(line, "rel.version") || strings.Contains(line, `"Version `) {
			t.Errorf("site/assets/download.js:%d writes a version number into the page: %s", i+1, strings.TrimSpace(line))
		}
	}
}
