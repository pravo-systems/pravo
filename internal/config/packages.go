package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"cuelang.org/go/cue/parser"
)

// lockFile is the provider lock file. It is CUE without a package clause and
// is not part of the specification.
const lockFile = "pravo.lock.cue"

// NoSpecificationError is returned by Load when the directory holds no CUE
// file that could be a specification. init creates the module in such a
// directory instead of failing.
type NoSpecificationError struct {
	Err error
}

func (e *NoSpecificationError) Error() string { return e.Err.Error() }
func (e *NoSpecificationError) Unwrap() error { return e.Err }

// noSpecification marks err as the absence of a specification when the
// directory had no candidate file.
func noSpecification(files []string, err error) error {
	if len(files) > 0 {
		return err
	}
	return &NoSpecificationError{Err: err}
}

// specFiles lists the CUE files of dir that the loader takes into the package,
// by name. Files the loader never reads (hidden, prefixed with an underscore,
// tests and tools) and the lock file are left out.
func specFiles(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var names []string
	for _, e := range entries {
		n := e.Name()
		switch {
		case e.IsDir(), !strings.HasSuffix(n, ".cue"), n == lockFile:
		case strings.HasPrefix(n, "."), strings.HasPrefix(n, "_"):
		case strings.HasSuffix(n, "_test.cue"), strings.HasSuffix(n, "_tool.cue"):
		default:
			names = append(names, n)
		}
	}
	return names, nil
}

// checkPackageClauses reads the package clause of every file in dir and
// reports a file that is not in the package of the others. The CUE loader
// skips a file without a package clause when another file has one, so a
// conflict written in it would never be reported. It returns the files that
// were looked at.
func checkPackageClauses(dir string) ([]string, error) {
	names, err := specFiles(dir)
	if err != nil {
		return nil, err
	}
	clauses := make([]string, len(names))
	unreadable := make([]bool, len(names))
	count := map[string]int{}
	for i, n := range names {
		f, err := parser.ParseFile(filepath.Join(dir, n), nil, parser.PackageClauseOnly)
		if err != nil {
			unreadable[i] = true // reported with its position when the root loads
			continue
		}
		clauses[i] = f.PackageName()
		if clauses[i] != "" {
			count[clauses[i]]++
		}
	}
	// The package of the directory is the one most files declare; the first
	// by name when two are equally common.
	pkg := ""
	for _, c := range clauses {
		if c != "" && (pkg == "" || count[c] > count[pkg]) {
			pkg = c
		}
	}
	if pkg == "" {
		return names, nil // no file has one: the loader says so
	}
	var problems []string
	for i, n := range names {
		switch c := clauses[i]; {
		case unreadable[i]:
		case c == "":
			problems = append(problems, fmt.Sprintf("%s has no package clause; the other files are package %s", n, pkg))
		case c != pkg:
			problems = append(problems, fmt.Sprintf("%s is package %s; the other files are package %s", n, c, pkg))
		}
	}
	if len(problems) == 0 {
		return names, nil
	}
	abs, _ := filepath.Abs(dir)
	return names, errors.New("loading " + abs + ":\n" + strings.Join(problems, "\n"))
}
