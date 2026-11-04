package registry

import (
	"archive/zip"
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// FuzzUnzip feeds mutated archives to unzip: it must not panic, and it must write
// nothing outside the directory it was given.
func FuzzUnzip(f *testing.F) {
	var b bytes.Buffer
	zw := zip.NewWriter(&b)
	for _, n := range []string{"terraform-provider-x_v1", "LICENSE", "dir/", "dir/file"} {
		h := &zip.FileHeader{Name: n, Method: zip.Deflate}
		h.SetMode(0o755)
		w, _ := zw.CreateHeader(h)
		if n[len(n)-1] != '/' {
			w.Write([]byte("content of " + n))
		}
	}
	zw.Close()
	f.Add(b.Bytes())
	f.Fuzz(func(t *testing.T, data []byte) {
		outer := t.TempDir()
		dir := filepath.Join(outer, "pkg")
		if err := os.Mkdir(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		arch := filepath.Join(outer, "a.zip")
		if err := os.WriteFile(arch, data, 0o644); err != nil {
			t.Fatal(err)
		}
		_ = unzip(arch, dir)
		ents, _ := os.ReadDir(outer)
		for _, e := range ents {
			if e.Name() != "pkg" && e.Name() != "a.zip" {
				t.Fatalf("wrote outside the directory: %s", e.Name())
			}
		}
		filepath.Walk(dir, func(p string, info os.FileInfo, err error) error {
			if err == nil && info.Mode()&os.ModeSymlink != 0 {
				t.Fatalf("symlink created: %s", p)
			}
			return nil
		})
	})
}
