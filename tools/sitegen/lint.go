package main

import (
	"bufio"
	_ "embed"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

//go:embed banned.txt
var defaultBanned string

// bannedRule is one line of banned.txt.
type bannedRule struct {
	pattern string
	re      *regexp.Regexp
}

// parseBanned reads one regular expression per line; blank lines and lines
// starting with # are ignored. Matching is case-insensitive.
func parseBanned(text, name string) ([]bannedRule, error) {
	var rules []bannedRule
	sc := bufio.NewScanner(strings.NewReader(text))
	for n := 1; sc.Scan(); n++ {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		re, err := regexp.Compile("(?i)" + line)
		if err != nil {
			return nil, fmt.Errorf("%s:%d: %v", name, n, err)
		}
		rules = append(rules, bannedRule{pattern: line, re: re})
	}
	return rules, sc.Err()
}

// loadBanned returns the rules from file, or the embedded list when file is
// empty.
func loadBanned(file string) ([]bannedRule, error) {
	if file == "" {
		return parseBanned(defaultBanned, "tools/sitegen/banned.txt")
	}
	data, err := os.ReadFile(file)
	if err != nil {
		return nil, err
	}
	return parseBanned(string(data), file)
}

var lintExts = map[string]bool{".md": true, ".html": true, ".json": true, ".txt": true}

// lintRoots checks every text file below the roots (and the plain files given)
// against the rules and returns one message per match.
func lintRoots(rules []bannedRule, skipDirs []string, roots ...string) []string {
	var msgs []string
	skip := map[string]bool{}
	for _, d := range skipDirs {
		if a, err := filepath.Abs(d); err == nil {
			skip[a] = true
		}
	}
	for _, root := range roots {
		st, err := os.Stat(root)
		if err != nil {
			continue
		}
		if !st.IsDir() {
			msgs = append(msgs, lintFile(rules, root)...)
			continue
		}
		_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			abs, _ := filepath.Abs(p)
			if d.IsDir() {
				base := d.Name()
				if skip[abs] || base == ".git" || base == "node_modules" || base == "assets" {
					return filepath.SkipDir
				}
				return nil
			}
			if skip[abs] {
				return nil
			}
			if !lintExts[strings.ToLower(filepath.Ext(p))] || d.Name() == "AUTHORING.md" {
				return nil
			}
			msgs = append(msgs, lintFile(rules, p)...)
			return nil
		})
	}
	return msgs
}

var (
	rawOpenRe  = regexp.MustCompile(`(?i)<(pre|script|style)\b`)
	rawCloseRe = regexp.MustCompile(`(?i)</(pre|script|style)>`)
)

// lintFile checks one file. Fenced code in Markdown and <pre>, <script> and
// <style> blocks in HTML are skipped.
func lintFile(rules []bannedRule, file string) []string {
	f, err := os.Open(file)
	if err != nil {
		return []string{fmt.Sprintf("%s: %v", file, err)}
	}
	defer f.Close()
	isMD := strings.EqualFold(filepath.Ext(file), ".md")
	isHTML := strings.EqualFold(filepath.Ext(file), ".html")
	var msgs []string
	var fence fenceTracker
	inRaw := false
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for n := 1; sc.Scan(); n++ {
		line := sc.Text()
		if isMD && fence.feed(line) {
			continue
		}
		if isHTML || isMD {
			opens := rawOpenRe.MatchString(line)
			closes := rawCloseRe.MatchString(line)
			switch {
			case inRaw:
				if closes {
					inRaw = false
				}
				continue
			case opens && closes:
				continue
			case opens:
				inRaw = true
				continue
			}
		}
		for _, r := range rules {
			if loc := r.re.FindStringIndex(line); loc != nil {
				msgs = append(msgs, fmt.Sprintf("%s:%d: banned phrase %q (rule %s)", file, n, line[loc[0]:loc[1]], r.pattern))
			}
		}
	}
	return msgs
}
