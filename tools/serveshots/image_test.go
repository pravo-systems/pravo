package main

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writePNG(t *testing.T, name string, img image.Image) string {
	t.Helper()
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestCheckImage(t *testing.T) {
	flat := image.NewRGBA(image.Rect(0, 0, 40, 30))
	for i := range flat.Pix {
		flat.Pix[i] = 0x20
	}
	nearly := image.NewRGBA(image.Rect(0, 0, 100, 100))
	for y := range 100 {
		for x := range 100 {
			nearly.Set(x, y, color.RGBA{0x10, 0x10, 0x10, 0xff})
		}
	}
	nearly.Set(1, 1, color.RGBA{0xff, 0, 0, 0xff})
	nearly.Set(2, 2, color.RGBA{0, 0xff, 0, 0xff})
	nearly.Set(3, 3, color.RGBA{0, 0, 0xff, 0xff})
	busy := image.NewRGBA(image.Rect(0, 0, 40, 30))
	for y := range 30 {
		for x := range 40 {
			busy.Set(x, y, color.RGBA{uint8(x * 6), uint8(y * 8), uint8(x ^ y), 0xff})
		}
	}

	if _, err := checkImage(writePNG(t, "busy.png", busy), 40, 30); err != nil {
		t.Errorf("a picture with content: %v", err)
	}
	if _, err := checkImage(writePNG(t, "flat.png", flat), 40, 30); err == nil || !strings.Contains(err.Error(), "blank") {
		t.Errorf("a single colour: %v", err)
	}
	if _, err := checkImage(writePNG(t, "nearly.png", nearly), 100, 100); err == nil || !strings.Contains(err.Error(), "blank") {
		t.Errorf("one colour on almost every pixel: %v", err)
	}
	if _, err := checkImage(writePNG(t, "busy.png", busy), 80, 60); err == nil || !strings.Contains(err.Error(), "expected 80x60") {
		t.Errorf("a wrong size: %v", err)
	}
	junk := filepath.Join(t.TempDir(), "junk.png")
	os.WriteFile(junk, []byte("not a png"), 0o644)
	if _, err := checkImage(junk, 40, 30); err == nil || !strings.Contains(err.Error(), "does not decode") {
		t.Errorf("a file that is not a PNG: %v", err)
	}
	if _, err := checkImage(filepath.Join(t.TempDir(), "none.png"), 1, 1); err == nil {
		t.Error("a missing file: no error")
	}
}

func TestPlaceholders(t *testing.T) {
	dir := t.TempDir()
	for _, s := range allShots() {
		p := filepath.Join(dir, s.File())
		if err := writePlaceholder(p, s); err != nil {
			t.Fatalf("%s: %v", s.Name, err)
		}
		info, err := checkImage(p, s.Width, s.Height)
		if err != nil {
			t.Errorf("%s: %v", s.Name, err)
		}
		if info.Bytes > 40<<10 {
			t.Errorf("%s: %d bytes for a flat panel", s.Name, info.Bytes)
		}
	}
}

func TestEveryShotNameCanBeDrawn(t *testing.T) {
	for _, s := range allShots() {
		for _, r := range strings.ToUpper(s.Name + " 0123456789x") {
			if _, ok := glyphs[r]; !ok {
				t.Errorf("no glyph for %q in %s", r, s.Name)
			}
		}
	}
	for r, rows := range glyphs {
		for i, row := range rows {
			if len(row) != 5 {
				t.Errorf("glyph %q row %d is %q", r, i, row)
			}
		}
	}
}

func TestCheckTheme(t *testing.T) {
	dark := imageInfo{Luma: 0.08}
	light := imageInfo{Luma: 0.93}
	grey := imageInfo{Luma: 0.45}
	if err := checkTheme(dark, "dark"); err != nil {
		t.Error(err)
	}
	if err := checkTheme(light, "light"); err != nil {
		t.Error(err)
	}
	if checkTheme(light, "dark") == nil || checkTheme(dark, "light") == nil {
		t.Error("a picture of the other theme: no error")
	}
	if checkTheme(grey, "dark") == nil || checkTheme(grey, "light") == nil {
		t.Error("a grey picture: no error")
	}
}

func TestLumaOfPlaceholders(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"x-dark", "x-light"} {
		s := Shot{Name: name, Width: 200, Height: 120}
		p := filepath.Join(dir, name+".png")
		if err := writePlaceholder(p, s); err != nil {
			t.Fatal(err)
		}
		info, err := checkImage(p, 200, 120)
		if err != nil {
			t.Fatal(err)
		}
		if err := checkTheme(info, strings.TrimPrefix(name, "x-")); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
}
