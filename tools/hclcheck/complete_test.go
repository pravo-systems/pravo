package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func write(t *testing.T, root, name, body string) {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func read(t *testing.T, root, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(name)))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestCompleteStubs(t *testing.T) {
	ws := t.TempDir()
	write(t, ws, "envs/prod/main.tf", `
locals {
  zone = "a" # local.commented is not read
}

variable "declared" {}

resource "aws_subnet" "main" {
  cidr_block = "${local.base}.0/24"
  vpc_id     = aws_vpc.main.id
  tags       = { Env = var.env, D = var.declared, Z = local.zone }
  user_data  = file("${path.module}/boot.json")
}

module "logs" {
  source    = "../../modules/bucket"
  name      = "logs"
  providers = { local = local.primary }
}

output "arn" { value = module.logs.arn }

moved {
  from = aws_subnet.old
  to   = aws_subnet.main
}

provider "local" {
  alias = "primary"
}
`)
	add, err := complete(ws, "envs/prod")
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(add.providers, ","); got != "aws,local" {
		t.Errorf("providers %q", got)
	}
	stubs := strings.Join(add.stubs, ",")
	for _, want := range []string{"var.env", "local.base", "aws_vpc.main", "file boot.json", "module ../../modules/bucket"} {
		if !strings.Contains(stubs, want) {
			t.Errorf("stubs %q lack %q", stubs, want)
		}
	}
	for _, not := range []string{"var.declared", "local.zone", "local.commented", "local.primary", "aws_subnet.old"} {
		if strings.Contains(stubs, not) {
			t.Errorf("stubs %q hold %q", stubs, not)
		}
	}
	got := read(t, ws, "envs/prod/"+stubFile)
	for _, want := range []string{`aws = { source = "hashicorp/aws" }`, `variable "env" {}`, `resource "aws_vpc" "main"`, `cidr_block = "10.0.0.0/16"`} {
		if !strings.Contains(got, want) {
			t.Errorf("%s lacks %q:\n%s", stubFile, want, got)
		}
	}
	mod := read(t, ws, "modules/bucket/"+stubFile)
	for _, want := range []string{`variable "name" {}`, `output "arn"`, `local = { source = "hashicorp/local" }`} {
		if !strings.Contains(mod, want) {
			t.Errorf("stub module lacks %q:\n%s", want, mod)
		}
	}
	if strings.Contains(mod, `variable "source"`) || strings.Contains(mod, `variable "providers"`) {
		t.Errorf("stub module declares a meta-argument:\n%s", mod)
	}
	if read(t, ws, "envs/prod/boot.json") != "{}\n" {
		t.Error("boot.json is not a JSON fixture")
	}
}

func TestCompleteLeavesAWrittenModuleAndRequiredProviders(t *testing.T) {
	ws := t.TempDir()
	write(t, ws, "main.tf", `
terraform {
  required_providers {
    random = { source = "hashicorp/random" }
  }
}

module "m" {
  source = "./modules/m"
}

resource "local_file" "f" {
  filename = "a"
  content  = random_pet.p.id
}
`)
	write(t, ws, "modules/m/main.tf", `output "o" { value = 1 }`)
	// A stub file from an earlier step is removed.
	write(t, ws, "modules/m/"+stubFile, "garbage")
	add, err := complete(ws, ".")
	if err != nil {
		t.Fatal(err)
	}
	if len(add.providers) != 0 {
		t.Errorf("providers added to a root that declares its own: %v", add.providers)
	}
	if _, err := os.Stat(filepath.Join(ws, "modules/m", stubFile)); !os.IsNotExist(err) {
		t.Error("the stub module was kept beside a written module")
	}
	if strings.Join(add.stubs, ",") != "random_pet.p" {
		t.Errorf("stubs %v", add.stubs)
	}
}

func TestCompleteOverride(t *testing.T) {
	ws := t.TempDir()
	write(t, ws, "override.tf", "resource \"local_file\" \"config\" {\n  file_permission = \"0644\"\n}\n")
	add, err := complete(ws, ".")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(add.stubs, ",") != "local_file.config" || strings.Join(add.providers, ",") != "local" {
		t.Errorf("got %+v", add)
	}
	got := read(t, ws, stubFile)
	if !strings.Contains(got, `filename = "stub.txt"`) {
		t.Errorf("the base resource has no stub body:\n%s", got)
	}
}
