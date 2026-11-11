package extract

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func buckets(n int) string {
	var b strings.Builder
	b.WriteString("package estate\n\nimport aws \"pravo.dev/providers/aws/aws\"\n\n$aws: aws.#Provider & {}\n\n")
	for i := range n {
		fmt.Fprintf(&b, "// bucket %d\nb%d: $aws.#Resource.aws_s3_bucket & {\n\tbucket: \"acme-%d-logs\"\n\tforce_destroy: false\n\ttags: {\n\t\tTeam: \"platform\"\n\t\tName: \"acme-%d-logs\"\n\t}\n}\n\n", i, i, i, i)
	}
	return b.String()
}

func TestFoldTurnsAlikeResourcesIntoADefinition(t *testing.T) {
	f, err := Fold([]File{{Name: "shadow.cue", Src: []byte(buckets(6))}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(f.Definitions) != 1 || f.Instances != 6 {
		t.Fatalf("definitions = %+v, instances = %d", f.Definitions, f.Instances)
	}
	out := string(f.Files["shadow.cue"])
	for _, want := range []string{"#S3Bucket: $aws.#Resource.aws_s3_bucket & {", "b0: #S3Bucket & {", "b5: #S3Bucket & {", "// bucket 3", "Team: \"platform\""} {
		if !strings.Contains(out, want) {
			t.Errorf("the rewrite lacks %q:\n%s", want, out)
		}
	}
	if strings.Count(out, "force_destroy") != 1 || strings.Count(out, "Team:") != 1 {
		t.Errorf("what is equal is written more than once:\n%s", out)
	}
	if len(out) >= len(buckets(6)) {
		t.Errorf("the rewrite is not shorter: %d bytes against %d", len(out), len(buckets(6)))
	}
}

func TestFoldLeavesWhatIsNotWorthADefinition(t *testing.T) {
	f, err := Fold([]File{{Name: "shadow.cue", Src: []byte(buckets(2))}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(f.Definitions) != 0 || len(f.Files) != 0 {
		t.Fatalf("two buckets became %+v", f)
	}
}

// An estate of 83 resources on AWS, written flat. The numbers are those of the prototype that
// this package came from: 7 definitions for 67 resources, 858 lines to 512, and pravo found no
// change on the applied estate (the proof is a plan; the tests here hold the text).
func TestFoldAFlatEstate(t *testing.T) {
	paths, err := filepath.Glob("testdata/estate/*.cue")
	if err != nil || len(paths) == 0 {
		t.Fatal("no estate", err)
	}
	var in []File
	lines := 0
	for _, p := range paths {
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		lines += strings.Count(string(b), "\n")
		in = append(in, File{Name: filepath.Base(p), Src: b})
	}
	f, err := Fold(in, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(f.Definitions) != 7 || f.Instances != 67 || f.Left != 16 {
		t.Errorf("%d definitions, %d instances, %d left; want 7, 67, 16", len(f.Definitions), f.Instances, f.Left)
	}
	after := 0
	for _, x := range in {
		src := x.Src
		if out, ok := f.Files[x.Name]; ok {
			src = out
		}
		after += strings.Count(string(src), "\n")
	}
	if after != 512 {
		t.Errorf("%d lines became %d, want 512", lines, after)
	}
	// The result is what it was folded to, and folding it again finds nothing more.
	again := make([]File, len(in))
	for i, x := range in {
		again[i] = File{Name: x.Name, Src: x.Src}
		if out, ok := f.Files[x.Name]; ok {
			again[i].Src = out
		}
	}
	g, err := Fold(again, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(g.Definitions) != 0 {
		t.Errorf("a folded estate folds again into %d definitions", len(g.Definitions))
	}
}
