package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// The migration demo embeds site/data/facts.json. The file is written by
// site/demos/facts/facts.go, which reads the state format and the plugin
// protocols from their sources; this test rereads them and fails when the file
// is stale. Run `go run site/demos/facts/facts.go` from the root of the module
// after a change to either source.
func TestFactsAreCurrent(t *testing.T) {
	if testing.Short() {
		t.Skip("rereading the sources runs a Go program")
	}
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "site", "demos", "facts", "facts.go")); err != nil {
		t.Skip("no site/demos/facts next to the tool")
	}
	goBin, err := exec.LookPath("go")
	if err != nil {
		t.Skip("no go command on the path")
	}
	cmd := exec.Command(goBin, "run", "site/demos/facts/facts.go", "-check")
	cmd.Dir = root
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
}
