// Command sitegen builds the pravo documentation site.
//
// It renders the Markdown pages under docs/<section>/ to HTML, assembles the
// landing page from site/landing/*.html, copies site/assets, and writes the
// search index, sitemap, llms.txt files and Markdown twins. Everything is
// static: no JavaScript is needed to read the output, and every link is
// relative, so the result works from the file system and from a sub-path.
//
// The JavaScript and CSS files of site/assets are minified on the way to the
// output; the sources are not changed. -minify=false copies them as written.
//
// Usage, from the module root:
//
//	go run ./tools/sitegen
//	go run ./tools/sitegen -minify=false
//	go run ./tools/sitegen -docs tools/sitegen/testdata/docs
//	go run ./tools/sitegen -pravo-bin ./pravo
//
// The sections, the version line, the repository link and the target of the
// Start button come from site/site.json. tools/sitegen/README.md describes
// that file and the Markdown extensions; tools/sitegen/HTML.md is the contract
// between the generated HTML and the style sheet and scripts in site/assets.
package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type options struct {
	docs, site, out, config  string
	pravoBin, banned, readme string
	baseURL                  string
	lint, check, minify      bool
}

func main() {
	log.SetFlags(0)
	log.SetPrefix("sitegen: ")

	o, _ := parseOptions(os.Args[1:], flag.ExitOnError)
	if err := run(o); err != nil {
		log.Fatal(err)
	}
}

// parseOptions reads the command line.
func parseOptions(args []string, handling flag.ErrorHandling) (options, error) {
	var o options
	fs := flag.NewFlagSet("sitegen", handling)
	fs.StringVar(&o.docs, "docs", "docs", "directory with the section directories of Markdown sources")
	fs.StringVar(&o.site, "site", "site", "directory with site.json, assets/ and landing/")
	fs.StringVar(&o.out, "out", filepath.Join("site", "public"), "output directory (emptied first)")
	fs.StringVar(&o.config, "config", "", "site configuration (default: <site>/site.json)")
	fs.StringVar(&o.pravoBin, "pravo-bin", "", "pravo binary; its version fills the footer line")
	fs.StringVar(&o.banned, "banned", "", "file of banned phrases (default: tools/sitegen/banned.txt, built in)")
	fs.StringVar(&o.readme, "readme", "README.md", "README to lint along with docs/ and site/ (skipped when absent)")
	fs.StringVar(&o.baseURL, "base-url", "", "absolute URL of the site, for sitemap.xml and canonical links (overrides site.json)")
	fs.BoolVar(&o.lint, "lint", true, "fail on banned phrases")
	fs.BoolVar(&o.check, "check", true, "check every link and anchor in the output")
	fs.BoolVar(&o.minify, "minify", true, "minify the JavaScript and CSS copied from <site>/assets; the sources are not changed")
	err := fs.Parse(args)
	return o, err
}

func run(o options) error {
	if o.config == "" {
		o.config = filepath.Join(o.site, "site.json")
	}
	cfg, err := loadConfig(o.config)
	if err != nil {
		return err
	}
	if o.baseURL != "" {
		cfg.BaseURL = strings.TrimRight(o.baseURL, "/")
	}
	s := &site{cfg: cfg, docsDir: o.docs, siteDir: o.site, outDir: o.out, msgs: &messages{}}
	if o.minify {
		s.minifier = newAssetMinifier()
	}
	if o.pravoBin != "" {
		pv, cv, err := detectVersions(o.pravoBin)
		if err != nil {
			return err
		}
		if pv != "" {
			cfg.PravoVersion = pv
		}
		if cv != "" {
			cfg.CUEVersion = cv
		}
		fmt.Printf("versions from %s: %s\n", o.pravoBin, cfg.applies())
	}
	s.applies = cfg.applies()
	if err := safeOutDir(o.out, o.docs, o.site, ".", "tools"); err != nil {
		return err
	}

	s.discover()
	s.arrange()

	if o.lint {
		rules, err := loadBanned(o.banned)
		if err != nil {
			return fmt.Errorf("banned phrases: %w", err)
		}
		skip := []string{o.out, filepath.Join(o.site, "public")}
		for _, f := range cfg.LintSkip {
			skip = append(skip, filepath.Join(o.site, filepath.FromSlash(f)))
		}
		for _, m := range lintRoots(rules, skip, o.docs, o.site, o.readme) {
			s.msgs.errs = append(s.msgs.errs, m)
		}
	}

	s.loadGlossary()
	for _, p := range s.allPages() {
		s.renderPage(p)
	}
	s.checkStart()
	s.prepareLanding()
	s.prepareAssets()

	reportMessages(s.msgs)
	if len(s.msgs.errs) > 0 {
		return fmt.Errorf("%d errors; nothing was written", len(s.msgs.errs))
	}

	if err := os.RemoveAll(o.out); err != nil {
		return err
	}
	if err := os.MkdirAll(o.out, 0o755); err != nil {
		return err
	}
	// A site that has an address is served by a web server, where a link to a
	// directory is the clean form of a link to its index.html.
	of := &outFiles{dir: o.out, pretty: s.cfg.BaseURL != ""}
	if err := s.copyStatic(of); err != nil {
		return err
	}
	if err := s.writeAll(of); err != nil {
		return err
	}
	pages := s.allPages()
	redirects := 0
	for _, sec := range s.sections {
		redirects += len(sec.redirects)
	}
	for _, sec := range s.sections {
		if !sec.Missing {
			fmt.Printf("wrote %s/ (%d pages)\n", sec.ID, len(sec.Pages))
		}
	}
	extras := "404 page, search index, llms files"
	if s.cfg.BaseURL != "" {
		extras = "404 page, search index, sitemap, llms files"
	}
	fmt.Printf("wrote %s: %d pages, %d redirects, landing page, %s\n", o.out, len(pages), redirects, extras)
	if line := s.minStats.summary(); line != "" {
		fmt.Println(line)
	}

	if o.check {
		problems, n := checkOutputAt(o.out, s.sitePath())
		if len(problems) > 0 {
			for _, p := range problems {
				fmt.Fprintln(os.Stderr, "  "+p)
			}
			return fmt.Errorf("%d broken links or anchors", len(problems))
		}
		fmt.Printf("checked links in %d pages: none broken\n", n)
	}
	return nil
}

// checkStart verifies that the Start button points at a page that will exist.
func (s *site) checkStart() {
	targets := map[string]bool{"index.html": true, "contents.html": true}
	for _, p := range s.allPages() {
		targets[p.Out] = true
	}
	if !targets[s.cfg.StartHref] {
		s.msgs.errorf("site.json: start_href %q is not a page of the site", s.cfg.StartHref)
	}
	for _, doc := range s.cfg.Suggest {
		if _, ok := s.byDoc[doc]; !ok {
			s.msgs.errorf("site.json: search_suggest %q is not a page", doc)
		}
	}
}

// reportMessages prints warnings and errors, each list sorted and without
// duplicates.
func reportMessages(m *messages) {
	dedupe := func(in []string) []string {
		seen := map[string]bool{}
		var out []string
		for _, s := range in {
			if !seen[s] {
				seen[s] = true
				out = append(out, s)
			}
		}
		sort.SliceStable(out, func(i, j int) bool { return out[i] < out[j] })
		return out
	}
	m.warns = dedupe(m.warns)
	m.errs = dedupe(m.errs)
	for _, w := range m.warns {
		fmt.Fprintln(os.Stderr, "warning: "+w)
	}
	for _, e := range m.errs {
		fmt.Fprintln(os.Stderr, "error: "+e)
	}
}
