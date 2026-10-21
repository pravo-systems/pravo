package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
)

// siteConfig is the content of site/site.json.
type siteConfig struct {
	Name         string          `json:"name"`          // brand name, "pravo"
	Title        string          `json:"title"`         // <title> of the landing page
	Description  string          `json:"description"`   // meta description, llms.txt summary
	PravoVersion string          `json:"pravo_version"` // "0.1"; replaced by -pravo-bin output
	CUEVersion   string          `json:"cue_version"`   // "0.17"; replaced by -pravo-bin build info
	Repo         string          `json:"repo"`          // repository URL; the link appears only when set
	BaseURL      string          `json:"base_url"`      // absolute site URL, used by sitemap.xml and canonical links
	StartLabel   string          `json:"start_label"`   // text of the primary button, "Start"
	StartHref    string          `json:"start_href"`    // target of the primary button, relative to the site root
	Glossary     string          `json:"glossary"`      // docs path of the glossary page, "reference/glossary.md"
	FooterNote   string          `json:"footer_note"`
	Preload      []string        `json:"preload"`        // font files (relative to the site root) to preload
	LintSkip     []string        `json:"lint_skip"`      // files and directories below the site directory that the phrase lint leaves alone
	Suggest      []string        `json:"search_suggest"` // docs paths of the pages the search palette offers before anything is typed
	Sections     []sectionConfig `json:"sections"`
}

// sectionConfig describes one top-level section of the site.
type sectionConfig struct {
	ID       string       `json:"id"`       // directory under docs/, data-section value
	Title    string       `json:"title"`    // nav label and breadcrumb
	Blurb    string       `json:"blurb"`    // one sentence, used in llms.txt and the contents page
	Nav      *bool        `json:"nav"`      // false keeps the section out of the header nav (default true)
	Progress bool         `json:"progress"` // pages get a "Mark as done" button
	Groups   []string     `json:"groups"`   // sidebar group order; unlisted groups follow in file order
	Pinned   []pinnedLink `json:"pinned"`   // links shown above the sidebar groups
}

type pinnedLink struct {
	Title string `json:"title"`
	Href  string `json:"href"` // docs path of a Markdown page, e.g. "reference/glossary.md"
}

var sectionIDRe = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)

// loadConfig reads and validates site.json. A missing file is an error: the
// section list lives there.
func loadConfig(file string) (*siteConfig, error) {
	data, err := os.ReadFile(file)
	if err != nil {
		return nil, fmt.Errorf("site config: %w", err)
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var c siteConfig
	if err := dec.Decode(&c); err != nil {
		return nil, fmt.Errorf("%s: %w", file, err)
	}
	if c.Name == "" {
		c.Name = "pravo"
	}
	if c.Title == "" {
		c.Title = c.Name
	}
	if c.StartLabel == "" {
		c.StartLabel = "Start"
	}
	if c.Glossary == "" {
		c.Glossary = "reference/glossary.md"
	}
	c.Repo = strings.TrimSpace(c.Repo)
	c.BaseURL = strings.TrimRight(strings.TrimSpace(c.BaseURL), "/")
	if len(c.Sections) == 0 {
		return nil, fmt.Errorf("%s: no sections", file)
	}
	seen := map[string]bool{}
	for i := range c.Sections {
		s := &c.Sections[i]
		if !sectionIDRe.MatchString(s.ID) {
			return nil, fmt.Errorf("%s: section id %q must be lowercase letters, digits and hyphens", file, s.ID)
		}
		if seen[s.ID] {
			return nil, fmt.Errorf("%s: duplicate section id %q", file, s.ID)
		}
		seen[s.ID] = true
		if s.Title == "" {
			return nil, fmt.Errorf("%s: section %q has no title", file, s.ID)
		}
	}
	if c.StartHref == "" {
		c.StartHref = c.Sections[0].ID + "/index.html"
	}
	c.StartHref = path.Clean(filepath.ToSlash(c.StartHref))
	return &c, nil
}

// inNav reports whether the section appears in the header navigation.
func (s *sectionConfig) inNav() bool { return s.Nav == nil || *s.Nav }

// applies returns the "pravo X.Y · CUE X.Y" line.
func (c *siteConfig) applies() string {
	var parts []string
	if c.PravoVersion != "" {
		parts = append(parts, "pravo "+c.PravoVersion)
	}
	if c.CUEVersion != "" {
		parts = append(parts, "CUE "+c.CUEVersion)
	}
	return strings.Join(parts, " · ")
}
