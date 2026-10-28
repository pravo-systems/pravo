package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"time"
)

// serveArgs are the arguments of the command that starts the viewer, before
// the plan: it opens no browser, prints one JSON line with the address and the
// token, and takes a free port.
var serveArgs = []string{"serve", "--no-open", "--json", "--port", "0"}

// A viewer is a running pravo serve.
type viewer struct {
	// Base is the origin, such as http://127.0.0.1:43211, without a path.
	Base  string
	Token string

	cmd    *exec.Cmd
	cancel context.CancelFunc
	done   chan struct{}
	stderr *syncBuffer
}

// open is the address that a shot opens: the token, so that the server sets
// its cookie and redirects to /, and the fragment of the view. A browser keeps
// the fragment through the redirect.
func (v *viewer) open(fragment string) string {
	return v.Base + "/?token=" + url.QueryEscape(v.Token) + fragment
}

// startViewer starts pravo serve in dir and waits for its first line of
// output. A plan, when not empty, is passed as --plan, and env is added to
// the environment of the command.
func startViewer(ctx context.Context, bin, dir, plan string, env []string, log io.Writer) (*viewer, error) {
	args := append([]string(nil), serveArgs...)
	if plan != "" {
		args = append(args, "--plan", plan)
	}
	cctx, cancel := context.WithCancel(ctx)
	cmd := exec.CommandContext(cctx, bin, args...)
	cmd.Dir = dir
	cmd.Env = append(commandEnv(os.Environ()), env...)
	cmd.WaitDelay = 3 * time.Second
	stderr := &syncBuffer{}
	cmd.Stderr = stderr
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		return nil, err
	}
	if log != nil {
		fmt.Fprintf(log, "  pravo %s\n", strings.Join(args, " "))
	}
	if err := cmd.Start(); err != nil {
		cancel()
		return nil, fmt.Errorf("pravo %s: %w", strings.Join(args, " "), err)
	}
	v := &viewer{cmd: cmd, cancel: cancel, done: make(chan struct{}), stderr: stderr}

	type line struct {
		base, token string
		err         error
	}
	first := make(chan line, 1)
	go func() {
		defer close(v.done)
		sc := bufio.NewScanner(stdout)
		sc.Buffer(make([]byte, 0, 64<<10), 1<<20)
		sent := false
		var seen []string
		for sc.Scan() {
			text := sc.Text()
			if sent {
				continue
			}
			seen = append(seen, text)
			if base, token, ok := parseServeLine(text); ok {
				first <- line{base: base, token: token}
				sent = true
			}
		}
		if !sent {
			first <- line{err: fmt.Errorf("pravo serve exited without printing an address; its output was:\n%s", indent(strings.TrimSpace(strings.Join(seen, "\n")+"\n"+stderr.String())))}
		}
		cmd.Wait()
	}()

	select {
	case l := <-first:
		if l.err != nil {
			cancel()
			return nil, l.err
		}
		v.Base, v.Token = l.base, l.token
		return v, nil
	case <-time.After(30 * time.Second):
		cancel()
		return nil, fmt.Errorf("pravo serve printed no address within 30 s; stderr:\n%s", indent(strings.TrimSpace(stderr.String())))
	case <-ctx.Done():
		cancel()
		return nil, ctx.Err()
	}
}

// stop ends the server and waits for it.
func (v *viewer) stop() {
	v.cancel()
	select {
	case <-v.done:
	case <-time.After(5 * time.Second):
	}
}

// hostRe finds a loopback address in text.
var hostRe = regexp.MustCompile(`https?://(?:127\.0\.0\.1|localhost|\[::1\]):\d+[^\s"']*`)

// parseServeLine reads a line that pravo serve --json prints: an object with
// the address of the server and its token. The address is a url, which may
// carry the token as a query parameter, or an addr of the form host:port; the
// token is a field of its own or a parameter of the url. A line that is not
// JSON is searched for a loopback address.
func parseServeLine(line string) (base, token string, ok bool) {
	line = strings.TrimSpace(line)
	if line == "" {
		return "", "", false
	}
	var obj map[string]any
	if json.Unmarshal([]byte(line), &obj) == nil {
		var addr string
		for _, k := range []string{"url", "address", "addr"} {
			if s, _ := obj[k].(string); s != "" {
				addr = s
				break
			}
		}
		token, _ = obj["token"].(string)
		return fromAddress(addr, token)
	}
	if m := hostRe.FindString(line); m != "" {
		return fromAddress(m, "")
	}
	return "", "", false
}

// fromAddress splits an address, a url or host:port, into the origin and the
// token, taken from the argument or from the query of the url.
func fromAddress(addr, token string) (string, string, bool) {
	if addr == "" {
		return "", "", false
	}
	if !strings.Contains(addr, "://") {
		addr = "http://" + addr
	}
	u, err := url.Parse(addr)
	if err != nil || u.Host == "" {
		return "", "", false
	}
	if token == "" {
		token = u.Query().Get("token")
	}
	if token == "" {
		return "", "", false
	}
	return u.Scheme + "://" + u.Host, token, true
}

// syncBuffer is a buffer that two goroutines may use.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	// Keep the start of a long log: the first errors matter most.
	if b.buf.Len() > 1<<20 {
		return len(p), nil
	}
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}
