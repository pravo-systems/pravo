package engine

import (
	"strings"
	"testing"
)

func shadowFile(blocks ...string) string {
	return "package estate\n\n// Decompiled.\n\n" + strings.Join(blocks, "\n") + "\n"
}

func block(addr, label string, lines ...string) string {
	return "// " + addr + "\n" + label + ": $p.#Resource.t & {\n\t" + strings.Join(lines, "\n\t") + "\n}\n"
}

func TestShadowBlocksFindsResourcesAndNotTheCommentsInThem(t *testing.T) {
	src := shadowFile(
		block("aws_s3_bucket.logs", "logs", "bucket: \"x\"", "// tags: left out"),
		"edge: {\n\t// module.edge.aws_instance.this\n\tthis: $p.#Resource.i & {\n\t\tname: \"e\"\n\t\t// secret: sensitive\n\t}\n}\n",
	)
	got := ShadowBlocks(src)
	if len(got) != 2 || got[0].Addr != "aws_s3_bucket.logs" || got[0].Label != "logs" || got[1].Addr != "module.edge.aws_instance.this" || got[1].Label != "this" {
		t.Fatalf("blocks = %+v", got)
	}
	if !strings.Contains(got[1].Text, "secret: sensitive") || !strings.HasSuffix(got[1].Text, "\t}\n") {
		t.Errorf("a block lost its body: %q", got[1].Text)
	}
}

func TestMergeShadow(t *testing.T) {
	a0 := block("t.a", "a", `v: 1`)
	b0 := block("t.b", "b", `v: 1`)
	c0 := block("t.c", "c", `v: 1`)
	d0 := block("t.d", "d", `v: 1`)
	base := ShadowBlockTexts(shadowFile(a0, b0, c0, d0))
	// The person edited b and c and removed nothing; the state changed a and c, dropped d, gained e.
	current := shadowFile(a0, block("t.b", "b", `v: 2`), block("t.c", "c", `v: 3`), d0)
	fresh := shadowFile(block("t.a", "a", `v: 9`), b0, block("t.c", "c", `v: 4`), block("t.e", "e", `v: 1`))
	m := MergeShadow(current, base, fresh)
	for _, want := range []string{`v: 9`, `v: 2`, `v: 3`, "// t.e"} {
		if !strings.Contains(m.Source, want) {
			t.Errorf("merged lacks %q:\n%s", want, m.Source)
		}
	}
	for _, not := range []string{`v: 4`, "// t.d"} {
		if strings.Contains(m.Source, not) {
			t.Errorf("merged has %q:\n%s", not, m.Source)
		}
	}
	if strings.Join(m.Edited, ",") != "t.b,t.c" || strings.Join(m.Updated, ",") != "t.a" || strings.Join(m.Added, ",") != "t.e" || strings.Join(m.Conflicts, ",") != "t.c" || len(m.Unplaced) != 0 {
		t.Errorf("merge = %+v", m)
	}

	// An edited block of a resource that left the state has no place, and is named.
	edited := shadowFile(a0, b0, c0, block("t.d", "d", `v: 7`))
	if m := MergeShadow(edited, base, shadowFile(a0, b0, c0)); strings.Join(m.Unplaced, ",") != "t.d" {
		t.Errorf("unplaced = %v", m.Unplaced)
	}

	// With no note, a block that differs is taken for an edit and counted.
	if m := MergeShadow(current, nil, fresh); m.Unknown != 3 || !strings.Contains(m.Source, `v: 2`) {
		t.Errorf("without a note: %+v\n%s", m, m.Source)
	}

	// A block that the person left alone takes the new name too, so that the others still refer to it.
	renamed := MergeShadow(shadowFile(a0), base, shadowFile(block("t.a", "a2", `v: 1`)))
	if !strings.Contains(renamed.Source, "a2:") || len(renamed.Edited) != 0 {
		t.Errorf("a renamed block: %+v", renamed)
	}
}
