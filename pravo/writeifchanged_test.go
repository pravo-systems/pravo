package pravo

import (
	"bytes"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

// A command that reads a generated file while another writes it must see the
// old text or the new text, never half of either.
func TestWriteIfChangedIsNotSeenHalfWritten(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pravo.lock.cue")
	a := bytes.Repeat([]byte("a"), 1<<16)
	b := bytes.Repeat([]byte("b"), 1<<16)
	if _, err := WriteIfChanged(path, a); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	done := make(chan struct{})
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 300; i++ {
			data := a
			if i%2 == 0 {
				data = b
			}
			if _, err := WriteIfChanged(path, data); err != nil {
				t.Error(err)
				return
			}
		}
		close(done)
	}()
	for running := true; running; {
		select {
		case <-done:
			running = false
		default:
		}
		got, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, a) && !bytes.Equal(got, b) {
			t.Fatalf("read a file of %d bytes that is neither version", len(got))
		}
	}
	wg.Wait()
	if m, _ := filepath.Glob(filepath.Join(filepath.Dir(path), ".*tmp*")); len(m) != 0 {
		t.Errorf("temporary files left: %v", m)
	}
}
