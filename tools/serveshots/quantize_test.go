package main

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"testing"
)

// A picture of a few flat colours keeps exactly those colours.
func TestQuantizeKeepsFlatColours(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 40, 40))
	colours := []color.RGBA{{20, 20, 24, 255}, {240, 240, 236, 255}, {90, 80, 200, 255}, {30, 160, 90, 255}}
	for y := range 40 {
		for x := range 40 {
			img.Set(x, y, colours[(x/20)+2*(y/20)])
		}
	}
	out := quantize(img, 256)
	if len(out.Palette) != len(colours) {
		t.Fatalf("palette of %d colours, want %d", len(out.Palette), len(colours))
	}
	for y := range 40 {
		for x := range 40 {
			if got, want := out.At(x, y), colours[(x/20)+2*(y/20)]; got != want {
				t.Fatalf("pixel %d,%d is %v, want %v", x, y, got, want)
			}
		}
	}
}

// A rare colour that differs from everything around it survives the
// reduction, and the shades of an edge are merged with one another.
func TestQuantizeKeepsARareHue(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 256, 64))
	// 256 greys across, a gradient with more colours than the palette holds.
	for y := range 64 {
		for x := range 256 {
			img.Set(x, y, color.RGBA{uint8(x), uint8(x), uint8(x), 255})
		}
	}
	// 1024 greys of the gradient, and the gradient is 256 shades: add more
	// shades so that a reduction has to happen.
	for y := range 32 {
		for x := range 256 {
			img.Set(x, y, color.RGBA{uint8(x), uint8(x), uint8((x + 1) % 256), 255})
		}
	}
	// A purple badge of a few pixels.
	purple := color.RGBA{140, 100, 220, 255}
	for y := 40; y < 44; y++ {
		for x := 10; x < 14; x++ {
			img.Set(x, y, purple)
		}
	}
	out := quantize(img, 64)
	if len(out.Palette) > 64 {
		t.Fatalf("palette of %d colours", len(out.Palette))
	}
	r, g, b, _ := out.At(11, 41).RGBA()
	if d := abs(int(r>>8)-140) + abs(int(g>>8)-100) + abs(int(b>>8)-220); d > 24 {
		t.Errorf("the purple became %d,%d,%d", r>>8, g>>8, b>>8)
	}
	// A grey stays a grey: the channels of the mapped colour are near one another.
	r, g, b, _ = out.At(200, 50).RGBA()
	if abs(int(r>>8)-int(b>>8)) > 8 || abs(int(r>>8)-int(g>>8)) > 8 || abs(int(r>>8)-200) > 8 {
		t.Errorf("the grey 200 became %d,%d,%d", r>>8, g>>8, b>>8)
	}
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

func TestShrinkPNGRewritesTheFile(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 64, 64))
	for y := range 64 {
		for x := range 64 {
			img.Set(x, y, color.RGBA{uint8(x * 4), uint8(y * 4), uint8((x + y) * 2), 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "x.png")
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := shrinkPNG(path, 256); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	out, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := out.(*image.Paletted); !ok {
		t.Errorf("the file is %T, not paletted", out)
	}
	if out.Bounds() != img.Bounds() {
		t.Errorf("bounds %v", out.Bounds())
	}
	if err := shrinkPNG(path, 0); err != nil {
		t.Errorf("n of 0 is a no-op: %v", err)
	}
	if err := shrinkPNG(path, 300); err == nil {
		t.Error("a palette of 300 colours is refused")
	}
}
