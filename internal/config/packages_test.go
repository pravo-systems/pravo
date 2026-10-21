package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeFiles puts the files in a new directory and returns it.
func writeFiles(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, content := range files {
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// A file without a package clause is skipped by the CUE loader when another
// file has one, so a conflict in it would never be reported. Load names it.
func TestLoadReportsAFileWithoutAPackageClause(t *testing.T) {
	dir := writeFiles(t, map[string]string{
		"main.cue": "package estate\n\nsize: 3\n",
		"x.cue":    "size: 4\n",
	})
	for name, load := range map[string]func() error{
		"Load":         func() error { _, err := Load(dir, LoadOptions{}); return err },
		"LoadSettings": func() error { _, err := LoadSettings(dir); return err },
	} {
		err := load()
		if err == nil {
			t.Fatalf("%s: a file without a package clause was accepted", name)
		}
		if !strings.Contains(err.Error(), "x.cue has no package clause; the other files are package estate") {
			t.Errorf("%s: error = %v", name, err)
		}
		var none *NoSpecificationError
		if errors.As(err, &none) {
			t.Errorf("%s: the directory has a specification: %v", name, err)
		}
	}
}

func TestLoadReportsAFileOfAnotherPackage(t *testing.T) {
	dir := writeFiles(t, map[string]string{
		"a.cue": "package estate\n\nsize: 3\n",
		"b.cue": "package estate\n\nname: \"x\"\n",
		"y.cue": "package other\n\nsize: 4\n",
	})
	_, err := Load(dir, LoadOptions{})
	if err == nil || !strings.Contains(err.Error(), "y.cue is package other; the other files are package estate") {
		t.Fatalf("error = %v", err)
	}
	if strings.Contains(err.Error(), "a.cue") || strings.Contains(err.Error(), "b.cue") {
		t.Errorf("only the file that does not belong is named: %v", err)
	}
}

// With two files and two packages the first by name is the package of the
// directory; every file is named when several do not belong.
func TestPackageOfTheDirectoryAndEveryOffender(t *testing.T) {
	dir := writeFiles(t, map[string]string{
		"a.cue": "package one\n",
		"b.cue": "package two\n",
		"c.cue": "x: 1\n",
	})
	_, err := checkPackageClauses(dir)
	if err == nil {
		t.Fatal("no error")
	}
	for _, want := range []string{
		"b.cue is package two; the other files are package one",
		"c.cue has no package clause; the other files are package one",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error lacks %q:\n%v", want, err)
		}
	}
}

// The lock file has no package clause by design, and the loader never reads
// hidden files, files that start with an underscore, tests and tools.
func TestFilesTheLoaderIgnoresAreNotReported(t *testing.T) {
	dir := writeFiles(t, map[string]string{
		"main.cue":        "package estate\n\nsize: 3\n",
		"pravo.lock.cue":  "providers: \"registry.opentofu.org/hashicorp/random\": version: \"3.0.0\"\n",
		"_scratch.cue":    "size: 4\n",
		".hidden.cue":     "size: 5\n",
		"estate_test.cue": "package estate\nsize: 7\n",
		"estate_tool.cue": "size: 6\n",
		"notes.txt":       "not cue",
	})
	if _, err := checkPackageClauses(dir); err != nil {
		t.Fatalf("error = %v", err)
	}
	r, err := Load(dir, LoadOptions{})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if r.PkgName != "estate" {
		t.Errorf("package = %q", r.PkgName)
	}
}

// A file with a package clause and a syntax error is reported by the loader
// with its position.
func TestSyntaxErrorIsLeftToTheLoader(t *testing.T) {
	dir := writeFiles(t, map[string]string{
		"main.cue": "package estate\n\nsize: 3\n",
		"bad.cue":  "package estate\n\nsize: [\n",
	})
	if _, err := checkPackageClauses(dir); err != nil {
		t.Fatalf("error = %v", err)
	}
	_, err := Load(dir, LoadOptions{})
	if err == nil || !strings.Contains(err.Error(), "bad.cue") || strings.Contains(err.Error(), "no package clause") {
		t.Errorf("error = %v", err)
	}
}

func TestEmptyDirectoryHasNoSpecification(t *testing.T) {
	for name, files := range map[string]map[string]string{
		"empty":     {},
		"lock file": {"pravo.lock.cue": "providers: x: version: \"1.0.0\"\n"},
		"underscore, test and notes": {
			"_x.cue":     "package a\n",
			"a_test.cue": "package a\n",
			"notes.md":   "text",
		},
	} {
		dir := writeFiles(t, files)
		_, err := Load(dir, LoadOptions{})
		var none *NoSpecificationError
		if !errors.As(err, &none) {
			t.Errorf("%s: error = %v, want NoSpecificationError", name, err)
		}
		if r, err := LoadSettings(dir); err != nil || r == nil {
			t.Errorf("%s: LoadSettings = %v, %v; want an empty root", name, r, err)
		}
	}
}

// When no file has a package clause the loader reports it; the directory has
// files, so it is not an empty one.
func TestFilesWithoutAnyPackageAreNotAnEmptyDirectory(t *testing.T) {
	dir := writeFiles(t, map[string]string{"x.cue": "size: 4\n"})
	_, err := Load(dir, LoadOptions{})
	if err == nil {
		t.Fatal("no error")
	}
	var none *NoSpecificationError
	if errors.As(err, &none) {
		t.Errorf("a directory with a CUE file is not empty: %v", err)
	}
}

// $settings.state.path may depend on a tag that has no default. init reads the
// block before the generated packages exist, with the tags of the command.
func TestLoadSettingsAppliesTags(t *testing.T) {
	dir := writeFiles(t, map[string]string{
		"main.cue": `package estate

import (
	rnd "pravo.dev/providers/hashicorp/random"
	"pravo.dev/pravo"
)

env: string @tag(env)

$settings: pravo.#Settings & {
	state: path: "state/\(env)"
}

$random: rnd.#Provider
`})
	r, err := LoadSettingsWith(dir, LoadOptions{Tags: []string{"env=dev"}})
	if err != nil {
		t.Fatalf("with -t env=dev: %v", err)
	}
	if r.StatePath != "state/dev" {
		t.Errorf("state path = %q, want state/dev", r.StatePath)
	}
	if r.Providers["random"] == nil {
		t.Errorf("providers = %+v; want random from the import", r.Providers)
	}
	if _, err := LoadSettingsWith(dir, LoadOptions{}); err == nil || !strings.Contains(err.Error(), "pass -t env=") {
		t.Errorf("without the tag the block is not concrete and the error says how to give it: %v", err)
	}
	// A tag that no field declares is an error, as it is when the whole
	// specification loads.
	if _, err := LoadSettingsWith(dir, LoadOptions{Tags: []string{"env=dev", "nope=1"}}); err == nil || !strings.Contains(err.Error(), "no tag for") {
		t.Errorf("an unknown tag: %v", err)
	}
}

func TestLoadSettingsAppliesTypedTagsAndSystemVariables(t *testing.T) {
	dir := writeFiles(t, map[string]string{
		"main.cue": `package estate

shard: int @tag(shard,type=int)
host:  string @tag(host,var=hostname)

$settings: state: path: "state/\(shard)-\(host)"
`})
	r, err := LoadSettingsWith(dir, LoadOptions{Tags: []string{"shard=3"}, SysTags: true})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(r.StatePath, "state/3-") || strings.HasSuffix(r.StatePath, "-") {
		t.Errorf("state path = %q", r.StatePath)
	}
}

func TestMissingModuleHintsAtInit(t *testing.T) {
	dir := writeFiles(t, map[string]string{
		"main.cue": "package estate\n\nimport rnd \"pravo.dev/providers/hashicorp/random\"\n\n$random: rnd.#Provider\n",
	})
	_, err := Load(dir, LoadOptions{})
	if err == nil {
		t.Fatal("no error")
	}
	for _, want := range []string{"imports are unavailable because there is no cue.mod/module.cue file", `hint: `, `Run "pravo init"`} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error lacks %q:\n%v", want, err)
		}
	}
	if ModuleNotFoundHint(nil) != nil {
		t.Error("no error, no hint")
	}
	plain := errors.New("boom")
	if ModuleNotFoundHint(plain) != plain {
		t.Error("an unrelated error is returned as it is")
	}
}
