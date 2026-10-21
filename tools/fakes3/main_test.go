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
// bound from the first line of standard output, uses the bucket and checks
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

	url := fmt.Sprintf("http://%s/bucket/key", addr)
	req, _ := http.NewRequest(http.MethodPut, url, strings.NewReader("value"))
	resp, err := http.DefaultClient.Do(req)
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("PUT: %v, %v", resp, err)
	}
	resp.Body.Close()
	resp, err = http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if string(body) != "value" {
		t.Errorf("GET = %q, want the object that was put", body)
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
	if !strings.Contains(stderr.String(), "fakes3: serving an in-memory bucket on "+addr) {
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
