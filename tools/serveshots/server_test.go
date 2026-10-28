package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseServeLine(t *testing.T) {
	tests := []struct {
		name, line, base, token string
		ok                      bool
	}{
		{"url and token", `{"url":"http://127.0.0.1:43211/","token":"abc123"}`, "http://127.0.0.1:43211", "abc123", true},
		{"token in the url", `{"url":"http://127.0.0.1:43211/?token=abc123"}`, "http://127.0.0.1:43211", "abc123", true},
		{"addr", `{"addr":"127.0.0.1:43211","token":"t"}`, "http://127.0.0.1:43211", "t", true},
		{"ipv6", `{"url":"http://[::1]:8080/?token=zz"}`, "http://[::1]:8080", "zz", true},
		{"more fields", `{"version":1,"url":"http://localhost:9/?token=a%2Bb","pid":4}`, "http://localhost:9", "a+b", true},
		{"text", `pravo serve: http://127.0.0.1:4568/?token=t0k  (Ctrl-C to stop)`, "http://127.0.0.1:4568", "t0k", true},
		{"no token", `{"url":"http://127.0.0.1:4568/"}`, "", "", false},
		{"no address", `{"token":"abc"}`, "", "", false},
		{"progress", `loading the specification`, "", "", false},
		{"empty", ``, "", "", false},
		{"not loopback", `see https://example.com/?token=x`, "", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			base, token, ok := parseServeLine(tt.line)
			if base != tt.base || token != tt.token || ok != tt.ok {
				t.Errorf("parseServeLine(%q) = %q, %q, %v; want %q, %q, %v", tt.line, base, token, ok, tt.base, tt.token, tt.ok)
			}
		})
	}
}

func TestViewerOpen(t *testing.T) {
	v := &viewer{Base: "http://127.0.0.1:4567", Token: "a+b"}
	got := v.open("#/?sel=x&motion=off")
	want := "http://127.0.0.1:4567/?token=a%2Bb#/?sel=x&motion=off"
	if got != want {
		t.Errorf("open = %q, want %q", got, want)
	}
}

func TestStartViewer(t *testing.T) {
	_, log := fake(t)
	dir := t.TempDir()
	v, err := startViewer(context.Background(), os.Args[0], dir, filepath.Join(dir, "change.plan"), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer v.stop()
	if v.Base != "http://127.0.0.1:4567" || v.Token != "abc" {
		t.Errorf("viewer = %q, %q", v.Base, v.Token)
	}
	got := calls(t, log)
	if len(got) != 1 || !strings.HasPrefix(got[0], "serve --no-open --json --port 0 --plan ") || !strings.HasSuffix(got[0], "change.plan") {
		t.Errorf("calls = %q", got)
	}
}

func TestStartViewerWithoutAPlan(t *testing.T) {
	_, log := fake(t)
	v, err := startViewer(context.Background(), os.Args[0], t.TempDir(), "", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer v.stop()
	if got := calls(t, log); len(got) != 1 || got[0] != "serve --no-open --json --port 0" {
		t.Errorf("calls = %q", got)
	}
}

func TestStartViewerReadsATextLine(t *testing.T) {
	fake(t)
	t.Setenv("SERVESHOTS_FAKE_SERVE", "text")
	v, err := startViewer(context.Background(), os.Args[0], t.TempDir(), "", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer v.stop()
	if v.Base != "http://127.0.0.1:4568" || v.Token != "t0k" {
		t.Errorf("viewer = %q, %q", v.Base, v.Token)
	}
}

func TestStartViewerReportsAServerThatExits(t *testing.T) {
	fake(t)
	t.Setenv("SERVESHOTS_FAKE_SERVE", "garbage")
	_, err := startViewer(context.Background(), os.Args[0], t.TempDir(), "", nil, nil)
	if err == nil || !strings.Contains(err.Error(), "listening, somewhere") {
		t.Errorf("error = %v", err)
	}
}
