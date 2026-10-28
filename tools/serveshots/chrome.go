package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"
)

var errNoChrome = errors.New("no Chrome or Chromium found: pass -chrome PATH or set CHROME")

// findChrome returns the browser to run: the argument, then $CHROME, then the
// usual places of the platform.
func findChrome(arg string) (string, error) {
	for _, c := range []string{arg, os.Getenv("CHROME")} {
		if c == "" {
			continue
		}
		if p, err := exec.LookPath(c); err == nil {
			return p, nil
		}
		return "", fmt.Errorf("chrome %q: not found", c)
	}
	var names []string
	switch runtime.GOOS {
	case "darwin":
		names = []string{
			"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
			"/Applications/Chromium.app/Contents/MacOS/Chromium",
			"/Applications/Google Chrome Canary.app/Contents/MacOS/Google Chrome Canary",
		}
	case "windows":
		for _, base := range []string{os.Getenv("ProgramFiles"), os.Getenv("ProgramFiles(x86)"), os.Getenv("LocalAppData")} {
			if base != "" {
				names = append(names, filepath.Join(base, "Google", "Chrome", "Application", "chrome.exe"))
			}
		}
	}
	names = append(names, "google-chrome", "google-chrome-stable", "chromium", "chromium-browser", "chrome")
	for _, n := range names {
		if p, err := exec.LookPath(n); err == nil {
			return p, nil
		}
	}
	return "", errNoChrome
}

// chromeArgs are the arguments that start a headless browser for one shot: a
// fresh profile, a window of the shot's size and a DevTools port that the
// browser picks and prints. The window opens a blank page; the program
// navigates it.
func chromeArgs(userData string, w, h int) []string {
	return []string{
		"--headless=new",
		"--disable-gpu",
		"--hide-scrollbars",
		"--no-first-run",
		"--no-default-browser-check",
		"--disable-extensions",
		"--disable-background-networking",
		"--remote-debugging-port=0",
		"--user-data-dir=" + userData,
		fmt.Sprintf("--window-size=%d,%d", w, h),
		"about:blank",
	}
}

var devtoolsRe = regexp.MustCompile(`DevTools listening on ws://127\.0\.0\.1:(\d+)/`)

// startBrowser starts Chrome and waits for it to say where DevTools listens.
func startBrowser(ctx context.Context, chrome, userData string, w, h int) (*browser, error) {
	cmd := exec.CommandContext(ctx, chrome, chromeArgs(userData, w, h)...)
	cmd.WaitDelay = 3 * time.Second
	pipe, err := cmd.StderrPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("chrome: %w", err)
	}
	b := &browser{stderr: &syncBuffer{}}
	exited := make(chan struct{})
	ports := make(chan string, 1)
	go func() {
		sc := bufio.NewScanner(pipe)
		sc.Buffer(make([]byte, 0, 64<<10), 1<<20)
		for sc.Scan() {
			line := sc.Text()
			b.stderr.Write([]byte(line + "\n"))
			if m := devtoolsRe.FindStringSubmatch(line); m != nil {
				select {
				case ports <- m[1]:
				default:
				}
			}
		}
	}()
	go func() { cmd.Wait(); close(exited) }()
	b.stop = func() {
		cmd.Process.Kill()
		select {
		case <-exited:
		case <-time.After(5 * time.Second):
		}
	}
	select {
	case b.port = <-ports:
		return b, nil
	case <-exited:
		return nil, fmt.Errorf("chrome exited before it listened:\n%s", indent(lastLines(b.stderr.String(), 8)))
	case <-time.After(30 * time.Second):
		b.stop()
		return nil, fmt.Errorf("chrome did not listen within 30 s:\n%s", indent(lastLines(b.stderr.String(), 8)))
	case <-ctx.Done():
		b.stop()
		return nil, ctx.Err()
	}
}

// shotOptions are the waits of a screenshot and the text that it replaces.
type shotOptions struct {
	// Min is the shortest the page gets to settle, and Max the longest.
	Min, Max time.Duration
	// Replace lists pairs of text and what stands for it. Once the page has
	// settled, every occurrence in its text and in its title and aria-label
	// attributes is replaced before the picture is taken. The viewer shows the
	// directory of the workspace and the address of the server, which belong to
	// the machine and not to the picture.
	Replace [][2]string
	// Inspect, when set, is called after the replacement with a function that
	// evaluates an expression in the page. The tests use it to read the page.
	Inspect func(eval func(expression string) (string, error)) error
	// Script, when set, runs in the page once it has settled, as the body of
	// an async function, and the page gets to settle again before the
	// replacement and the picture.
	Script string
}

// replaceScript replaces text in the page: the pairs are an array of [from, to].
const replaceScript = `((pairs) => {
  const fix = (s) => { for (const [a, b] of pairs) s = s.split(a).join(b); return s; };
  const w = document.createTreeWalker(document.body, NodeFilter.SHOW_TEXT);
  for (let n = w.nextNode(); n; n = w.nextNode()) { const v = fix(n.nodeValue); if (v !== n.nodeValue) n.nodeValue = v; }
  for (const el of document.querySelectorAll("[title],[aria-label]")) {
    for (const a of ["title", "aria-label"]) if (el.hasAttribute(a)) el.setAttribute(a, fix(el.getAttribute(a)));
  }
  return true;
})(%s)`

// screenshot opens address in a fresh browser profile at the size of the shot
// and a device scale factor of 2, waits for the page to settle and writes the
// PNG to out. It fails when the browser fails, when the page does not settle
// and when it logs an error: an uncaught exception, a console.error, a failed
// request, a refused policy. The notes are the other messages that it logged.
func screenshot(ctx context.Context, chrome, userData, out, address string, w, h int, o shotOptions) (notes []string, err error) {
	if err := removeProfile(userData); err != nil {
		return nil, err
	}
	if err := os.Remove(out); err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, o.Max+40*time.Second)
	defer cancel()
	b, err := startBrowser(ctx, chrome, userData, w, h)
	if err != nil {
		return nil, err
	}
	defer b.stop()

	sock, err := b.pageSocket(ctx)
	if err != nil {
		return nil, err
	}
	ws, err := dialWS(ctx, sock)
	if err != nil {
		return nil, fmt.Errorf("devtools: %w", err)
	}
	defer ws.Close()
	c := newCDP(ws)

	for _, m := range []string{"Page.enable", "Runtime.enable", "Log.enable"} {
		if _, err := c.call(ctx, m, map[string]any{}); err != nil {
			return nil, err
		}
	}
	if _, err := c.call(ctx, "Emulation.setDeviceMetricsOverride", map[string]any{
		"width": w, "height": h, "deviceScaleFactor": scale, "mobile": false,
	}); err != nil {
		return nil, err
	}
	raw, err := c.call(ctx, "Page.navigate", map[string]any{"url": address})
	if err != nil {
		return nil, err
	}
	var nav struct {
		ErrorText string `json:"errorText"`
	}
	if json.Unmarshal(raw, &nav) == nil && nav.ErrorText != "" {
		return nil, fmt.Errorf("the browser could not open the address: %s", nav.ErrorText)
	}
	// The faces of the page load after the first layout, and the layout moves
	// when they arrive.
	if _, err := c.call(ctx, "Runtime.evaluate", map[string]any{"expression": "document.fonts.ready.then(() => true)", "awaitPromise": true}); err != nil {
		return nil, err
	}
	if err := waitSettled(ctx, c, 250*time.Millisecond, 4, o.Min, o.Max); err != nil {
		return nil, err
	}
	evaluate := func(expression string, await bool) (string, error) {
		raw, err := c.call(ctx, "Runtime.evaluate", map[string]any{"expression": expression, "returnByValue": true, "awaitPromise": await})
		if err != nil {
			return "", err
		}
		var r struct {
			Result struct {
				Value any `json:"value"`
			} `json:"result"`
			ExceptionDetails *struct {
				Text      string `json:"text"`
				Exception *struct {
					Description string `json:"description"`
				} `json:"exception"`
			} `json:"exceptionDetails"`
		}
		if err := json.Unmarshal(raw, &r); err != nil {
			return "", err
		}
		if d := r.ExceptionDetails; d != nil {
			text := d.Text
			if d.Exception != nil && d.Exception.Description != "" {
				text = d.Exception.Description
			}
			return "", fmt.Errorf("evaluating in the page: %s", text)
		}
		return fmt.Sprint(r.Result.Value), nil
	}
	eval := func(expression string) (string, error) { return evaluate(expression, false) }
	if o.Script != "" {
		if _, err := evaluate("(async () => {"+o.Script+"\n})()", true); err != nil {
			return nil, &pageError{fmt.Sprintf("the script of the shot failed: %v", err)}
		}
		if err := waitSettled(ctx, c, 250*time.Millisecond, 4, max(o.Min, time.Second), o.Max); err != nil {
			return nil, err
		}
	}
	if len(o.Replace) > 0 {
		pairs, err := json.Marshal(o.Replace)
		if err != nil {
			return nil, err
		}
		if _, err := eval(fmt.Sprintf(replaceScript, pairs)); err != nil {
			return nil, err
		}
	}
	if o.Inspect != nil {
		if err := o.Inspect(eval); err != nil {
			return nil, err
		}
	}
	raw, err = c.call(ctx, "Page.captureScreenshot", map[string]any{"format": "png", "fromSurface": true})
	if err != nil {
		return nil, err
	}
	data, err := decodeScreenshot(raw)
	if err != nil {
		return nil, err
	}
	var log consoleLog
	drainEvents(c, &log)
	if err := os.WriteFile(out, data, 0o644); err != nil {
		return log.Notes, err
	}
	if len(log.Errors) > 0 {
		return log.Notes, &pageError{fmt.Sprintf("the page logged %d errors:\n%s", len(log.Errors), indent(strings.Join(log.Errors, "\n")))}
	}
	return log.Notes, nil
}

// pageError is a failure of the page and not of the browser: a run of the
// browser that fails for another reason may be tried again, and this one
// would fail the same way.
type pageError struct{ msg string }

func (e *pageError) Error() string { return e.msg }

// screenshotRetry is screenshot, tried once more when the browser fails.
func screenshotRetry(ctx context.Context, chrome, userData, out, address string, w, h int, o shotOptions) ([]string, error) {
	notes, err := screenshot(ctx, chrome, userData, out, address, w, h, o)
	var pe *pageError
	if err != nil && !errors.As(err, &pe) && ctx.Err() == nil {
		return screenshot(ctx, chrome, userData, out, address, w, h, o)
	}
	return notes, err
}

// removeProfile removes the profile of a browser that has been stopped. The
// helper processes of Chrome can outlive the main one for a moment and write to
// the profile, which makes the removal fail with "directory not empty": it is
// tried again for a few seconds.
func removeProfile(dir string) error {
	var err error
	for range 40 {
		if err = os.RemoveAll(dir); err == nil {
			return nil
		}
		time.Sleep(250 * time.Millisecond)
	}
	return err
}
