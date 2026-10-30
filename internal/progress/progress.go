// Package progress shows what a command is doing on a terminal until it has a
// result: a spinner and one status line on stderr, drawn from the spans the
// command already starts. It draws nothing when stderr is not a terminal, in CI,
// under TERM=dumb or with -v, and it never writes to stdout, so what a command
// prints is the same with or without it.
package progress

import (
	"context"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/mattn/go-isatty"
	"go.opentelemetry.io/otel/attribute"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
	"golang.org/x/term"
)

var frames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

// delay is how long a command runs before anything is drawn, so that a quick
// one never flickers. It is a variable for the tests.
var delay = 400 * time.Millisecond

const (
	tick = 80 * time.Millisecond

	clearLine = "\r\x1b[K"
	hideCur   = "\x1b[?25l"
	showCur   = "\x1b[?25h"
)

// Wanted reports whether a terminal user should see progress: stderr is a
// terminal that can redraw a line, and nothing says to keep the output plain.
// get reads the environment.
func Wanted(get func(string) string) bool {
	f := os.Stderr
	if !isatty.IsTerminal(f.Fd()) && !isatty.IsCygwinTerminal(f.Fd()) {
		return false
	}
	if t := get("TERM"); t == "dumb" {
		return false
	}
	for _, k := range []string{"CI", "GITHUB_ACTIONS", "BUILDKITE", "TF_BUILD"} {
		if get(k) != "" {
			return false
		}
	}
	switch strings.ToLower(get("PRAVO_PROGRESS")) {
	case "off", "false", "0", "no", "none":
		return false
	}
	return true
}

type node struct {
	name   string
	parent trace.SpanID
	start  time.Time
	tasks  int
}

// Tracker is a span processor that follows the work in flight and draws it.
// It is inert until Start is called, so it can be installed before the
// command, and with it the flags, are known.
type Tracker struct {
	w io.Writer

	mu      sync.Mutex
	active  map[trace.SpanID]*node
	order   []trace.SpanID // start order of the active spans
	done    int            // resource tasks finished in the current phase
	running bool
	drawn   bool // a status line is on the screen
	atStart bool // the cursor is at the start of a line
	color   bool
	began   time.Time
	frame   int
	stop    chan struct{}
	stopped chan struct{}
}

// New returns a tracker that draws on w.
func New(w io.Writer) *Tracker {
	return &Tracker{w: w, active: map[trace.SpanID]*node{}, atStart: true}
}

// Start begins drawing. color adds colour to the line.
func (t *Tracker) Start(color bool) {
	t.mu.Lock()
	if t.running {
		t.mu.Unlock()
		return
	}
	t.running, t.color, t.began = true, color, time.Now()
	t.stop, t.stopped = make(chan struct{}), make(chan struct{})
	t.mu.Unlock()
	go t.loop()
}

// Stop removes the status line and stops drawing.
func (t *Tracker) Stop() {
	t.mu.Lock()
	if !t.running {
		t.mu.Unlock()
		return
	}
	t.running = false
	stop, stopped := t.stop, t.stopped
	t.mu.Unlock()
	close(stop)
	<-stopped
	t.mu.Lock()
	t.eraseLocked()
	t.mu.Unlock()
}

func (t *Tracker) loop() {
	defer close(t.stopped)
	tk := time.NewTicker(tick)
	defer tk.Stop()
	for {
		select {
		case <-t.stop:
			return
		case <-tk.C:
			t.mu.Lock()
			t.drawLocked()
			t.mu.Unlock()
		}
	}
}

// write draws; a terminal that cannot be written to has nothing to be told.
func (t *Tracker) write(s string) { _, _ = io.WriteString(t.w, s) }

// eraseLocked clears the status line if one is drawn.
func (t *Tracker) eraseLocked() {
	if t.drawn {
		t.write(clearLine + showCur)
		t.drawn = false
	}
}

func (t *Tracker) drawLocked() {
	if !t.running || !t.atStart || time.Since(t.began) < delay {
		return
	}
	line := t.statusLocked()
	if line == "" {
		t.eraseLocked()
		return
	}
	t.frame = (t.frame + 1) % len(frames)
	spin := frames[t.frame]
	if t.color {
		spin = "\x1b[36m" + spin + "\x1b[0m"
	}
	if !t.drawn {
		t.write(hideCur)
	}
	t.write(clearLine + spin + " " + line)
	t.drawn = true
}

// Guard returns a writer that takes the status line off the screen before w is
// written to, and puts nothing back until the writes end on a line start, so a
// prompt that waits mid-line is never drawn over.
func (t *Tracker) Guard(w io.Writer) io.Writer { return guard{t, w} }

type guard struct {
	t *Tracker
	w io.Writer
}

func (g guard) Write(p []byte) (int, error) {
	g.t.mu.Lock()
	defer g.t.mu.Unlock()
	g.t.eraseLocked()
	n, err := g.w.Write(p)
	if n > 0 {
		g.t.atStart = p[n-1] == '\n'
	}
	return n, err
}

// statusLocked is the text of the line: what is being done, how far along it
// is, and for how long.
func (t *Tracker) statusLocked() string {
	var head, res *node
	resources := 0
	total := 0
	for _, id := range t.order {
		n := t.active[id]
		switch {
		case strings.HasPrefix(n.name, "resource "):
			resources++
			res = n
		case strings.HasPrefix(n.name, "phase "), strings.HasPrefix(n.name, "setup "), strings.HasPrefix(n.name, "engine discover"):
			head = n
			if n.tasks > 0 {
				total = n.tasks
			}
		}
	}
	if head == nil && res == nil {
		return ""
	}
	var parts []string
	var since time.Time
	if head != nil {
		parts = append(parts, headline(head.name))
		since = head.start
	}
	if res != nil && (head == nil || strings.HasPrefix(head.name, "phase ")) {
		if head == nil {
			parts = append(parts, "Working")
			since = res.start
		}
		count := strconv.Itoa(t.done)
		if total > 0 {
			count = fmt.Sprintf("%d/%d", t.done, total)
		}
		parts = append(parts, count+" done")
		addr := strings.TrimPrefix(res.name, "resource ")
		if resources > 1 {
			addr += fmt.Sprintf(" +%d", resources-1)
		}
		parts = append(parts, addr)
	}
	line := parts[0]
	if len(parts) > 1 {
		line += " · " + strings.Join(parts[1:], " · ")
	}
	if e := time.Since(since); e >= time.Second {
		line += fmt.Sprintf(" · %ds", int(e.Seconds()))
	}
	return truncate(line, termWidth()-3)
}

// headline words a span name for a person: "phase evaluate and refresh" becomes
// "Evaluating and refreshing".
func headline(name string) string {
	kind, rest, _ := strings.Cut(name, " ")
	if kind == "engine" {
		return "Discovering " + strings.TrimPrefix(rest, "discover ")
	}
	if s, ok := words[rest]; ok {
		return s
	}
	for prefix, s := range words {
		if strings.HasSuffix(prefix, " ") && strings.HasPrefix(rest, prefix) {
			return s + " " + strings.TrimPrefix(rest, prefix)
		}
	}
	return strings.ToUpper(rest[:1]) + rest[1:]
}

var words = map[string]string{
	"load specification":   "Reading the specification",
	"read lock file":       "Reading the lock file",
	"open state":           "Opening the state",
	"resolve providers":    "Resolving providers",
	"load schemas":         "Loading provider schemas",
	"check specification":  "Checking the specification",
	"begin transaction":    "Beginning a transaction",
	"plan destroy":         "Planning the destroy",
	"moves":                "Checking moves",
	"read inputs":          "Reading inputs",
	"evaluate and refresh": "Evaluating and refreshing",
	"plan orphans":         "Planning orphans",
	"frontier":             "Finding the next resources",
	"reservations":         "Checking reservations",
	"finish transaction":   "Finishing the transaction",
	"lock":                 "Waiting for the lock",
	"check revisions":      "Checking revisions",
	"apply changes":        "Applying changes",
	"destroy orphans":      "Destroying orphans",
	"read back":            "Reading back what was applied",
	"record outputs":       "Recording outputs",
	"commit moves":         "Committing moves",
	"resolve provider ":    "Resolving",
	"install provider ":    "Installing",
	"checksum provider ":   "Checking",
	"load schema ":         "Loading the schema of",
	"start provider ":      "Starting",
	"configure provider ":  "Configuring",
	"unseal state keys":    "Unsealing the state",
}

func truncate(s string, width int) string {
	r := []rune(s)
	if width < 10 || len(r) <= width {
		return s
	}
	return string(r[:width-1]) + "…"
}

func termWidth() int {
	if w, _, err := term.GetSize(int(os.Stderr.Fd())); err == nil && w > 0 {
		return w
	}
	return 80
}

// ---- sdktrace.SpanProcessor ----

var _ sdktrace.SpanProcessor = (*Tracker)(nil)

// OnStart records a span in flight.
func (t *Tracker) OnStart(_ context.Context, s sdktrace.ReadWriteSpan) {
	n := &node{name: s.Name(), parent: s.Parent().SpanID(), start: s.StartTime()}
	for _, kv := range s.Attributes() {
		if kv.Key == attribute.Key("pravo.tasks") {
			n.tasks = int(kv.Value.AsInt64())
		}
	}
	id := s.SpanContext().SpanID()
	t.mu.Lock()
	t.active[id] = n
	t.order = append(t.order, id)
	if strings.HasPrefix(n.name, "phase ") {
		t.done = 0
	}
	t.mu.Unlock()
}

// OnEnd forgets a span.
func (t *Tracker) OnEnd(s sdktrace.ReadOnlySpan) {
	id := s.SpanContext().SpanID()
	t.mu.Lock()
	defer t.mu.Unlock()
	n, ok := t.active[id]
	if !ok {
		return
	}
	delete(t.active, id)
	for i, o := range t.order {
		if o == id {
			t.order = append(t.order[:i], t.order[i+1:]...)
			break
		}
	}
	if strings.HasPrefix(n.name, "resource ") {
		t.done++
	}
}

// Shutdown and ForceFlush have nothing to flush.
func (t *Tracker) Shutdown(context.Context) error   { t.Stop(); return nil }
func (t *Tracker) ForceFlush(context.Context) error { return nil }
