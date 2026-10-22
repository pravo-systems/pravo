package serve

import (
	"bufio"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestAuth(t *testing.T) {
	p := newProject(t)
	token := p.srv.Token()
	if len(token) != 32 {
		t.Fatalf("the token has %d characters, want 32 (128 bits in hex)", len(token))
	}
	get := func(path string, hdr map[string]string) (*http.Response, string) {
		req, _ := http.NewRequest("GET", p.base+path, nil)
		for k, v := range hdr {
			req.Header.Set(k, v)
		}
		resp, body := p.send(req)
		return resp, string(body)
	}
	for _, api := range []string{"/api/ping", "/api/graph", "/api/detail?addr=shared", "/api/source?addr=shared", "/api/search?q=a", "/api/events", "/api/nothing"} {
		resp, body := get(api, nil)
		if resp.StatusCode != 401 {
			t.Errorf("%s without a token: %d, want 401", api, resp.StatusCode)
		}
		if strings.Contains(body, "shared") || strings.Contains(body, "services") || strings.Contains(body, p.dir) {
			t.Errorf("%s: the 401 names something: %s", api, body)
		}
		resp, _ = get(api, map[string]string{"Authorization": "Bearer " + token[:31] + "x"})
		if resp.StatusCode != 401 {
			t.Errorf("%s with a wrong token: %d, want 401", api, resp.StatusCode)
		}
		resp, _ = get(api, map[string]string{"Authorization": "Basic " + token})
		if resp.StatusCode != 401 {
			t.Errorf("%s with the token as Basic: %d, want 401", api, resp.StatusCode)
		}
	}
	// A token in the query of an API call is not accepted: it would end up in logs and history.
	if resp, _ := get("/api/ping?token="+token, nil); resp.StatusCode != 401 {
		t.Errorf("a token in the query of /api/ping: %d, want 401", resp.StatusCode)
	}
	if resp, body := get("/api/ping", map[string]string{"Authorization": "bearer " + token}); resp.StatusCode != 200 || !strings.Contains(body, `"ok":true`) {
		t.Errorf("bearer: %d %s", resp.StatusCode, body)
	}

	// The cookie flow: the address that is printed sets the cookie and redirects without the token.
	resp, _ := get("/?token="+token+"&view=tree", nil)
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("/?token: %d, want a redirect", resp.StatusCode)
	}
	if loc := resp.Header.Get("Location"); loc != "/?view=tree" {
		t.Errorf("the redirect goes to %q, want /?view=tree (without the token)", loc)
	}
	if resp, _ := get("/?token="+token, nil); resp.Header.Get("Location") != "/" {
		t.Errorf("the redirect goes to %q, want /", resp.Header.Get("Location"))
	}
	var cookie *http.Cookie
	for _, c := range resp.Cookies() {
		if strings.HasPrefix(c.Name, "pravo_serve") {
			cookie = c
		}
	}
	if cookie == nil {
		t.Fatal("the redirect sets no pravo_serve cookie")
	}
	if !cookie.HttpOnly || cookie.SameSite != http.SameSiteStrictMode || cookie.Path != "/" || cookie.Value != token {
		t.Errorf("the cookie is %+v, want HttpOnly, SameSite=Strict, Path=/ and the token", cookie)
	}
	if resp, body := get("/api/ping", map[string]string{"Cookie": cookie.Name + "=" + cookie.Value}); resp.StatusCode != 200 {
		t.Errorf("the cookie does not open the API: %d %s", resp.StatusCode, body)
	}
	if resp, _ := get("/api/ping", map[string]string{"Cookie": cookie.Name + "=" + cookie.Value[:31] + "z"}); resp.StatusCode != 401 {
		t.Errorf("a wrong cookie: %d, want 401", resp.StatusCode)
	}
	if resp, _ := get("/?token=wrong", nil); resp.StatusCode != 401 || len(resp.Cookies()) != 0 {
		t.Errorf("a wrong token on /: %d with %d cookies, want 401 and none", resp.StatusCode, len(resp.Cookies()))
	}
	// The interface itself carries no data, so it needs no token.
	if resp, body := get("/", nil); resp.StatusCode != 200 || !strings.Contains(body, "<") {
		t.Errorf("/: %d", resp.StatusCode)
	}
}

func TestHostAndFetchSite(t *testing.T) {
	p := newProject(t)
	port := p.port
	cases := []struct {
		host string
		want int
	}{
		{p.host, 200},
		{"localhost:" + strconv.Itoa(port), 200},
		{"[::1]:" + strconv.Itoa(port), 200},
		{"LOCALHOST:" + strconv.Itoa(port), 200},
		{"evil.example:" + strconv.Itoa(port), 421},
		{"127.0.0.1", 421},
		{"127.0.0.1:" + strconv.Itoa(port+1), 421},
		{"localhost:" + strconv.Itoa(port) + ".evil.example", 421},
		{"0.0.0.0:" + strconv.Itoa(port), 421},
	}
	for _, c := range cases {
		for _, path := range []string{"/api/ping", "/"} {
			req, _ := http.NewRequest("GET", p.base+path, nil)
			req.Host = c.host
			req.Header.Set("Authorization", "Bearer "+p.srv.Token())
			resp, _ := p.send(req)
			if resp.StatusCode != c.want {
				t.Errorf("Host %q %s: %d, want %d", c.host, path, resp.StatusCode, c.want)
			}
		}
	}
	for site, want := range map[string]int{"same-origin": 200, "none": 200, "cross-site": 403, "same-site": 403, "": 200} {
		resp, _ := p.do("GET", "/api/ping", map[string]string{"Sec-Fetch-Site": site})
		if resp.StatusCode != want {
			t.Errorf("Sec-Fetch-Site %q on the API: %d, want %d", site, resp.StatusCode, want)
		}
	}
	// A page of another port of this host may navigate here, and cross-site may not.
	if resp, _ := p.do("GET", "/", map[string]string{"Sec-Fetch-Site": "same-site"}); resp.StatusCode != 200 {
		t.Errorf("a same-site navigation: %d, want 200", resp.StatusCode)
	}
	if resp, _ := p.do("GET", "/", map[string]string{"Sec-Fetch-Site": "cross-site"}); resp.StatusCode != 403 {
		t.Errorf("a cross-site navigation: %d, want 403", resp.StatusCode)
	}
}

func TestNoCORSAndMethods(t *testing.T) {
	p := newProject(t)
	for _, path := range []string{"/api/ping", "/api/graph", "/", "/missing", "/api/missing"} {
		resp, _ := p.do("GET", path, map[string]string{"Origin": "https://evil.example"})
		for k := range resp.Header {
			if strings.HasPrefix(strings.ToLower(k), "access-control-") {
				t.Errorf("GET %s sends %s", path, k)
			}
		}
	}
	for _, method := range []string{"OPTIONS", "POST", "PUT", "DELETE", "PATCH"} {
		resp, _ := p.do(method, "/api/graph", map[string]string{"Origin": "https://evil.example", "Access-Control-Request-Method": "GET"})
		if resp.StatusCode != http.StatusForbidden {
			t.Errorf("%s from a foreign origin: %d, want 403", method, resp.StatusCode)
		}
		if resp, _ := p.do(method, "/api/graph", nil); resp.StatusCode != http.StatusMethodNotAllowed {
			t.Errorf("%s: %d, want 405", method, resp.StatusCode)
		}
		for k := range resp.Header {
			if strings.HasPrefix(strings.ToLower(k), "access-control-") {
				t.Errorf("%s sends %s", method, k)
			}
		}
	}
}

func TestHeaders(t *testing.T) {
	p := newProject(t)
	for _, path := range []string{"/api/graph", "/api/ping", "/", "/index.html", "/nothing"} {
		resp, _ := p.do("GET", path, nil)
		h := resp.Header
		if got := h.Get("Content-Security-Policy"); got != contentSecurityPolicy {
			t.Errorf("%s: CSP is %q", path, got)
		}
		for k, want := range map[string]string{"X-Content-Type-Options": "nosniff", "Referrer-Policy": "no-referrer", "Cache-Control": "no-store"} {
			if got := h.Get(k); got != want {
				t.Errorf("%s: %s is %q, want %q", path, k, got, want)
			}
		}
	}
	for _, want := range []string{"default-src 'none'", "script-src 'self'", "style-src 'self' 'unsafe-inline'", "img-src 'self' data:", "font-src 'self'", "connect-src 'self'", "base-uri 'none'", "form-action 'none'", "frame-ancestors 'none'"} {
		if !strings.Contains(contentSecurityPolicy, want) {
			t.Errorf("the CSP lacks %q", want)
		}
	}
	resp, _ := p.do("GET", "/api/graph", nil)
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("graph content type %q", ct)
	}
	// Fonts of the interface are served with a font type.
	if resp, _ := p.do("GET", "/assets/fonts/plex-sans-400.woff2", nil); resp.StatusCode == 200 && resp.Header.Get("Content-Type") != "font/woff2" {
		t.Errorf("a font is served as %q", resp.Header.Get("Content-Type"))
	}
	// A directory of the interface is not listed.
	if resp, body := p.do("GET", "/assets/", nil); resp.StatusCode != 404 {
		t.Errorf("/assets/ lists a directory: %d %s", resp.StatusCode, body)
	}
}

func TestLoopbackOnly(t *testing.T) {
	ln, err := Listen(0)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	if ip := ln.Addr().(*net.TCPAddr).IP; !ip.IsLoopback() {
		t.Errorf("Listen binds %s, not a loopback address", ip)
	}
	p := newProject(t)
	wide, err := net.Listen("tcp", "0.0.0.0:0")
	if err != nil {
		t.Skip("cannot listen on all interfaces here: ", err)
	}
	err = p.srv.Serve(context.Background(), wide)
	if err == nil || !strings.Contains(err.Error(), "loopback") {
		t.Errorf("Serve on %s returned %v, want a refusal", wide.Addr(), err)
	}
	if _, err := net.DialTimeout("tcp", wide.Addr().String(), 200*time.Millisecond); err == nil {
		t.Errorf("the listener on %s was left open", wide.Addr())
	}
}

// Events: the first one carries the revision; a change of the state sends
// another, and the graph then has the revision of the event.
func TestEventsOnStateChange(t *testing.T) {
	p := newProject(t)
	req, _ := http.NewRequest("GET", p.base+"/api/events", nil)
	req.Header.Set("Authorization", "Bearer "+p.srv.Token())
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/event-stream") {
		t.Fatalf("content type %q", ct)
	}
	lines := make(chan string, 100)
	go func() {
		sc := bufio.NewScanner(resp.Body)
		for sc.Scan() {
			lines <- sc.Text()
		}
		close(lines)
	}()
	next := func(prefix string, within time.Duration) string {
		t.Helper()
		deadline := time.After(within)
		for {
			select {
			case l, ok := <-lines:
				if !ok {
					t.Fatalf("the stream ended while waiting for %q", prefix)
				}
				if strings.HasPrefix(l, prefix) {
					return l
				}
			case <-deadline:
				t.Fatalf("no line %q within %s", prefix, within)
			}
		}
	}
	readEvent := func() Event {
		t.Helper()
		next("event: change", 10*time.Second)
		var ev Event
		if err := json.Unmarshal([]byte(strings.TrimPrefix(next("data: ", time.Second), "data: ")), &ev); err != nil {
			t.Fatal(err)
		}
		return ev
	}
	first := readEvent()
	g := p.graph()
	if first.Revision != g.Revision {
		t.Errorf("the first event has revision %s, the graph %s", first.Revision, g.Revision)
	}
	next(": keepalive", 5*time.Second)

	r := record("hosts.x", "fake_db", `{"name":"x","port":1}`)
	if err := p.store.Set(r, ""); err != nil {
		t.Fatal(err)
	}
	ev := readEvent()
	if ev.Revision == first.Revision {
		t.Errorf("the revision did not change")
	}
	if !contains(ev.What, "state") {
		t.Errorf("the event says %v, want state", ev.What)
	}
	g2 := p.graph()
	if g2.Revision != ev.Revision {
		t.Errorf("after the event the graph has revision %s, the event %s", g2.Revision, ev.Revision)
	}
	// One write is one event: a temporary file that the write makes is not a change.
	select {
	case l := <-lines:
		if strings.HasPrefix(l, "event: change") {
			t.Errorf("a second event for one write")
		}
	case <-time.After(300 * time.Millisecond):
	}
	for _, n := range g2.Nodes {
		if n.Addr == "hosts.x" && n.Presence != "both" {
			t.Errorf("hosts.x is %s after it was recorded, want both", n.Presence)
		}
	}

	// A change of the specification.
	writeSpec(t, p.dir, specCUE+"\nextra: $fake.#Resource.fake_db & {name: \"extra\"}\n")
	for {
		ev = readEvent()
		if contains(ev.What, "spec") {
			break
		}
	}
	found := false
	for _, n := range p.graph().Nodes {
		found = found || n.Addr == "extra"
	}
	if !found {
		t.Errorf("the node added to the specification is not in the graph")
	}
	// A lock.
	if err := p.store.Lock("shared", "pravo apply", "txn-x"); err != nil {
		t.Fatal(err)
	}
	for {
		ev = readEvent()
		if contains(ev.What, "locks") {
			break
		}
	}
	for _, n := range p.graph().Nodes {
		if n.Addr == "shared" && !n.Locked {
			t.Errorf("shared is not marked as locked")
		}
	}
	if d := p.detail("shared"); d.Lock == nil || d.Lock.Command != "pravo apply" || d.Lock.Kind != "lock" {
		t.Errorf("the lock of shared is %+v", d.Lock)
	}
}

func contains(l []string, s string) bool {
	for _, x := range l {
		if x == s {
			return true
		}
	}
	return false
}

// Serve returns when its context ends, although an event stream is open.
func TestShutdownEndsEventStreams(t *testing.T) {
	p := newProject(t)
	req, _ := http.NewRequest("GET", p.base+"/api/events", nil)
	req.Header.Set("Authorization", "Bearer "+p.srv.Token())
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	p.stop()
	select {
	case err := <-p.done:
		p.done <- nil // for the cleanup
		if err != nil {
			t.Errorf("Serve returned %v", err)
		}
	case <-time.After(8 * time.Second):
		t.Fatal("Serve did not return with an event stream open")
	}
	buf := make([]byte, 4096)
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := resp.Body.Read(buf); err != nil {
			return
		}
	}
	t.Error("the event stream was not closed")
}
