package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// These tests run the real tofu. They use terraform_data, which tofu
// provides itself, so they need no network.
func needTofu(t *testing.T) *runner {
	t.Helper()
	path, err := exec.LookPath("tofu")
	if err != nil {
		t.Skip("tofu is not installed")
	}
	return newRunner(path, t.TempDir(), 2*time.Minute)
}

func page(t *testing.T, name, src string) []Block {
	t.Helper()
	bs, err := extract(name, src)
	if err != nil {
		t.Fatal(err)
	}
	return bs
}

func TestCheckPage(t *testing.T) {
	r := needTofu(t)
	src := "```hcl title=\"variables.tf\"\nvariable \"n\" {\n  type = number\n}\n```\n\n" +
		"```hcl title=\"main.tf\"\nresource \"terraform_data\" \"x\" {\n  input = var.n + var.m\n}\n```\n\n" +
		"```hcl title=\"main.tf, lines 1-3\"\nlocals {\n  a = 1\n}\n```\n\n" +
		"```hcl title=\"bad.tf\"\nresource \"terraform_data\" \"y\" {\n  nope = 1\n}\n```\n\n" +
		"<!-- hclcheck: expect=error match=\"nope\" -->\n```hcl title=\"worse.tf\"\nresource \"terraform_data\" \"z\" {\n  nope = 1\n}\n```\n\n" +
		"<!-- hclcheck: expect=error -->\n```hcl title=\"fine.tf\"\nlocals {\n  b = 1\n}\n```\n\n" +
		"```hcl title=\"ugly.tf\"\nlocals {\nc=1\n}\n```\n\n" +
		"```hcl skip=\"needs a cluster\"\nlocals {}\n```\n\n" +
		"```hcl title=\"one.tf, two.tf\"\nlocals {}\n```\n"
	res := r.checkPage(t.TempDir(), page(t, "p.md", src))
	type want struct{ status, stage, note string }
	wants := []want{
		{statusOK, "", ""},
		{statusStubs, "", "var.m"},
		{statusStubs, "", ""}, // lines 1-3 appends to main.tf
		{statusFail, "validate", "nope"},
		{statusExpected, "validate", "nope"},
		{statusFail, "validate", "carried over: bad.tf:2"}, // bad.tf stays in the root
		{statusFail, "fmt", "tofu fmt would change"},
		{statusSkipped, "", "needs a cluster"},
		{statusFail, "assemble", "one block, one file"},
	}
	if len(res) != len(wants) {
		t.Fatalf("%d results", len(res))
	}
	for i, w := range wants {
		g := res[i]
		if g.Status != w.status || g.Stage != w.stage || !strings.Contains(g.Note, w.note) {
			t.Errorf("block %d: got %s/%s %q, want %+v\n%s", i+1, g.Status, g.Stage, g.Note, w, g.Detail)
		}
	}
}

func TestCheckPageExpectedErrorFoundNone(t *testing.T) {
	r := needTofu(t)
	src := "<!-- hclcheck: expect=error -->\n```hcl\nlocals {\n  a = 1\n}\n```\n\n" +
		"<!-- hclcheck: expect=error -->\n```hcl\nlocals {\n  b = nope\n}\n```\n\n" +
		"```hcl\nlocals {\n  c = 1\n}\n```\n"
	res := r.checkPage(t.TempDir(), page(t, "p.md", src))
	if res[0].Status != statusFail || !strings.Contains(res[0].Note, "expected an error and found none") {
		t.Errorf("block 1: %s %q", res[0].Status, res[0].Note)
	}
	if res[1].Status != statusExpected {
		t.Errorf("block 2: %s %q", res[1].Status, res[1].Note)
	}
	// Neither block above is left in the root: the third is valid on its own.
	if res[2].Status != statusOK {
		t.Errorf("block 3: %s %q\n%s", res[2].Status, res[2].Note, res[2].Detail)
	}
}

func TestCheckPageVarsFile(t *testing.T) {
	r := needTofu(t)
	src := "```hcl title=\"variables.tf\"\nvariable \"n\" {\n  type = number\n}\n```\n\n" +
		"```hcl title=\"terraform.tfvars\"\nn = 1\n```\n\n" +
		"```hcl title=\"other.tfvars\"\nn = \"text\"\n```\n"
	res := r.checkPage(t.TempDir(), page(t, "p.md", src))
	if res[0].Status != statusOK || res[1].Status != statusOK {
		t.Fatalf("%+v %+v", res[0], res[1])
	}
	// other.tfvars is not loaded by tofu console, so only terraform.tfvars is evaluated.
	if res[2].Status != statusOK {
		t.Errorf("got %s %q", res[2].Status, res[2].Note)
	}
}

func TestRunExitStatus(t *testing.T) {
	needTofu(t)
	dir := t.TempDir()
	good := filepath.Join(dir, "good.md")
	bad := filepath.Join(dir, "bad.md")
	os.WriteFile(good, []byte("```hcl\nlocals {\n  a = 1\n}\n```\n"), 0o644)
	os.WriteFile(bad, []byte("```hcl\nlocals {\n  a = nope\n}\n```\n"), 0o644)
	cache := t.TempDir()

	var out, errb bytes.Buffer
	if code := run([]string{"-cache", cache, good}, &out, &errb); code != 0 {
		t.Fatalf("good page: exit %d\n%s%s", code, out.String(), errb.String())
	}
	if !strings.Contains(out.String(), "1 blocks: 1 ok") {
		t.Errorf("summary missing:\n%s", out.String())
	}
	out.Reset()
	if code := run([]string{"-cache", cache, "-v", bad}, &out, &errb); code != 1 {
		t.Fatalf("bad page: exit %d\n%s", code, out.String())
	}
	if !strings.Contains(out.String(), "FAIL validate") || !strings.Contains(out.String(), "1 failed") {
		t.Errorf("failure not reported:\n%s", out.String())
	}
	if code := run([]string{"-tofu", "no-such-tofu-binary", "-cache", cache, good}, &out, &errb); code != 2 {
		t.Errorf("missing tofu: exit %d", code)
	}
	if code := run([]string{"-list", good, bad}, &out, &errb); code != 0 {
		t.Errorf("-list: exit %d", code)
	}
}
