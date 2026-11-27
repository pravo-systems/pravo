// Package applyview draws an apply on a terminal. A line is printed for each
// resource as its change ends, and a block below the lines shows what is in
// flight and how far the apply has come, redrawn in place. It reads the
// engine's events and nothing else, so what it shows is what -json streams.
//
// It never prints a value of a resource: the id of a resource is the one that
// the engine reports, shortened, and an error is left to the command that ends
// with it.
package applyview

import (
	"fmt"
	"io"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/pravo-systems/pravo/internal/engine"
)

var frames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

const (
	reset  = "\x1b[0m"
	bold   = "\x1b[1m"
	dim    = "\x1b[2m"
	red    = "\x1b[31m"
	green  = "\x1b[32m"
	yellow = "\x1b[33m"
	cyan   = "\x1b[36m"

	tick     = 100 * time.Millisecond
	maxFlies = 8  // in-flight rows drawn before "and N more"
	maxAddr  = 44 // widest address column
	barWidth = 24
)

// Mode says when the view is drawn: "auto" on a terminal that can redraw, "live"
// always, "plain" never.
type Mode string

const (
	Auto  Mode = "auto"
	Live  Mode = "live"
	Plain Mode = "plain"
)

// ParseMode reads the value of --progress or $PRAVO_PROGRESS.
func ParseMode(s string) (Mode, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", "auto":
		return Auto, nil
	case "live", "on", "true", "1", "yes":
		return Live, nil
	case "plain", "off", "false", "0", "no", "none":
		return Plain, nil
	}
	return Auto, fmt.Errorf("unknown progress mode %q: use auto, live or plain", s)
}

// Wanted reports whether the view is drawn for the mode: auto asks for a terminal
// that redraws, no CI and no TERM=dumb, and on Windows one that is known to read
// escape sequences. tty says that standard output is a terminal.
func Wanted(mode Mode, tty bool, goos string, get func(string) string) bool {
	switch mode {
	case Live:
		return true
	case Plain:
		return false
	}
	if !tty || get("TERM") == "dumb" {
		return false
	}
	for _, k := range []string{"CI", "GITHUB_ACTIONS", "BUILDKITE", "TF_BUILD"} {
		if get(k) != "" {
			return false
		}
	}
	if goos == "windows" {
		// The old console host does not read the sequences. Windows Terminal and
		// the terminals of editors say so.
		return get("WT_SESSION") != "" || get("TERM_PROGRAM") != "" || get("ConEmuANSI") == "ON" || get("ANSICON") != ""
	}
	return true
}

type flight struct {
	addr, kind string
	start      time.Time
}

// View follows the events of one apply.
type View struct {
	w     io.Writer
	color bool
	width func() int
	hold  func() func() // takes another status line off the screen

	mu      sync.Mutex
	on      bool // the apply has started
	total   int
	done    int
	failed  int
	began   time.Time
	flying  map[string]*flight
	order   []string
	block   int // lines of the block on the screen
	frame   int
	release func()
	stop    chan struct{}
	stopped chan struct{}
	now     func() time.Time
}

// New returns a view that draws on w. width gives the columns of the terminal;
// hold, when set, is called when the apply starts and returns what to call when
// it ends, so that a status line of another kind gives way.
func New(w io.Writer, color bool, width func() int, hold func() func()) *View {
	return &View{w: w, color: color, width: width, hold: hold, flying: map[string]*flight{}, now: time.Now}
}

func (v *View) c(code, s string) string {
	if !v.color || s == "" {
		return s
	}
	return code + s + reset
}

// Handle takes an event of the engine.
func (v *View) Handle(ev engine.Event) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if ev.Kind == "apply" {
		switch ev.Phase {
		case "start":
			v.begin(ev.Total)
		case "end":
			v.finish()
		}
		return
	}
	if !v.on || !tracked(ev.Kind) {
		return
	}
	switch ev.Phase {
	case "start":
		if _, ok := v.flying[ev.Addr]; !ok {
			v.order = append(v.order, ev.Addr)
		}
		v.flying[ev.Addr] = &flight{addr: ev.Addr, kind: ev.Kind, start: v.now()}
	case "done":
		v.end(ev, false)
	case "error":
		v.end(ev, true)
	}
	v.draw()
}

// Log prints a line that does not belong to a resource above the block. The lines
// that the engine prints for a change, as it starts and as it ends, are left out:
// the block and the rows show them.
func (v *View) Log(format string, args ...any) {
	line := fmt.Sprintf(format, args...)
	if started.MatchString(line) {
		return
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	if !v.on {
		v.print(line + "\n")
		return
	}
	v.erase()
	v.print(line + "\n")
	v.draw()
}

var started = regexp.MustCompile(`^\S+: (((Creating|Modifying|Destroying|Opening|Reading|Refreshing state)\.\.\.)|(Creating replacement|Destroying previous instance)\.\.\.|((Creation|Modifications|Replacement|Destruction|Read) complete after ))`)

func tracked(kind string) bool {
	switch kind {
	case "create", "update", "replace", "destroy", "delete":
		return true
	}
	return false
}

func (v *View) begin(total int) {
	if v.on {
		return
	}
	v.on, v.total, v.began = true, total, v.now()
	if v.hold != nil {
		v.release = v.hold()
	}
	v.stop, v.stopped = make(chan struct{}), make(chan struct{})
	go v.loop(v.stop, v.stopped)
	v.draw()
}

func (v *View) loop(stop, stopped chan struct{}) {
	defer close(stopped)
	tk := time.NewTicker(tick)
	defer tk.Stop()
	for {
		select {
		case <-stop:
			return
		case <-tk.C:
			v.mu.Lock()
			if v.on {
				v.frame++
				v.draw()
			}
			v.mu.Unlock()
		}
	}
}

// finish takes the block off the screen. What was still in flight is said to have
// stopped, with the time it had been going on.
func (v *View) finish() {
	if !v.on {
		return
	}
	v.erase()
	for _, a := range v.order {
		if f := v.flying[a]; f != nil {
			v.print(v.row("·", dim, a, "stopped", v.since(f.start), "") + "\n")
		}
	}
	v.flying, v.order, v.on = map[string]*flight{}, nil, false
	stop, stopped, rel := v.stop, v.stopped, v.release
	v.release = nil
	v.mu.Unlock()
	close(stop)
	<-stopped
	v.mu.Lock()
	if rel != nil {
		rel()
	}
}

func (v *View) end(ev engine.Event, failed bool) {
	f := v.flying[ev.Addr]
	delete(v.flying, ev.Addr)
	for i, a := range v.order {
		if a == ev.Addr {
			v.order = append(v.order[:i], v.order[i+1:]...)
			break
		}
	}
	elapsed := time.Duration(ev.Elapsed) * time.Millisecond
	if ev.Elapsed == 0 && f != nil {
		elapsed = v.now().Sub(f.start)
	}
	v.erase()
	if failed {
		v.failed++
		v.print(v.row("✗", red, ev.Addr, "failed", dur(elapsed), "") + "\n")
		return
	}
	v.done++
	sym, color := symbol(ev.Kind)
	v.print(v.row(sym, color, ev.Addr, past(ev.Kind), dur(elapsed), shortID(ev.ID)) + "\n")
}

// cols is the width the lines are fitted to.
func (v *View) cols() int {
	if v.width != nil {
		if w := v.width(); w > 20 {
			return w
		}
	}
	return 100
}

// row is one line: a symbol, the address, what happened, how long it took and,
// dimmed, the id when there is room for it.
func (v *View) row(sym, color, addr, what, took, id string) string {
	cols := v.cols() - 1
	aw := v.addrWidth()
	addr = clip(addr, aw)
	line := fmt.Sprintf("  %s %-*s  %-10s %7s", v.c(color, sym), aw, addr, what, took)
	used := 2 + 1 + 1 + aw + 2 + 10 + 1 + 7
	if id != "" && used+2+utf8.RuneCountInString(id) <= cols {
		line += "  " + v.c(dim, id)
	}
	return strings.TrimRight(line, " ")
}

// addrWidth is the column of the addresses: as wide as the longest one seen, up
// to a limit, so that the columns of a run stay where they are.
func (v *View) addrWidth() int {
	w := 24
	for _, a := range v.order {
		if n := utf8.RuneCountInString(a); n > w {
			w = n
		}
	}
	if w > maxAddr {
		w = maxAddr
	}
	// What is left of a line when the address has its column: the symbol, the
	// words and the time take 24 columns.
	if room := v.cols() - 1 - 24; w > room {
		w = max(room, 8)
	}
	return w
}

// draw replaces the block: the changes in flight, the longest first, and the bar.
func (v *View) draw() {
	if !v.on {
		return
	}
	v.erase()
	var b strings.Builder
	n := 0
	fl := make([]*flight, 0, len(v.order))
	for _, a := range v.order {
		fl = append(fl, v.flying[a])
	}
	sort.SliceStable(fl, func(i, j int) bool { return fl[i].start.Before(fl[j].start) })
	for i, f := range fl {
		if i == maxFlies {
			fmt.Fprintf(&b, "    %s\n", v.c(dim, fmt.Sprintf("and %d more in flight", len(fl)-maxFlies)))
			n++
			break
		}
		spin := frames[(v.frame+i)%len(frames)]
		b.WriteString(v.row(spin, cyan, f.addr, ing(f.kind), v.since(f.start), "") + "\n")
		n++
	}
	b.WriteString(v.bar() + "\n")
	n++
	v.print(b.String())
	v.block = n
}

func (v *View) bar() string {
	elapsed := v.c(dim, v.since(v.began))
	if v.total <= 0 {
		return fmt.Sprintf("  %s %d done  %s", v.c(dim, "■"), v.done, elapsed)
	}
	fill := v.done * barWidth / v.total
	if fill > barWidth {
		fill = barWidth
	}
	bar := v.c(green, strings.Repeat("■", fill)) + v.c(dim, strings.Repeat("□", barWidth-fill))
	s := fmt.Sprintf("  %s  %d/%d", bar, v.done, v.total)
	if v.failed > 0 {
		s += "  " + v.c(red, fmt.Sprintf("%d failed", v.failed))
	}
	return s + "  " + elapsed
}

// erase takes the block off the screen, leaving the cursor where it started.
func (v *View) erase() {
	if v.block > 0 {
		v.print(fmt.Sprintf("\x1b[%dA\r\x1b[J", v.block))
		v.block = 0
	}
}

// print writes to the terminal; a terminal that cannot be written to has nothing to be told.
func (v *View) print(s string) { _, _ = io.WriteString(v.w, s) }

func (v *View) since(t time.Time) string { return dur(v.now().Sub(t)) }

func symbol(kind string) (string, string) {
	switch kind {
	case "create":
		return "+", green
	case "update":
		return "~", yellow
	case "replace":
		return "±", yellow
	}
	return "-", red
}

func past(kind string) string {
	switch kind {
	case "create":
		return "created"
	case "update":
		return "updated"
	case "replace":
		return "replaced"
	}
	return "destroyed"
}

func ing(kind string) string {
	switch kind {
	case "create":
		return "creating"
	case "update":
		return "updating"
	case "replace":
		return "replacing"
	}
	return "destroying"
}

// dur writes a time as a person reads it: 0.4s, 31s, 2m 55s.
func dur(d time.Duration) string {
	switch {
	case d < time.Second:
		return fmt.Sprintf("%.1fs", d.Seconds())
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm %02ds", int(d.Minutes()), int(d.Seconds())%60)
	}
	return fmt.Sprintf("%dh %02dm", int(d.Hours()), int(d.Minutes())%60)
}

// shortID keeps what tells one object from another: the last part of an ARN or a
// path, not the account and region in front of it.
func shortID(id string) string {
	if id == "" {
		return ""
	}
	if i := strings.IndexAny(id, "\n\r"); i >= 0 {
		id = id[:i]
	}
	if strings.HasPrefix(id, "arn:") || strings.Contains(id, "://") {
		if i := strings.LastIndexAny(id, ":/"); i >= 0 && i < len(id)-1 {
			id = id[i+1:]
		}
	}
	if i := strings.Index(id, "|"); i >= 0 {
		id = id[:i]
	}
	return clip(id, 32)
}

func clip(s string, w int) string {
	if utf8.RuneCountInString(s) <= w {
		return s
	}
	r := []rune(s)
	return string(r[:w-1]) + "…"
}
