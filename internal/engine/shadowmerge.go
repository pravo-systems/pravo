package engine

import (
	"regexp"
	"sort"
	"strings"
)

// A shadow that is taken again does not throw away what a person changed in it. The file
// that pravo shadow writes is a series of blocks, one for each resource of the Terraform state,
// each under a comment with the Terraform address. The note of the shadow keeps the text of
// each block as it was written. A new shadow is merged with the file as it is now, one block at
// a time:
//
//	the person did not touch the block        the new text
//	only the person changed it                 the person's text
//	both changed it                            the person's text, and the block is named
//
// The merge is by address, not by line, so that a state that moved a hundred lines does not
// disturb an edit, and an edit does not hide a change of the state.

// ShadowBlock is the text of one resource in the file of a shadow.
type ShadowBlock struct {
	// Addr is the Terraform address, from the comment above the block.
	Addr string
	// Label is the name that the block gives the resource.
	Label string
	// Text is the comment, the block and the line break after it.
	Text string
}

var shadowAddrComment = regexp.MustCompile(`^(\t*)// ((?:module\.[^ ]+\.)?[A-Za-z_][A-Za-z0-9_-]*\.[^ ]+)$`)

// ShadowBlocks cuts the source of a shadow into its blocks, in order. A block begins at a
// comment that is a Terraform address and ends at the closing brace at the same indent.
func ShadowBlocks(src string) []ShadowBlock {
	lines := strings.SplitAfter(src, "\n")
	var out []ShadowBlock
	for i := 0; i < len(lines); i++ {
		m := shadowAddrComment.FindStringSubmatch(strings.TrimRight(lines[i], "\n"))
		if m == nil || i+1 >= len(lines) {
			continue
		}
		label, _, ok := strings.Cut(strings.TrimSpace(lines[i+1]), ":")
		if !ok || !strings.HasSuffix(strings.TrimRight(lines[i+1], "\n"), "{") {
			continue
		}
		end := m[1] + "}"
		for j := i + 1; j < len(lines); j++ {
			if strings.TrimRight(lines[j], "\n") == end {
				out = append(out, ShadowBlock{Addr: m[2], Label: label, Text: strings.Join(lines[i:j+1], "")})
				i = j
				break
			}
		}
	}
	return out
}

// ShadowBlockTexts is the text of each block by address, the form the note keeps.
func ShadowBlockTexts(src string) map[string]string {
	out := map[string]string{}
	for _, b := range ShadowBlocks(src) {
		out[b.Addr] = b.Text
	}
	return out
}

// ShadowMerged is the outcome of a merge.
type ShadowMerged struct {
	Source string
	// Edited are the Terraform addresses whose block is the person's.
	Edited []string
	// Updated are the blocks that took the new text, Added those the state gained.
	Updated, Added []string
	// Conflicts are the edited blocks that the state changed as well, and Unplaced the edited
	// blocks of resources that left the state, which have no place in the new file.
	Conflicts, Unplaced []string
	// Unknown is the number of blocks that differ from the fresh text when the note has no
	// text to tell an edit from a change of the state: they are taken to be edits.
	Unknown int
}

// MergeShadow merges the fresh source of a shadow into the file as it is now. base holds the
// blocks as the last shadow wrote them (nil when the note is gone).
func MergeShadow(current string, base map[string]string, fresh string) ShadowMerged {
	cur := map[string]ShadowBlock{}
	for _, b := range ShadowBlocks(current) {
		cur[b.Addr] = b
	}
	var res ShadowMerged
	seen := map[string]bool{}
	var out strings.Builder
	rest := fresh
	for _, nb := range ShadowBlocks(fresh) {
		seen[nb.Addr] = true
		i := strings.Index(rest, nb.Text)
		out.WriteString(rest[:i])
		rest = rest[i+len(nb.Text):]
		text := nb.Text
		cb, has := cur[nb.Addr]
		old, hasBase := base[nb.Addr]
		switch {
		case !has:
			res.Added = append(res.Added, nb.Addr)
		case cb.Text == nb.Text:
		case hasBase && cb.Text == old:
			res.Updated = append(res.Updated, nb.Addr)
		case cb.Label != nb.Label:
			// Another name: the other blocks refer to the new one.
			res.Updated = append(res.Updated, nb.Addr)
		default:
			text = cb.Text
			res.Edited = append(res.Edited, nb.Addr)
			if hasBase && nb.Text != old {
				res.Conflicts = append(res.Conflicts, nb.Addr)
			}
			if !hasBase {
				res.Unknown++
			}
		}
		out.WriteString(text)
	}
	out.WriteString(rest)
	for addr, cb := range cur {
		if seen[addr] {
			continue
		}
		if old, ok := base[addr]; !ok || cb.Text != old {
			res.Unplaced = append(res.Unplaced, addr)
		}
	}
	res.Source = out.String()
	sort.Strings(res.Edited)
	sort.Strings(res.Updated)
	sort.Strings(res.Added)
	sort.Strings(res.Conflicts)
	sort.Strings(res.Unplaced)
	return res
}
