package applyview

import (
	"bytes"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/pravo-systems/pravo/internal/engine"
)

type lockedBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *lockedBuf) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *lockedBuf) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

func newView(buf *lockedBuf) *View {
	return New(buf, false, func() int { return 100 }, nil)
}

func TestRowsAndBar(t *testing.T) {
	var buf lockedBuf
	v := newView(&buf)
	v.Handle(engine.Event{Kind: "apply", Phase: "start", Total: 2})
	v.Handle(engine.Event{Kind: "create", Addr: "net.vpc", Phase: "start"})
	v.Handle(engine.Event{Kind: "create", Addr: "lb.main", Phase: "start"})
	v.Handle(engine.Event{Kind: "create", Addr: "net.vpc", Phase: "done", Elapsed: 1500, ID: "arn:aws:ec2:eu-north-1:123456789012:vpc/vpc-0abc"})
	v.Handle(engine.Event{Kind: "apply", Phase: "end"})
	out := buf.String()
	for _, want := range []string{"+ net.vpc", "created", "1s", "vpc-0abc", "1/2", "creating", "lb.main", "stopped"} {
		if !strings.Contains(out, want) {
			t.Errorf("the output lacks %q:\n%s", want, out)
		}
	}
	for _, not := range []string{"123456789012", "arn:aws"} {
		if strings.Contains(out, not) {
			t.Errorf("the output has %q, which the id is shortened without:\n%s", not, out)
		}
	}
}

func TestFailureIsCounted(t *testing.T) {
	var buf lockedBuf
	v := newView(&buf)
	v.Handle(engine.Event{Kind: "apply", Phase: "start", Total: 3})
	v.Handle(engine.Event{Kind: "update", Addr: "alarms.slow", Phase: "start"})
	v.Handle(engine.Event{Kind: "update", Addr: "alarms.slow", Phase: "error", Message: "secret-value-in-an-error"})
	v.Handle(engine.Event{Kind: "apply", Phase: "end"})
	out := buf.String()
	if !strings.Contains(out, "✗ alarms.slow") || !strings.Contains(out, "failed") || !strings.Contains(out, "1 failed") {
		t.Errorf("the failure is not shown:\n%s", out)
	}
	if strings.Contains(out, "secret-value-in-an-error") {
		t.Errorf("the view prints the message of an error, which the command prints once:\n%s", out)
	}
}

func TestEventsBeforeTheApplyAreIgnored(t *testing.T) {
	var buf lockedBuf
	v := newView(&buf)
	v.Handle(engine.Event{Kind: "create", Addr: "a.b", Phase: "done"})
	v.Handle(engine.Event{Kind: "read", Addr: "a.c", Phase: "start"})
	if buf.String() != "" {
		t.Errorf("the view drew before the apply started: %q", buf.String())
	}
}

func TestLogSkipsTheLinesTheViewDraws(t *testing.T) {
	var buf lockedBuf
	v := newView(&buf)
	v.Log("%s: Creating... %s", "a.b", "[id=x]")
	v.Log("%s: Creation complete after %s %s", "a.b", "1s", "[id=x]")
	v.Log("Warning: %s", "careful")
	if got := buf.String(); got != "Warning: careful\n" {
		t.Errorf("Log printed %q", got)
	}
}

func TestRowsFitTheTerminal(t *testing.T) {
	var buf lockedBuf
	v := New(&buf, false, func() int { return 50 }, nil)
	v.Handle(engine.Event{Kind: "apply", Phase: "start", Total: 1})
	long := "some.very.long.address." + strings.Repeat("x", 80)
	v.Handle(engine.Event{Kind: "create", Addr: long, Phase: "start"})
	v.Handle(engine.Event{Kind: "create", Addr: long, Phase: "done", Elapsed: 10, ID: strings.Repeat("i", 40)})
	for _, l := range strings.Split(buf.String(), "\n") {
		l = strings.TrimLeft(l, "\x1b[0123456789AJ\r")
		if n := len([]rune(l)); n > 49 {
			t.Errorf("a line of %d columns in a terminal of 50: %q", n, l)
		}
	}
}

func TestHoldIsReleased(t *testing.T) {
	var buf lockedBuf
	held, released := false, false
	v := New(&buf, false, nil, func() func() { held = true; return func() { released = true } })
	v.Handle(engine.Event{Kind: "apply", Phase: "start"})
	if !held || released {
		t.Fatalf("held %v, released %v after the start", held, released)
	}
	v.Handle(engine.Event{Kind: "apply", Phase: "end"})
	if !released {
		t.Error("the status line was not given back")
	}
}

func TestTheLoopStopsWithTheApply(t *testing.T) {
	var buf lockedBuf
	v := newView(&buf)
	v.Handle(engine.Event{Kind: "apply", Phase: "start", Total: 1})
	time.Sleep(2 * tick)
	v.Handle(engine.Event{Kind: "apply", Phase: "end"})
	n := len(buf.String())
	time.Sleep(2 * tick)
	if len(buf.String()) != n {
		t.Error("the view drew after the apply ended")
	}
}

func TestWanted(t *testing.T) {
	env := func(kv ...string) func(string) string {
		return func(k string) string {
			for i := 0; i < len(kv); i += 2 {
				if kv[i] == k {
					return kv[i+1]
				}
			}
			return ""
		}
	}
	for _, c := range []struct {
		name string
		mode Mode
		tty  bool
		goos string
		env  func(string) string
		want bool
	}{
		{"a terminal", Auto, true, "linux", env(), true},
		{"a pipe", Auto, false, "linux", env(), false},
		{"CI", Auto, true, "linux", env("CI", "true"), false},
		{"dumb", Auto, true, "linux", env("TERM", "dumb"), false},
		{"forced", Live, false, "linux", env("CI", "true"), true},
		{"plain", Plain, true, "linux", env(), false},
		{"the old Windows console", Auto, true, "windows", env(), false},
		{"Windows Terminal", Auto, true, "windows", env("WT_SESSION", "x"), true},
	} {
		if got := Wanted(c.mode, c.tty, c.goos, c.env); got != c.want {
			t.Errorf("%s: %v, want %v", c.name, got, c.want)
		}
	}
}

func TestParseMode(t *testing.T) {
	for in, want := range map[string]Mode{"": Auto, "auto": Auto, "LIVE": Live, "plain": Plain, "off": Plain} {
		if got, err := ParseMode(in); err != nil || got != want {
			t.Errorf("%q: %v, %v", in, got, err)
		}
	}
	if _, err := ParseMode("fancy"); err == nil {
		t.Error("a mode that does not exist was accepted")
	}
}

func TestShortID(t *testing.T) {
	for in, want := range map[string]string{
		"vpc-0abc": "vpc-0abc",
		"arn:aws:secretsmanager:us-east-2:192216148574:secret:harebell/us/ops-key-WzNbgC": "ops-key-WzNbgC",
		"https://sqs.us-east-2.amazonaws.com/192216148574/harebell-installs-failed":       "harebell-installs-failed",
		"arn:aws:s3:::bucket|terraform-xyz":                                               "bucket",
	} {
		if got := shortID(in); got != want {
			t.Errorf("shortID(%q) = %q, want %q", in, got, want)
		}
	}
}
