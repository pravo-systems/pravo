package env

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLookup(t *testing.T) {
	e := New([]string{"A=1", "B=", "A=2", "C=x=y"})
	for name, want := range map[string]string{"A": "2", "B": "", "C": "x=y", "D": ""} {
		if got := e.Get(name); got != want {
			t.Errorf("Get(%s) = %q, want %q", name, got, want)
		}
	}
	if _, ok := e.Lookup("B"); !ok {
		t.Error("B is set, to the empty string")
	}
	if _, ok := e.Lookup("D"); ok {
		t.Error("D is not set")
	}
	if _, ok := (Env{}).Lookup("A"); ok {
		t.Error("the zero value holds nothing")
	}
}

func TestWithAndWithout(t *testing.T) {
	e := New([]string{"A=1", "B=2"})
	f := e.With("A", "3").With("C", "4").Without("B")
	if got := strings.Join(f.Environ(), " "); got != "A=3 C=4" {
		t.Errorf("got %q", got)
	}
	if got := strings.Join(e.Environ(), " "); got != "A=1 B=2" {
		t.Errorf("the original changed: %q", got)
	}
}

func TestUserDirs(t *testing.T) {
	e := New([]string{"HOME=/home/u", "USERPROFILE=/home/u", "XDG_CONFIG_HOME=/xdg/config", "XDG_CACHE_HOME=/xdg/cache", "AppData=/home/u/ad", "LocalAppData=/home/u/lad"})
	for name, f := range map[string]func() (string, bool){"home": e.UserHomeDir, "config": e.UserConfigDir, "cache": e.UserCacheDir} {
		if d, ok := f(); !ok || d == "" {
			t.Errorf("%s: no directory", name)
		}
	}
	if _, ok := (Env{}).UserHomeDir(); ok {
		t.Error("an empty environment has no home")
	}
}

// processState lists what reads, or changes, the environment of the process.
var processState = map[string]bool{
	"Getenv": true, "LookupEnv": true, "Environ": true, "Setenv": true, "Unsetenv": true, "Clearenv": true,
	"ExpandEnv": true, "Expand": true, "UserHomeDir": true, "UserConfigDir": true, "UserCacheDir": true,
}

// allowed are the files under internal/ that may name them, and why.
var allowed = map[string]string{
	"internal/env/os.go":                 "OS: the one read of the environment",
	"internal/state/statetest/format.go": "a package for tests, which a test selects with a variable",
}

// TestNoProcessEnvironment keeps the rule of the package: nothing under
// internal/ reads or changes the environment of the process. A value of this
// package is passed in instead. Tests, tools and cmd are not held to it.
func TestNoProcessEnvironment(t *testing.T) {
	root := filepath.Join("..", "..", "internal")
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return err
		}
		rel := filepath.ToSlash(strings.TrimPrefix(path, ".."+string(filepath.Separator)+".."+string(filepath.Separator)))
		if _, ok := allowed[rel]; ok {
			return nil
		}
		src, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		f, err := parser.ParseFile(token.NewFileSet(), path, src, 0)
		if err != nil {
			return err
		}
		osName := "os"
		for _, imp := range f.Imports {
			if imp.Path.Value == `"os"` && imp.Name != nil {
				osName = imp.Name.Name
			}
		}
		ast.Inspect(f, func(n ast.Node) bool {
			sel, ok := n.(*ast.SelectorExpr)
			if !ok || !processState[sel.Sel.Name] {
				return true
			}
			if id, ok := sel.X.(*ast.Ident); ok && id.Name == osName {
				t.Errorf("%s: os.%s reads or changes the environment of the process; take an env.Env", rel, sel.Sel.Name)
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
