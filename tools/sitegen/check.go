package main

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	xhtml "golang.org/x/net/html"
)

// htmlInfo is what the checker needs to know about one generated page.
type htmlInfo struct {
	ids       map[string]bool
	dupIDs    []string
	links     []string // href, src and poster values
	dataPages []string // data-page values (root-relative page paths)
	refresh   []string // targets of <meta http-equiv="refresh">
}

var refreshRe = regexp.MustCompile(`(?i)url\s*=\s*['"]?([^'";]+)`)

func scanHTML(data []byte) htmlInfo {
	info := htmlInfo{ids: map[string]bool{}}
	z := xhtml.NewTokenizer(strings.NewReader(string(data)))
	dup := map[string]bool{}
	for {
		tt := z.Next()
		if tt == xhtml.ErrorToken {
			return info
		}
		if tt != xhtml.StartTagToken && tt != xhtml.SelfClosingTagToken {
			continue
		}
		tok := z.Token()
		var httpEquiv, content string
		for _, a := range tok.Attr {
			switch a.Key {
			case "id":
				if info.ids[a.Val] && !dup[a.Val] {
					dup[a.Val] = true
					info.dupIDs = append(info.dupIDs, a.Val)
				}
				info.ids[a.Val] = true
			case "href", "xlink:href", "src", "poster":
				if tok.Data != "base" {
					info.links = append(info.links, a.Val)
				}
			case "data-page":
				info.dataPages = append(info.dataPages, a.Val)
			case "http-equiv":
				httpEquiv = strings.ToLower(a.Val)
			case "content":
				content = a.Val
			}
		}
		if tok.Data == "meta" && httpEquiv == "refresh" {
			if m := refreshRe.FindStringSubmatch(content); m != nil {
				info.refresh = append(info.refresh, strings.TrimSpace(m[1]))
			}
		}
	}
}

// externalRef reports whether a reference leaves the site.
func externalRef(ref string) bool {
	return schemeRe.MatchString(ref) || strings.HasPrefix(ref, "//")
}

// checkOutput verifies the generated site: every relative link, asset
// reference, fragment, redirect target and search index entry must exist, and
// no page may use an id twice. It returns the problems, sorted.
func checkOutput(outDir string) (problems []string, pages int) {
	return checkOutputAt(outDir, "/")
}

// checkOutputAt is checkOutput for a site served below the path sitePath
// ("/" or "/pravo/"). 404.html is the one page that uses paths from the root,
// since a host serves it for an address at any depth.
func checkOutputAt(outDir, sitePath string) (problems []string, pages int) {
	infos := map[string]htmlInfo{} // keyed by slash path relative to outDir
	_ = filepath.WalkDir(outDir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(p, ".html") {
			return nil
		}
		rel, _ := filepath.Rel(outDir, p)
		data, err := os.ReadFile(p)
		if err != nil {
			problems = append(problems, fmt.Sprintf("%s: %v", rel, err))
			return nil
		}
		infos[filepath.ToSlash(rel)] = scanHTML(data)
		return nil
	})
	pages = len(infos)

	// resolve turns a reference found in page from into a site path and fragment.
	resolve := func(from, ref string) (target, frag string, ok bool) {
		if ref == "" || externalRef(ref) {
			return "", "", false
		}
		if from == "404.html" && strings.HasPrefix(ref, sitePath) && !strings.HasPrefix(ref, "//") {
			ref = strings.TrimPrefix(ref, sitePath)
			if ref == "" {
				ref = "index.html"
			}
		}
		if strings.HasPrefix(ref, "/") {
			problems = append(problems, fmt.Sprintf("%s: link %q is root-relative; the site works from any path, so links are relative", from, ref))
			return "", "", false
		}
		refPath, frag, _ := strings.Cut(ref, "#")
		if i := strings.IndexByte(refPath, '?'); i >= 0 {
			refPath = refPath[:i]
		}
		if dec, err := url.PathUnescape(refPath); err == nil {
			refPath = dec
		}
		if dec, err := url.PathUnescape(frag); err == nil {
			frag = dec
		}
		if refPath == "" {
			return from, frag, true
		}
		target = path.Join(path.Dir(from), refPath)
		if strings.HasSuffix(refPath, "/") || refPath == "." || refPath == ".." {
			target = path.Join(target, "index.html")
		}
		return target, frag, true
	}
	exists := func(rel string) bool {
		st, err := os.Stat(filepath.Join(outDir, filepath.FromSlash(rel)))
		return err == nil && !st.IsDir()
	}
	checkTarget := func(from, ref string) {
		target, frag, ok := resolve(from, ref)
		if !ok {
			return
		}
		if strings.HasPrefix(target, "../") || target == ".." {
			problems = append(problems, fmt.Sprintf("%s: link %q leaves the site", from, ref))
			return
		}
		if !exists(target) {
			problems = append(problems, fmt.Sprintf("%s: link %q: no such file", from, ref))
			return
		}
		if frag != "" && !strings.HasPrefix(frag, ":~:") && strings.HasSuffix(target, ".html") {
			if ti, ok := infos[target]; ok && !ti.ids[frag] {
				problems = append(problems, fmt.Sprintf("%s: link %q: no element with id %q", from, ref, frag))
			}
		}
	}

	names := make([]string, 0, len(infos))
	for n := range infos {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, from := range names {
		info := infos[from]
		for _, id := range info.dupIDs {
			problems = append(problems, fmt.Sprintf("%s: id %q is used more than once", from, id))
		}
		for _, ref := range info.links {
			checkTarget(from, ref)
		}
		for _, ref := range info.refresh {
			checkTarget(from, ref)
		}
		for _, dp := range info.dataPages {
			if !exists(dp) {
				problems = append(problems, fmt.Sprintf("%s: data-page %q: no such page", from, dp))
			}
		}
	}

	// Search index: every url exists and every heading id is on its page.
	if data, err := os.ReadFile(filepath.Join(outDir, "search-index.json")); err == nil {
		var entries []searchEntry
		if err := json.Unmarshal(data, &entries); err != nil {
			problems = append(problems, fmt.Sprintf("search-index.json: %v", err))
		}
		for _, e := range entries {
			if !exists(e.URL) {
				problems = append(problems, fmt.Sprintf("search-index.json: url %q: no such file", e.URL))
				continue
			}
			for _, h := range e.Headings {
				if ti, ok := infos[e.URL]; ok && !ti.ids[h.ID] {
					problems = append(problems, fmt.Sprintf("search-index.json: %s: heading id %q is not on the page", e.URL, h.ID))
				}
			}
		}
	}
	sort.Strings(problems)
	uniq := problems[:0]
	for i, p := range problems {
		if i == 0 || p != problems[i-1] {
			uniq = append(uniq, p)
		}
	}
	return uniq, pages
}
