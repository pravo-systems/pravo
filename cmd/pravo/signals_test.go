package main

import (
	"os"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

// recorder stands in for the apply that a command runs.
type recorder struct {
	mu       sync.Mutex
	inFlight int
	stops    int
	abandons int
	notes    []string
	events   []string
	// hold, when set, is what Stop waits for: a provider that does not answer.
	hold chan struct{}
	// settle, when set, is what Abandon waits for: a state that does not answer.
	settle chan struct{}
}

func (r *recorder) InFlight() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.inFlight
}

func (r *recorder) Stop() {
	r.mu.Lock()
	r.stops++
	r.events = append(r.events, "stop")
	hold := r.hold
	r.mu.Unlock()
	if hold != nil {
		<-hold
	}
}

func (r *recorder) Abandon() []string {
	r.mu.Lock()
	r.abandons++
	r.events = append(r.events, "abandon")
	notes, settle := r.notes, r.settle
	r.mu.Unlock()
	if settle != nil {
		<-settle
	}
	return notes
}

func (r *recorder) counts() (stops, abandons int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.stops, r.abandons
}

// lines is a writer that can be read while it is written.
type lines struct {
	mu sync.Mutex
	b  strings.Builder
}

func (l *lines) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *lines) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

// signalled runs watchSignals and returns what a test needs to drive it.
type signalled struct {
	sigs      chan os.Signal
	finished  chan struct{}
	cancelled chan struct{}
	exited    chan int
	out       *lines
	over      chan struct{}
}

func watch(in interrupter, grace time.Duration) *signalled {
	s := &signalled{
		sigs: make(chan os.Signal, 3), finished: make(chan struct{}), cancelled: make(chan struct{}),
		exited: make(chan int, 1), out: &lines{}, over: make(chan struct{}),
	}
	go func() {
		defer close(s.over)
		watchSignals(s.sigs, s.finished, func() { close(s.cancelled) }, in, s.out, func(code int) { s.exited <- code }, grace)
	}()
	return s
}

func (s *signalled) signal() { s.sigs <- syscall.SIGINT }

func eventually(t *testing.T, what string, ok func() bool) {
	t.Helper()
	for deadline := time.Now().Add(5 * time.Second); !ok(); time.Sleep(2 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
	}
}

// The first signal cancels the command and says what it waits for; it asks no
// provider to stop. The second asks them. The end of the time allowed, or a
// third signal, gives the apply up and exits.
func TestSignalsAreHandledInStages(t *testing.T) {
	const first = "Interrupted. Waiting for 2 operations; interrupt again to ask the providers to stop.\n"

	t.Run("the grace runs out", func(t *testing.T) {
		in := &recorder{inFlight: 2, notes: []string{"creation of x was in flight; its result is unknown; check the provider"}}
		s := watch(in, 200*time.Millisecond)
		s.signal()
		<-s.cancelled
		eventually(t, "the first message", func() bool { return s.out.String() == first })
		if stops, _ := in.counts(); stops != 0 {
			t.Errorf("the first signal asked the providers to stop")
		}
		select {
		case code := <-s.exited:
			t.Fatalf("exited with %d after the first signal", code)
		case <-time.After(300 * time.Millisecond):
		}
		s.signal()
		eventually(t, "the providers to be asked", func() bool { stops, _ := in.counts(); return stops == 1 })
		select {
		case code := <-s.exited:
			if code != 1 {
				t.Errorf("exit code %d, want 1", code)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("did not exit once the time allowed was over")
		}
		if got, want := strings.Join(in.events, ","), "stop,abandon"; got != want {
			t.Errorf("events %s, want %s", got, want)
		}
		text := s.out.String()
		for _, want := range []string{first, "Asking the providers to stop. Waiting up to 0s", "Exiting without waiting for the providers. The result of these changes is not known:\n  creation of x was in flight"} {
			if !strings.Contains(text, want) {
				t.Errorf("output lacks %q:\n%s", want, text)
			}
		}
	})

	t.Run("a third signal", func(t *testing.T) {
		in := &recorder{inFlight: 1}
		s := watch(in, time.Hour)
		s.signal()
		<-s.cancelled
		s.signal()
		eventually(t, "the providers to be asked", func() bool { stops, _ := in.counts(); return stops == 1 })
		select {
		case <-s.exited:
			t.Fatal("exited before the third signal")
		case <-time.After(50 * time.Millisecond):
		}
		s.signal()
		if code := <-s.exited; code != 1 {
			t.Errorf("exit code %d, want 1", code)
		}
		if _, abandons := in.counts(); abandons != 1 {
			t.Errorf("abandoned %d times, want 1", abandons)
		}
		if text := s.out.String(); !strings.Contains(text, "Waiting for 1 operation;") || !strings.Contains(text, "Exiting.\n") {
			t.Errorf("output:\n%s", text)
		}
	})

	t.Run("a provider that does not answer", func(t *testing.T) {
		in := &recorder{inFlight: 1, hold: make(chan struct{})}
		defer close(in.hold)
		s := watch(in, time.Hour)
		s.signal()
		<-s.cancelled
		s.signal()
		eventually(t, "the providers to be asked", func() bool { stops, _ := in.counts(); return stops == 1 })
		s.signal()
		select {
		case code := <-s.exited:
			if code != 1 {
				t.Errorf("exit code %d, want 1", code)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("the third signal did not exit while a provider was asked to stop")
		}
	})

	t.Run("a state that does not answer", func(t *testing.T) {
		defer func(old time.Duration) { abandonWait = old }(abandonWait)
		abandonWait = 100 * time.Millisecond
		in := &recorder{inFlight: 1, settle: make(chan struct{})}
		defer close(in.settle)
		s := watch(in, time.Hour)
		s.signal()
		<-s.cancelled
		s.signal()
		eventually(t, "the providers to be asked", func() bool { stops, _ := in.counts(); return stops == 1 })
		s.signal()
		select {
		case code := <-s.exited:
			if code != 1 {
				t.Errorf("exit code %d, want 1", code)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("the command did not exit while the state was not answering")
		}
		if text := s.out.String(); !strings.Contains(text, "The state did not answer within 0s. Exiting;") {
			t.Errorf("output:\n%s", text)
		}
	})

	t.Run("the command ends first", func(t *testing.T) {
		in := &recorder{inFlight: 2}
		s := watch(in, time.Hour)
		s.signal()
		<-s.cancelled
		eventually(t, "the first message", func() bool { return s.out.String() == first })
		close(s.finished)
		<-s.over
		if stops, abandons := in.counts(); stops != 0 || abandons != 0 {
			t.Errorf("stopped %d times and abandoned %d times although the apply finished", stops, abandons)
		}
		select {
		case code := <-s.exited:
			t.Errorf("exited with %d", code)
		default:
		}
	})

	t.Run("nothing is in flight", func(t *testing.T) {
		in := &recorder{}
		s := watch(in, time.Hour)
		s.signal()
		<-s.cancelled
		close(s.finished)
		<-s.over
		if text := s.out.String(); text != "" {
			t.Errorf("a command with no change in flight said %q", text)
		}
	})

	t.Run("a second signal with nothing in flight", func(t *testing.T) {
		in := &recorder{}
		s := watch(in, time.Hour)
		s.signal()
		<-s.cancelled
		s.signal()
		select {
		case code := <-s.exited:
			if code != 1 {
				t.Errorf("exit code %d, want 1", code)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("did not exit although there was nothing to wait for")
		}
		if stops, abandons := in.counts(); stops != 0 || abandons != 1 {
			t.Errorf("stopped %d times and abandoned %d times, want 0 and 1", stops, abandons)
		}
		if text := s.out.String(); text != "Exiting.\n" {
			t.Errorf("output %q", text)
		}
	})

	t.Run("no signal", func(t *testing.T) {
		s := watch(&recorder{}, time.Hour)
		close(s.finished)
		<-s.over
		select {
		case <-s.cancelled:
			t.Error("the command was cancelled although no signal came")
		default:
		}
	})
}
