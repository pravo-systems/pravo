package convert

import (
	"fmt"
	"strings"
	"testing"

	"cuelang.org/go/cue"
	"cuelang.org/go/cue/cuecontext"
)

// benchSpec writes n tasks that read each other through plain references,
// interpolation, a list and a comprehension; with lets, each also reads through
// a let.
func benchSpec(n int, lets bool) (string, []string) {
	var b strings.Builder
	var tasks []string
	b.WriteString("zones: [\"a\", \"b\"]\n")
	if lets {
		b.WriteString("let _base = r0\n")
	}
	for i := 0; i < n; i++ {
		prev := fmt.Sprintf("r%d", max(i-1, 0))
		fmt.Fprintf(&b, "r%d: {\n\tid: string | null\n\tname: \"n-\\(%s.id)\"\n\ttags: {a: %s.id, b: \"x\"}\n\tlist: [%s.id, \"y\"]\n\tall: [for z in zones {\"\\(z)-\\(%s.id)\"}]\n", i, prev, prev, prev, prev)
		if lets {
			b.WriteString("\tbase: _base.id\n")
		}
		b.WriteString("}\n")
		tasks = append(tasks, fmt.Sprintf("r%d", i))
	}
	return b.String(), tasks
}

func benchDependencies(b *testing.B, lets bool) {
	src, tasks := benchSpec(400, lets)
	v := cuecontext.New().CompileString(src)
	if err := v.Err(); err != nil {
		b.Fatal(err)
	}
	resolve := taskPaths{paths: tasks, root: v}
	paths := make([]cue.Path, len(tasks))
	for i, t := range tasks {
		paths[i] = cue.ParsePath(t)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for _, p := range paths {
			Dependencies(v.LookupPath(p), p, resolve)
		}
	}
}

// The cost of reading the dependencies of 400 tasks that read each other.
func BenchmarkDependencies(b *testing.B)         { benchDependencies(b, false) }
func BenchmarkDependenciesWithLets(b *testing.B) { benchDependencies(b, true) }
