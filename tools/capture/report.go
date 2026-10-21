package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// totals accumulates the summary line.
type totals struct {
	Pages, Ran, Failed int
	Commands           int
	Rewrote, Added     int
	Removed            int
}

// printResult writes the report of one page.
func printResult(w io.Writer, res *pageResult, update, verbose bool, t *totals) {
	p := res.Page
	t.Pages++
	if res.Skipped != "" {
		if verbose {
			fmt.Fprintf(w, "skip    %s  (%s)\n", p.Path, res.Skipped)
		}
		printWarnings(w, p)
		return
	}
	if res.Kept != "" {
		defer fmt.Fprintf(w, "        workspace kept in %s\n", filepath.Join(res.Kept, "ws"))
	}
	t.Ran++
	t.Commands += res.Commands
	t.Rewrote += res.Rewrote
	t.Added += res.Added
	t.Removed += res.Removed

	if res.failed() {
		t.Failed++
		fmt.Fprintf(w, "FAIL    %s\n", p.Path)
		for _, e := range p.Errors {
			fmt.Fprintf(w, "  %s:%d: %s\n", p.Path, e.Line, e.Msg)
		}
		if res.Err != nil {
			fmt.Fprintf(w, "  %s: %v\n", p.Path, res.Err)
		}
		for _, f := range res.Failures {
			where := fmt.Sprintf("block %d", f.Block)
			if f.Stage != "main" {
				where += " (" + f.Stage + ")"
			}
			fmt.Fprintf(w, "  %s:%d: %s: %s\n", p.Path, f.Line, where, f.Title)
			for _, l := range f.Detail {
				fmt.Fprintf(w, "      %s\n", l)
			}
		}
		printWarnings(w, p)
		return
	}
	switch {
	case update && res.Rewrote+res.Added+res.Removed > 0:
		fmt.Fprintf(w, "updated %s  %s\n", p.Path, updateSummary(res))
	default:
		fmt.Fprintf(w, "ok      %s  %s\n", p.Path, plural(res.Commands, "command"))
	}
	printWarnings(w, p)
}

func updateSummary(res *pageResult) string {
	var parts []string
	if res.Rewrote > 0 {
		parts = append(parts, fmt.Sprintf("%s rewritten", plural(res.Rewrote, "out block")))
	}
	if res.Added > 0 {
		parts = append(parts, fmt.Sprintf("%d added", res.Added))
	}
	if res.Removed > 0 {
		parts = append(parts, fmt.Sprintf("%d removed", res.Removed))
	}
	return strings.Join(parts, ", ")
}

func printWarnings(w io.Writer, p *page) {
	for _, wn := range p.Warnings {
		fmt.Fprintf(w, "warning %s:%d: %s\n", p.Path, wn.Line, wn.Msg)
	}
}

func plural(n int, noun string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, noun)
	}
	return fmt.Sprintf("%d %ss", n, noun)
}

func (t totals) line(update bool) string {
	parts := []string{plural(t.Ran, "page"), plural(t.Commands, "command") + " run"}
	if update {
		var changed []string
		if t.Rewrote > 0 {
			changed = append(changed, plural(t.Rewrote, "out block")+" rewritten")
		}
		if t.Added > 0 {
			changed = append(changed, fmt.Sprintf("%d added", t.Added))
		}
		if t.Removed > 0 {
			changed = append(changed, fmt.Sprintf("%d removed", t.Removed))
		}
		if len(changed) == 0 {
			changed = []string{"no changes"}
		}
		parts = append(parts, strings.Join(changed, ", "))
	}
	switch {
	case t.Failed > 0:
		parts = append(parts, plural(t.Failed, "page")+" failed")
	case !update:
		parts = append(parts, "no differences")
	}
	return "capture: " + strings.Join(parts, ", ")
}

// jsonPage is the -json record of one page.
type jsonPage struct {
	Page    string   `json:"page"`
	Title   string   `json:"title,omitempty"`
	Version []string `json:"version,omitempty"`
	Blocks  []record `json:"blocks"`
}

func newJSONPage(res *pageResult, version []string) jsonPage {
	jp := jsonPage{Page: filepath.ToSlash(res.Page.Path), Title: res.Page.FM.Title, Version: version, Blocks: res.Records}
	if jp.Blocks == nil {
		jp.Blocks = []record{}
	}
	return jp
}

// writeJSON writes the run records. One page goes to the file out; several
// pages go to the directory out, one file per page, named after the page.
func writeJSON(out string, results []*pageResult, roots []string, version []string) error {
	var ran []*pageResult
	for _, r := range results {
		if r.Skipped == "" && len(r.Page.Errors) == 0 {
			ran = append(ran, r)
		}
	}
	if len(ran) == 0 {
		return fmt.Errorf("-json: no page ran")
	}
	asDir := len(ran) > 1
	if info, err := os.Stat(out); err == nil && info.IsDir() {
		asDir = true
	}
	if !asDir {
		return writeJSONFile(out, newJSONPage(ran[0], version))
	}
	used := map[string]bool{}
	for _, r := range ran {
		name := jsonName(r.Page.Path, roots)
		if used[name] {
			return fmt.Errorf("-json: two pages would write %s", name)
		}
		used[name] = true
		if err := writeJSONFile(filepath.Join(out, name), newJSONPage(r, version)); err != nil {
			return err
		}
	}
	return nil
}

// jsonName names the file of a page: its path below the directory it was
// found in, or its base name when it was given as a file, with .json.
func jsonName(pagePath string, roots []string) string {
	for _, root := range roots {
		if rel, err := filepath.Rel(root, pagePath); err == nil && !strings.HasPrefix(rel, "..") && rel != "." {
			return strings.TrimSuffix(rel, filepath.Ext(rel)) + ".json"
		}
	}
	return strings.TrimSuffix(filepath.Base(pagePath), filepath.Ext(pagePath)) + ".json"
}

func writeJSONFile(path string, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o644)
}
