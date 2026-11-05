package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The import command starts the provider and configures it from its block, as every other command does:
// before, it configured the provider with an empty block, so a provider that needs a region or an endpoint
// (the AWS provider) refused with "invalid AWS Region" and any credentials of the block were not used.
func TestImportConfiguresTheProviderFromItsBlock(t *testing.T) {
	rt := &runContext{}

	p := newDemoProvider(t, demoObject{"srv-1", "web", "eu"})
	spec := "package estate\n\nimport demo \"pravo.dev/providers/demo/demo\"\n\n$demo: demo.#Provider & {$path: " + fmt.Sprintf("%q", demoBinary) + ", token: \"tk-import-7\"}\nweb: $demo.#Resource.demo_server & {name: \"web\"}\n"
	dir := specDir(t, map[string]string{"main.cue": spec})
	if text, code := runPravo(t, rt, dir, "init"); code != 0 {
		t.Fatalf("init exited %d:\n%s", code, text)
	}
	if text, code := runPravo(t, rt, dir, "import", "web", "srv-1"); code != 0 {
		t.Fatalf("import exited %d:\n%s", code, text)
	}
	b, err := os.ReadFile(filepath.Join(p.dir, "received"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "token=tk-import-7") {
		t.Errorf("the provider was not given the token of its block: %q", b)
	}
}
