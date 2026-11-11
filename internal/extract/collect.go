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
	"cuelang.org/go/cue/token"
)

// SrcFile is one file of the specification.
type SrcFile struct {
	Path   string
	Name   string
	Src    []byte
	AST    *ast.File // parsed without comments: what is compared
	ASTc   *ast.File // parsed with comments: where comments are found
	Stable bool      // the file is already formatted, so formatting the result changes nothing else
}

// Res is a resource written at the top of a file as LABEL: $PROVIDER.#Resource.TYPE & {...}.
type Res struct {
	File       *SrcFile
	Label      string // the label as written
	Prov, Type string
	Head       string // "$aws.#Resource.aws_s3_bucket"
	Body       *ast.StructLit
	Tree       *Node
	Seq        int // order in the specification
	LabelStart int // byte offsets in the file
	DocStart   int // where its doc comment starts, or LabelStart
	ValStart   int
	ValEnd     int
	Comments   []string // comments inside the value: they move above the instance
}

func (r *Res) srcLen() int { return r.ValEnd - r.LabelStart }

// Spec is what was read.
type Spec struct {
	Dir    string
	Files  []*SrcFile
	Res    []*Res
	Names  map[string]bool // every top-level label and every identifier used: a new name must not be one of them
	Other  []string        // resources that are not written in the plain form, with the reason
	Nested int             // resource expressions that are not at the top of a file
}

// Input is a file of the specification, as it is on disk or as an earlier pass wrote it.
type Input struct {
	Path, Name string
	Src        []byte
}

func parseSpec(dir string, in []Input) (*Spec, error) {
	sp := &Spec{Dir: dir, Names: map[string]bool{}}
	for _, i := range in {
		p, name, src := i.Path, i.Name, i.Src
		f, err := parser.ParseFile(p, src)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", name, err)
		}
		fc, err := parser.ParseFile(p, src, parser.ParseComments)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", name, err)
		}
		sf := &SrcFile{Path: p, Name: name, Src: src, AST: f, ASTc: fc}
		if out, err := format.Source(src); err == nil && bytes.Equal(out, src) {
			sf.Stable = true
		}
		sp.Files = append(sp.Files, sf)
	}
	seq := 0
	for _, sf := range sp.Files {
		// The fields of the file with comments, by the offset of their label.
		withComments := map[int]*ast.Field{}
		for _, d := range sf.ASTc.Decls {
			if f, ok := d.(*ast.Field); ok {
				withComments[f.Pos().Offset()] = f
			}
		}
		ast.Walk(sf.AST, func(n ast.Node) bool {
			switch x := n.(type) {
			case *ast.Ident:
				sp.Names[x.Name] = true
			case *ast.SelectorExpr:
				if labelName(x.Sel) == "#Resource" {
					sp.Nested++
				}
			}
			return true
		}, nil)
		for _, d := range sf.AST.Decls {
			f, ok := d.(*ast.Field)
			if !ok {
				continue
			}
			label, plain := plainLabel(f.Label)
			if label != "" {
				sp.Names[label] = true
			}
			bin, ok := f.Value.(*ast.BinaryExpr)
			if !ok || bin.Op != token.AND {
				continue
			}
			prov, typ, ok := resourceHead(bin.X)
			if !ok {
				continue
			}
			sp.Nested-- // this one is at the top
			body, isStruct := bin.Y.(*ast.StructLit)
			switch {
			case !plain || f.Constraint != token.ILLEGAL || len(f.Attrs) > 0 || f.Alias != nil:
				sp.Other = append(sp.Other, fmt.Sprintf("%s (%s): its label is not a plain name", label, typ))
				continue
			case !isStruct:
				sp.Other = append(sp.Other, fmt.Sprintf("%s (%s): it is not written as one struct after the type", label, typ))
				continue
			}
			r := &Res{
				File: sf, Label: label, Prov: prov, Type: typ, Body: body, Seq: seq,
				Head:       nodeText(bin.X),
				LabelStart: f.Pos().Offset(),
				ValStart:   f.Value.Pos().Offset(),
				ValEnd:     f.Value.End().Offset(),
			}
			r.DocStart = r.LabelStart
			if fc := withComments[r.LabelStart]; fc != nil {
				for _, cg := range ast.Comments(fc) {
					if o := cg.Pos().Offset(); o < r.DocStart {
						r.DocStart = o
					}
				}
				r.Comments = commentsInside(fc.Value, r.ValStart, r.ValEnd)
			}
			r.Tree = toNode(body)
			if r.Tree.Kind != KStruct {
				sp.Other = append(sp.Other, fmt.Sprintf("%s (%s): its body holds more than plain fields (a comprehension, an embedding or a constraint)", label, typ))
				continue
			}
			seq++
			sp.Res = append(sp.Res, r)
		}
	}
	return sp, nil
}

// plainLabel gives the name of a label and whether it is a regular field name.
func plainLabel(l ast.Label) (string, bool) {
	switch x := l.(type) {
	case *ast.Ident:
		if strings.HasPrefix(x.Name, "#") || strings.HasPrefix(x.Name, "_") {
			return x.Name, false
		}
		return x.Name, true
	case *ast.BasicLit:
		if x.Kind == token.STRING {
			if s, err := literal.Unquote(x.Value); err == nil {
				return s, true
			}
		}
	}
	return "", false
}

func labelName(l ast.Label) string {
	if id, ok := l.(*ast.Ident); ok {
		return id.Name
	}
	return ""
}

// resourceHead recognises $PROVIDER.#Resource.TYPE.
func resourceHead(e ast.Expr) (prov, typ string, ok bool) {
	sel, ok := e.(*ast.SelectorExpr)
	if !ok {
		return "", "", false
	}
	typ = labelName(sel.Sel)
	inner, ok := sel.X.(*ast.SelectorExpr)
	if !ok || labelName(inner.Sel) != "#Resource" || typ == "" {
		return "", "", false
	}
	id, ok := inner.X.(*ast.Ident)
	if !ok || !strings.HasPrefix(id.Name, "$") {
		return "", "", false
	}
	return id.Name, typ, true
}

// commentsInside lists the comments attached to nodes of e that lie in [from, to).
func commentsInside(e ast.Expr, from, to int) []string {
	type at struct {
		off  int
		text string
	}
	var found []at
	seen := map[int]bool{}
	ast.Walk(e, func(n ast.Node) bool {
		for _, cg := range ast.Comments(n) {
			for _, c := range cg.List {
				o := c.Pos().Offset()
				if o >= from && o < to && !seen[o] {
					seen[o] = true
					found = append(found, at{o, strings.TrimRight(c.Text, " \t")})
				}
			}
		}
		return true
	}, nil)
	sort.Slice(found, func(i, j int) bool { return found[i].off < found[j].off })
	var out []string
	for _, c := range found {
		out = append(out, c.text)
	}
	return out
}

// nodeText prints an expression on its own.
func nodeText(n ast.Node) string {
	b, err := format.Node(n)
	if err != nil {
		return fmt.Sprintf("<%v>", err)
	}
	return string(b)
}
