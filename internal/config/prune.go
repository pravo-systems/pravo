package config

import (
	"bytes"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"cuelang.org/go/cue/load"
)

// The generated package of a provider defines every type the provider has: an
// AWS package is 8 MB of CUE and 2,000 types, and a specification uses a few
// dozen. CUE parses, compiles and evaluates all of it on every run, which is most
// of the time a small estate takes. The file stays whole on disk, for editors and
// for pravo schema; a run loads a copy of it that has only the types the
// specification names.
//
// What is left out is blanked, not deleted, so that every line keeps its number
// and a position in a message is a position in the file on disk.

// pruneMinSize is the size under which a generated file is loaded as it is.
var pruneMinSize int64 = 1 << 20

// typeUse is the types of the providers that a body of CUE names, and whether it
// names the catalogues in a way that this cannot read.
type typeUse struct {
	// names holds, for each of #Resource, #Data, #Ephemeral, #Discovery and
	// #Function, the types that are used.
	names map[string]map[string]bool
	// dynamic is set when a catalogue is used as a whole or indexed by a
	// computed name: then any type may be needed.
	dynamic bool
}

// A use of a type is $aws.#Resource.aws_s3_bucket, or #Function."arn_parse". A
// catalogue that is not followed by a name is used as a whole.
var typeUseRe = regexp.MustCompile(`#(Resource|Data|Ephemeral|Discovery|Function)\b(?:\.("(?:[^"\\]|\\.)*"|[A-Za-z_][A-Za-z0-9_]*))?`)

// scanTypeUse reads the CUE files under dirs for the types they name. The
// directories cue.mod (apart from cue.mod/pkg, where dependencies are kept), the
// directories that start with a dot and node_modules are not read.
func scanTypeUse(dirs ...string) *typeUse {
	use := &typeUse{names: map[string]map[string]bool{}}
	seen := map[string]bool{}
	for _, dir := range dirs {
		_ = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if d.IsDir() {
				switch name := d.Name(); {
				case p == dir:
					return nil
				case name == "cue.mod":
					// Dependencies are in cue.mod/pkg; what is generated is not read.
					if pkg := filepath.Join(p, "pkg"); !seen[pkg] {
						seen[pkg] = true
						use.scanDir(pkg)
					}
					return filepath.SkipDir
				case name == "node_modules" || strings.HasPrefix(name, "."):
					return filepath.SkipDir
				}
				return nil
			}
			if strings.HasSuffix(p, ".cue") && !seen[p] {
				seen[p] = true
				use.scanFile(p)
			}
			return nil
		})
	}
	return use
}

func (u *typeUse) scanDir(dir string) {
	_ = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() && strings.HasSuffix(p, ".cue") {
			u.scanFile(p)
		}
		return nil
	})
}

func (u *typeUse) scanFile(path string) {
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	u.scan(data)
}

func (u *typeUse) scan(data []byte) {
	for _, m := range typeUseRe.FindAllSubmatch(data, -1) {
		kind, name := "#"+string(m[1]), string(m[2])
		if name == "" {
			u.dynamic = true
			continue
		}
		if strings.HasPrefix(name, `"`) {
			s, err := strconv.Unquote(name)
			if err != nil || strings.Contains(s, `\(`) {
				u.dynamic = true
				continue
			}
			name = s
		}
		if u.names[kind] == nil {
			u.names[kind] = map[string]bool{}
		}
		u.names[kind][name] = true
	}
}

// catalogueOpen is the line that opens each catalogue of a generated package;
// the catalogue runs to the next line that is a closing brace on its own, and
// each type in it starts at one tab and ends at "\t}".
var catalogueOpen = map[string]string{
	"_#Resource: {":  "#Resource",
	"_#Data: {":      "#Data",
	"_#Ephemeral: {": "#Ephemeral",
	"_#Discovery: {": "#Discovery",
	"_#Function: {":  "#Function",
}

// pruneGenerated returns src with the types that use does not name blanked, and
// whether it blanked any. It returns src as it is when it does not have the
// layout that pravo writes (the four catalogues at package level, and the
// catalogue of discoveries, which a package from an earlier pravo lacks), so
// that a package from an earlier pravo is loaded whole.
func pruneGenerated(src []byte, use *typeUse) ([]byte, bool) {
	lines := bytes.Split(src, []byte("\n"))
	opened := 0
	removed := 0
	for i := 0; i < len(lines); i++ {
		kind, ok := catalogueOpen[string(lines[i])]
		if !ok {
			continue
		}
		if kind != "#Discovery" {
			opened++
		}
		keep := use.names[kind]
		doc := -1 // where the comment above the type starts
		j := i + 1
		for ; j < len(lines) && string(lines[j]) != "}"; j++ {
			l := lines[j]
			if name, ok := typeStart(l); ok {
				end := j
				for end < len(lines) && string(lines[end]) != "\t}" {
					end++
				}
				if end == len(lines) {
					return src, false
				}
				if !keep[name] {
					from := j
					if doc >= 0 {
						from = doc
					}
					for k := from; k <= end; k++ {
						lines[k] = nil
					}
					removed++
				}
				j, doc = end, -1
				continue
			}
			if bytes.HasPrefix(l, []byte("\t//")) {
				if doc < 0 {
					doc = j
				}
			} else {
				doc = -1
			}
		}
		if j == len(lines) {
			return src, false
		}
		i = j
	}
	if opened != len(catalogueOpen)-1 {
		return src, false
	}
	if removed == 0 {
		return src, false
	}
	return bytes.Join(lines, []byte("\n")), true
}

// typeStart reports whether l opens a type of a catalogue, "\tname: {", and
// returns the name.
func typeStart(l []byte) (string, bool) {
	if len(l) < 5 || l[0] != '\t' || l[1] == '\t' || l[1] == '/' || !bytes.HasSuffix(l, []byte(": {")) {
		return "", false
	}
	name := string(l[1 : len(l)-3])
	name = strings.TrimSuffix(name, "?")
	if strings.HasPrefix(name, `"`) {
		s, err := strconv.Unquote(name)
		if err != nil {
			return "", false
		}
		return s, true
	}
	return name, true
}

// prunedOverlay returns the generated provider packages of the module at modRoot
// that are large enough to prune, without the types that use does not name, as an
// overlay for the CUE loader. It returns nothing when the specification uses a
// catalogue in a way that cannot be read, since any type may be needed then.
func prunedOverlay(modRoot string, use *typeUse, noPrune bool) map[string]load.Source {
	if use.dynamic || noPrune {
		return nil
	}
	dir := filepath.Join(modRoot, "cue.mod", "gen", "pravo.dev", "providers")
	var overlay map[string]load.Source
	_ = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(p, "_gen.cue") {
			return nil
		}
		if info, err := d.Info(); err != nil || info.Size() < pruneMinSize {
			return nil
		}
		src, err := os.ReadFile(p)
		if err != nil {
			return nil
		}
		if pruned, ok := pruneGenerated(src, use); ok {
			if overlay == nil {
				overlay = map[string]load.Source{}
			}
			overlay[p] = load.FromBytes(pruned)
		}
		return nil
	})
	return overlay
}
