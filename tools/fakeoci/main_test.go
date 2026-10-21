package main

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"
)

// TestListensAndReportsThePort starts the server on port 0, reads the port it
// bound from the first line of standard output, calls the registry API and checks
// that the server returns when standard input ends.
func TestListensAndReportsThePort(t *testing.T) {
	stdinR, stdinW := io.Pipe()
	stdoutR, stdoutW := io.Pipe()
	var stderr strings.Builder
	done := make(chan error, 1)
	go func() {
		done <- run([]string{"-addr", "127.0.0.1:0", "-exit-on-eof"}, stdinR, stdoutW, &stderr)
	}()

	line, err := bufio.NewReader(stdoutR).ReadString('\n')
	if err != nil {
		t.Fatalf("no readiness line: %v", err)
	}
	addr, ok := strings.CutPrefix(strings.TrimSpace(line), "listening on ")
	if !ok || strings.HasSuffix(addr, ":0") {
		t.Fatalf("readiness line = %q, want \"listening on 127.0.0.1:<port>\"", line)
	}

	resp, err := http.Get(fmt.Sprintf("http://%s/v2/", addr))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || resp.Header.Get("Docker-Distribution-Api-Version") == "" {
		t.Errorf("GET /v2/ = %d, %v; want the registry API", resp.StatusCode, resp.Header)
	}

	stdinW.Close()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("run returned %v after standard input ended", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the server did not exit when standard input ended")
	}
	if !strings.Contains(stderr.String(), "fakeoci: serving an in-memory OCI registry on "+addr) {
		t.Errorf("log = %q", stderr.String())
	}
}

func TestRunsUntilStoppedWithoutExitOnEOF(t *testing.T) {
	// An empty standard input must not end the server unless it is asked to.
	stdoutR, stdoutW := io.Pipe()
	done := make(chan error, 1)
	go func() {
		done <- run([]string{"-addr", "127.0.0.1:0"}, strings.NewReader(""), stdoutW, io.Discard)
	}()
	if _, err := bufio.NewReader(stdoutR).ReadString('\n'); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		t.Fatalf("run returned %v with an empty standard input", err)
	case <-time.After(200 * time.Millisecond):
	}
}

func TestBindFailureIsAnError(t *testing.T) {
	held, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	var out strings.Builder
	err = run([]string{"-addr", held.Addr().String()}, strings.NewReader(""), &out, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "address already in use") {
		t.Errorf("run on a held port = %v, want address already in use", err)
	}
	if out.Len() != 0 {
		t.Errorf("a server that did not bind announced %q", out.String())
	}
}

func TestUnknownFlag(t *testing.T) {
	if err := run([]string{"-nope"}, strings.NewReader(""), io.Discard, io.Discard); err == nil {
		t.Error("an unknown flag is accepted")
	}
}
