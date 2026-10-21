package main

import (
	"reflect"
	"testing"
)

func TestExtract(t *testing.T) {
	src := "---\ntitle: x\n---\n\n" +
		"```hcl title=\"main.tf\" hl=\"2\"\nvariable \"a\" {}\n```\n\n" +
		"```cue\nx: 1\n```\n\n" +
		"````text\n```hcl\nnot a block\n```\n````\n\n" +
		"~~~hcl file=modules/m/main.tf\noutput \"o\" { value = 1 }\n~~~\n\n" +
		"   ```hcl\n   locals {}\n   ```\n"
	got, err := extract("p.md", src)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("got %d blocks, want 3: %+v", len(got), got)
	}
	if got[0].Path != "main.tf" || got[0].Body != "variable \"a\" {}\n" || got[0].Line != 5 || got[0].N != 1 {
		t.Errorf("block 1: %+v", got[0])
	}
	if got[1].Path != "modules/m/main.tf" || got[1].N != 2 {
		t.Errorf("block 2: %+v", got[1])
	}
	if got[2].Body != "locals {}\n" || got[2].Path != "main.tf" {
		t.Errorf("block 3 (indented fence): %+v", got[2])
	}
}

func TestMarkers(t *testing.T) {
	src := "text\n\n<!-- hclcheck: expect=error match=\"Missing resource\" -->\n```hcl\nx\n```\n\n" +
		"```hcl skip=\"needs a cluster\"\ny\n```\n\n" +
		"<!-- not a marker -->\n```hcl\nz\n```\n"
	got, err := extract("p.md", src)
	if err != nil {
		t.Fatal(err)
	}
	if got[0].Expect != "error" || got[0].Match != "Missing resource" {
		t.Errorf("comment marker: %+v", got[0])
	}
	if got[1].Skip != "needs a cluster" {
		t.Errorf("info marker: %+v", got[1])
	}
	if got[2].Expect != "" || got[2].Skip != "" {
		t.Errorf("a plain comment is no marker: %+v", got[2])
	}
	for _, bad := range []string{
		"```hcl expect=warning\nx\n```\n",
		"```hcl skip\nx\n```\n",
		"```hcl match=abc\nx\n```\n",
	} {
		if _, err := extract("p.md", bad); err == nil {
			t.Errorf("%q: want an error", bad)
		}
	}
}

func TestPathOf(t *testing.T) {
	cases := []struct {
		file, title string
		path        string
		excerpt     bool
		files       []string
	}{
		{"", "", "main.tf", false, []string{"main.tf"}},
		{"", "variables.tf", "variables.tf", false, []string{"variables.tf"}},
		{"", "envs/prod/main.tf", "envs/prod/main.tf", false, []string{"envs/prod/main.tf"}},
		{"", "platform/main.tf, lines 8-41", "platform/main.tf", true, []string{"platform/main.tf"}},
		{"", "modules/keys/variables.tf, main.tf", "modules/keys/variables.tf", false, []string{"modules/keys/variables.tf", "main.tf"}},
		{"team.auto.tfvars", "team.auto.tfvars", "team.auto.tfvars", false, []string{"team.auto.tfvars"}},
		{"a.tf", "something else", "a.tf", false, []string{"a.tf"}},
		{"", "an example", "main.tf", false, []string{"main.tf"}},
	}
	for _, c := range cases {
		path, excerpt, files := pathOf(c.file, c.title)
		if path != c.path || excerpt != c.excerpt || !reflect.DeepEqual(files, c.files) {
			t.Errorf("pathOf(%q, %q) = %q, %v, %v", c.file, c.title, path, excerpt, files)
		}
	}
}

func TestSplitInfo(t *testing.T) {
	got, err := splitInfo(`hcl title="a b, c" hl='1-2' x=y`)
	if err != nil || !reflect.DeepEqual(got, []string{"hcl", "title=a b, c", "hl=1-2", "x=y"}) {
		t.Errorf("got %q, %v", got, err)
	}
	if _, err := splitInfo(`hcl title="open`); err == nil {
		t.Error("want an error for an unterminated quote")
	}
}
