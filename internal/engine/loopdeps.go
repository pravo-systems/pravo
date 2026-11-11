package engine

import (
	"cuelang.org/go/cue"

	"github.com/pravo-systems/pravo/internal/convert"
)

// DependenciesFromSyntax makes every graph the analysis of the syntax alone, as
// it was before the evaluator was read (convert.Loops). The command line sets
// it from PRAVO_DEPENDENCIES=syntax before a command runs; nothing changes it
// while one does.
var DependenciesFromSyntax = false

// LoopRead tells the analysis what a loop variable stands for in the body of a
// task: the elements the graph found, or nothing better than the source.
func (r taskResolver) LoopRead(rd convert.LoopRead) ([]cue.Path, bool) {
	return r.e.loops.Read(rd)
}

// SetLoops sets what the engine reads the loops of the graph from.
func (r taskResolver) SetLoops(l *convert.Loops) { r.e.loops = l }
