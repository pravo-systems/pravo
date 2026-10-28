package main

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"sort"
)

// A shot of the viewer has a few thousand colours: the flat colours of the
// interface and the shades that the edges of the text and the wires take. A
// PNG of it compresses to about a megabyte at the scale of the shots. Reduced
// to a palette of 256 colours it is a fifth of that and looks the same, since
// the shades of an edge are near one another and the palette keeps every
// colour that covers an area.
//
// The palette is chosen with an octree: every pixel is inserted by the bits of
// its channels, and the deepest nodes with the fewest pixels are merged until
// the leaves fit. A merge joins colours that share their high bits, so a rare
// colour that differs from its neighbours (the hue of a badge) is kept, where
// a median cut would average it into the grey around it.

// defaultColours is the size of the palette of a shot.
const defaultColours = 256

// octreeDepth is how many bits of each channel the tree keeps apart.
const octreeDepth = 8

type octNode struct {
	children [8]*octNode
	// r, g, b are the sums of the channels of the pixels under the node, and
	// n their number; a node that is reduced takes them from its children.
	r, g, b, n uint64
	leaf       bool
	index      int
}

type octree struct {
	root   *octNode
	leaves int
	// levels holds, for each depth, the nodes that have children: the
	// candidates of a reduction.
	levels [octreeDepth][]*octNode
}

func (t *octree) insert(r, g, b uint8) {
	if t.root == nil {
		t.root = &octNode{}
	}
	node := t.root
	for depth := 0; depth < octreeDepth; depth++ {
		shift := 7 - depth
		i := (int(r>>shift)&1)<<2 | (int(g>>shift)&1)<<1 | int(b>>shift)&1
		if node.children[i] == nil {
			child := &octNode{}
			if depth == octreeDepth-1 {
				child.leaf = true
				t.leaves++
			}
			if node.n == 0 && !hasChild(node) {
				t.levels[depth] = append(t.levels[depth], node)
			}
			node.children[i] = child
		}
		node = node.children[i]
	}
	node.r += uint64(r)
	node.g += uint64(g)
	node.b += uint64(b)
	node.n++
}

func hasChild(n *octNode) bool {
	for _, c := range n.children {
		if c != nil {
			return true
		}
	}
	return false
}

// reduce merges nodes until the tree has at most n leaves: at the deepest
// level that still has nodes with children, the node with the fewest pixels
// under it takes the sums of its children and becomes a leaf.
func (t *octree) reduce(n int) {
	for depth := octreeDepth - 1; depth >= 0 && t.leaves > n; depth-- {
		nodes := t.levels[depth]
		sort.Slice(nodes, func(i, j int) bool { return pixels(nodes[i]) < pixels(nodes[j]) })
		for _, node := range nodes {
			if t.leaves <= n {
				break
			}
			merged := 0
			for i, c := range node.children {
				if c == nil {
					continue
				}
				node.r += c.r
				node.g += c.g
				node.b += c.b
				node.n += c.n
				node.children[i] = nil
				merged++
			}
			node.leaf = true
			t.leaves -= merged - 1
		}
		t.levels[depth] = nil
	}
}

// pixels is the number of pixels under a node whose children are leaves.
func pixels(n *octNode) uint64 {
	var sum uint64
	for _, c := range n.children {
		if c != nil {
			sum += c.n
		}
	}
	return sum + n.n
}

// palette numbers the leaves and returns their mean colours.
func (t *octree) palette() color.Palette {
	var p color.Palette
	var walk func(n *octNode)
	walk = func(n *octNode) {
		if n.leaf {
			n.index = len(p)
			p = append(p, color.RGBA{uint8(n.r / n.n), uint8(n.g / n.n), uint8(n.b / n.n), 255})
			return
		}
		for _, c := range n.children {
			if c != nil {
				walk(c)
			}
		}
	}
	walk(t.root)
	return p
}

// lookup is the index of the leaf that a colour falls under.
func (t *octree) lookup(r, g, b uint8) int {
	node := t.root
	for depth := 0; !node.leaf; depth++ {
		shift := 7 - depth
		i := (int(r>>shift)&1)<<2 | (int(g>>shift)&1)<<1 | int(b>>shift)&1
		node = node.children[i]
	}
	return node.index
}

// quantize returns img on a palette of at most n colours. An image with no
// more colours than that is returned on a palette of exactly its colours.
func quantize(img image.Image, n int) *image.Paletted {
	b := img.Bounds()
	rgba, ok := img.(*image.RGBA)
	if !ok {
		rgba = image.NewRGBA(b)
		for y := b.Min.Y; y < b.Max.Y; y++ {
			for x := b.Min.X; x < b.Max.X; x++ {
				rgba.Set(x, y, img.At(x, y))
			}
		}
	}
	t := &octree{}
	for y := b.Min.Y; y < b.Max.Y; y++ {
		row := rgba.Pix[(y-b.Min.Y)*rgba.Stride:]
		for x := 0; x < b.Dx(); x++ {
			t.insert(row[x*4], row[x*4+1], row[x*4+2])
		}
	}
	t.reduce(n)
	out := image.NewPaletted(b, t.palette())
	for y := b.Min.Y; y < b.Max.Y; y++ {
		row := rgba.Pix[(y-b.Min.Y)*rgba.Stride:]
		for x := 0; x < b.Dx(); x++ {
			out.Pix[(y-b.Min.Y)*out.Stride+x] = uint8(t.lookup(row[x*4], row[x*4+1], row[x*4+2]))
		}
	}
	return out
}

// shrinkPNG rewrites the PNG at path on a palette of n colours. With n of 0
// it leaves the file as it is.
func shrinkPNG(path string, n int) error {
	if n <= 0 {
		return nil
	}
	if n > 256 {
		return fmt.Errorf("a palette holds at most 256 colours, not %d", n)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	img, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		return err
	}
	var buf bytes.Buffer
	enc := png.Encoder{CompressionLevel: png.BestCompression}
	if err := enc.Encode(&buf, quantize(img, n)); err != nil {
		return err
	}
	return os.WriteFile(path, buf.Bytes(), 0o644)
}
