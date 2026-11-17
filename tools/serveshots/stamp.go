package main

import (
	"crypto/sha256"
	"encoding/hex"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
)

// The pictures are made when the site is published, so they always show the
// interface of the checkout. What can go stale is the hand work around them: the
// crops of site/demos/views/build.sh and the callouts of
// site/landing/12-views.html, which sit at places in the pictures. The stamp
// records a digest of the files that the viewer serves (its page, scripts,
// styles and fonts) as they were when that work was checked, and -check-stamp
// says when they differ. It warns and does not fail: a change that moves
// nothing that a callout points at needs only -stamp.
const (
	viewerDir  = "internal/serve/web"
	landingDir = "site/demos/views"
	stampFile  = landingDir + "/viewer.stamp"
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
