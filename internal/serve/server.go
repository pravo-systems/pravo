package serve

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"mime"
	"net"
	"net/http"
	"path"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/pravo-systems/pravo/internal/config"
	"github.com/pravo-systems/pravo/internal/engine"
	"github.com/pravo-systems/pravo/internal/env"
	"github.com/pravo-systems/pravo/internal/protect"
	"github.com/pravo-systems/pravo/internal/registry"
	"github.com/pravo-systems/pravo/internal/state"
)

//go:embed web/index.html web/assets
var webFS embed.FS

// contentSecurityPolicy is sent with every response: the page loads its own
// scripts, styles, fonts and data, and nothing else.
const contentSecurityPolicy = "default-src 'none'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; font-src 'self'; connect-src 'self'; base-uri 'none'; form-action 'none'; frame-ancestors 'none'"

// maxStreams bounds the open event streams. A browser holds one per tab; more
// than this are refused so that a client cannot take the descriptors of the
// process.
const maxStreams = 32

// eventWriteTimeout is how long one write to an event stream may take. A reader
// that has stopped reading is dropped.
var eventWriteTimeout = 10 * time.Second

func init() {
	// Fonts are served from the binary, whatever the host's type tables say.
	_ = mime.AddExtensionType(".woff2", "font/woff2")
	_ = mime.AddExtensionType(".woff", "font/woff")
	_ = mime.AddExtensionType(".svg", "image/svg+xml")
	_ = mime.AddExtensionType(".webmanifest", "application/manifest+json")
}

// Options configure a Server.
type Options struct {
	// Dir is the project directory, absolute.
	Dir string
	// Env is the environment the viewer reads: the variables of the specification's
	// pravo.#Env nodes, PRAVO_HOME for the schema cache, and what the CUE module
	// cache is found by. Empty unless set; the environment of the process is not read.
	Env env.Env
	// Tags and SysTags are the values the specification is loaded with (-t, -T).
	Tags    []string
	SysTags bool
	// PlanFile is a saved plan to show on top of the graph, "" for none.
	PlanFile string
	// PlanID is the id, or the beginning of the id, of a plan stored in the state
	// to show on top of the graph, "" for none. It excludes PlanFile. The page can
	// show any other stored plan in its place.
	PlanID string
	// Version is the version of the pravo binary.
	Version string
	// OpenState opens the state for reading. It must not create or change
	// anything. The default is OpenReadOnly, which reads a local directory.
	OpenState func(ctx context.Context, root *config.Root) (*state.Store, error)
	// Protection tells the Access view how the state is protected, from the store
	// and the readers file. The command fills it from the state package. Nil means
	// that the state is protected as its format says and no identity was given.
	Protection Protection
	// VerifyTxn checks the signatures of a transaction of a sealed state against the
	// readers file, and returns "verified", "key_not_in_file", "invalid" or "unsigned" with the
	// name the readers file gives the signer. Nil, or an empty status, means that the
	// signatures are not checked.
	VerifyTxn func(st *state.Store, t *state.Transaction) (status, signer string)
	// PolicyReaders gives the readers that the signatures of the policy versions are
	// checked against, and false when there is no readers file to check them against.
	// Nil means the same.
	PolicyReaders func(st *state.Store) ([]protect.Reader, bool)
	// SecretRules gives the rules that the signatures of the secret versions are judged by: the
	// readers with their roles, the keys that left and were vouched for, and whether the roles
	// are pinned in the state. The bool is false when there is no readers file to judge by, and
	// the string says why the roles could not be used when they fail their check. Nil means no
	// readers file.
	SecretRules func(st *state.Store) (rules state.RecordRules, ok bool, problem string)
	// VerifyPlan checks the signatures of a stored plan against the readers file, given the
	// digest of the plan file as it was read. The second result is false when there is
	// nothing to check: the state is not sealed. Nil means the same. The approvals of such
	// a plan are read against the readers that PolicyReaders gives.
	VerifyPlan func(ctx context.Context, st *state.Store, id, digest string) (*PlanSignatures, bool)
	// AccessDir is the directory that holds readers.txt. When it is empty the
	// viewer looks for .pravo/access in the project directory and its parents up
	// to the repository.
	AccessDir string
	// Schemas finds the schema of a provider at a version. The default reads
	// the cache under $PRAVO_HOME.
	Schemas SchemaFunc
	// Poll is how often the watcher looks at the project (default 1 s).
	Poll time.Duration
	// KeepAlive is how often /api/events sends a comment line (default 15 s).
	KeepAlive time.Duration
	// TraceDir is where the recorded runs are read from. The default is
	// .pravo/traces of the project directory.
	TraceDir string
	// Token is the secret of this run. A random one is made when it is empty.
	Token string
	// Chat turns the read-only chat on (README rule 12). Nil is off.
	Chat *ChatOptions
}

// staleModelFor is how long the last model is served when the project cannot be
// read: a state that is being written is read again a moment later.
const staleModelFor = 5 * time.Second

// Server is the read-only viewer. It holds no lock of the state and writes
// nothing: it reads the specification, the state and a plan file, builds a model
// of them for each revision, and serves it.
type Server struct {
	// planMeta keeps the summaries of the stored plans between the models.
	planMeta *engine.PlanMetaCache
	opts     Options
	token    string
	cookie   string
	schemas  *cacheStore
	watch    *watcher

	// buildMu orders the builds of models, and guards spec, plan and failedAt.
	buildMu     sync.Mutex
	spec        *specState
	plan        *planCache
	failedAt    time.Time
	failedSince time.Time

	mu  sync.RWMutex
	cur *model

	hostMu sync.RWMutex
	hosts  map[string]bool

	stop     chan struct{}
	stopOnce sync.Once

	// traceMu guards traceSums, the summaries of the recorded runs.
	traceMu   sync.Mutex
	traceSums traceCache

	// files caches the plan files of the project that the studio reads.
	files planFileCache

	// uploads holds the plan files that were dropped on the studio, in memory.
	uploads planUploads

	// streams counts the open event streams.
	streams atomic.Int32

	// chatActive counts the questions being answered.
	chatActive atomic.Int32
}

// New makes a server and builds its first model, so that a state that cannot be
// read is reported before anything listens.
func New(opts Options) (*Server, error) {
	if opts.Dir == "" {
		return nil, errors.New("serve: no project directory")
	}
	if opts.PlanFile != "" && opts.PlanID != "" {
		return nil, errors.New("serve: a plan file and a stored plan cannot both be shown")
	}
	if opts.OpenState == nil {
		opts.OpenState = OpenReadOnly
	}
	if opts.Poll <= 0 {
		opts.Poll = time.Second
	}
	if opts.KeepAlive <= 0 {
		opts.KeepAlive = 15 * time.Second
	}
	if opts.Token == "" {
		var b [16]byte
		if _, err := rand.Read(b[:]); err != nil {
			return nil, err
		}
		opts.Token = hex.EncodeToString(b[:])
	}
	s := &Server{opts: opts, planMeta: engine.NewPlanMetaCache(), token: opts.Token, schemas: newCacheStore(opts.Env), hosts: map[string]bool{}, stop: make(chan struct{})}
	s.watch = newWatcher(opts.Poll, opts.Dir, opts.PlanFile)
	s.watch.traces = s.traceDir()
	m, err := s.current()
	if err != nil {
		return nil, err
	}
	// A plan that is named and cannot be found is told at once, as a plan file that
	// cannot be read is: the page would show the state alone and a warning.
	if opts.PlanID != "" && m.plan != nil && m.plan.failed {
		return nil, m.plan.err
	}
	return s, nil
}

// Token returns the secret of this run.
func (s *Server) Token() string { return s.token }

// StateDescription names the remote state that the specification points at:
// "s3://bucket/prefix", with " via ENDPOINT" for an S3-compatible store, or the
// URL of an http state. It is ""
// for a local state or when the specification does not load. The command
// prints it, since the viewer reads whatever backend the specification names.
func (s *Server) StateDescription() string {
	m, err := s.current()
	if err != nil || m.root == nil || !m.root.State.Remote() {
		return ""
	}
	st := m.root.State
	if st.Backend == "http" {
		return st.URL
	}
	d := "s3://" + st.Bucket + "/" + st.Prefix
	if st.Endpoint != "" {
		d += " via " + st.Endpoint
	}
	return d
}

// Revision returns the revision the server holds.
func (s *Server) Revision() string { return s.watch.revision() }

// URL returns the address to open, with the token, for a listener.
func (s *Server) URL(addr net.Addr) string {
	host := addr.String()
	return "http://" + host + "/?token=" + s.token
}

// Listen opens a listener on the loopback interface. There is no way to ask for
// another address.
func Listen(port int) (net.Listener, error) {
	ln, err := net.Listen("tcp4", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
	if err == nil || port != 0 {
		return ln, err
	}
	if ln6, err6 := net.Listen("tcp6", "[::1]:0"); err6 == nil {
		return ln6, nil
	}
	return nil, err
}

// Serve serves on the listener until the context ends, then stops the watcher
// and the open event streams and shuts the server down. The listener must be on
// a loopback address.
func (s *Server) Serve(ctx context.Context, ln net.Listener) error {
	tcp, ok := ln.Addr().(*net.TCPAddr)
	if !ok || !tcp.IP.IsLoopback() {
		_ = ln.Close()
		return fmt.Errorf("serve: %s is not a loopback address", ln.Addr())
	}
	port := strconv.Itoa(tcp.Port)
	s.hostMu.Lock()
	s.hosts = map[string]bool{"127.0.0.1:" + port: true, "localhost:" + port: true, "[::1]:" + port: true}
	s.cookie = "pravo_serve_" + port
	s.hostMu.Unlock()

	wctx, cancel := context.WithCancel(ctx)
	defer cancel()
	go s.watch.run(wctx)

	srv := &http.Server{Handler: s.Handler(), ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 2 * time.Minute,
		ErrorLog: log.New(io.Discard, "", 0)}
	errc := make(chan error, 1)
	go func() { errc <- srv.Serve(ln) }()
	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
	}
	s.stopOnce.Do(func() { close(s.stop) })
	sctx, done := context.WithTimeout(context.Background(), 3*time.Second)
	defer done()
	if err := srv.Shutdown(sctx); err != nil {
		_ = srv.Close()
	}
	return nil
}

// Handler returns the handler of the server. It answers only requests whose
// Host is a loopback name with the port the server listens on, so it is
// meaningful once Serve has the listener.
func (s *Server) Handler() http.Handler { return http.HandlerFunc(s.serveHTTP) }

func (s *Server) hostOK(host string) bool {
	s.hostMu.RLock()
	defer s.hostMu.RUnlock()
	return s.hosts[strings.ToLower(host)]
}

// originOK reports whether an Origin header names this server.
func (s *Server) originOK(origin string) bool {
	return s.hostOK(strings.TrimPrefix(origin, "http://")) && strings.HasPrefix(origin, "http://")
}

func (s *Server) cookieName() string {
	s.hostMu.RLock()
	defer s.hostMu.RUnlock()
	return s.cookie
}

func (s *Server) serveHTTP(w http.ResponseWriter, r *http.Request) {
	defer func() {
		// A request that fails does not take the server with it.
		if v := recover(); v != nil {
			if err, _ := v.(error); !errors.Is(err, http.ErrAbortHandler) {
				writeError(w, http.StatusInternalServerError, "internal error")
			}
		}
	}()
	h := w.Header()
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Referrer-Policy", "no-referrer")
	h.Set("Cache-Control", "no-store")
	h.Set("Content-Security-Policy", contentSecurityPolicy)
	// No other origin may embed a response or keep a handle on the window.
	h.Set("Cross-Origin-Resource-Policy", "same-origin")
	h.Set("Cross-Origin-Opener-Policy", "same-origin")
	if !s.hostOK(r.Host) {
		writeError(w, http.StatusMisdirectedRequest, "misdirected request")
		return
	}
	// A request that names an origin names this one. A browser that sends no
	// Sec-Fetch-Site still sends Origin on a request of another origin, and on
	// every request that is not a plain GET of the page itself.
	if o := r.Header.Get("Origin"); o != "" && !s.originOK(o) {
		writeError(w, http.StatusForbidden, "forbidden")
		return
	}
	// A page of another site must not reach the server through the browser. A
	// page of another port of this host is "same-site"; it may navigate here,
	// and may not read the API.
	switch site := r.Header.Get("Sec-Fetch-Site"); site {
	case "", "same-origin", "none":
	case "same-site":
		if strings.HasPrefix(r.URL.Path, "/api/") {
			writeError(w, http.StatusForbidden, "forbidden")
			return
		}
	default:
		writeError(w, http.StatusForbidden, "forbidden")
		return
	}
	// The studio takes a plan file that a person drops on it: the one request that is not a GET. It judges and keeps nothing on disk.
	if r.Method == http.MethodPost && (r.URL.Path == "/api/studio/plan" || r.URL.Path == "/api/chat") {
		s.api(w, r)
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		h.Set("Allow", "GET, HEAD")
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if strings.HasPrefix(r.URL.Path, "/api/") {
		s.api(w, r)
		return
	}
	s.page(w, r)
}

func (s *Server) tokenOK(t string) bool {
	return subtle.ConstantTimeCompare([]byte(t), []byte(s.token)) == 1
}

func (s *Server) authorized(r *http.Request) bool {
	// Every cookie of the name counts: a page of the same site can set another
	// with the same name and a longer path, which a browser sends first.
	name := s.cookieName()
	for _, c := range r.Cookies() {
		if c.Name == name && s.tokenOK(c.Value) {
			return true
		}
	}
	if h := r.Header.Get("Authorization"); len(h) > 7 && strings.EqualFold(h[:7], "bearer ") {
		return s.tokenOK(strings.TrimSpace(h[7:]))
	}
	return false
}

// page serves the interface, and takes the token from the address that "pravo
// serve" prints: it sets the cookie and redirects to the address without it.
func (s *Server) page(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/" && r.URL.Query().Has("token") {
		q := r.URL.Query()
		if !s.tokenOK(q.Get("token")) {
			writeError(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		//nolint:gosec // the viewer listens on loopback over plain HTTP, where a Secure cookie would never be sent
		http.SetCookie(w, &http.Cookie{Name: s.cookieName(), Value: s.token, Path: "/", HttpOnly: true, SameSite: http.SameSiteStrictMode})
		q.Del("token")
		target := "/"
		if len(q) > 0 {
			target += "?" + q.Encode()
		}
		http.Redirect(w, r, target, http.StatusSeeOther)
		return
	}
	name := strings.TrimPrefix(path.Clean("/"+r.URL.Path), "/")
	if name == "" {
		name = "index.html"
	}
	fi, err := fs.Stat(webFS, path.Join("web", name))
	if err != nil || fi.IsDir() {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	f, err := webFS.Open(path.Join("web", name))
	if err != nil {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	defer f.Close()
	rs, ok := f.(io.ReadSeeker)
	if !ok {
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	http.ServeContent(w, r, name, time.Time{}, rs)
}

func (s *Server) api(w http.ResponseWriter, r *http.Request) {
	if !s.authorized(r) {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	switch r.URL.Path {
	case "/api/chat/config":
		s.chatConfig(w)
		return
	case "/api/chat":
		s.chatHandler(w, r)
		return
	case "/api/ping":
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "version": APIVersion})
		return
	case "/api/events":
		s.events(w, r)
		return
	case "/api/traces":
		s.traces(w, r)
		return
	case "/api/trace":
		s.trace(w, r)
		return
	case "/api/graph", "/api/detail", "/api/source", "/api/search", "/api/fields", "/api/plans", "/api/plan", "/api/access", "/api/discovery", "/api/adopt", "/api/policies", "/api/secrets", "/api/studio", "/api/studio/try", "/api/studio/doc", "/api/studio/plan":
	default:
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	m, err := s.current()
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "the project cannot be read: "+err.Error())
		return
	}
	switch r.URL.Path {
	case "/api/graph":
		writeRaw(w, http.StatusOK, m.graphJSON)
	case "/api/detail":
		if mn := s.node(w, r, m); mn != nil {
			if po, ok := s.planOf(w, r, m); ok {
				writeJSON(w, http.StatusOK, m.detailFor(mn, po))
			}
		}
	case "/api/source":
		if mn := s.node(w, r, m); mn != nil {
			if po, ok := s.planOf(w, r, m); ok {
				writeJSON(w, http.StatusOK, m.sourceFor(mn, po))
			}
		}
	case "/api/plans":
		writeJSON(w, http.StatusOK, m.planList())
	case "/api/access":
		writeRaw(w, http.StatusOK, m.accessJSON)
	case "/api/discovery":
		writeRaw(w, http.StatusOK, m.disc.json)
	case "/api/policies":
		writeRaw(w, http.StatusOK, m.polJSON)
	case "/api/secrets":
		writeRaw(w, http.StatusOK, m.secsJSON)
	case "/api/studio":
		writeJSON(w, http.StatusOK, s.studio(m))
	case "/api/studio/plan":
		s.uploadHandler(w, r, m)
	case "/api/studio/try":
		s.tryHandler(w, r, m)
	case "/api/studio/doc":
		s.docHandler(w, r, m)
	case "/api/adopt":
		s.adopt(w, r, m)
	case "/api/plan":
		id := r.URL.Query().Get("id")
		if po, ok := s.storedPlan(w, r, m, id); ok {
			writeJSON(w, http.StatusOK, m.planOverlayDoc(po))
		}
	case "/api/fields":
		q := r.URL.Query().Get("q")
		if len(q) > 512 {
			q = q[:512]
		}
		writeJSON(w, http.StatusOK, m.fieldMatches(q))
	case "/api/search":
		q := r.URL.Query().Get("q")
		if len(q) > 512 {
			q = q[:512]
		}
		writeJSON(w, http.StatusOK, m.search(q))
	}
}

// node finds the node a request names by its address. An address is a key: it is
// looked up in the model and is never used as a path.
func (s *Server) node(w http.ResponseWriter, r *http.Request, m *model) *mnode {
	addr := r.URL.Query().Get("addr")
	if addr == "" || len(addr) > 2048 {
		writeError(w, http.StatusBadRequest, "addr is required")
		return nil
	}
	mn := m.nodes[addr]
	if mn == nil {
		writeError(w, http.StatusNotFound, "no such node")
		return nil
	}
	return mn
}

// planOf finds the plan a request is about: the one that plan=ID names, no plan
// at all for plan=none, else the plan that is laid over the graph.
func (s *Server) planOf(w http.ResponseWriter, r *http.Request, m *model) (*planOverlay, bool) {
	if !r.URL.Query().Has("plan") {
		return m.plan, true
	}
	id := r.URL.Query().Get("plan")
	if id == "none" {
		return nil, true
	}
	return s.storedPlan(w, r, m, id)
}

// storedPlan finds the overlay of the stored plan with the id. The id is looked
// up among the plans that the model lists, and is never used as a path. An id
// that begins another is not taken for it: the page has the whole id from the
// listing.
func (s *Server) storedPlan(w http.ResponseWriter, r *http.Request, m *model, id string) (*planOverlay, bool) {
	if id == "" || len(id) > 80 || !engine.IsPlanID(id) {
		writeError(w, http.StatusBadRequest, "id is required: the id of a stored plan, such as plan-2f9c1a4e")
		return nil, false
	}
	po, err := m.overlayOf(r.Context(), id)
	switch {
	case errors.Is(err, errNoSuchPlan):
		writeError(w, http.StatusNotFound, "no such stored plan")
		return nil, false
	case err != nil:
		writeError(w, http.StatusServiceUnavailable, "the plan cannot be read: "+err.Error())
		return nil, false
	}
	return po, true
}

// events streams a "change" event whenever the revision changes. The first event
// carries the revision the server holds, so that a page that fetched the graph
// before it connected learns that it missed a change.
func (s *Server) events(w http.ResponseWriter, r *http.Request) {
	rc := http.NewResponseController(w)
	if s.streams.Add(1) > maxStreams {
		s.streams.Add(-1)
		w.Header().Set("Retry-After", "5")
		writeError(w, http.StatusServiceUnavailable, "too many event streams are open")
		return
	}
	defer s.streams.Add(-1)
	ch, cancel := s.watch.bus.subscribe()
	defer cancel()
	h := w.Header()
	h.Set("Content-Type", "text/event-stream; charset=utf-8")
	h.Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	if r.Method == http.MethodHead {
		return
	}
	// send writes under a deadline, so that a reader that has stopped reading
	// is dropped and does not keep the stream for ever.
	send := func(write func()) bool {
		_ = rc.SetWriteDeadline(time.Now().Add(eventWriteTimeout))
		write()
		return rc.Flush() == nil
	}
	if !send(func() {
		fmt.Fprint(w, ": connected\n\n")
		writeEvent(w, Event{Revision: s.watch.revision(), What: []string{}})
	}) {
		return
	}
	ka := time.NewTicker(s.opts.KeepAlive)
	defer ka.Stop()
	for {
		var ok bool
		select {
		case <-r.Context().Done():
			return
		case <-s.stop:
			return
		case ev := <-ch:
			ok = send(func() { writeEvent(w, ev) })
		case <-ka.C:
			ok = send(func() { fmt.Fprint(w, ": keepalive\n\n") })
		}
		if !ok {
			return
		}
	}
}

func writeEvent(w io.Writer, ev Event) {
	if ev.What == nil {
		ev.What = []string{}
	}
	data, _ := json.Marshal(ev)
	fmt.Fprintf(w, "event: change\ndata: %s\n\n", data)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	data, err := json.Marshal(v)
	if err != nil {
		writeRaw(w, http.StatusInternalServerError, []byte(`{"error":"internal error"}`))
		return
	}
	writeRaw(w, status, data)
}

func writeRaw(w http.ResponseWriter, status int, data []byte) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write(data)
}

// current returns the model of the revision the watcher holds, building it when
// the project changed since the last one. Requests that arrive while it is
// built wait for it.
func (s *Server) current() (*model, error) {
	s.mu.RLock()
	m := s.cur
	s.mu.RUnlock()
	if m != nil && m.rev == s.watch.revision() {
		return m, nil
	}
	s.buildMu.Lock()
	defer s.buildMu.Unlock()
	s.mu.RLock()
	m = s.cur
	s.mu.RUnlock()
	if m != nil && m.rev == s.watch.revision() {
		return m, nil
	}
	if m != nil && time.Since(s.failedAt) < time.Second {
		return m, nil
	}
	nm, err := s.build()
	if err != nil {
		s.failedAt = time.Now()
		if s.failedSince.IsZero() {
			s.failedSince = s.failedAt
		}
		if m != nil && time.Since(s.failedSince) < staleModelFor {
			return m, nil // the last good model, while a write that was in the way ends
		}
		return nil, err
	}
	s.failedSince = time.Time{}
	s.mu.Lock()
	old := s.cur
	s.cur = nm
	s.mu.Unlock()
	if old != nil && old.store != nil && old.store != nm.store {
		// The keys of the store that was replaced are wiped once the requests that
		// hold its model are done.
		time.AfterFunc(oldModelGrace, old.store.Close)
	}
	return nm, nil
}

// oldModelGrace is how long the store of a model that was replaced keeps its keys.
var oldModelGrace = time.Minute

// build reads the project and makes a model of it. The caller holds buildMu.
func (s *Server) build() (*model, error) {
	ctx := context.Background()
	sig := s.watch.specNow()
	var loadStart time.Time
	loaded := false
	if s.spec == nil || s.spec.sig != sig {
		loadStart = time.Now()
		s.spec = loadSpec(s.opts, sig)
		loaded = true
		s.watch.setSpecFiles(s.spec.files)
	}
	ss := s.spec
	ss.cueMu.Lock()
	defer ss.cueMu.Unlock()
	root := ss.stateRoot(s.opts)
	local := ""
	if !root.State.Remote() {
		local = root.StateFile()
	}
	s.watch.setState(local, nil)
	s.watch.setAccess(s.accessPath())
	rev, sigs := s.watch.sample()
	if loaded && sigs["spec"] != sig && !modifiedSince(ss.files, loadStart) {
		// The file list grew with the load; that is not a change of the files.
		ss.sig = sigs["spec"]
	}
	store, err := s.opts.OpenState(ctx, root)
	if err != nil {
		time.Sleep(100 * time.Millisecond) // a record that was being replaced
		if store, err = s.opts.OpenState(ctx, root); err != nil {
			return nil, err
		}
	}
	if local == "" {
		s.watch.setState("", store.Backend())
	}
	m := s.newModel(ctx, rev, ss, store, root)
	s.watch.setPlanExpiry(m.stored.nextExpiry())
	return m, nil
}

// stateRoot returns the root that says where the state is: the specification,
// else what could be read of its settings, else the default location.
func (ss *specState) stateRoot(opts Options) *config.Root {
	switch {
	case ss.root != nil:
		return ss.root
	case ss.settings != nil:
		return ss.settings
	}
	return &config.Root{Dir: opts.Dir, StatePath: config.DefaultStatePath}
}

// lookupSchema returns the cached schema of a provider version.
func (s *Server) lookupSchema(src registry.Source, version string) (Schemas, bool) {
	if s.opts.Schemas != nil {
		return s.opts.Schemas(src, version)
	}
	return s.schemas.lookup(src, version)
}
