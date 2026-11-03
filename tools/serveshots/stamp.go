package main

import (
	"crypto/sha256"
	"encoding/hex"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
)

// The pictures show the interface of the viewer, so they go stale when the
// interface changes. A full run records a digest of the files that the viewer
// serves (its page, scripts, styles and fonts) in viewer.stamp, and a test
// compares it with the files of the checkout. A change to the interface then
// fails the build until the pictures are made again, whether or not the change
// seemed visible.
const (
	viewerDir  = "internal/serve/web"
	stampFile  = "tools/serveshots/viewer.stamp"
	landingDir = "site/demos/views"
)

// viewerDigest is the SHA-256 of the files that the viewer embeds: index.html
// and everything below assets, by path and content, in path order.
func viewerDigest(root string) (string, error) {
	base := filepath.Join(root, viewerDir)
	paths := []string{filepath.Join(base, "index.html")}
	err := filepath.WalkDir(filepath.Join(base, "assets"), func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			paths = append(paths, p)
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	sort.Strings(paths)
	h := sha256.New()
	for _, p := range paths {
		b, err := os.ReadFile(p)
		if err != nil {
			return "", err
		}
		rel, _ := filepath.Rel(base, p)
		h.Write([]byte(filepath.ToSlash(rel)))
		h.Write([]byte{0})
		h.Write(b)
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
