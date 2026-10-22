package main

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestChromeArgs(t *testing.T) {
	args := chromeArgs("/p", 1440, 900)
	for _, want := range []string{
		"--headless=new",
		"--user-data-dir=/p",
		"--window-size=1440,900",
		"--remote-debugging-port=0",
	} {
		found := false
		for _, a := range args {
			if a == want {
				found = true
			}
		}
		if !found {
			t.Errorf("no %s in %q", want, args)
		}
	}
	if last := args[len(args)-1]; last != "about:blank" {
		t.Errorf("the page is %q", last)
	}
}

func TestFindChromeNamedButMissing(t *testing.T) {
	if _, err := findChrome(filepath.Join(t.TempDir(), "no-such-chrome")); err == nil {
		t.Error("no error")
	}
}

func event(method, params string) cdpEvent {
	return cdpEvent{Method: method, Params: []byte(params)}
}

func TestConsoleLog(t *testing.T) {
	var l consoleLog
	for _, ev := range []cdpEvent{
		event("Runtime.consoleAPICalled", `{"type":"log","args":[{"type":"string","value":"hello"},{"type":"number","value":3,"description":"3"}]}`),
		event("Runtime.consoleAPICalled", `{"type":"warning","args":[{"type":"string","value":"careful"}]}`),
		event("Runtime.consoleAPICalled", `{"type":"error","args":[{"type":"string","value":"boom"}]}`),
		event("Runtime.consoleAPICalled", `{"type":"error","args":[{"type":"object","description":"TypeError: x is undefined"}]}`),
		event("Runtime.consoleAPICalled", `{"type":"table","args":[]}`),
		event("Runtime.exceptionThrown", `{"exceptionDetails":{"text":"Uncaught","url":"http://h/app.js","lineNumber":12,"exception":{"description":"ReferenceError: y is not defined\n    at f (app.js:12)"}}}`),
		event("Log.entryAdded", `{"entry":{"level":"error","text":"Failed to load resource: the server responded with a status of 500 ()","url":"http://h/api/graph"}}`),
		event("Log.entryAdded", `{"entry":{"level":"error","text":"Failed to load resource: the server responded with a status of 404 ()","url":"http://h/favicon.ico"}}`),
		event("Log.entryAdded", `{"entry":{"level":"warning","text":"deprecated"}}`),
		event("Log.entryAdded", `{"entry":{"level":"info","text":"fine"}}`),
		event("Page.loadEventFired", `{}`),
	} {
		l.add(ev)
	}
	wantErrors := []string{
		"console.error: boom",
		"console.error: TypeError: x is undefined",
		"exception: ReferenceError: y is not defined (http://h/app.js:12)",
		"browser: Failed to load resource: the server responded with a status of 500 () (http://h/api/graph)",
	}
	if strings.Join(l.Errors, "\n") != strings.Join(wantErrors, "\n") {
		t.Errorf("errors =\n%s\nwant\n%s", strings.Join(l.Errors, "\n"), strings.Join(wantErrors, "\n"))
	}
	wantNotes := []string{"console.log: hello 3", "console.warning: careful", "browser: deprecated"}
	if strings.Join(l.Notes, "\n") != strings.Join(wantNotes, "\n") {
		t.Errorf("notes =\n%s\nwant\n%s", strings.Join(l.Notes, "\n"), strings.Join(wantNotes, "\n"))
	}
}

func TestDecodeScreenshot(t *testing.T) {
	// "\x89PNG" in base64 is iVBORw==.
	if _, err := decodeScreenshot([]byte(`{"data":"iVBORw=="}`)); err != nil {
		t.Error(err)
	}
	if _, err := decodeScreenshot([]byte(`{"data":"aGVsbG8="}`)); err == nil {
		t.Error("data that is not a PNG: no error")
	}
	if _, err := decodeScreenshot([]byte(`{"data":"!!"}`)); err == nil {
		t.Error("data that is not base64: no error")
	}
}

// TestScreenshot drives a real browser against a local page. It is skipped
// where none is installed.
func TestScreenshot(t *testing.T) {
	if testing.Short() {
		t.Skip("starts a browser for each page")
	}
	chrome, err := findChrome("")
	if err != nil {
		t.Skip(err)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/ok", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `<!doctype html><body style="margin:0;background:linear-gradient(90deg,#123,#fa0)"><h1 style="color:#fff">shot</h1><script>console.log("fine"); document.body.append(window.innerWidth + "x" + window.innerHeight + "@" + devicePixelRatio)</script>`)
	})
	mux.HandleFunc("/path", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `<!doctype html><body style="margin:0;background:linear-gradient(90deg,#123,#fa0)"><p id="t" title="/work/a/b" aria-label="/work/a/b">project /work/a/b and /work/a/b/pravo.state</p><p>127.0.0.1:4242</p>`)
	})
	mux.HandleFunc("/late", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `<!doctype html><body style="margin:0;background:#123"><script>setTimeout(() => { document.body.style.background = "linear-gradient(90deg,#123,#fa0)"; document.body.textContent = "late" }, 700)</script>`)
	})
	mux.HandleFunc("/throws", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `<!doctype html><body style="margin:0;background:linear-gradient(90deg,#123,#fa0)"><script>throw new Error("boom from the page")</script>`)
	})
	mux.HandleFunc("/logs", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `<!doctype html><body style="margin:0;background:linear-gradient(90deg,#123,#fa0)"><script>console.error("an error that was logged")</script>`)
	})
	mux.HandleFunc("/fails", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `<!doctype html><body style="margin:0;background:linear-gradient(90deg,#123,#fa0)"><script src="/missing.js"></script>`)
	})
	mux.HandleFunc("/blank", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `<!doctype html><body style="margin:0;background:#202020">`)
	})
	// A stream that never ends, like the event stream of the viewer.
	mux.HandleFunc("/stream", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	})
	mux.HandleFunc("/sse", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `<!doctype html><body style="margin:0;background:linear-gradient(90deg,#123,#fa0)"><h1 style="color:#fff">stream</h1><script>new EventSource("/stream")</script>`)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	dir := t.TempDir()
	// The helper processes of Chrome can still be writing to the profile when
	// the test ends, so it is not in the directory that the test removes: it is
	// removed again until Chrome has let go of it.
	profile, err := os.MkdirTemp("", "serveshots-profile-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		for i := 0; i < 20; i++ {
			if os.RemoveAll(profile) == nil {
				return
			}
			time.Sleep(250 * time.Millisecond)
		}
	})
	opts := shotOptions{Min: 500 * time.Millisecond, Max: 15 * time.Second}
	shoot := func(path string) ([]string, error) {
		return screenshot(context.Background(), chrome, profile, filepath.Join(dir, strings.Trim(path, "/")+".png"), srv.URL+path, 320, 200, opts)
	}

	notes, err := shoot("/ok")
	if err != nil {
		t.Fatal(err)
	}
	if len(notes) != 1 || !strings.Contains(notes[0], "fine") {
		t.Errorf("notes = %q", notes)
	}
	if _, err := checkImage(filepath.Join(dir, "ok.png"), 320*scale, 200*scale); err != nil {
		t.Error(err)
	}

	// The page changes after 700 ms; the shot waits for it.
	if _, err := shoot("/late"); err != nil {
		t.Fatal(err)
	}
	if _, err := checkImage(filepath.Join(dir, "late.png"), 320*scale, 200*scale); err != nil {
		t.Errorf("a page that changes after it loads: %v", err)
	}

	// Text that belongs to the machine is replaced before the picture is taken.
	var seen string
	replaced := opts
	replaced.Replace = [][2]string{{"/work/a/b", "~/estate/platform"}, {"127.0.0.1:4242", "127.0.0.1:8700"}}
	replaced.Inspect = func(eval func(string) (string, error)) error {
		v, err := eval(`document.body.innerText + "|" + document.getElementById("t").title + "|" + document.getElementById("t").getAttribute("aria-label")`)
		seen = v
		return err
	}
	if _, err := screenshot(context.Background(), chrome, profile, filepath.Join(dir, "path.png"), srv.URL+"/path", 320, 200, replaced); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"project ~/estate/platform and ~/estate/platform/pravo.state", "127.0.0.1:8700", "|~/estate/platform|~/estate/platform"} {
		if !strings.Contains(seen, want) {
			t.Errorf("the page reads %q, which lacks %q", seen, want)
		}
	}
	if strings.Contains(seen, "/work/a/b") || strings.Contains(seen, "4242") {
		t.Errorf("the page still shows the original text: %q", seen)
	}

	// A page with an open stream still settles and is shot.
	if _, err := shoot("/sse"); err != nil {
		t.Fatalf("a page with an open event stream: %v", err)
	}

	for path, want := range map[string]string{
		"/throws": "boom from the page",
		"/logs":   "an error that was logged",
		"/fails":  "missing.js",
	} {
		if _, err := shoot(path); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: error = %v, want one that has %q", path, err, want)
		}
	}

	if _, err := shoot("/blank"); err != nil {
		t.Fatal(err)
	}
	if _, err := checkImage(filepath.Join(dir, "blank.png"), 320*scale, 200*scale); err == nil || !strings.Contains(err.Error(), "blank") {
		t.Errorf("a blank page: %v", err)
	}

	if _, err := os.Stat(filepath.Join(dir, "none.png")); err == nil {
		t.Fatal("unexpected file")
	}
}
