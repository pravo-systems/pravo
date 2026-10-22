package serve

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// A directory of more than fullScanLimit records is looked at as a directory and
// read entry by entry now and then. Neither makes a change out of nothing, and a
// write that the directory does not show is found by the full read.
func TestWatcherOfALargeStateIsQuiet(t *testing.T) {
	dir := t.TempDir()
	res := filepath.Join(dir, "resources")
	os.MkdirAll(res, 0o755)
	for i := 0; i < fullScanLimit+50; i++ {
		os.WriteFile(filepath.Join(res, fmt.Sprintf("r%04d.json", i)), []byte("{}"), 0o644)
	}
	w := newWatcher(time.Hour, dir, "")
	w.setState(dir, nil)
	sub, cancel := w.bus.subscribe()
	defer cancel()
	first, _ := w.sample()
	for i := 0; i < 3*fullScanEvery; i++ {
		if rev, _ := w.sample(); rev != first {
			t.Fatalf("poll %d: the revision moved from %s to %s with nothing written", i, first, rev)
		}
	}
	select {
	case ev := <-sub:
		t.Fatalf("an event with nothing written: %+v", ev)
	default:
	}
	// A record written the way the local backend writes it: a temporary file, renamed.
	os.WriteFile(filepath.Join(res, "r0001.json.tmp"), []byte(`{"a":1}`), 0o644)
	os.Rename(filepath.Join(res, "r0001.json.tmp"), filepath.Join(res, "r0001.json"))
	rev, _ := w.sample()
	if rev == first {
		t.Fatal("a record that was written is not seen")
	}
	if ev := <-sub; len(ev.What) != 1 || ev.What[0] != "state" || ev.Revision != rev {
		t.Errorf("event %+v", ev)
	}
	for i := 0; i < 3*fullScanEvery; i++ {
		if r2, _ := w.sample(); r2 != rev {
			t.Fatalf("poll %d: the full read made a second change of one write", i)
		}
	}
	// A write that left the directory as it was is found by the full read.
	p := filepath.Join(res, "r0002.json")
	fi, _ := os.Stat(res)
	os.WriteFile(p, []byte(`{"changed":true}`), 0o644)
	os.Chtimes(res, fi.ModTime(), fi.ModTime())
	found := false
	for i := 0; i < 2*fullScanEvery && !found; i++ {
		r2, _ := w.sample()
		found = r2 != rev
	}
	if !found {
		t.Error("a record rewritten in place is not found by the full read")
	}
}

func TestWatcherSeesSpecPlanAndLocks(t *testing.T) {
	dir := t.TempDir()
	writeSpec(t, dir, specCUE)
	plan := filepath.Join(dir, "plan.pravo")
	w := newWatcher(time.Hour, dir, plan)
	w.setState(filepath.Join(dir, "pravo.state"), nil)
	sub, cancel := w.bus.subscribe()
	defer cancel()
	w.sample()
	expect := func(what string, change func()) {
		t.Helper()
		change()
		before := w.revision()
		w.sample()
		if w.revision() == before {
			t.Fatalf("%s: not seen", what)
		}
		ev := <-sub
		if len(ev.What) != 1 || ev.What[0] != what {
			t.Errorf("%s: the event says %v", what, ev.What)
		}
	}
	expect("spec", func() { os.WriteFile(filepath.Join(dir, "main.cue"), []byte(specCUE+"\n// edited\n"), 0o644) })
	expect("spec", func() { os.WriteFile(filepath.Join(dir, "second.cue"), []byte("package demo\n"), 0o644) })
	expect("plan", func() { os.WriteFile(plan, []byte("x"), 0o644) })
	expect("plan", func() { os.Remove(plan) })
	os.MkdirAll(filepath.Join(dir, "pravo.state", "locks"), 0o755)
	w.sample()
	<-sub // the state directory appears
	expect("locks", func() { os.WriteFile(filepath.Join(dir, "pravo.state", "locks", "a.lock"), []byte("{}"), 0o644) })
}

func TestHeadline(t *testing.T) {
	for in, want := range map[string]string{
		"loading /x/y:\npravo.local/m@v0: import failed: cannot find package \"pravo.dev/pravo\"\nhint: the specification imports packages. Run \"pravo init\" to create it": "the specification imports packages. Run \"pravo init\" to create it",
		"evaluating /x:\nsize: conflicting values 11 and 3": "size: conflicting values 11 and 3",
		"plain": "plain",
	} {
		if got := headline(in); got != want {
			t.Errorf("headline(%q) = %q, want %q", in, got, want)
		}
	}
}
