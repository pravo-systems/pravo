package serve

import (
	"bufio"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hashicorp/terraform-plugin-go/tftypes"

	"github.com/pravo-systems/pravo/internal/engine"
	"github.com/pravo-systems/pravo/internal/state"
)

// These tests are exploit attempts. Each one does what an attacker would do and
// requires that it fails.

// A page of another origin cannot reach the API even by a browser that sends no
// Sec-Fetch-Site: the Origin it sends names it.
func TestAPIRefusesAForeignOrigin(t *testing.T) {
	p := newProject(t)
	for _, origin := range []string{"https://evil.example", "http://evil.example", "null", "http://localhost:9999", "http://127.0.0.1:1", p.base + ".evil.example", "http://127.0.0.1:" + fmt.Sprint(p.port) + "@evil.example"} {
		for _, path := range []string{"/api/ping", "/api/graph", "/api/events", "/api/detail?addr=shared"} {
			resp, body := p.do("GET", path, map[string]string{"Origin": origin})
			if resp.StatusCode != http.StatusForbidden {
				t.Errorf("Origin %q on %s: %d, want 403 (%.80s)", origin, path, resp.StatusCode, body)
			}
		}
	}
	// The interface itself sends the Origin of the page on a same-origin request
	// that is not a GET, and on none that is: both are fine.
	for _, origin := range []string{"", p.base, "http://localhost:" + fmt.Sprint(p.port), "http://[::1]:" + fmt.Sprint(p.port)} {
		hdr := map[string]string{}
		if origin != "" {
			hdr["Origin"] = origin
		}
		if resp, _ := p.do("GET", "/api/ping", hdr); resp.StatusCode != 200 {
			t.Errorf("Origin %q: %d, want 200", origin, resp.StatusCode)
		}
	}
}

// Every response, error responses included, carries the headers that stop a
// page of another origin from embedding or reading it.
func TestResponsesAreNotEmbeddable(t *testing.T) {
	p := newProject(t)
	for _, path := range []string{"/", "/index.html", "/assets/api.js", "/nothing", "/api/ping", "/api/graph", "/api/missing", "/?token=" + p.srv.Token(), "/?token=wrong"} {
		resp, _ := p.do("GET", path, nil)
		if got := resp.Header.Get("Cross-Origin-Resource-Policy"); got != "same-origin" {
			t.Errorf("%s: Cross-Origin-Resource-Policy is %q", path, got)
		}
		if got := resp.Header.Get("Cross-Origin-Opener-Policy"); got != "same-origin" {
			t.Errorf("%s: Cross-Origin-Opener-Policy is %q", path, got)
		}
		if got := resp.Header.Get("X-Content-Type-Options"); got != "nosniff" {
			t.Errorf("%s: X-Content-Type-Options is %q", path, got)
		}
		if got := resp.Header.Get("Cache-Control"); got != "no-store" {
			t.Errorf("%s: Cache-Control is %q", path, got)
		}
	}
	// Even a request the host check refuses.
	req, _ := http.NewRequest("GET", p.base+"/api/ping", nil)
	req.Host = "evil.example"
	resp, _ := p.send(req)
	if resp.Header.Get("Cross-Origin-Resource-Policy") != "same-origin" || resp.Header.Get("Content-Security-Policy") == "" {
		t.Errorf("a refused request lacks the headers: %v", resp.Header)
	}
}

// A page of the same site can set a cookie with the name of the real one but
// another path; the browser then sends both, the bad one first. The good one
// still opens the API.
func TestCookieTossingDoesNotLockTheOwnerOut(t *testing.T) {
	p := newProject(t)
	name := fmt.Sprintf("pravo_serve_%d", p.port)
	for _, cookie := range []string{
		name + "=bad; " + name + "=" + p.srv.Token(),
		name + "=; " + name + "=" + p.srv.Token(),
		"other=1; " + name + "=bad; " + name + "=" + p.srv.Token() + "; z=2",
	} {
		resp, _ := p.send(newReq(t, p.base+"/api/ping", map[string]string{"Cookie": cookie}))
		if resp.StatusCode != 200 {
			t.Errorf("Cookie %q: %d, want 200", cookie, resp.StatusCode)
		}
	}
	// And neither of two wrong ones does.
	resp, _ := p.send(newReq(t, p.base+"/api/ping", map[string]string{"Cookie": name + "=bad; " + name + "=worse"}))
	if resp.StatusCode != 401 {
		t.Errorf("two wrong cookies: %d, want 401", resp.StatusCode)
	}
	// A cookie of the name of another port's server is not this server's.
	resp, _ = p.send(newReq(t, p.base+"/api/ping", map[string]string{"Cookie": "pravo_serve_1=" + p.srv.Token()}))
	if resp.StatusCode != 401 {
		t.Errorf("the token under another port's cookie name: %d, want 401", resp.StatusCode)
	}
}

func newReq(t *testing.T, url string, hdr map[string]string) *http.Request {
	t.Helper()
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		t.Fatal(err)
	}
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	return req
}

// The token is in the error bodies of none of the responses and in the headers
// of none that is not the cookie.
func TestTokenIsNotEchoed(t *testing.T) {
	p := newProject(t)
	tok := p.srv.Token()
	for _, path := range []string{"/?token=" + tok + "x", "/?token=" + tok + "&token=" + tok, "/api/ping?token=" + tok, "/api/graph?token=" + tok, "/nothing?token=" + tok, "/?token=" + tok + "&x=" + tok} {
		resp, body := p.send(newReq(t, p.base+path, nil))
		if strings.Contains(string(body), tok+"x") {
			t.Errorf("%s: the body echoes the token: %s", path, body)
		}
		for k, v := range resp.Header {
			if k == "Set-Cookie" {
				continue
			}
			if strings.Contains(strings.Join(v, ","), tok) && !(k == "Location" && strings.Contains(path, "x="+tok)) {
				t.Errorf("%s: header %s carries the token: %v", path, k, v)
			}
		}
	}
	// The redirect: only the path "/", and the other parameters encoded.
	for _, q := range []string{"next=//evil.example", "next=https://evil.example/", "a=%0d%0aSet-Cookie:%20x=1", "a=%00", "%2f%2fevil.example=1", "a=b%26token%3D" + tok} {
		resp, body := p.send(newReq(t, p.base+"/?token="+tok+"&"+q, nil))
		loc := resp.Header.Get("Location")
		if resp.StatusCode != http.StatusSeeOther || !strings.HasPrefix(loc, "/?") || strings.HasPrefix(loc, "//") || strings.ContainsAny(loc, "\r\n") {
			t.Errorf("%s: %d Location %q", q, resp.StatusCode, loc)
		}
		if strings.Contains(loc, "token="+tok) || strings.Contains(string(body), "token="+tok) {
			t.Errorf("%s: the redirect still carries the token: %q", q, loc)
		}
		if len(resp.Header["Set-Cookie"]) != 1 {
			t.Errorf("%s: %d Set-Cookie headers", q, len(resp.Header["Set-Cookie"]))
		}
	}
	// The token is accepted from the address "/" only.
	resp, _ := p.send(newReq(t, p.base+"/assets/api.js?token="+tok, nil))
	if len(resp.Cookies()) != 0 {
		t.Errorf("a cookie is set from a path other than /")
	}
}

// Raw requests that a client library would not make.
func TestRawRequestForms(t *testing.T) {
	p := newProject(t)
	tok := p.srv.Token()
	raw := func(req string) string {
		c, err := net.DialTimeout("tcp", p.host, 2*time.Second)
		if err != nil {
			t.Fatal(err)
		}
		defer c.Close()
		_ = c.SetDeadline(time.Now().Add(5 * time.Second))
		fmt.Fprint(c, req)
		line, _ := bufio.NewReader(c).ReadString('\n')
		return strings.TrimSpace(line)
	}
	auth := "Authorization: Bearer " + tok + "\r\n"
	good := "Host: " + p.host + "\r\n"
	for name, c := range map[string]struct{ req, want string }{
		"http/1.0 without a host":      {"GET /api/ping HTTP/1.0\r\n" + auth + "\r\n", "421"},
		"two hosts, bad second":        {"GET /api/ping HTTP/1.1\r\n" + good + "Host: evil.example\r\n" + auth + "\r\n", "400"},
		"two hosts, bad first":         {"GET /api/ping HTTP/1.1\r\nHost: evil.example\r\n" + good + auth + "\r\n", "400"},
		"absolute form, evil host":     {"GET http://evil.example/api/ping HTTP/1.1\r\n" + good + auth + "\r\n", "421"},
		"userinfo in the host":         {"GET /api/ping HTTP/1.1\r\nHost: evil.example@" + p.host + "\r\n" + auth + "\r\n", "400"},
		"host with a trailing dot":     {"GET /api/ping HTTP/1.1\r\nHost: localhost.:" + fmt.Sprint(p.port) + "\r\n" + auth + "\r\n", "421"},
		"host with a space":            {"GET /api/ping HTTP/1.1\r\nHost: " + p.host + " evil\r\n" + auth + "\r\n", "400"},
		"host with a tab suffix":       {"GET /api/ping HTTP/1.1\r\nHost: " + p.host + "\t\r\n" + auth + "\r\n", "200"},
		"decimal address":              {"GET /api/ping HTTP/1.1\r\nHost: 2130706433:" + fmt.Sprint(p.port) + "\r\n" + auth + "\r\n", "421"},
		"mapped ipv6":                  {"GET /api/ping HTTP/1.1\r\nHost: [::ffff:127.0.0.1]:" + fmt.Sprint(p.port) + "\r\n" + auth + "\r\n", "421"},
		"long ipv6":                    {"GET /api/ping HTTP/1.1\r\nHost: [0:0:0:0:0:0:0:1]:" + fmt.Sprint(p.port) + "\r\n" + auth + "\r\n", "421"},
		"port zero":                    {"GET /api/ping HTTP/1.1\r\nHost: 127.0.0.1:0\r\n" + auth + "\r\n", "421"},
		"leading zeros in the port":    {"GET /api/ping HTTP/1.1\r\nHost: 127.0.0.1:0" + fmt.Sprint(p.port) + "\r\n" + auth + "\r\n", "421"},
		"authorization twice, bad one": {"GET /api/ping HTTP/1.1\r\n" + good + "Authorization: Bearer nope\r\n" + auth + "\r\n", "401"},
		"chunked body on a GET":        {"GET /api/ping HTTP/1.1\r\n" + good + auth + "Transfer-Encoding: chunked\r\n\r\n0\r\n\r\n", "200"},
	} {
		got := raw(c.req)
		if !strings.Contains(got, " "+c.want) {
			t.Errorf("%s: %q, want %s", name, got, c.want)
		}
	}
}

// HEAD on the event stream answers and ends; it does not hold the connection.
func TestHeadOnEventsEnds(t *testing.T) {
	p := newProject(t)
	done := make(chan struct{})
	go func() {
		defer close(done)
		resp, _ := p.do("HEAD", "/api/events", nil)
		if resp.StatusCode != 200 {
			t.Errorf("HEAD /api/events: %d", resp.StatusCode)
		}
	}()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("HEAD /api/events holds the connection open")
	}
}

// Event streams are bounded: a client that opens many does not take the file
// descriptors of the process.
func TestEventStreamsAreBounded(t *testing.T) {
	p := newProject(t)
	var conns []net.Conn
	defer func() {
		for _, c := range conns {
			c.Close()
		}
	}()
	var refused int
	for i := 0; i < maxStreams+20; i++ {
		c, err := net.DialTimeout("tcp", p.host, 2*time.Second)
		if err != nil {
			t.Fatal(err)
		}
		conns = append(conns, c)
		fmt.Fprintf(c, "GET /api/events HTTP/1.1\r\nHost: %s\r\nAuthorization: Bearer %s\r\n\r\n", p.host, p.srv.Token())
		_ = c.SetReadDeadline(time.Now().Add(2 * time.Second))
		line, err := bufio.NewReader(c).ReadString('\n')
		if err != nil {
			t.Fatalf("stream %d: %v", i, err)
		}
		if strings.Contains(line, " 503") {
			refused++
		}
	}
	if refused < 20 {
		t.Errorf("%d of %d streams were refused, want at least 20", refused, maxStreams+20)
	}
	// The API still answers.
	if resp, _ := p.do("GET", "/api/ping", nil); resp.StatusCode != 200 {
		t.Errorf("ping while the streams are open: %d", resp.StatusCode)
	}
}

// A reader that never reads does not keep a goroutine for ever: each write has
// a deadline.
func TestStalledEventReaderIsDropped(t *testing.T) {
	old := eventWriteTimeout
	eventWriteTimeout = 300 * time.Millisecond
	defer func() { eventWriteTimeout = old }()
	p := newProject(t)
	c, err := net.Dial("tcp", p.host)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if tc, ok := c.(*net.TCPConn); ok {
		_ = tc.SetReadBuffer(1) // as small as the system lets it be
	}
	fmt.Fprintf(c, "GET /api/events HTTP/1.1\r\nHost: %s\r\nAuthorization: Bearer %s\r\n\r\n", p.host, p.srv.Token())
	// Never read. The server's keep-alive comments fill the buffers in time, or
	// they do not on a system with a large buffer; either way the server must
	// stay answerable and shut down.
	time.Sleep(500 * time.Millisecond)
	if resp, _ := p.do("GET", "/api/ping", nil); resp.StatusCode != 200 {
		t.Errorf("ping: %d", resp.StatusCode)
	}
}

// A hostile project: names, values and messages that are markup. The server
// sends them as data; nothing in a response is HTML that a browser would run,
// and a script close tag never reaches the page unescaped in JSON.
func TestHostileStrings(t *testing.T) {
	const evil = `</script><img src=x onerror=alert(1)>"'` + "`" + `& `
	dir := t.TempDir()
	writeSpec(t, dir, specCUE+`
"`+jsonEscape(evil)+`": $fake.#Resource.fake_db & {name: "`+jsonEscape(evil)+`", zones: ["`+jsonEscape(evil)+`"]}
`)
	st, err := state.Open(filepath.Join(dir, "pravo.state"))
	if err != nil {
		t.Fatal(err)
	}
	seed(t, st)
	attrs, _ := json.Marshal(map[string]any{"name": evil, "tags": map[string]string{evil: evil}, "zones": []string{evil}, evil: evil})
	r := record("services.a.file", "fake_file", string(attrs))
	r.Name, r.Type = evil, evil
	if err := st.Set(r, ""); err != nil {
		t.Fatal(err)
	}
	txn := st.Begin("apply", "pravo apply "+evil)
	txn.Record("services.a.file", "update", 1, 2)
	_ = txn.Finish("applied", evil, true)
	p := start(t, dir, st, Options{})
	for _, path := range []string{"/api/graph", "/api/detail?addr=services.a.file", "/api/source?addr=services.a.file", "/api/search?q=" + queryEscape(evil), "/api/detail?addr=" + queryEscape(evil), "/api/detail?addr=" + queryEscape(`"`+evil+`"`)} {
		resp, body := p.do("GET", path, nil)
		if strings.Contains(string(body), "</script>") || strings.Contains(string(body), "<img") {
			t.Errorf("%s sends markup unescaped: %.200s", path, body)
		}
		if !strings.HasPrefix(resp.Header.Get("Content-Type"), "application/json") {
			t.Errorf("%s: content type %q", path, resp.Header.Get("Content-Type"))
		}
		var any interface{}
		if err := json.Unmarshal(body, &any); err != nil {
			t.Errorf("%s: not JSON: %v", path, err)
		}
	}
}

func jsonEscape(s string) string {
	b, _ := json.Marshal(s)
	return strings.Trim(string(b), `"`)
}

// Many requests at once, with the project changing under them: nothing races
// and nothing panics. Run with -race.
func TestConcurrentRequestsWhileTheProjectChanges(t *testing.T) {
	p := newProject(t)
	var wg sync.WaitGroup
	stop := make(chan struct{})
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			paths := []string{"/api/graph", "/api/detail?addr=services.a.db", "/api/source?addr=services.a.db", "/api/search?q=db", "/api/detail?addr=shared", "/api/source?addr=hosts.x"}
			for n := 0; ; n++ {
				select {
				case <-stop:
					return
				default:
				}
				resp, _ := p.do("GET", paths[(i+n)%len(paths)], nil)
				if resp.StatusCode >= 500 {
					t.Errorf("%s: %d", paths[(i+n)%len(paths)], resp.StatusCode)
					return
				}
			}
		}(i)
	}
	for i := 0; i < 15; i++ {
		r := record("shared", "fake_db", fmt.Sprintf(`{"name":"shared","port":%d}`, 5000+i))
		_ = p.store.Set(r, "")
		os.WriteFile(filepath.Join(p.dir, "extra.cue"), []byte(fmt.Sprintf("package demo\n\nextra%d: 1\n", i)), 0o644)
		time.Sleep(60 * time.Millisecond)
	}
	close(stop)
	wg.Wait()
}

// The command line of a run is recorded whole, and it can hold a secret: a tag
// given with -t is how one reaches a specification.
func TestHistoryAndLocksAreScrubbed(t *testing.T) {
	p := newProject(t)
	const tagSecret = "tag-pass-zzzzzzzz" // not a value of the state, and its tag does not say secret
	const keySecret = "key-secret-yyyyyyyy"
	p.store.SetActor("agent "+secretBlob, "because "+secretBlob, "")
	txn := p.store.Begin("apply", "pravo apply -t region=eu -t dbpw="+tagSecret+" --tag db_password="+keySecret+" -tpw="+tagSecret+" --tag=x="+tagSecret+
		" --password "+keySecret+" --token="+keySecret+" -T --note "+secretBlob+" --reason 'rotate "+secretBlob+"'")
	txn.Record("shared", "update", 1, 2)
	if err := txn.Finish("applied", "1 changed", true); err != nil {
		t.Fatal(err)
	}
	if _, err := p.store.ReserveAll([]string{"shared"}, "pravo plan -t apikey="+tagSecret+" "+secretBlob, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the transaction and the reservation", func() bool { d := p.detail("shared"); return len(d.History) > 1 && d.Lock != nil })
	body := p.get("/api/detail?addr=shared")
	for _, s := range []string{tagSecret, keySecret, secretBlob} {
		if strings.Contains(string(body), s) {
			t.Errorf("the detail sends %q: %s", s, body)
		}
	}
	d := p.detail("shared")
	var cmd string
	for _, h := range d.History {
		if strings.Contains(h.Command, "region") {
			cmd = h.Command
		}
	}
	for _, want := range []string{"pravo apply", "-t region=" + mask, "-T", "--password " + mask, "--token=" + mask, "--tag db_password=" + mask} {
		if !strings.Contains(cmd, want) {
			t.Errorf("the command %q lost %q", cmd, want)
		}
	}
	if d.Lock == nil || !strings.HasPrefix(d.Lock.Command, "pravo plan -t apikey="+mask) {
		t.Errorf("the lock: %+v", d.Lock)
	}
}

func TestScrubCommand(t *testing.T) {
	s := newSecretSet()
	s.add("hunter2hunter2")
	for in, want := range map[string]string{
		"pravo apply":            "pravo apply",
		"pravo apply  -t  a=b":   "pravo apply  -t  a=" + mask,
		"pravo apply -t a":       "pravo apply -t a",
		"pravo apply -t a=":      "pravo apply -t a=",
		"pravo apply -t 'a=b c'": "pravo apply -t a=" + mask + " " + mask,
		"pravo apply -t k=correct horse battery --auto": "pravo apply -t k=" + mask + " " + mask + " " + mask + " --auto",
		"pravo apply -t k=v -t j=w plan.json":           "pravo apply -t k=" + mask + " -t j=" + mask + " " + mask,
		"pravo apply --password two words --yes":        "pravo apply --password " + mask + " " + mask + " --yes",
		"pravo apply --tag=a=b":                         "pravo apply --tag=a=" + mask,
		"pravo apply -ta=b":                             "pravo apply -ta=" + mask,
		"pravo apply --password x":                      "pravo apply --password " + mask,
		"pravo apply --api_key=x":                       "pravo apply --api_key=" + mask,
		"pravo apply -replace db.main":                  "pravo apply -replace db.main",
		"pravo import db.main hunter2hunter2":           "pravo import db.main " + mask,
		"pravo import db.main id-hunter2hunter2-suffix": "pravo import db.main id-" + mask + "-suffix",
		"pravo apply -t\ta=b\n":                         "pravo apply -t\ta=" + mask + "\n",
		"":                                              "",
		"pravo apply --tag":                             "pravo apply --tag",
		// A short flag clustered with others that take no argument, which pflag accepts.
		"pravo -v plan --store -vt pw=hunter2value": "pravo -v plan --store -vt pw=" + mask,
		"pravo plan -Tt owner=SECRETV1":             "pravo plan -Tt owner=" + mask,
		"pravo plan -vtowner=SECRETV1":              "pravo plan -vtowner=" + mask,
		"pravo plan -Ttowner=SECRETV1 -v":           "pravo plan -Ttowner=" + mask + " -v",
		"pravo plan -vvt k=a b --yes":               "pravo plan -vvt k=" + mask + " " + mask + " --yes",
		"pravo plan -vt 'owner=SECRETV1' -vt j=w":   "pravo plan -vt owner=" + mask + " -vt j=" + mask,
		"pravo plan -t=k=SECRETV1 -v":               "pravo plan -t=" + mask + " -v",
		"pravo plan -out plan.pravo":                "pravo plan -out plan.pravo",
		"pravo plan -vtaa -T":                       "pravo plan -vtaa -T",
	} {
		if got := scrubCommand(in, s); got != want {
			t.Errorf("scrubCommand(%q) = %q, want %q", in, got, want)
		}
	}
}

// A line of the source that is not the literal of a sensitive attribute can
// still hold a value of the state that is sensitive: the header of a
// comprehension, a comment.
func TestSourceLinesWithholdStateSecrets(t *testing.T) {
	dir := t.TempDir()
	writeSpec(t, dir, specCUE+`
// The password is `+secretBlob+`, see the wiki.
vault: {
	for pw in ["`+secretBlob+`"] { // `+secretBlob+`
		v: $fake.#Resource.fake_db & {name: "v", blob: pw}
	}
}
`)
	st, err := state.Open(filepath.Join(dir, "pravo.state"))
	if err != nil {
		t.Fatal(err)
	}
	seed(t, st)
	p := start(t, dir, st, Options{})
	body := p.get("/api/graph")
	var g Graph
	_ = json.Unmarshal(body, &g)
	var addr string
	for _, n := range g.Nodes {
		if strings.HasPrefix(n.Addr, "vault.") {
			addr = n.Addr
			break
		}
	}
	if addr == "" {
		t.Fatalf("no vault node in the graph: %s", body)
	}
	noSecrets(t, "source", p.get("/api/source?addr="+queryEscape(addr)), secretBlob)
	s := p.source(addr)
	var marked bool
	for _, b := range s.Blocks {
		for _, l := range b.Lines {
			if l.Mark == "sensitive" {
				marked = true
			}
		}
	}
	if !marked {
		t.Errorf("no line is marked: %+v", s.Blocks)
	}
}

// A sensitive value that only the plan holds is a secret like those of the
// state: another attribute of the plan that embeds it is withheld.
func TestPlanSecretsAreDerived(t *testing.T) {
	p := newProject(t)
	m, err := p.srv.current()
	if err != nil {
		t.Fatal(err)
	}
	sch := fakeSchemas().Type("managed", "fake_db")
	obj := sch.Block.Type()
	val := func(set map[string]any) tftypes.Value {
		attrs := map[string]tftypes.Value{}
		for name, typ := range obj.AttributeTypes {
			if v, ok := set[name]; ok {
				attrs[name] = tftypes.NewValue(typ, v)
			} else {
				attrs[name] = tftypes.NewValue(typ, nil)
			}
		}
		return tftypes.NewValue(obj, attrs)
	}
	const fresh = "brand-new-plan-secret-42"
	before := val(map[string]any{"name": "n", "blob": "old-blob-value-1"})
	after := val(map[string]any{"name": "n", "blob": fresh, "zones": []tftypes.Value{tftypes.NewValue(tftypes.String, "z-"+fresh)}})
	other := &engine.Change{Addr: "services.a.file", Mode: "managed", Type: "fake_db", Action: engine.ActionUpdate, Schema: sch, Prior: before, Planned: after}
	m.plan = &planOverlay{entries: map[string]*planEntry{"services.a.file": {action: "update"}}, changes: map[string]*engine.Change{"services.a.file": other}}
	if got := m.changeOf(m.nodes["services.a.file"]); got == nil || !containsSecret(got, fresh) {
		// Without the collection, the derived rule does not know the new secret.
		t.Logf("before the collection the embed is not seen: %v", got != nil)
	}
	m.collectPlanSecrets()
	ch := m.changeOf(m.nodes["services.a.file"])
	if ch == nil {
		t.Fatal("no change")
	}
	data, _ := json.Marshal(ch)
	if strings.Contains(string(data), fresh) {
		t.Errorf("the change sends the new secret: %s", data)
	}
	var seen bool
	for _, a := range ch.Attrs {
		if a.Path == "zones[0]" || a.Path == "zones" {
			seen = true
			if !a.AfterRedacted {
				t.Errorf("%+v is not redacted", a)
			}
		}
	}
	if !seen {
		t.Errorf("zones is not in the change: %s", data)
	}
}

func containsSecret(c *Change, s string) bool {
	data, _ := json.Marshal(c)
	return strings.Contains(string(data), s)
}

// A number can be a secret too: a PIN.
func TestSensitiveNumbersAreDerived(t *testing.T) {
	s := newSecretSet()
	s.add(json.Number("482913"))
	s.add(json.Number("42"))
	if !s.leaks("pin=482913") {
		t.Error("a sensitive number of six digits is not tracked")
	}
	if s.leaks("42") {
		t.Error("a number of two digits is tracked")
	}
}

// A symlink in the project that points at a file that is not CUE: the error that
// the page shows names no content of it beyond what a parser says of its first
// token, and the file is never served.
func TestSymlinkToASecretFile(t *testing.T) {
	dir := t.TempDir()
	writeSpec(t, dir, specCUE)
	outside := filepath.Join(t.TempDir(), "credentials")
	const body = "[default]\naws_access_key_id = AKIAIOSFODNN7EXAMPLE\naws_secret_access_key = wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY\n"
	if err := os.WriteFile(outside, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(dir, "creds.cue")); err != nil {
		t.Skip("no symlinks here: ", err)
	}
	st, err := state.Open(filepath.Join(dir, "pravo.state"))
	if err != nil {
		t.Fatal(err)
	}
	seed(t, st)
	p := start(t, dir, st, Options{})
	g := p.get("/api/graph")
	for _, secret := range []string{"AKIAIOSFODNN7EXAMPLE", "wJalrXUtnFEMI", "aws_secret_access_key"} {
		if strings.Contains(string(g), secret) {
			t.Errorf("the graph shows %q of a file that is not CUE: %.300s", secret, g)
		}
	}
}

// A file that was loaded and is then replaced by a link to another file is not
// served as the file that was loaded.
func TestRetargetedSymlinkIsNotServed(t *testing.T) {
	// The watcher must not reload the specification while the link is swapped: a
	// slow machine would otherwise load the new file as the specification.
	dir := t.TempDir()
	writeSpec(t, dir, specCUE)
	st, err := state.Open(filepath.Join(dir, "pravo.state"))
	if err != nil {
		t.Fatal(err)
	}
	seed(t, st)
	p := start(t, dir, st, Options{Poll: time.Hour})
	if _, err := p.srv.current(); err != nil {
		t.Fatal(err)
	}
	abs := filepath.Join(p.dir, "main.cue")
	orig, _ := os.ReadFile(abs)
	other := filepath.Join(t.TempDir(), "other.cue")
	// The same layout, so that the field that the page looks for is in it.
	marker := strings.ReplaceAll(string(orig), `{name: "shared"`, `{name: "PLANTED_MARKER_LINE secret-from-another-file"`)
	if marker == string(orig) {
		t.Fatal("the marker was not planted")
	}
	if err := os.WriteFile(other, []byte(marker), 0o600); err != nil {
		t.Fatal(err)
	}
	// Swap while the server has the specification loaded; read at once, before
	// the watcher has noticed.
	if err := os.Remove(abs); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(other, abs); err != nil {
		t.Skip("no symlinks here: ", err)
	}
	defer func() { os.Remove(abs); os.WriteFile(abs, orig, 0o644) }()
	for i := 0; i < 20; i++ {
		resp, body := p.do("GET", "/api/source?addr=shared", nil)
		if resp.StatusCode == 200 && strings.Contains(string(body), "PLANTED_MARKER_LINE") {
			t.Fatalf("a file that replaced a loaded one is served: %s", body)
		}
		if strings.Contains(string(body), "secret-from-another-file") {
			t.Fatalf("content of the replacing file: %s", body)
		}
	}
}

// A record with a very large attribute does not take the viewer down: the
// graph and the detail answer in bounded time and the answer is bounded.
func TestHugeAttribute(t *testing.T) {
	size := 24 << 20
	if n := os.Getenv("PRAVO_SERVE_HUGE"); n != "" {
		size = atoiOr0(n) << 20
	}
	dir := t.TempDir()
	writeSpec(t, dir, specCUE)
	st, err := state.Open(filepath.Join(dir, "pravo.state"))
	if err != nil {
		t.Fatal(err)
	}
	seed(t, st)
	big := strings.Repeat("A", size)
	attrs, _ := json.Marshal(map[string]any{"path": "/tmp/big", "content": big, "unknown": big})
	if err := st.Set(record("services.a.file", "fake_file", string(attrs)), ""); err != nil {
		t.Fatal(err)
	}
	began := time.Now()
	p := start(t, dir, st, Options{})
	if g := p.get("/api/graph"); len(g) > 1<<20 {
		t.Errorf("the graph of a state with a huge attribute is %d bytes", len(g))
	}
	d := p.get("/api/detail?addr=services.a.file")
	if len(d) > 1<<20 {
		t.Errorf("the detail of a node with a huge attribute is %d bytes", len(d))
	}
	// Eight at once.
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); p.get("/api/detail?addr=services.a.file") }()
	}
	wg.Wait()
	if took := time.Since(began); took > 30*time.Second {
		t.Errorf("a state with an attribute of %d MiB takes %v to answer", size>>20, took)
	}
}

// Deep nesting in a record: the viewer neither overflows its stack nor sends the depth.
func TestDeeplyNestedAttribute(t *testing.T) {
	dir := t.TempDir()
	writeSpec(t, dir, specCUE)
	st, err := state.Open(filepath.Join(dir, "pravo.state"))
	if err != nil {
		t.Fatal(err)
	}
	seed(t, st)
	for _, depth := range []int{50, 9990} {
		nest := strings.Repeat(`{"k":`, depth) + `"deep-` + secretBlob + `"` + strings.Repeat("}", depth)
		arr := strings.Repeat("[", depth) + `"x"` + strings.Repeat("]", depth)
		raw := `{"path":"/tmp/n","content":"c","tags":` + nest + `,"zones":` + arr + `,"legacy":` + nest + `}`
		if err := st.Set(record("services.a.file", "fake_file", raw), ""); err != nil {
			t.Logf("depth %d: the store refuses it: %v", depth, err)
			continue
		}
		p := start(t, dir, st, Options{})
		began := time.Now()
		body := p.get("/api/detail?addr=services.a.file")
		p.get("/api/graph")
		p.get("/api/source?addr=services.a.file")
		noSecrets(t, fmt.Sprintf("detail at depth %d", depth), body, secretBlob)
		if time.Since(began) > 5*time.Second {
			t.Errorf("depth %d took %v", depth, time.Since(began))
		}
		if len(body) > 1<<20 {
			t.Errorf("depth %d: %d bytes", depth, len(body))
		}
		p.stop()
	}
}

// A secret embedded in another attribute is found in the forms a specification
// puts it in: percent-encoded in a connection string, escaped in a JSON
// document, base64 in user data (at any alignment), hex.
func TestSecretsAreFoundInTheirEncodedForms(t *testing.T) {
	for _, secret := range []string{"hunter2hunter2", "p@ss/w0rd#1?x=y&z", `quo"te\back<angle>`, "short6", "uni-código-ключ-密码-123", strings.Repeat("k", 40)} {
		s := newSecretSet()
		s.add(secret)
		forms := map[string]string{
			"plain":        "x=" + secret + "!",
			"query":        "postgres://app:" + url.QueryEscape(secret) + "@db/app",
			"path":         "https://h/" + url.PathEscape(secret) + "/x",
			"json":         `{"password":"` + jsonInner(secret, true) + `","a":1}`,
			"json plain":   `{"password":"` + jsonInner(secret, false) + `","a":1}`,
			"hex":          "key=" + hex.EncodeToString([]byte(secret)),
			"upper hex":    "key=" + strings.ToUpper(hex.EncodeToString([]byte(secret))),
			"in a list":    strings.Join([]string{"a", secret, "b"}, ","),
			"at the end":   "pw:" + secret,
			"at the start": secret + " is the password",
		}
		for lead := 0; lead < 7; lead++ {
			for trail := 0; trail < 7; trail++ {
				text := strings.Repeat("L", lead) + secret + strings.Repeat("T", trail)
				forms[fmt.Sprintf("base64 %d/%d", lead, trail)] = base64.StdEncoding.EncodeToString([]byte(text))
				forms[fmt.Sprintf("urlsafe %d/%d", lead, trail)] = base64.URLEncoding.EncodeToString([]byte(text))
				forms[fmt.Sprintf("raw base64 %d/%d", lead, trail)] = base64.RawStdEncoding.EncodeToString([]byte(text))
				forms[fmt.Sprintf("wrapped %d/%d", lead, trail)] = "#!/bin/sh\necho " + base64.StdEncoding.EncodeToString([]byte("user=root\n"+text+"\nend")) + "\n"
			}
		}
		for name, text := range forms {
			if !s.leaks(text) {
				t.Errorf("%q is not found in its %s form: %q", secret, name, text)
			}
		}
		// Text that holds none of it is not withheld.
		for _, benign := range []string{"hello world", "region=eu-west-1", base64.StdEncoding.EncodeToString([]byte("a perfectly ordinary sentence about nothing")), "postgres://app@db/app", "1234567890"} {
			if s.leaks(benign) {
				t.Errorf("%q hides %q", secret, benign)
			}
		}
	}
	// The forms are not tracked for a short secret: those are too short to say anything.
	s := newSecretSet()
	s.add("abc")
	if len(s.list) != 0 {
		t.Errorf("a short secret was kept: %v", s.list)
	}
	// And a long one is bounded.
	s = newSecretSet()
	s.add(strings.Repeat("k", 60000))
	if len(s.list) > 6 {
		t.Errorf("%d forms of a 60 KB secret", len(s.list))
	}
}

// A specification that does not load says why, and the reason can quote a value
// that was given with -t.
func TestSpecErrorDoesNotQuoteSecretTags(t *testing.T) {
	const tagValue = "zz-not-digits-SECRETVALUE"
	dir := t.TempDir()
	writeSpec(t, dir, specCUE+`
db_password: =~"^[0-9]+$" @tag(db_password)
`)
	st, err := state.Open(filepath.Join(dir, "pravo.state"))
	if err != nil {
		t.Fatal(err)
	}
	seed(t, st)
	p := start(t, dir, st, Options{Tags: []string{"db_password=" + tagValue}})
	g := p.graph()
	if g.Project.SpecError == "" {
		t.Skip("the specification loaded: the error that quotes the tag cannot be made here")
	}
	body := p.get("/api/graph")
	noSecrets(t, "graph", body, tagValue, secretBlob)
	if !strings.Contains(g.Project.SpecError, mask) {
		t.Logf("the error does not quote the tag at all: %.300s", g.Project.SpecError)
	}
}

// A record whose shape is not the schema's (written by another version of the
// provider, or by hand) is not a way around the schema: a value that the schema
// marks sensitive somewhere below an attribute is withheld wherever it shows.
func TestShapeMismatchDoesNotUnmaskSensitiveValues(t *testing.T) {
	dir := t.TempDir()
	writeSpec(t, dir, specCUE)
	st, err := state.Open(filepath.Join(dir, "pravo.state"))
	if err != nil {
		t.Fatal(err)
	}
	seed(t, st)
	const a, b, c, d, e = "mismatch-pin-1111", "mismatch-key-2222", "mismatch-pin-3333", "mismatch-key-4444", "mismatch-pin-5555"
	// rule is a list of blocks with a sensitive pin; settings is an object with a sensitive key.
	attrs := `{"name":"x",` +
		`"rule":{"cidr":"10.0.0.0/8","pin":"` + a + `"},` + // a block given as a map
		`"settings":[{"region":"eu","key":"` + b + `"}],` + // an object given as a list
		`"tags":{"outer":{"pin":"` + c + `"}},` + // a map of strings given objects
		`"zones":[{"key":"` + d + `"}],` + // a list of strings given objects
		`"api_token_x":"` + e + `"}`
	if err := st.Set(record("services.a.db", "fake_db", attrs), ""); err != nil {
		t.Fatal(err)
	}
	p := start(t, dir, st, Options{})
	body := p.get("/api/detail?addr=services.a.db")
	noSecrets(t, "detail", body, a, b, c, d, e)
	noSecrets(t, "graph", p.get("/api/graph"), a, b, c, d, e)
}

// The value of a sensitive output is a secret: an attribute that holds it is
// withheld although its schema marks nothing, and so is an output that holds it.
func TestSensitiveOutputsAreSecrets(t *testing.T) {
	const outSecret = "only-in-the-output-9f8e7d6c"
	dir := t.TempDir()
	writeSpec(t, dir, specCUE)
	st, err := state.Open(filepath.Join(dir, "pravo.state"))
	if err != nil {
		t.Fatal(err)
	}
	seed(t, st)
	if err := st.Set(record("shared", "fake_db", `{"name":"shared","port":5432,"tags":{"url":"https://h/?k=`+outSecret+`"}}`), ""); err != nil {
		t.Fatal(err)
	}
	outs := map[string]json.RawMessage{
		"a_plain": json.RawMessage(`"x ` + outSecret + ` y"`), // sorts before the sensitive one
		"secret":  json.RawMessage(`{"$sensitive":"` + outSecret + `"}`),
		"safe":    json.RawMessage(`"fine"`),
	}
	if err := st.SetOutputs(outs); err != nil {
		t.Fatal(err)
	}
	p := start(t, dir, st, Options{})
	noSecrets(t, "graph", p.get("/api/graph"), outSecret)
	noSecrets(t, "detail", p.get("/api/detail?addr=shared"), outSecret)
	g := p.graph()
	for _, o := range g.Outputs {
		switch o.Name {
		case "a_plain":
			if o.Redacted != "derived" {
				t.Errorf("a_plain: %+v", o)
			}
		case "safe":
			if o.Value != "fine" {
				t.Errorf("safe: %+v", o)
			}
		}
	}
}

func TestJSONDepth(t *testing.T) {
	for in, want := range map[string]int{
		``:                         0,
		`{}`:                       1,
		`{"a":[1,{"b":[]}]}`:       4,
		`{"a":"[[[[{{{{"}`:         1,
		`{"a":"\"[[[[","b":[[1]]}`: 3,
		`{"a":"\\","b":[[[1]]]}`:   4,
		`"[[[["`:                   0,
		`[[[[[[[[[[1]]]]]]]]]]`:    10,
	} {
		if got := jsonDepth([]byte(in)); got != want {
			t.Errorf("jsonDepth(%s) = %d, want %d", in, got, want)
		}
	}
	deep := strings.Repeat("[", 100000) + strings.Repeat("]", 100000)
	if _, err := decodeAttrs(json.RawMessage(`{"a":` + deep + `}`)); err == nil {
		t.Error("a record nested 100000 levels deep was decoded")
	}
}
