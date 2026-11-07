package config

import (
	"fmt"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"cuelang.org/go/cue"
	cueerrors "cuelang.org/go/cue/errors"
)

// DetailsWithHints is cueerrors.Details with a line after each "undefined field" and
// "field not allowed" error: the closest names that do exist ("did you mean ...?"),
// the fields of the struct the name was looked up in, and, when the name was found
// one level up, why the reference did not reach it. v is the value that produced err.
func DetailsWithHints(v cue.Value, err error) string {
	var out strings.Builder
	out.WriteString(cueerrors.Details(err, nil))
	seen := map[string]bool{}
	for _, e := range cueerrors.Errors(err) {
		hint := hintFor(v, e)
		if hint == "" || seen[hint] {
			continue
		}
		seen[hint] = true
		if p := strings.Join(e.Path(), "."); p != "" {
			hint = strings.Replace(hint, "  hint:", "  hint ("+p+"):", 1)
		}
		out.WriteString(hint + "\n")
	}
	return out.String()
}

var selectorBefore = regexp.MustCompile(`([\$#_A-Za-z][\w\$#]*(?:\.[\$#_A-Za-z][\w\$#]*)*)\.$`)

func hintFor(v cue.Value, e cueerrors.Error) string {
	msg := e.Error()
	var name string
	notAllowed := false
	if i := strings.Index(msg, "undefined field: "); i >= 0 {
		name = strings.TrimSuffix(strings.TrimSpace(msg[i+len("undefined field: "):]), ":")
	} else if strings.HasSuffix(msg, "field not allowed") {
		if p := e.Path(); len(p) > 0 {
			name = p[len(p)-1]
			notAllowed = true
		}
	}
	if rest, ok := strings.CutPrefix(msg[strings.Index(msg, ": ")+2:], "invalid operand "); ok && !notAllowed {
		// "name.id" where a field called name sits around the reference: it found that
		// field, not the one meant.
		if op, _, _ := strings.Cut(rest, " "); op != "" {
			if at := shadowedAt(v, op, e.Path()); at != "" {
				return shadowHint(op, at)
			}
		}
	}
	if name == "" || strings.ContainsAny(name, " \t") {
		return ""
	}
	path := e.Path()

	// The struct that was asked for the name: for a missing selector, what the
	// text before the dot names; for a field the schema does not have, the
	// struct that holds it.
	var parent cue.Value
	var chain string
	if notAllowed {
		parent = lookup(v, path[:len(path)-1])
	} else if chain = chainBefore(e); chain != "" {
		parent = lookup(v, strings.Split(chain, "."))
		// A reference that starts in the field being defined finds that field
		// first: name.id inside "triggers: name: name.id" means triggers.name.
		head := strings.Split(chain, ".")[0]
		if top := lookup(v, strings.Split(chain, ".")); top.Exists() && hasField(top, name) {
			if at := shadowedAt(v, head, path); at != "" {
				return shadowHint(head, at)
			}
		}
	}

	var pool []string
	listed := false
	if parent.Exists() {
		for _, n := range fieldNames(parent) {
			if n != name {
				pool = append(pool, n)
			}
		}
		listed = len(pool) > 0 && len(pool) <= 8 && !strings.HasPrefix(name, "$")
	}
	if len(pool) == 0 {
		pool = fieldNames(v)
		if notAllowed {
			return ""
		}
	}
	var b strings.Builder
	b.WriteString("  hint:")
	near := closest(name, pool)
	if len(near) > 0 {
		b.WriteString(" did you mean " + strings.Join(near, " or ") + "?")
	}
	if listed {
		what := "this struct has"
		if notAllowed {
			what = strings.Join(path[:len(path)-1], ".") + " has"
		}
		if chain != "" {
			what = chain + " has"
		}
		b.WriteString(" " + what + ": " + strings.Join(pool, ", ") + ".")
	}
	if len(near) == 0 && !listed {
		return ""
	}
	if notAllowed || strings.HasSuffix(chain, "#Resource") || strings.HasSuffix(chain, "#Data") {
		b.WriteString(" `pravo schema <type>` prints every field of a type.")
	}
	return b.String()
}

func hasString(s []string, x string) bool {
	for _, e := range s {
		if e == x {
			return true
		}
	}
	return false
}

// chainBefore reads the source text before the position of e and returns the dotted
// reference that ends there: for "other: known.idd" at "idd" it returns "known".
func chainBefore(e cueerrors.Error) string {
	for _, pos := range cueerrors.Positions(e) {
		if !pos.IsValid() || pos.Filename() == "" {
			continue
		}
		src, err := os.ReadFile(pos.Filename())
		if err != nil {
			continue
		}
		lines := strings.Split(string(src), "\n")
		if pos.Line() < 1 || pos.Line() > len(lines) {
			continue
		}
		line := lines[pos.Line()-1]
		if pos.Column() < 2 || pos.Column()-1 > len(line) {
			continue
		}
		if m := selectorBefore.FindStringSubmatch(line[:pos.Column()-1]); m != nil {
			return m[1]
		}
	}
	return ""
}

func lookup(v cue.Value, path []string) cue.Value {
	sels := make([]cue.Selector, 0, len(path))
	for _, p := range path {
		switch {
		case strings.HasPrefix(p, "#"):
			sels = append(sels, cue.Def(p))
		case strings.HasPrefix(p, "_"):
			return cue.Value{}
		default:
			if n, err := strconv.Atoi(p); err == nil {
				sels = append(sels, cue.Index(n))
			} else {
				sels = append(sels, cue.Str(p))
			}
		}
	}
	return v.LookupPath(cue.MakePath(sels...))
}

func hasField(v cue.Value, name string) bool {
	return hasString(fieldNames(v), name)
}

// fieldNames lists the regular and definition fields of a struct, optional ones
// included, in order.
func fieldNames(v cue.Value) []string {
	it, err := v.Fields(cue.Optional(true), cue.Definitions(true))
	if err != nil {
		return nil
	}
	var out []string
	for it.Next() {
		n := strings.TrimSuffix(it.Selector().String(), "?")
		if strings.HasPrefix(n, "$") {
			continue // the keys pravo adds to every resource are not what a typo is meant for
		}
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// closest returns up to two names of pool within a small edit distance of name.
func closest(name string, pool []string) []string {
	limit := 2
	if len(name) <= 4 {
		limit = 1
	}
	type cand struct {
		n string
		d int
	}
	var cs []cand
	for _, p := range pool {
		if p == name {
			continue
		}
		if d := distance(name, p); d <= limit {
			cs = append(cs, cand{p, d})
		}
	}
	sort.SliceStable(cs, func(i, j int) bool { return cs[i].d < cs[j].d })
	var out []string
	for i := 0; i < len(cs) && i < 2; i++ {
		out = append(out, cs[i].n)
	}
	return out
}

func distance(a, b string) int {
	prev := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		cur := make([]int, len(b)+1)
		cur[0] = i
		for j := 1; j <= len(b); j++ {
			c := 1
			if a[i-1] == b[j-1] {
				c = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+c)
		}
		prev = cur
	}
	return prev[len(b)]
}

// shadowedAt reports where a field called head is declared in a struct that encloses the
// field at path, below the top level: a reference to head from there finds that field
// before the top-level one. It is "" when no enclosing struct has one.
func shadowedAt(v cue.Value, head string, path []string) string {
	if !lookup(v, []string{head}).Exists() {
		return ""
	}
	for i := len(path) - 1; i >= 1; i-- {
		if lookup(v, append(append([]string{}, path[:i]...), head)).Exists() {
			return strings.Join(append(append([]string{}, path[:i]...), head), ".")
		}
	}
	return ""
}

func shadowHint(name, at string) string {
	return fmt.Sprintf("  hint: %s here is the field %s, in a struct around this reference, not the %s at the top level. Give the outer one another name outside the body, `_outer: %s`, and write `_outer.<field>`.",
		name, at, name, name)
}
