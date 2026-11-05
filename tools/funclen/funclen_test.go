package funclen

import (
	"bufio"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// limit is the longest function that needs no entry in baseline.txt.
const limit = 100

// roots are the directories of the module whose functions are measured. Generated
// code, tests and the programs under tools and site are left out.
var roots = []string{"cmd", "internal", "pravo"}

// measure returns the length in lines of every function over the limit, keyed by
// "path Receiver.Name".
func measure(t *testing.T) map[string]int {
	t.Helper()
	long := map[string]int{}
	fset := token.NewFileSet()
	for _, r := range roots {
		err := filepath.WalkDir(filepath.Join("..", "..", r), func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			name := d.Name()
			if d.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") || strings.HasSuffix(name, ".pb.go") {
				return nil
			}
			f, err := parser.ParseFile(fset, p, nil, 0)
			if err != nil {
				return err
			}
			rel, _ := filepath.Rel(filepath.Join("..", ".."), p)
			rel = filepath.ToSlash(rel)
			for _, decl := range f.Decls {
				fd, ok := decl.(*ast.FuncDecl)
				if !ok || fd.Body == nil {
					continue
				}
				n := fset.Position(fd.End()).Line - fset.Position(fd.Pos()).Line + 1
				if n > limit {
					long[rel+" "+funcName(fd)] = n
				}
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	return long
}

func funcName(fd *ast.FuncDecl) string {
	if fd.Recv == nil || len(fd.Recv.List) == 0 {
		return fd.Name.Name
	}
	t := fd.Recv.List[0].Type
	if s, ok := t.(*ast.StarExpr); ok {
		t = s.X
	}
	if ix, ok := t.(*ast.IndexExpr); ok {
		t = ix.X
	}
	if id, ok := t.(*ast.Ident); ok {
		return id.Name + "." + fd.Name.Name
	}
	return fd.Name.Name
}

func readBaseline(t *testing.T) map[string]int {
	t.Helper()
	m := map[string]int{}
	f, err := os.Open("baseline.txt")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		i := strings.LastIndex(line, " ")
		n, err := strconv.Atoi(line[i+1:])
		if err != nil {
			t.Fatalf("baseline.txt: %q: %v", line, err)
		}
		m[line[:i]] = n
	}
	return m
}

func TestNoFunctionGrowsPastTheLimit(t *testing.T) {
	long := measure(t)
	if os.Getenv("FUNCLEN_UPDATE") != "" {
		var keys []string
		for k := range long {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		var b strings.Builder
		fmt.Fprintf(&b, "# Functions longer than %d lines, with their length. The check in funclen_test.go\n", limit)
		b.WriteString("# lets an entry shrink and fails when it grows; when a function is brought under the\n")
		b.WriteString("# limit, delete its line (or run FUNCLEN_UPDATE=1 go test ./tools/funclen).\n")
		for _, k := range keys {
			fmt.Fprintf(&b, "%s %d\n", k, long[k])
		}
		if err := os.WriteFile("baseline.txt", []byte(b.String()), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	base := readBaseline(t)
	var keys []string
	for k := range long {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		n := long[k]
		was, listed := base[k]
		switch {
		case !listed:
			t.Errorf("%s is %d lines, over the limit of %d: split it into steps", k, n, limit)
		case n > was:
			t.Errorf("%s grew from %d to %d lines: split it instead of adding to it", k, was, n)
		case n < was:
			t.Errorf("%s is now %d lines, down from %d: lower its entry in tools/funclen/baseline.txt (FUNCLEN_UPDATE=1 go test ./tools/funclen)", k, n, was)
		}
	}
	for k := range base {
		if _, ok := long[k]; !ok {
			t.Errorf("%s is no longer over %d lines: delete its entry from tools/funclen/baseline.txt (FUNCLEN_UPDATE=1 go test ./tools/funclen)", k, limit)
		}
	}
}
