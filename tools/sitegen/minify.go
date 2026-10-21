package main

import (
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/tdewolff/minify/v2"
	"github.com/tdewolff/minify/v2/css"
	"github.com/tdewolff/minify/v2/js"
)

// Minification covers the JavaScript and the style sheets that are copied from
// site/assets to the output. The sources stay readable: nothing is written
// back, and the testdata pages that load site/assets directly see the sources.
// The HTML that the generator writes is not minified, because the code
// windows, terminals and recorded output carry their text and line breaks
// in the markup.

const (
	mimeCSS = "text/css"
	mimeJS  = "application/javascript"
)

// assetMinifier minifies one asset at a time.
type assetMinifier struct {
	m *minify.M
}

func newAssetMinifier() *assetMinifier {
	m := minify.New()
	m.AddFunc(mimeCSS, css.Minify)
	m.AddFunc(mimeJS, js.Minify)
	return &assetMinifier{m: m}
}

// minifyKind returns the media type of an asset that is minified, or "" for
// any other file. A file whose name says it is minified already is left alone.
func minifyKind(rel string) string {
	base := strings.ToLower(path.Base(rel))
	if strings.Contains(base, ".min.") {
		return ""
	}
	switch path.Ext(base) {
	case ".js":
		return mimeJS
	case ".css":
		return mimeCSS
	}
	return ""
}

// minify returns src minified. A file of another type, and a result that is
// not smaller than the source, come back as they are.
func (a *assetMinifier) minify(rel string, src []byte) ([]byte, error) {
	kind := minifyKind(rel)
	if kind == "" {
		return src, nil
	}
	// The script minifier renames variables inside the buffer it is given, so
	// it gets a copy and src stays as it was read.
	out, err := a.m.Bytes(kind, append([]byte(nil), src...))
	if err != nil {
		return nil, err
	}
	if len(out) >= len(src) {
		return src, nil
	}
	return out, nil
}

// minifyStats counts the files that were minified and their sizes.
type minifyStats struct {
	files    int
	from, to int
}

// prepareAssets minifies every JavaScript and CSS file of site/assets into
// memory before the messages are reported, so that a file the minifier cannot
// parse fails the build with the other errors and nothing is written. It does
// nothing when minification is off.
func (s *site) prepareAssets() {
	if s.minifier == nil {
		return
	}
	root := filepath.Join(s.siteDir, "assets")
	s.minified = map[string][]byte{}
	s.minStats = minifyStats{}
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if strings.HasPrefix(path.Base(rel), ".") || minifyKind(rel) == "" {
			return nil
		}
		src, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		out, err := s.minifier.minify(rel, src)
		if err != nil {
			s.msgs.errorf("%s: minify: %v", p, err)
			return nil
		}
		s.minified[rel] = out
		s.minStats.files++
		s.minStats.from += len(src)
		s.minStats.to += len(out)
		return nil
	})
	if err != nil && !os.IsNotExist(err) {
		s.msgs.errorf("%s: %v", root, err)
	}
}

// assetData returns the bytes to write for a file of site/assets: the
// minified text when there is one.
func (s *site) assetData(rel string, data []byte) []byte {
	if out, ok := s.minified[rel]; ok {
		return out
	}
	return data
}

// summary is the line that the generator prints after the build.
func (st minifyStats) summary() string {
	if st.files == 0 {
		return ""
	}
	return fmt.Sprintf("minified %d scripts and style sheets: %d to %d bytes", st.files, st.from, st.to)
}
