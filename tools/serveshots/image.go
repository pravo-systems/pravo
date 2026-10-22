package main

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"os"
	"path/filepath"
	"strings"
)

// imageInfo describes a PNG that passed checkImage.
type imageInfo struct {
	Width, Height int
	Bytes         int64
	// Colours is the number of different colours, counted up to a limit, and
	// Dominant the share of the pixels that the most common one covers.
	Colours  int
	Dominant float64
	// Luma is the mean brightness of the picture, from 0 (black) to 1 (white).
	Luma float64
}

// colourLimit stops the count of different colours.
const colourLimit = 4096

// checkImage decodes the PNG at path and fails when it is not the wanted size,
// does not decode, or is blank: a single colour, or one colour that covers
// almost every pixel. A page that did not render leaves such an image.
func checkImage(path string, wantW, wantH int) (imageInfo, error) {
	var info imageInfo
	data, err := os.ReadFile(path)
	if err != nil {
		return info, err
	}
	info.Bytes = int64(len(data))
	img, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		return info, fmt.Errorf("%s does not decode as a PNG: %w", filepath.Base(path), err)
	}
	b := img.Bounds()
	info.Width, info.Height = b.Dx(), b.Dy()
	if info.Width != wantW || info.Height != wantH {
		return info, fmt.Errorf("%s is %dx%d, expected %dx%d", filepath.Base(path), info.Width, info.Height, wantW, wantH)
	}
	counts := map[uint32]int{}
	most := 0
	var luma float64
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			r, g, bl, a := img.At(x, y).RGBA()
			luma += 0.2126*float64(r) + 0.7152*float64(g) + 0.0722*float64(bl)
			k := (r>>8)<<24 | (g>>8)<<16 | (bl>>8)<<8 | a>>8
			if len(counts) < colourLimit || counts[k] > 0 {
				counts[k]++
				if counts[k] > most {
					most = counts[k]
				}
			}
		}
	}
	info.Colours = len(counts)
	info.Dominant = float64(most) / float64(info.Width*info.Height)
	info.Luma = luma / 65535 / float64(info.Width*info.Height)
	switch {
	case info.Colours < 4:
		return info, fmt.Errorf("%s is blank: %d colours", filepath.Base(path), info.Colours)
	case info.Dominant > 0.995:
		return info, fmt.Errorf("%s is blank: one colour covers %.1f%% of it", filepath.Base(path), info.Dominant*100)
	}
	return info, nil
}

// checkTheme fails when a picture is not of the theme that it was made for: a
// dark picture has a mean brightness below 0.4 and a light one above 0.5. A
// page that ignored the theme in its fragment shows the other one.
func checkTheme(info imageInfo, theme string) error {
	switch {
	case theme == "dark" && info.Luma > 0.4:
		return fmt.Errorf("the picture is for the dark theme and its mean brightness is %.2f", info.Luma)
	case theme == "light" && info.Luma < 0.5:
		return fmt.Errorf("the picture is for the light theme and its mean brightness is %.2f", info.Luma)
	}
	return nil
}

// writePlaceholder writes a flat panel of the shot's viewport size with its
// name drawn on it. The documentation builds with these until a run of the
// program replaces them with the real pictures.
func writePlaceholder(path string, s Shot) error {
	bg := color.RGBA{0x14, 0x18, 0x1f, 0xff}
	fg := color.RGBA{0xc9, 0xd1, 0xd9, 0xff}
	edge := color.RGBA{0x2b, 0x33, 0x3d, 0xff}
	accent := color.RGBA{0x3d, 0xdb, 0xb0, 0xff}
	if strings.HasSuffix(s.Name, "-light") {
		accent = color.RGBA{0x0a, 0x73, 0x5a, 0xff}
		bg = color.RGBA{0xf4, 0xf1, 0xea, 0xff}
		fg = color.RGBA{0x2a, 0x2a, 0x2a, 0xff}
		edge = color.RGBA{0xd6, 0xd0, 0xc4, 0xff}
	}
	img := image.NewRGBA(image.Rect(0, 0, s.Width, s.Height))
	draw.Draw(img, img.Bounds(), &image.Uniform{bg}, image.Point{}, draw.Src)
	rect := func(x0, y0, x1, y1 int, c color.RGBA) {
		draw.Draw(img, image.Rect(x0, y0, x1, y1), &image.Uniform{c}, image.Point{}, draw.Src)
	}
	rect(0, 0, s.Width, 2, edge)
	rect(0, s.Height-2, s.Width, s.Height, edge)
	rect(0, 0, 2, s.Height, edge)
	rect(s.Width-2, 0, s.Width, s.Height, edge)

	title := s.Name
	size := fmt.Sprintf("%dx%d", s.Width, s.Height)
	px := max(2, min(12, (s.Width-80)/(len(title)*6)))
	drawText(img, title, (s.Width-textWidth(title, px))/2, s.Height/2-4*px, px, fg)
	rect((s.Width-textWidth(title, px))/2, s.Height/2+4*px, (s.Width+textWidth(title, px))/2, s.Height/2+5*px, accent)
	small := max(1, px/3)
	drawText(img, size, (s.Width-textWidth(size, small))/2, s.Height/2+7*px, small, fg)

	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, buf.Bytes(), 0o644)
}

// textWidth is the width of text in pixels at the scale px: five columns and a
// column of space for each character.
func textWidth(text string, px int) int { return len(text)*6*px - px }

// drawText draws text in the 5 by 7 glyphs below, each pixel of a glyph a
// square of px pixels.
func drawText(img *image.RGBA, text string, x, y, px int, c color.RGBA) {
	for _, r := range strings.ToUpper(text) {
		rows, ok := glyphs[r]
		if !ok {
			rows = glyphs['?']
		}
		for row, bits := range rows {
			for col := 0; col < 5; col++ {
				if bits[col] == '#' {
					draw.Draw(img, image.Rect(x+col*px, y+row*px, x+(col+1)*px, y+(row+1)*px), &image.Uniform{c}, image.Point{}, draw.Src)
				}
			}
		}
		x += 6 * px
	}
}

// glyphs is a 5 by 7 font for the characters of the shot names, as seven rows
// of five columns.
var glyphs = map[rune][7]string{
	' ': {".....", ".....", ".....", ".....", ".....", ".....", "....."},
	'?': {"#####", "....#", "...#.", "..#..", ".....", "..#..", "..#.."},
	'-': {".....", ".....", ".....", "#####", ".....", ".....", "....."},
	'.': {".....", ".....", ".....", ".....", ".....", ".##..", ".##.."},
	'0': {".###.", "#...#", "#..##", "#.#.#", "##..#", "#...#", ".###."},
	'1': {"..#..", ".##..", "..#..", "..#..", "..#..", "..#..", ".###."},
	'2': {".###.", "#...#", "....#", "...#.", "..#..", ".#...", "#####"},
	'3': {"####.", "....#", "....#", ".###.", "....#", "....#", "####."},
	'4': {"...#.", "..##.", ".#.#.", "#..#.", "#####", "...#.", "...#."},
	'5': {"#####", "#....", "####.", "....#", "....#", "#...#", ".###."},
	'6': {"..##.", ".#...", "#....", "####.", "#...#", "#...#", ".###."},
	'7': {"#####", "....#", "...#.", "..#..", ".#...", ".#...", ".#..."},
	'8': {".###.", "#...#", "#...#", ".###.", "#...#", "#...#", ".###."},
	'9': {".###.", "#...#", "#...#", ".####", "....#", "...#.", ".##.."},
	'A': {".###.", "#...#", "#...#", "#####", "#...#", "#...#", "#...#"},
	'B': {"####.", "#...#", "#...#", "####.", "#...#", "#...#", "####."},
	'C': {".###.", "#...#", "#....", "#....", "#....", "#...#", ".###."},
	'D': {"####.", "#...#", "#...#", "#...#", "#...#", "#...#", "####."},
	'E': {"#####", "#....", "#....", "####.", "#....", "#....", "#####"},
	'F': {"#####", "#....", "#....", "####.", "#....", "#....", "#...."},
	'G': {".###.", "#...#", "#....", "#.###", "#...#", "#...#", ".####"},
	'H': {"#...#", "#...#", "#...#", "#####", "#...#", "#...#", "#...#"},
	'I': {".###.", "..#..", "..#..", "..#..", "..#..", "..#..", ".###."},
	'J': {"..###", "...#.", "...#.", "...#.", "...#.", "#..#.", ".##.."},
	'K': {"#...#", "#..#.", "#.#..", "##...", "#.#..", "#..#.", "#...#"},
	'L': {"#....", "#....", "#....", "#....", "#....", "#....", "#####"},
	'M': {"#...#", "##.##", "#.#.#", "#.#.#", "#...#", "#...#", "#...#"},
	'N': {"#...#", "##..#", "#.#.#", "#..##", "#...#", "#...#", "#...#"},
	'O': {".###.", "#...#", "#...#", "#...#", "#...#", "#...#", ".###."},
	'P': {"####.", "#...#", "#...#", "####.", "#....", "#....", "#...."},
	'Q': {".###.", "#...#", "#...#", "#...#", "#.#.#", "#..#.", ".##.#"},
	'R': {"####.", "#...#", "#...#", "####.", "#.#..", "#..#.", "#...#"},
	'S': {".####", "#....", "#....", ".###.", "....#", "....#", "####."},
	'T': {"#####", "..#..", "..#..", "..#..", "..#..", "..#..", "..#.."},
	'U': {"#...#", "#...#", "#...#", "#...#", "#...#", "#...#", ".###."},
	'V': {"#...#", "#...#", "#...#", "#...#", "#...#", ".#.#.", "..#.."},
	'W': {"#...#", "#...#", "#...#", "#.#.#", "#.#.#", "##.##", "#...#"},
	'X': {"#...#", "#...#", ".#.#.", "..#..", ".#.#.", "#...#", "#...#"},
	'Y': {"#...#", "#...#", ".#.#.", "..#..", "..#..", "..#..", "..#.."},
	'Z': {"#####", "....#", "...#.", "..#..", ".#...", "#....", "#####"},
}
