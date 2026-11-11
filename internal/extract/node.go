package extract

import (
	"sort"
	"strings"

	"cuelang.org/go/cue/ast"
	"cuelang.org/go/cue/literal"
	"cuelang.org/go/cue/token"
)

// The kinds of a node of the normal form.
const (
	KStruct = iota // plain fields only
	KList          // a closed list
	KCall          // f(args)
	KInterp        // "a\(x)b"
	KLit           // a string, a number, a boolean or null
	KOther         // anything else: compared by its text
)

// Node is the normal form of an expression: what anti-unification walks.
type Node struct {
	Kind    int
	Fields  []*NField // KStruct, in the order of the source
	Elems   []*Node   // the elements of a list, the arguments of a call, the expressions of an interpolation
	Fun     string    // KCall
	Static  []string  // KInterp: the text between the expressions, one more than Elems
	LitKind string    // KLit: string, bytes, int, float, bool, null
	LitVal  string    // KLit: the string unquoted, or the text of the literal
	Text    string    // KOther: its text
	Expr    ast.Expr
	Key     string // equal keys mean equal values (fields in any order)
	Shape   string // the key with every leaf blanked
	OneLine bool   // written on one line in the source
}

// NField is a field of a struct of the normal form.
type NField struct {
	Label    string // the name
	LabelSrc string // the label as written
	Val      *Node
}

func (n *Node) field(label string) *NField {
	for _, f := range n.Fields {
		if f.Label == label {
			return f
		}
	}
	return nil
}

// scalar tells whether the node is a literal that a parameter can carry, and of which family.
func (n *Node) scalar() (family string, ok bool) {
	if n.Kind != KLit {
		return "", false
	}
	switch n.LitKind {
	case "string":
		return "string", true
	case "int", "float":
		return "number", true
	case "bool":
		return "bool", true
	}
	return "", false
}

func toNode(e ast.Expr) *Node {
	n := &Node{Expr: e}
	if e.Pos().IsValid() && e.End().IsValid() {
		n.OneLine = e.Pos().Line() == e.End().Line()
	}
	switch x := e.(type) {
	case *ast.StructLit:
		if !structNode(n, x) {
			return other(n)
		}
	case *ast.ListLit:
		n.Kind = KList
		for _, el := range x.Elts {
			if _, ok := el.(*ast.Ellipsis); ok {
				return other(n)
			}
			if _, ok := el.(*ast.Comprehension); ok {
				return other(n)
			}
			n.Elems = append(n.Elems, toNode(el))
		}
		n.Key, n.Shape = "["+joinKeys(n.Elems)+"]", "["+joinShapes(n.Elems)+"]"
	case *ast.CallExpr:
		n.Kind = KCall
		n.Fun = oneLine(nodeText(x.Fun))
		for _, a := range x.Args {
			n.Elems = append(n.Elems, toNode(a))
		}
		n.Key, n.Shape = n.Fun+"("+joinKeys(n.Elems)+")", n.Fun+"("+joinShapes(n.Elems)+")"
	case *ast.Interpolation:
		static, exprs, ok := splitInterpolation(x)
		if !ok {
			return other(n)
		}
		n.Kind = KInterp
		n.Static = static
		for _, a := range exprs {
			n.Elems = append(n.Elems, toNode(a))
		}
		n.Key = "I(" + literal.String.Quote(strings.Join(static, "\x00")) + ";" + joinKeys(n.Elems) + ")"
		n.Shape = "_"
	case *ast.BasicLit:
		if !litNode(n, x) {
			return other(n)
		}
	case *ast.UnaryExpr:
		if lit, ok := x.X.(*ast.BasicLit); ok && (x.Op == token.SUB || x.Op == token.ADD) && (lit.Kind == token.INT || lit.Kind == token.FLOAT) {
			n.Kind = KLit
			n.LitKind = "int"
			if lit.Kind == token.FLOAT {
				n.LitKind = "float"
			}
			n.LitVal = lit.Value
			if x.Op == token.SUB {
				n.LitVal = "-" + lit.Value
			}
			n.Key, n.Shape = "L:"+n.LitKind+":"+literal.String.Quote(n.LitVal), "_"
			return n
		}
		return other(n)
	default:
		return other(n)
	}
	return n
}

// structNode reads the fields of a struct into n. It is false for a struct that is not plain:
// one with an embedding, a constraint, an attribute, an alias or a label twice.
func structNode(n *Node, x *ast.StructLit) bool {
	n.Kind = KStruct
	seen := map[string]bool{}
	for _, d := range x.Elts {
		f, ok := d.(*ast.Field)
		if !ok {
			return false
		}
		label, plain := plainLabel(f.Label)
		if !plain || f.Constraint != token.ILLEGAL || len(f.Attrs) > 0 || f.Alias != nil || seen[label] {
			return false
		}
		seen[label] = true
		n.Fields = append(n.Fields, &NField{Label: label, LabelSrc: nodeText(f.Label), Val: toNode(f.Value)})
	}
	parts := make([]string, 0, len(n.Fields))
	shapes := make([]string, 0, len(n.Fields))
	for _, f := range n.Fields {
		parts = append(parts, literal.String.Quote(f.Label)+":"+f.Val.Key)
		shapes = append(shapes, literal.String.Quote(f.Label)+":"+f.Val.Shape)
	}
	sort.Strings(parts)
	sort.Strings(shapes)
	n.Key = "{" + strings.Join(parts, ",") + "}"
	n.Shape = "{" + strings.Join(shapes, ",") + "}"
	return true
}

// litNode reads a literal into n. It is false for a literal that is not a string, a number, a
// boolean or null.
func litNode(n *Node, x *ast.BasicLit) bool {
	n.Kind = KLit
	switch x.Kind {
	case token.STRING:
		s, err := literal.Unquote(x.Value)
		if err != nil {
			return false
		}
		n.LitKind, n.LitVal = "string", s
		if strings.HasPrefix(x.Value, "'") {
			n.LitKind = "bytes"
		}
	case token.INT:
		n.LitKind, n.LitVal = "int", x.Value
	case token.FLOAT:
		n.LitKind, n.LitVal = "float", x.Value
	case token.TRUE, token.FALSE:
		n.LitKind, n.LitVal = "bool", x.Value
	case token.NULL:
		n.LitKind, n.LitVal = "null", "null"
	default:
		return false
	}
	n.Key, n.Shape = "L:"+n.LitKind+":"+literal.String.Quote(n.LitVal), "_"
	return true
}

func other(n *Node) *Node {
	n.Kind = KOther
	n.Fields, n.Elems = nil, nil
	n.Text = oneLine(nodeText(n.Expr))
	n.Key, n.Shape = "E:"+n.Text, "_"
	return n
}

func joinKeys(ns []*Node) string {
	parts := make([]string, len(ns))
	for i, n := range ns {
		parts[i] = n.Key
	}
	return strings.Join(parts, ",")
}

func joinShapes(ns []*Node) string {
	parts := make([]string, len(ns))
	for i, n := range ns {
		parts[i] = n.Shape
	}
	return strings.Join(parts, ",")
}

// oneLine folds the white space of a text that holds no string over several lines.
func oneLine(s string) string {
	if !strings.ContainsAny(s, "\n\t") {
		return s
	}
	return strings.Join(strings.Fields(s), " ")
}

// splitInterpolation gives the text between the expressions of a plain "..." interpolation.
func splitInterpolation(x *ast.Interpolation) (static []string, exprs []ast.Expr, ok bool) {
	n := len(x.Elts)
	if n < 3 || n%2 == 0 {
		return nil, nil, false
	}
	for i, el := range x.Elts {
		if i%2 == 1 {
			exprs = append(exprs, el)
			continue
		}
		lit, isLit := el.(*ast.BasicLit)
		if !isLit {
			return nil, nil, false
		}
		s := lit.Value
		if i == 0 {
			if !strings.HasPrefix(s, `"`) || strings.HasPrefix(s, `"""`) {
				return nil, nil, false
			}
			s = s[1:]
		} else {
			if !strings.HasPrefix(s, ")") {
				return nil, nil, false
			}
			s = s[1:]
		}
		if i == n-1 {
			if !strings.HasSuffix(s, `"`) {
				return nil, nil, false
			}
			s = s[:len(s)-1]
		} else {
			if !strings.HasSuffix(s, `\(`) {
				return nil, nil, false
			}
			s = s[:len(s)-2]
		}
		u, err := literal.Unquote(`"` + s + `"`)
		if err != nil {
			return nil, nil, false
		}
		static = append(static, u)
	}
	return static, exprs, true
}

// quoteInner is the text of s inside a "..." literal.
func quoteInner(s string) string {
	q := literal.String.Quote(s)
	return q[1 : len(q)-1]
}
