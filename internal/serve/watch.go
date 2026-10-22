package serve

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/pravo-systems/pravo/internal/state"
)

// The components of the signature the watcher keeps, in the order they are
// named in an event.
var components = []string{"state", "spec", "plan", "locks"}

// fullScanEvery is how often, in polls, a state directory of many records is
// read entry by entry. In between, the directories themselves are looked at:
// the local backend writes a record to a temporary file and renames it, which
// changes its directory.
const fullScanEvery = 10

// fullScanLimit is the number of entries up to which every poll reads them all.
const fullScanLimit = 2000

// remoteEvery is how often, in polls, a state in a bucket is looked at.
const remoteEvery = 5

// watcher polls what the model is made of and says when it changed. It reads
// directories and file attributes, and for a bucket, a listing. It writes nothing.
type watcher struct {
	poll time.Duration
	bus  *bus

	mu   sync.Mutex
	sigs map[string]string
	rev  string
	n    int

	// The inputs of a sample, set by the server as it learns them.
	dir      string // the project directory
	files    []string
	location string // the directory of a local state, "" for a remote one
	backend  state.Backend
	plan     string
	remote   int // polls since the remote state was looked at

	// scan is what the full reads of a large state directory found.
	scan struct {
		full, quickAtFull string
		bump              int
	}
}

func newWatcher(poll time.Duration, dir, plan string) *watcher {
	return &watcher{poll: poll, bus: newBus(), dir: dir, plan: plan, sigs: map[string]string{}}
}

// revision returns the revision of the last sample.
func (w *watcher) revision() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.rev
}

// setSpecFiles tells the watcher which files the specification was loaded from.
func (w *watcher) setSpecFiles(files []string) {
	w.mu.Lock()
	w.files = files
	w.mu.Unlock()
}

// setState tells the watcher where the state is.
func (w *watcher) setState(location string, b state.Backend) {
	w.mu.Lock()
	w.location, w.backend = location, b
	w.mu.Unlock()
}

// sample reads the signature of every component now. It also applies it, as a
// poll does, so that the revision a model is labelled with is the one the
// watcher holds, and an event is sent if the sample differs from the last. It
// returns the revision and the signatures.
func (w *watcher) sample() (string, map[string]string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.sampleLocked()
}

func (w *watcher) sampleLocked() (string, map[string]string) {
	w.n++
	next := map[string]string{
		"spec":  specSignature(w.dir, w.files, w.noSpec()),
		"plan":  fileSignature(w.plan),
		"state": w.sigs["state"],
		"locks": w.sigs["locks"],
	}
	if w.location != "" {
		next["state"], next["locks"] = w.localState()
	} else if w.backend != nil {
		if w.remote == 0 || w.sigs["state"] == "" {
			next["state"], next["locks"] = remoteStateSignature(w.backend)
		}
		w.remote = (w.remote + 1) % remoteEvery
	}
	var what []string
	first := w.rev == ""
	for _, c := range components {
		if next[c] != w.sigs[c] {
			what = append(what, c)
		}
	}
	w.sigs = next
	if len(what) > 0 || first {
		w.rev = revisionOf(next)
		if !first {
			w.bus.publish(Event{Revision: w.rev, What: what})
		}
	}
	out := make(map[string]string, len(next))
	for k, v := range next {
		out[k] = v
	}
	return w.rev, out
}

// noSpec reports that no file list is known: the specification has not been
// loaded or does not load. The generated packages are then part of the signature,
// since "pravo init" writing them is what makes it load.
func (w *watcher) noSpec() bool { return len(w.files) == 0 }

// specNow returns the signature of the specification files now.
func (w *watcher) specNow() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return specSignature(w.dir, w.files, w.noSpec())
}

// run polls until the context ends.
func (w *watcher) run(ctx context.Context) {
	t := time.NewTicker(w.poll)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			w.sample()
		}
	}
}

// revisionOf digests the signatures into a short revision.
func revisionOf(sigs map[string]string) string {
	h := sha256.New()
	for _, c := range components {
		fmt.Fprintf(h, "%s=%s\n", c, sigs[c])
	}
	return "r-" + hex.EncodeToString(h.Sum(nil))[:10]
}

// fileSignature is the attributes of a file that change when it is written.
func fileSignature(path string) string {
	if path == "" {
		return ""
	}
	fi, err := os.Stat(path)
	if err != nil {
		return path + "|-"
	}
	return fmt.Sprintf("%s|%d|%d", path, fi.ModTime().UnixNano(), fi.Size())
}

// dirSignature signs a directory: with entries, by what each entry is (a
// temporary file that a write is still making is not one), else by the
// attributes of the directory itself.
func dirSignature(dir string, entries bool) string {
	if !entries {
		fi, err := os.Stat(dir)
		if err != nil {
			return dir + "|-"
		}
		return digest(fmt.Sprintf("%s|%d|%d", dir, fi.ModTime().UnixNano(), fi.Size()))
	}
	list, err := os.ReadDir(dir)
	if err != nil {
		return dir + "|-"
	}
	var b strings.Builder
	b.WriteString(dir)
	for _, e := range list {
		if e.IsDir() || strings.HasSuffix(e.Name(), ".tmp") {
			continue
		}
		if info, err := e.Info(); err == nil {
			fmt.Fprintf(&b, ";%s|%d|%d", e.Name(), info.ModTime().UnixNano(), info.Size())
		}
	}
	return digest(b.String())
}

func digest(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:8])
}

// localState signs a state directory: the records, the transactions and the
// outputs, and apart from them the locks. A directory of a few records is read
// entry by entry on every poll. A large one is looked at as a directory, which
// the local backend changes with every write (it writes a temporary file and
// renames it), and read entry by entry now and then; a write that the directory
// did not show counts as a change when that read finds it.
func (w *watcher) localState() (stateSig, locksSig string) {
	dir := w.location
	resources := filepath.Join(dir, "resources")
	list, _ := os.ReadDir(resources)
	var records string
	if len(list) <= fullScanLimit {
		records = dirSignature(resources, true)
	} else {
		records = dirSignature(resources, false)
		if w.n%fullScanEvery == 0 || w.scan.full == "" {
			full := dirSignature(resources, true)
			if w.scan.full != "" && full != w.scan.full && records == w.scan.quickAtFull {
				w.scan.bump++
			}
			w.scan.full, w.scan.quickAtFull = full, records
		}
	}
	parts := []string{
		fileSignature(filepath.Join(dir, "meta.json")),
		fileSignature(filepath.Join(dir, "outputs.json")),
		records,
		dirSignature(filepath.Join(dir, "txn"), false),
		strconv.Itoa(w.scan.bump),
	}
	return digest(strings.Join(parts, "\n")), dirSignature(filepath.Join(dir, "locks"), true)
}

// remoteStateSignature signs a state in a bucket by listing it. A record that is
// written again keeps its key, so the listing alone would miss it; the
// transactions, which every apply adds, and the outputs are looked at as well.
func remoteStateSignature(b state.Backend) (stateSig, locksSig string) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	var parts []string
	for _, prefix := range []string{"resources", "txn"} {
		keys, err := b.List(ctx, prefix)
		if err != nil {
			parts = append(parts, prefix+"|!")
			continue
		}
		parts = append(parts, prefix+"|"+digest(strings.Join(keys, ";")))
	}
	for _, key := range []string{"meta.json", "outputs.json"} {
		data, err := b.Get(ctx, key)
		if err != nil {
			parts = append(parts, key+"|-")
			continue
		}
		parts = append(parts, key+"|"+digest(string(data)))
	}
	keys, _ := b.List(ctx, "locks")
	return digest(strings.Join(parts, "\n")), digest(strings.Join(keys, ";"))
}

// specSignature signs the files the specification is made of, the files that
// could become part of it, and, when it does not load, the generated packages.
func specSignature(dir string, files []string, generated bool) string {
	set := map[string]bool{}
	for _, f := range files {
		set[f] = true
	}
	if list, err := os.ReadDir(dir); err == nil {
		for _, e := range list {
			if !e.IsDir() && strings.HasSuffix(e.Name(), ".cue") {
				set[filepath.Join(dir, e.Name())] = true
			}
		}
	}
	if root := moduleRootOf(dir); root != "" {
		set[filepath.Join(root, "cue.mod", "module.cue")] = true
		if generated {
			_ = filepath.WalkDir(filepath.Join(root, "cue.mod", "gen"), func(p string, d os.DirEntry, err error) error {
				if err == nil && !d.IsDir() {
					set[p] = true
				}
				return nil
			})
		}
	}
	set[filepath.Join(dir, "pravo.lock.cue")] = true
	names := make([]string, 0, len(set))
	for f := range set {
		names = append(names, f)
	}
	sort.Strings(names)
	var b strings.Builder
	for _, f := range names {
		b.WriteString(fileSignature(f))
		b.WriteByte('\n')
	}
	return digest(b.String())
}

// moduleRootOf finds the directory with a cue.mod at or above dir.
func moduleRootOf(dir string) string {
	for d := dir; ; {
		if fi, err := os.Stat(filepath.Join(d, "cue.mod")); err == nil && fi.IsDir() {
			return d
		}
		parent := filepath.Dir(d)
		if parent == d {
			return ""
		}
		d = parent
	}
}

// modifiedSince reports whether one of the files was written at or after t.
func modifiedSince(files []string, t time.Time) bool {
	for _, f := range files {
		if fi, err := os.Stat(f); err == nil && !fi.ModTime().Before(t) {
			return true
		}
	}
	return false
}

// bus delivers events to the readers of /api/events.
type bus struct {
	mu   sync.Mutex
	subs map[chan Event]struct{}
}

func newBus() *bus { return &bus{subs: map[chan Event]struct{}{}} }

// subscribe returns a channel of events and the function that ends the
// subscription.
func (b *bus) subscribe() (<-chan Event, func()) {
	ch := make(chan Event, 8)
	b.mu.Lock()
	b.subs[ch] = struct{}{}
	b.mu.Unlock()
	return ch, func() {
		b.mu.Lock()
		delete(b.subs, ch)
		b.mu.Unlock()
	}
}

// publish sends the event to every subscriber. A subscriber that does not keep
// up loses its oldest event: the newest says the page is out of date.
func (b *bus) publish(ev Event) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for ch := range b.subs {
		for {
			select {
			case ch <- ev:
			default:
				select {
				case <-ch:
					continue
				default:
				}
			}
			break
		}
	}
}
