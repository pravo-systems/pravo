package main

import (
	"reflect"
	"strings"
	"testing"
)

func TestStripComments(t *testing.T) {
	src := "a = 1 # note\nb = \"# kept\" // gone\n/* x\ny */ c = 2\n"
	got := stripComments(src)
	if strings.Count(got, "\n") != strings.Count(src, "\n") {
		t.Errorf("line count changed: %q", got)
	}
	if !strings.Contains(got, `"# kept"`) || !strings.Contains(got, "c = 2") {
		t.Errorf("code was removed: %q", got)
	}
	for _, gone := range []string{"note", "gone", "x\n", "y *"} {
		if strings.Contains(got, gone) {
			t.Errorf("comment text %q survived in %q", gone, got)
		}
	}
}

func TestSkipString(t *testing.T) {
	for _, s := range []string{
		`"plain"`,
		`"a ${local.m["k"]} b"`,
		`"escaped \" quote"`,
		`"literal $${not} and %%{not}"`,
		`"${join(",", [for x in var.l : "${x}-y"])}"`,
	} {
		if got := skipString(s+" tail", 0); got != len(s) {
			t.Errorf("skipString(%s) = %d, want %d", s, got, len(s))
		}
	}
}

func TestBlocksAndAttrs(t *testing.T) {
	src := stripComments(`
variable "filename" {}
variable "p" { default = "0600" }

locals {
  a = 1
  b = {
    c = 2
    d = "x ${var.y["z"]}"
  }
  e = [1, 2,
       3]
}

resource "aws_s3_bucket" "b" {
  for_each = toset(["x"])
  tags = { Owner = "p" }
}

terraform {
  required_providers {
    aws = {
      source = "hashicorp/aws"
    }
    random = { source = "hashicorp/random" }
  }
}
`)
	bs := blocks(src)
	var heads []string
	for _, b := range bs {
		heads = append(heads, b.Type+" "+strings.Join(b.Labels, "."))
	}
	want := []string{"variable filename", "variable p", "locals ", "resource aws_s3_bucket.b", "terraform "}
	if !reflect.DeepEqual(heads, want) {
		t.Fatalf("blocks %q, want %q", heads, want)
	}
	if got := attrs(bs[2].Body); !reflect.DeepEqual(got, []string{"a", "b", "e"}) {
		t.Errorf("locals attrs %q", got)
	}
	if got := attrs(bs[3].Body); !reflect.DeepEqual(got, []string{"for_each", "tags"}) {
		t.Errorf("resource attrs %q", got)
	}
	tf := blocks(bs[4].Body)
	if len(tf) != 1 || tf[0].Type != "required_providers" {
		t.Fatalf("terraform sub-blocks %+v", tf)
	}
	if got := attrs(tf[0].Body); !reflect.DeepEqual(got, []string{"aws", "random"}) {
		t.Errorf("required_providers names %q", got)
	}
	if v, ok := attrValue(bs[1].Body, "default"); !ok || v != `"0600"` {
		t.Errorf("attrValue default = %q, %v", v, ok)
	}
	if _, ok := attrValue(bs[1].Body, "type"); ok {
		t.Error("attrValue found a missing attribute")
	}
}
