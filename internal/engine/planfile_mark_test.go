package engine

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// markedPlan saves the plan of n changes, one for each of the resources r000,
// r001 and so on.
func markedPlan(t *testing.T, n int) (string, *harness) {
	t.Helper()
	h := newHarness(t)
	h.apply(manySpec(n, func(int) string { return "v1" }))
	file, _ := planned(t, h, manySpec(n, func(int) string { return "v2" }))
	return file, h
}

func appliedIn(t *testing.T, file string) map[string]string {
	t.Helper()
	pf, err := ReadPlanFile(file)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for _, sc := range pf.Changes {
		if sc.Applied {
			out[sc.Addr] = sc.AppliedTxn
		}
	}
	return out
}

// Applies from one plan file that run at the same time each mark their own
// changes, and every mark stays in the file.
func TestMarkAppliedKeepsTheMarksOfConcurrentApplies(t *testing.T) {
	const parts, per = 12, 5
	file, _ := markedPlan(t, parts*per)
	var wg sync.WaitGroup
	errs := make(chan error, parts)
	start := make(chan struct{})
	for i := 0; i < parts; i++ {
		pf, err := ReadPlanFile(file) // every apply read the file before any wrote it
		if err != nil {
			t.Fatal(err)
		}
		var addrs []string
		for j := 0; j < per; j++ {
			addrs = append(addrs, fmt.Sprintf("r%03d", i*per+j))
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			errs <- MarkApplied(file, pf, addrs, fmt.Sprintf("txn-%d", i))
		}()
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Error(err)
		}
	}
	got := appliedIn(t, file)
	if len(got) != parts*per {
		t.Fatalf("%d of %d changes are marked applied", len(got), parts*per)
	}
	for i := 0; i < parts; i++ {
		for j := 0; j < per; j++ {
			addr := fmt.Sprintf("r%03d", i*per+j)
			if want := fmt.Sprintf("txn-%d", i); got[addr] != want {
				t.Errorf("%s is marked applied by %q, want %q", addr, got[addr], want)
			}
		}
	}
	entries, err := os.ReadDir(filepath.Dir(file))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Errorf("the directory of the plan file holds %d files, want only the plan file", len(entries))
	}
}

// Marks made one after the other keep the earlier ones, and the file keeps its
// mode, its place behind a link and its content apart from the marks.
func TestMarkAppliedKeepsModeAndLink(t *testing.T) {
	file, _ := markedPlan(t, 4)
	if err := os.Chmod(file, 0o640); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(filepath.Dir(file), "link.plan")
	if err := os.Symlink(file, link); err != nil {
		t.Fatal(err)
	}
	before, err := ReadPlanFile(file)
	if err != nil {
		t.Fatal(err)
	}
	first, _ := ReadPlanFile(file)
	second, _ := ReadPlanFile(file)
	if err := MarkApplied(link, first, []string{"r000", "r001"}, "txn-a"); err != nil {
		t.Fatal(err)
	}
	if err := MarkApplied(link, second, []string{"r002"}, "txn-b"); err != nil {
		t.Fatal(err)
	}
	got := appliedIn(t, file)
	want := map[string]string{"r000": "txn-a", "r001": "txn-a", "r002": "txn-b"}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("marked %v, want %v", got, want)
	}
	if info, err := os.Lstat(link); err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Errorf("the link is gone: %v", err)
	}
	if info, err := os.Stat(file); err != nil || info.Mode().Perm() != 0o640 {
		t.Errorf("mode %v, error %v, want 0640", info.Mode().Perm(), err)
	}
	after, err := ReadPlanFile(file)
	if err != nil {
		t.Fatal(err)
	}
	if after.ApplyTxn != "txn-b" || after.Lineage != before.Lineage || after.ConfigFingerprint != before.ConfigFingerprint || len(after.Changes) != len(before.Changes) {
		t.Errorf("the file changed beyond its marks: apply txn %q, lineage %q", after.ApplyTxn, after.Lineage)
	}
}

// A file that holds another plan than the one that was applied is left as it is.
func TestMarkAppliedLeavesAnotherPlanAlone(t *testing.T) {
	file, h := markedPlan(t, 3)
	applied, err := ReadPlanFile(file)
	if err != nil {
		t.Fatal(err)
	}
	other, _ := planned(t, h, manySpec(3, func(int) string { return "v3" }))
	data, err := os.ReadFile(other)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, data, 0o600); err != nil {
		t.Fatal(err)
	}
	err = MarkApplied(file, applied, []string{"r000"}, "txn")
	if err == nil || !strings.Contains(err.Error(), "another plan") {
		t.Fatalf("error %v, want that the file holds another plan", err)
	}
	if got := appliedIn(t, file); len(got) != 0 {
		t.Errorf("the other plan was marked: %v", got)
	}
}
