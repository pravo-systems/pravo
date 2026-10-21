package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMedian(t *testing.T) {
	for _, c := range []struct {
		in   []float64
		want float64
	}{
		{nil, 0},
		{[]float64{3}, 3},
		{[]float64{3, 1, 2}, 2},
		{[]float64{4, 1, 3, 2}, 2.5},
		{[]float64{1.6, 1.5, 1.7}, 1.6},
	} {
		if got := median(c.in); got != c.want {
			t.Errorf("median(%v) = %v, want %v", c.in, got, c.want)
		}
	}
}

// rows of results.tsv: tool n scenario rep wall user sys rss exit reads locks
// plan-file summary.
const sampleRows = `pravo	2000	plan_change_default	1	0.600	0.500	0.100	110.0	0	1	-	-	Plan: 1 to add
pravo	2000	plan_change_default	2	0.580	0.490	0.090	111.0	0	1	-	-	Plan: 1 to add
pravo	2000	plan_change_default	3	0.640	0.510	0.100	112.0	0	1	-	-	Plan: 1 to add
tofu	2000	plan_change_default	1	2.400	3.000	0.500	290.0	0	2001	-	-	Plan: 1 to add
tofu	2000	plan_change_default	2	2.500	3.100	0.500	288.0	0	2001	-	-	Plan: 1 to add
tofu	2000	plan_change_default	3	2.300	2.900	0.500	289.0	0	2001	-	-	Plan: 1 to add
pravo	2000	apply_change_default	0	0.700	0.600	0.100	130.0	0	1	1	-	Apply complete
pravo	2000	apply_change_default	1	0.650	0.550	0.100	131.0	0	1	-	-	Apply complete
pravo	2000	apply_change_default	2	0.660	0.560	0.100	132.0	0	1	-	-	Apply complete
pravo	2000	plan_out_change_default	1	0.600	0.500	0.100	110.0	0	1	-	3090	Plan: 1 to add
pravo	5000	apply_create	1	10.000	20.000	5.000	350.0	0	0	-	-	Apply complete
`

func TestAssembleTakesMedians(t *testing.T) {
	doc, err := assemble(sampleRows, Meta{Reps: 3})
	if err != nil {
		t.Fatal(err)
	}
	find := func(tool, scenario string, n int) Row {
		t.Helper()
		for _, r := range doc.Rows {
			if r.Tool == tool && r.Scenario == scenario && r.N == n {
				return r
			}
		}
		t.Fatalf("no row for %s %s %d", tool, scenario, n)
		return Row{}
	}
	p := find("pravo", "plan_change_default", 2000)
	if p.Reps != 3 || p.WallS != 0.6 || p.UserS != 0.5 || p.RSSMB != 111 || p.Reads != 1 || p.Locks != nil || p.PlanFileBytes != nil {
		t.Errorf("pravo plan: %+v", p)
	}
	if want := []float64{0.6, 0.58, 0.64}; len(p.WallAll) != 3 || p.WallAll[0] != want[0] || p.WallAll[1] != want[1] || p.WallAll[2] != want[2] {
		t.Errorf("wall_s_all = %v, want %v", p.WallAll, want)
	}
	tf := find("tofu", "plan_change_default", 2000)
	if tf.WallS != 2.4 || tf.Reads != 2001 {
		t.Errorf("tofu plan: %+v", tf)
	}
	// Repetition 0 counts the locks and does not count toward the time.
	a := find("pravo", "apply_change_default", 2000)
	if a.Reps != 2 || a.WallS != 0.655 || a.Locks == nil || *a.Locks != 1 {
		t.Errorf("pravo apply: %+v", a)
	}
	if f := find("pravo", "plan_out_change_default", 2000); f.PlanFileBytes == nil || *f.PlanFileBytes != 3090 {
		t.Errorf("plan file: %+v", f)
	}

	by := doc.By["n2000"]["plan_change_default"]
	if got := by["speedup"]; got != 4.0 {
		t.Errorf("speedup = %v, want 4 (2.4 s against 0.6 s)", got)
	}
	if _, ok := doc.By["n5000"]["apply_create"]["speedup"]; ok {
		t.Error("a scenario that only pravo ran has a speedup")
	}

	// The file is JSON that a page can address by size and scenario.
	b, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	var back struct {
		By map[string]map[string]map[string]json.RawMessage `json:"by"`
	}
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatal(err)
	}
	if _, ok := back.By["n2000"]["plan_change_default"]["pravo"]; !ok {
		t.Errorf("by.n2000.plan_change_default.pravo is missing: %s", b)
	}
}

func TestAssembleRejectsMalformedRows(t *testing.T) {
	for _, row := range []string{
		"pravo\t2000\tplan\t1\t0.5",
		"pravo\tmany\tplan\t1\t0.5\t0.5\t0.1\t100\t0\t1\t-\t-\tx",
		"pravo\t2000\tplan\t1\tslow\t0.5\t0.1\t100\t0\t1\t-\t-\tx",
	} {
		if _, err := assemble(row+"\n", Meta{}); err == nil {
			t.Errorf("row %q was accepted", row)
		}
	}
}

func TestTimeRecordsTheCommand(t *testing.T) {
	record := filepath.Join(t.TempDir(), "row")
	var stderr bytes.Buffer
	code, err := timeCmd([]string{"-r", record, "--", "sh", "-c", "exit 3"}, &stderr)
	if err != nil || code != 0 {
		t.Fatalf("code %d, err %v", code, err)
	}
	b, err := os.ReadFile(record)
	if err != nil {
		t.Fatal(err)
	}
	f := strings.Fields(string(b))
	if len(f) != 5 || f[4] != "3" {
		t.Errorf("record = %q, want five fields ending in the exit code 3", b)
	}

	if _, err := timeCmd([]string{"-r", record}, &stderr); err == nil {
		t.Error("a missing command was accepted")
	}
}

func TestMetaNamesTheBinaryItMeasured(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "pravo")
	content := []byte("#!/bin/sh\necho pravo test\n")
	if err := os.WriteFile(bin, content, 0o755); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	if code := run([]string{"meta", "-pravo", bin, "-tofu", filepath.Join(dir, "missing"), "-root", dir, "-reps", "3", "-sizes", "500 2000"}, &stdout, &stderr); code != 0 {
		t.Fatalf("meta exited %d: %s", code, stderr.String())
	}
	var m Meta
	if err := json.Unmarshal(stdout.Bytes(), &m); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(content)
	if want := hex.EncodeToString(sum[:]); m.Build.PravoSHA256 != want {
		t.Errorf("digest %q, want %q", m.Build.PravoSHA256, want)
	}
	if m.Tools["pravo"] != "pravo test" {
		t.Errorf("tools %v", m.Tools)
	}
	if m.Build.Revision != "" {
		t.Errorf("a directory that is no repository has the revision %q", m.Build.Revision)
	}
}
