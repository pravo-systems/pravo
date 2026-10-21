package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func write(t *testing.T, root, name, content string) {
	t.Helper()
	p := filepath.Join(root, name)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestFindModuleRoot(t *testing.T) {
	d := t.TempDir()
	write(t, d, "cue.mod/module.cue", "module: \"example.com/x@v0\"\nlanguage: version: \"v0.14.0\"\n")
	write(t, d, "envs/dev/main.cue", "package dev\n")
	got := FindModuleRoot(filepath.Join(d, "envs", "dev"))
	// macOS temp dirs resolve through /private; compare after evaluating links.
	want, _ := filepath.EvalSymlinks(d)
	gotR, _ := filepath.EvalSymlinks(got)
	if gotR != want {
		t.Errorf("FindModuleRoot = %q, want %q", got, d)
	}
	if got := FindModuleRoot(t.TempDir()); got != "" {
		t.Errorf("a directory with no cue.mod above it should give \"\", got %q", got)
	}
}

func TestDefaultRegistry(t *testing.T) {
	if got := DefaultRegistry("example.com/x@v0", "cue.dev/x"); got != "" {
		t.Errorf("no github.com module: want empty, got %q", got)
	}
	got := DefaultRegistry("github.com/acme/vpc@v1", "github.com/acme/other@v0", "github.com/zed/q@v0", "example.com/y@v0")
	want := "github.com/acme=ghcr.io/acme/cue,github.com/zed=ghcr.io/zed/cue,registry.cue.works"
	if got != want {
		t.Errorf("DefaultRegistry = %q, want %q", got, want)
	}
}

func TestConfigureRegistryRespectsUser(t *testing.T) {
	t.Setenv("CUE_REGISTRY", "my.registry.example")
	d := t.TempDir()
	write(t, d, "cue.mod/module.cue", "module: \"github.com/acme/x@v0\"\nlanguage: version: \"v0.14.0\"\n")
	ConfigureRegistry(d)
	if os.Getenv("CUE_REGISTRY") != "my.registry.example" {
		t.Errorf("an explicit CUE_REGISTRY must not be overridden, got %q", os.Getenv("CUE_REGISTRY"))
	}
	t.Setenv("CUE_REGISTRY", "")
	os.Unsetenv("CUE_REGISTRY")
	ConfigureRegistry(d)
	if !strings.Contains(os.Getenv("CUE_REGISTRY"), "github.com/acme=ghcr.io/acme/cue") {
		t.Errorf("default mapping not set: %q", os.Getenv("CUE_REGISTRY"))
	}
}

func TestModuleRootOf(t *testing.T) {
	root, ver := moduleRootOf("/c/mod/extract/github.com/acme/vpc@v1.2.0/net/net.cue")
	if root != "/c/mod/extract/github.com/acme/vpc@v1.2.0" || ver != "v1.2.0" {
		t.Errorf("moduleRootOf = %q %q", root, ver)
	}
}

// A shared package elsewhere in the module is part of the specification:
// changing it must change the fingerprint of a root that imports it.
func TestFingerprintCoversSharedPackages(t *testing.T) {
	d := t.TempDir()
	write(t, d, "cue.mod/module.cue", "module: \"example.com/infra@v0\"\nlanguage: version: \"v0.14.0\"\n")
	write(t, d, "modules/net/net.cue", "package net\n\n#Net: {name: string}\n")
	write(t, d, "envs/dev/main.cue", "package dev\n\nimport \"example.com/infra/modules/net\"\n\nn: net.#Net & {name: \"x\"}\n")
	r, err := Load(filepath.Join(d, "envs", "dev"), LoadOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if !r.SharedModule() {
		t.Error("the root lives below its module root")
	}
	f1, err := r.Fingerprint()
	if err != nil || len(f1) != 32 {
		t.Fatalf("fingerprint %q %v", f1, err)
	}
	found := false
	for _, s := range r.Sources {
		if strings.HasSuffix(s, filepath.Join("modules", "net", "net.cue")) {
			found = true
		}
	}
	if !found {
		t.Errorf("the shared package is not among the loaded sources: %v", r.Sources)
	}
	// Same content, same fingerprint, even when loaded again.
	r2, _ := Load(filepath.Join(d, "envs", "dev"), LoadOptions{})
	if f2, _ := r2.Fingerprint(); f2 != f1 {
		t.Error("fingerprint is not stable across loads")
	}
	write(t, d, "modules/net/net.cue", "package net\n\n#Net: {name: string, extra: *1 | int}\n")
	r3, err := Load(filepath.Join(d, "envs", "dev"), LoadOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if f3, _ := r3.Fingerprint(); f3 == f1 {
		t.Error("changing a shared package must change the fingerprint")
	}
}

func TestUnusedPackageDoesNotAffectFingerprint(t *testing.T) {
	d := t.TempDir()
	write(t, d, "cue.mod/module.cue", "module: \"example.com/infra@v0\"\nlanguage: version: \"v0.14.0\"\n")
	write(t, d, "envs/dev/main.cue", "package dev\n\nx: 1\n")
	write(t, d, "modules/other/o.cue", "package other\n\ny: 1\n")
	r, _ := Load(filepath.Join(d, "envs", "dev"), LoadOptions{})
	f1, _ := r.Fingerprint()
	write(t, d, "modules/other/o.cue", "package other\n\ny: 2\n")
	r2, _ := Load(filepath.Join(d, "envs", "dev"), LoadOptions{})
	if f2, _ := r2.Fingerprint(); f2 != f1 {
		t.Error("a package the root does not import must not change its fingerprint")
	}
}
