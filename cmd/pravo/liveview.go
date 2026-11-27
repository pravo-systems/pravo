package main

import (
	"os"
	"runtime"

	"github.com/mattn/go-isatty"
	"golang.org/x/term"

	"github.com/pravo-systems/pravo/internal/applyview"
	"github.com/pravo-systems/pravo/internal/engine"
)

// showEvents sets how the progress of an apply that changes things is shown: as
// newline-delimited JSON events with --json, as the live view on a terminal, and
// as the lines the engine prints otherwise. Commands that only plan print a single
// document, so they do not ask for it.
func showEvents(rt *runContext, opts *engine.Options) {
	switch {
	case rt.flags.JSON:
		opts.Event = func(ev engine.Event) {
			if !ev.View {
				emitLine(ev)
			}
		}
	default:
		if v := liveView(rt); v != nil {
			opts.Event, opts.Progress = v.Handle, v.Log
		}
	}
}

// progressMode is the --progress flag, or $PRAVO_PROGRESS when the flag is not given.
// A value that is not one is taken as auto: the flag is checked where it is read.
func progressMode(rt *runContext) applyview.Mode {
	v := rt.flags.Progress
	if v == "" {
		v = environment().Get("PRAVO_PROGRESS")
	}
	m, err := applyview.ParseMode(v)
	if err != nil {
		return applyview.Auto
	}
	return m
}

// liveView returns the view that draws an apply on the terminal, or nil when the
// apply is printed as lines: with -v, with --markdown, in CI, on a terminal that
// cannot redraw, or when --progress=plain says so.
func liveView(rt *runContext) *applyview.View {
	if rt.flags.Verbose > 0 || rt.flags.Markdown || rt.out == nil {
		return nil
	}
	if v := rt.flags.Progress; v != "" {
		if _, err := applyview.ParseMode(v); err != nil {
			return nil
		}
	}
	f := os.Stdout
	tty := isatty.IsTerminal(f.Fd()) || isatty.IsCygwinTerminal(f.Fd())
	if !applyview.Wanted(progressMode(rt), tty, runtime.GOOS, environment().Get) {
		return nil
	}
	width := func() int {
		if w, _, err := term.GetSize(int(f.Fd())); err == nil {
			return w
		}
		return 0
	}
	hold := func() func() {
		if spinner == nil {
			return func() {}
		}
		return spinner.Hold()
	}
	return applyview.New(rt.out.Out, rt.out.Color, width, hold)
}
