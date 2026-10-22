// Package skills holds the pravo skill: SKILL.md, written by hand, and the
// reference files that tools/skillgen builds from the documentation. The
// binary embeds it, so "pravo skill install" writes the skill that matches the
// version of pravo that is running.
package skills

import "embed"

// FS holds the skill below the directory pravo: pravo/SKILL.md and
// pravo/references/....
//
//go:embed pravo
var FS embed.FS

// Dir is the name of the directory of the skill inside FS, and the name of the
// skill.
const Dir = "pravo"
