package extract

import (
	"bytes"
	"fmt"
	"sort"
	"strings"

	"cuelang.org/go/cue/ast"
	"cuelang.org/go/cue/format"
	"cuelang.org/go/cue/literal"
	"cuelang.org/go/cue/parser"
)

type edit struct {
	start, end int
	text       string
	seq        int
}

// rewrite gives the new text of every file that changes. Only the value of each instance is
// replaced, and the definition is put before the first instance: what stands between is not
// touched, so the comments above a resource stay above it. A comment inside a body that goes away
// moves above its instance.
func rewrite(res *Result) (map[string][]byte, error) {
	edits := map[*SrcFile][]edit{}
	seq := 0
	add := func(f *SrcFile, start, end int, text string) {
		edits[f] = append(edits[f], edit{start, end, text, seq})
		seq++
	}
	for _, x := range res.Defs {
		first := x.Members[0]
		doc := fmt.Sprintf("// %s is what %d resources of type %s have in common.\n", x.Name, len(x.Members), x.Type)
		if kept := sortedKeys(x.Kept); len(kept) > 0 {
			doc += "// Every instance sets " + strings.Join(kept, ", ") + " itself.\n"
		}
		add(first.File, first.DocStart, first.DocStart, doc+x.DefText+"\n\n")
		for i, m := range x.Members {
			if len(m.Comments) > 0 {
				add(m.File, m.LabelStart, m.LabelStart, strings.Join(m.Comments, "\n")+"\n")
			}
			add(m.File, m.ValStart, m.ValEnd, x.InstText[i])
			if i == 0 {
				continue
			}
			// Two instances of one line each, with nothing but an empty line between them, need no empty line.
			prev := x.Members[i-1]
			if prev.File != m.File || prev.ValEnd > m.LabelStart || len(m.Comments) > 0 || m.DocStart != m.LabelStart ||
				strings.Contains(x.InstText[i-1], "\n") || strings.Contains(x.InstText[i], "\n") {
				continue
			}
			gap := m.File.Src[prev.ValEnd:m.LabelStart]
			if len(bytes.TrimSpace(gap)) == 0 && bytes.Count(gap, []byte("\n")) >= 2 {
				add(m.File, prev.ValEnd, m.LabelStart, "\n")
			}
		}
	}
	out := map[string][]byte{}
	for f, es := range edits {
		sort.Slice(es, func(i, j int) bool {
			if es[i].start != es[j].start {
				return es[i].start > es[j].start
			}
			return es[i].seq > es[j].seq
		})
		src := append([]byte(nil), f.Src...)
		for _, e := range es {
			src = append(src[:e.start], append([]byte(e.text), src[e.end:]...)...)
		}
		src, err := dropUnusedImports(f.Path, src)
		if err != nil {
			return nil, fmt.Errorf("%s: the rewritten file does not parse: %w\n%s", f.Name, err, src)
		}
		formatted, err := format.Source(src)
		if err != nil {
			return nil, fmt.Errorf("%s: the rewritten file does not format: %w", f.Name, err)
		}
		out[f.Name] = formatted
	}
	return out, nil
}

// dropUnusedImports removes the imports that the file no longer uses: a definition takes the
// calls of its instances with it, and CUE refuses a file that imports what it does not use.
func dropUnusedImports(path string, src []byte) ([]byte, error) {
	f, err := parser.ParseFile(path, src, parser.ParseComments)
	if err != nil {
		return nil, err
	}
	used := map[string]int{}
	ast.Walk(f, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.ImportSpec:
			return false
		case *ast.Ident:
			used[x.Name]++
		}
		return true
	}, nil)
	changed := false
	var decls []ast.Decl
	for _, d := range f.Decls {
		imp, ok := d.(*ast.ImportDecl)
		if !ok {
			decls = append(decls, d)
			continue
		}
		var specs []*ast.ImportSpec
		for _, s := range imp.Specs {
			name := ""
			if s.Name != nil {
				name = s.Name.Name
			} else if p, err := literal.Unquote(s.Path.Value); err == nil {
				name = p[strings.LastIndex(p, "/")+1:]
				if i := strings.Index(name, ":"); i >= 0 {
					name = name[i+1:]
				}
			}
			if used[name] == 0 {
				changed = true
				continue
			}
			specs = append(specs, s)
		}
		if len(specs) > 0 {
			imp.Specs = specs
			decls = append(decls, imp)
		}
	}
	if !changed {
		return src, nil
	}
	f.Decls = decls
	return format.Node(f)
}
