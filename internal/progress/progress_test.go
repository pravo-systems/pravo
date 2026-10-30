package progress

import (
	"bytes"
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"go.opentelemetry.io/otel/attribute"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
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
func (l *lockedBuf) String() string { l.mu.Lock(); defer l.mu.Unlock(); return l.b.String() }

func newProvider(t *testing.T, tr *Tracker) trace.Tracer {
	t.Helper()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(tr))
	t.Cleanup(func() { _ = tp.Shutdown(context.Background()) })
	return tp.Tracer("test")
}

func TestStatusNamesThePhaseAndTheCount(t *testing.T) {
	tr := New(&lockedBuf{})
	tracer := newProvider(t, tr)
	ctx, phase := tracer.Start(context.Background(), "phase apply changes", trace.WithAttributes(attribute.Int("pravo.tasks", 4)))
	_, a := tracer.Start(ctx, "resource web")
	a.End()
	_, b := tracer.Start(ctx, "resource db")
	tr.mu.Lock()
	got := tr.statusLocked()
	tr.mu.Unlock()
	if !strings.HasPrefix(got, "Applying changes · 1/4 done · db") {
		t.Fatalf("status = %q", got)
	}
	b.End()
	phase.End()
	tr.mu.Lock()
	got = tr.statusLocked()
	tr.mu.Unlock()
	if got != "" {
		t.Fatalf("status after everything ended = %q", got)
	}
}

func TestProviderSetupReadsAsAVerbAndName(t *testing.T) {
	tr := New(&lockedBuf{})
	tracer := newProvider(t, tr)
	_, s := tracer.Start(context.Background(), "setup install provider random")
	defer s.End()
	tr.mu.Lock()
	defer tr.mu.Unlock()
	if got := tr.statusLocked(); got != "Installing random" {
		t.Fatalf("status = %q", got)
	}
}

func TestNothingIsDrawnBeforeTheDelay(t *testing.T) {
	buf := &lockedBuf{}
	tr := New(buf)
	tracer := newProvider(t, tr)
	tr.Start(false)
	_, s := tracer.Start(context.Background(), "phase lock")
	time.Sleep(3 * tick)
	s.End()
	tr.Stop()
	if buf.String() != "" {
		t.Fatalf("drew %q for a command that was quick", buf.String())
	}
}

func TestTheLineGivesWayToOutputAndWaitsForALineStart(t *testing.T) {
	old := delay
	delay = 0
	t.Cleanup(func() { delay = old })
	buf := &lockedBuf{}
	tr := New(buf)
	tracer := newProvider(t, tr)
	tr.Start(false)
	_, s := tracer.Start(context.Background(), "phase lock")
	deadline := time.Now().Add(2 * time.Second)
	for !strings.Contains(buf.String(), "Waiting for the lock") {
		if time.Now().After(deadline) {
			t.Fatalf("nothing drawn: %q", buf.String())
		}
		time.Sleep(tick / 2)
	}
	w := tr.Guard(buf)
	w.Write([]byte("Continue? ")) // a prompt: ends mid-line
	mark := len(buf.String())
	time.Sleep(4 * tick)
	if after := buf.String()[mark:]; after != "" {
		t.Fatalf("drew over a prompt: %q", after)
	}
	w.Write([]byte("yes\n"))
	s.End()
	tr.Stop()
	out := buf.String()
	if !strings.Contains(out, clearLine+showCur+"Continue? ") {
		t.Fatalf("the line was not cleared before the prompt: %q", out)
	}
	if !strings.HasSuffix(out, "yes\n") && !strings.HasSuffix(out, "yes\n"+clearLine+showCur) {
		t.Fatalf("unexpected tail: %q", out)
	}
}

func TestStopLeavesNothingOnTheScreen(t *testing.T) {
	old := delay
	delay = 0
	t.Cleanup(func() { delay = old })
	buf := &lockedBuf{}
	tr := New(buf)
	tracer := newProvider(t, tr)
	tr.Start(false)
	_, s := tracer.Start(context.Background(), "phase lock")
	time.Sleep(3 * tick)
	tr.Stop()
	s.End()
	if out := buf.String(); !strings.HasSuffix(out, clearLine+showCur) {
		t.Fatalf("the line was left on the screen: %q", out)
	}
}
