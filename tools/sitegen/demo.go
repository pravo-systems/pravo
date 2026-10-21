package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// A demo is a block of HTML that a page of the documentation embeds:
//
//	:::demo NAME
//	:::
//
// The block is site/demos/NAME/embed.html. It may use the tokens of a landing
// partial ({{data file.key}}, {{json file.key}}, {{start}} and the others), and
// it is written as if it stood on a page at the root of the site: a relative
// href or src is prefixed with the path from the page to the root, so that
// assets/demos/NAME.js and learn/index.html resolve from any depth.

var (
	demoNameRe = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)
	demoAttrRe = regexp.MustCompile(`\b(href|src)="([^"]*)"`)
	// demoDirectiveRe matches the opener and closer of a demo in a page body.
	demoDirectiveRe = regexp.MustCompile(`(?m)^:::demo[ \t]+\S+[ \t]*\n:::[ \t]*$`)
)

// demoHTML reads and expands the embed file of a demo for a page whose path
// to the site root is root.
func (s *site) demoHTML(name, root string) (string, error) {
	rel := "demos/" + name + "/embed.html"
	raw, err := os.ReadFile(filepath.Join(s.siteDir, filepath.FromSlash(rel)))
	if err != nil {
		return "", fmt.Errorf("site/%s cannot be read: %v", rel, err)
	}
	text := s.substituteTokens(rel, string(raw))
	text = demoAttrRe.ReplaceAllStringFunc(text, func(m string) string {
		sub := demoAttrRe.FindStringSubmatch(m)
		v := sub[2]
		if v == "" || strings.HasPrefix(v, "#") || strings.HasPrefix(v, "/") || strings.Contains(v, ":") {
			return m
		}
		return sub[1] + `="` + root + v + `"`
	})
	return strings.TrimSpace(text), nil
}

// renderDemo renders ":::demo NAME".
func (r *renderer) renderDemo(d *directive) string {
	if !demoNameRe.MatchString(d.arg) {
		r.errorf(d.line, ":::demo needs a name of lowercase letters, digits and hyphens, found %q", d.arg)
		return ""
	}
	text, err := r.s.demoHTML(d.arg, rootPrefix(r.p.Out))
	if err != nil {
		r.errorf(d.line, ":::demo %s: %v", d.arg, err)
		return ""
	}
	if strings.Contains(text, "data-widget=") {
		r.hasWidget = true // the page loads widgets.js
	}
	return fmt.Sprintf("<div class=\"demo\" data-demo=\"%s\">\n%s\n</div>", d.arg, text)
}
