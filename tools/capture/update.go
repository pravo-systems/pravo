package main

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// applyEdits rewrites the page file with the planned changes to its out
// blocks. Commands, files and prose are copied unchanged: the edits only
// touch lines of out blocks, and the new blocks that follow run blocks.
func (pr *pageRun) applyEdits() error {
	edits := append([]edit{}, pr.edits...)
	sort.Slice(edits, func(i, j int) bool { return edits[i].at() > edits[j].at() })

	lines := append([]string{}, pr.p.Lines...)
	for _, e := range edits {
		switch e.kind {
		case editReplace:
			lines = replaceOut(lines, e.out, e.lines)
		case editInsert:
			lines = insertOut(lines, e.run, e.lines)
		case editRemove:
			lines = splice(lines, e.run.Close+1, e.out.Close+1, nil)
		}
	}
	text := strings.Join(lines, "\n")
	if pr.p.FinalNL {
		text += "\n"
	}
	if pr.p.BOM {
		text = bom + text
	}
	return writeAtomic(pr.p.Path, []byte(text))
}

// splice replaces lines[from:to] with repl.
func splice(lines []string, from, to int, repl []string) []string {
	out := make([]string, 0, len(lines)-(to-from)+len(repl))
	out = append(out, lines[:from]...)
	out = append(out, repl...)
	return append(out, lines[to:]...)
}

// replaceOut swaps the content of an existing out block. The opening fence
// line is kept, so the info string stays as written; the fence grows only
// when the new content has a line that would close it.
func replaceOut(lines []string, out *block, content []string) []string {
	n := fenceLength(content, out.FenceCh, out.FenceLen)
	open := lines[out.Open]
	if n > out.FenceLen {
		open = open[:out.Indent] + strings.Repeat(string(out.FenceCh), n) + open[out.Indent+out.FenceLen:]
	}
	indent := strings.Repeat(" ", out.Indent)
	repl := append([]string{open}, indented(content, indent)...)
	repl = append(repl, indent+strings.Repeat(string(out.FenceCh), n))
	return splice(lines, out.Open, out.Close+1, repl)
}

// insertOut adds an out block after the run block, separated by a blank line.
func insertOut(lines []string, run *block, content []string) []string {
	n := fenceLength(content, '`', 3)
	fence := strings.Repeat("`", n)
	indent := strings.Repeat(" ", run.Indent)
	repl := []string{"", indent + fence + "text out"}
	repl = append(repl, indented(content, indent)...)
	repl = append(repl, indent+fence)
	if next := run.Close + 1; next < len(lines) && strings.TrimSpace(lines[next]) != "" {
		repl = append(repl, "")
	}
	return splice(lines, run.Close+1, run.Close+1, repl)
}

func indented(content []string, indent string) []string {
	out := make([]string, len(content))
	for i, l := range content {
		if l != "" {
			l = indent + l
		}
		out[i] = l
	}
	return out
}

// fenceLength returns the shortest fence of ch, at least floor long, that no
// line of content can close.
func fenceLength(content []string, ch byte, floor int) int {
	n := floor
	for _, l := range content {
		t := strings.TrimLeft(l, " ")
		run := 0
		for run < len(t) && t[run] == ch {
			run++
		}
		if run >= 3 && run >= n {
			n = run + 1
		}
	}
	return n
}

// writeAtomic replaces the file at path, keeping its mode, so that an
// interrupted run never leaves half a page.
func writeAtomic(path string, data []byte) error {
	if real, err := filepath.EvalSymlinks(path); err == nil {
		path = real
	}
	mode := os.FileMode(0o644)
	if info, err := os.Stat(path); err == nil {
		mode = info.Mode().Perm()
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".capture-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(name)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(name)
		return err
	}
	if err := os.Chmod(name, mode); err != nil {
		os.Remove(name)
		return err
	}
	return os.Rename(name, path)
}
