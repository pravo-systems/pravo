package main

import (
	"bytes"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"go.yaml.in/yaml/v3"
)

// frontMatter is the YAML header of a page (docs/AUTHORING.md section 3).
type frontMatter struct {
	Title       string
	Description string
	Order       int
	HasOrder    bool
	Group       string
	Minutes     int
	Needs       string
	Learn       string
	DoneWhen    string
	Level       string
	Redirect    string
	AppliesTo   string
	Keywords    string // words a reader may type that the page does not say, for search
	Capture     bool   // false when the page declares "capture: false"
	NoTOC       bool   // true when the page declares "toc: false": no table of contents, a wider column
	Gate        string // the check that ends a step of a runbook; a page that has one is a step
}

// knownKeys lists every key a page may carry. "normalize", "services" and
// "env" belong to tools/capture and are ignored here.
var knownKeys = map[string]bool{
	"title": true, "description": true, "order": true, "group": true,
	"minutes": true, "needs": true, "learn": true, "done_when": true,
	"level": true, "redirect": true, "applies_to": true, "capture": true,
	"normalize": true, "keywords": true, "services": true, "env": true, "toc": true, "gate": true,
}

var levels = map[string]bool{"beginner": true, "intermediate": true, "advanced": true}

// splitFrontMatter separates the leading "---" block from the body. It returns
// the YAML text, the body and the number of lines the header occupies (so
// that line numbers in messages refer to the file).
func splitFrontMatter(src []byte) (yamlText string, body []byte, headerLines int, ok bool) {
	t := bytes.TrimPrefix(src, []byte("\xef\xbb\xbf"))
	t = bytes.ReplaceAll(t, []byte("\r\n"), []byte("\n"))
	if !bytes.HasPrefix(t, []byte("---\n")) {
		return "", t, 0, false
	}
	rest := t[4:]
	end := -1
	pos := 0
	for pos <= len(rest) {
		nl := bytes.IndexByte(rest[pos:], '\n')
		var line []byte
		if nl < 0 {
			line = rest[pos:]
		} else {
			line = rest[pos : pos+nl]
		}
		if string(bytes.TrimRight(line, " \t")) == "---" {
			end = pos
			if nl < 0 {
				body = nil
			} else {
				body = rest[pos+nl+1:]
			}
			break
		}
		if nl < 0 {
			break
		}
		pos += nl + 1
	}
	if end < 0 {
		return "", t, 0, false
	}
	yamlText = string(rest[:end])
	headerLines = 2 + strings.Count(yamlText, "\n")
	return yamlText, body, headerLines, true
}

// parseFrontMatter decodes and validates the header. file is used in
// messages only.
func parseFrontMatter(file, text string) (frontMatter, error) {
	fm := frontMatter{Capture: true}
	var root yaml.Node
	if err := yaml.Unmarshal([]byte(text), &root); err != nil {
		return fm, fmt.Errorf("%s: front matter: %v", file, err)
	}
	if root.Kind == 0 {
		return fm, nil
	}
	if root.Kind != yaml.DocumentNode || len(root.Content) != 1 || root.Content[0].Kind != yaml.MappingNode {
		return fm, fmt.Errorf("%s: front matter must be a mapping", file)
	}
	m := root.Content[0]
	var unknown []string
	for i := 0; i+1 < len(m.Content); i += 2 {
		k, v := m.Content[i], m.Content[i+1]
		key := k.Value
		if !knownKeys[key] {
			unknown = append(unknown, key)
			continue
		}
		if key == "normalize" || key == "services" || key == "env" {
			continue
		}
		if v.Kind != yaml.ScalarNode {
			return fm, fmt.Errorf("%s: front matter: %q must be a single value", file, key)
		}
		val := strings.TrimSpace(v.Value)
		switch key {
		case "title":
			fm.Title = val
		case "description":
			fm.Description = val
		case "group":
			fm.Group = val
		case "needs":
			fm.Needs = val
		case "learn":
			fm.Learn = val
		case "done_when":
			fm.DoneWhen = val
		case "level":
			fm.Level = val
		case "redirect":
			fm.Redirect = val
		case "applies_to":
			fm.AppliesTo = val
		case "keywords":
			fm.Keywords = val
		case "gate":
			fm.Gate = val
		case "order":
			n, err := strconv.Atoi(val)
			if err != nil {
				return fm, fmt.Errorf("%s: front matter: order %q is not an integer", file, val)
			}
			fm.Order, fm.HasOrder = n, true
		case "minutes":
			n, err := strconv.Atoi(val)
			if err != nil || n <= 0 {
				return fm, fmt.Errorf("%s: front matter: minutes %q is not a positive integer", file, val)
			}
			fm.Minutes = n
		case "toc":
			b, err := strconv.ParseBool(val)
			if err != nil {
				return fm, fmt.Errorf("%s: front matter: toc %q is not true or false", file, val)
			}
			fm.NoTOC = !b
		case "capture":
			b, err := strconv.ParseBool(val)
			if err != nil {
				return fm, fmt.Errorf("%s: front matter: capture %q is not true or false", file, val)
			}
			fm.Capture = b
		}
	}
	if len(unknown) > 0 {
		sort.Strings(unknown)
		return fm, fmt.Errorf("%s: front matter: unknown key %s", file, strings.Join(unknown, ", "))
	}
	if fm.Level != "" && !levels[fm.Level] {
		return fm, fmt.Errorf("%s: front matter: level %q must be beginner, intermediate or advanced", file, fm.Level)
	}
	return fm, nil
}

// validate checks the keys a rendered page needs.
func (fm *frontMatter) validate(file string, isIndex bool) error {
	if fm.Redirect != "" {
		return nil
	}
	var missing []string
	if fm.Title == "" {
		missing = append(missing, "title")
	}
	if fm.Description == "" {
		missing = append(missing, "description")
	}
	if !fm.HasOrder && !isIndex {
		missing = append(missing, "order")
	}
	if len(missing) > 0 {
		return fmt.Errorf("%s: front matter: missing %s", file, strings.Join(missing, ", "))
	}
	return nil
}
