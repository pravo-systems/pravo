package main

import (
	"io"
	"math"
	"os"
	"strings"
	"testing"
)

func TestRatio(t *testing.T) {
	black, white := rgba{0, 0, 0, 1}, rgba{255, 255, 255, 1}
	if r := ratio(black, white); math.Abs(r-21) > 0.01 {
		t.Fatalf("black on white = %.2f, want 21", r)
	}
	half := rgba{255, 255, 255, 0.5}.over(black)
	if half.r < 127 || half.r > 128 {
		t.Fatalf("composite = %v", half)
	}
}

// The stylesheet that ships must pass: every text pair at 4.5:1, every
// interface pair at 3:1, in both themes.
func TestStylesheetPasses(t *testing.T) {
	b, err := os.ReadFile("../../../site/assets/style.css")
	if err != nil {
		t.Skip("stylesheet not found:", err)
	}
	n, err := check(string(b), false, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if n > 0 {
		var sb strings.Builder
		check(string(b), false, &sb)
		t.Fatalf("%d contrast failures:\n%s", n, sb.String())
	}
}
