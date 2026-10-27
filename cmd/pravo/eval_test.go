package main

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// run executes pravo eval in dir and returns what it printed.
func runEval(t *testing.T, dir string, args ...string) (string, error) {
	t.Helper()
	old, _ := os.Getwd()
	if err := os.Chdir(dir); err != nil { //nolint:usetesting // see the deferred Chdir
		t.Fatal(err)
	}
	defer os.Chdir(old) //nolint:usetesting // the directory is put back after each call, so that a relative path in the next call means what it meant in the first
	r, w, _ := os.Pipe()
	stdout := os.Stdout
	os.Stdout = w
	cmd := evalCmd()
	cmd.SetArgs(args)
	cmd.SilenceUsage, cmd.SilenceErrors = true, true
	err := cmd.Execute()
	w.Close()
	os.Stdout = stdout
	b, _ := io.ReadAll(r)
	return string(b), err
}

func TestEvalPrintsTheEvaluatedValue(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "cue.mod"), 0o755)
	os.WriteFile(filepath.Join(dir, "cue.mod", "module.cue"), []byte("module: \"x.dev/learn\"\nlanguage: version: \"v0.14.0\"\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "a.cue"), []byte(`package learn

#Bucket: {
	name:   string
	region: "eu-west-1" | "us-east-1"
	tags:   [string]: string
}

logs:  #Bucket & {name: "logs", region: "eu-west-1", tags: team: "platform"}
sizes: [for s in ["a", "b"] {kb: len(s) * 10}]
env:   *"dev" | "prod" @tag(env)
open:  #Bucket & {name: "x"}
`), 0o644)

	out, err := runEval(t, dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"kb: 10", `env: "dev"`, `team: "platform"`} {
		if !strings.Contains(out, want) {
			t.Errorf("the evaluated output lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "for s in") || strings.HasPrefix(out, "{") {
		t.Errorf("comprehensions are expanded and the root has no braces:\n%s", out)
	}
	if out, _ := runEval(t, dir, "-e", "logs.tags.team"); strings.TrimSpace(out) != `"platform"` {
		t.Errorf("-e printed %q", out)
	}
	if out, _ := runEval(t, dir, "-e", "len(sizes)"); strings.TrimSpace(out) != "2" {
		t.Errorf("-e on an expression printed %q", out)
	}
	if _, err := runEval(t, dir, "-c"); err == nil || !strings.Contains(err.Error(), "not concrete") {
		t.Errorf("-c must refuse the incomplete value: %v", err)
	}
}
