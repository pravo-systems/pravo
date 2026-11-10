package tfstate

import (
	"os"
	"path/filepath"
	"testing"
)

func FuzzConvert(f *testing.F) {
	files, _ := filepath.Glob("testdata/*.tfstate")
	for _, p := range files {
		b, _ := os.ReadFile(p)
		f.Add(b)
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		_, _ = Convert(data)
	})
}
