// Package netcap records the network calls that a provider makes, without a line of the provider's code changing. A provider
// is a child process, and pravo is the one that starts it: it gives the process a proxy to use (HTTPS_PROXY) and a certificate
// authority to trust (SSL_CERT_FILE and AWS_CA_BUNDLE), both of them its own and made for the run. The proxy opens each
// request, notes the method, the host, the path without its query, the status, the sizes and the time, sends it on, and
// every request is a span under the call of the provider that was running.
//
// Never recorded: headers, bodies, query strings. The authority lives in memory and in a file that only the provider
// process is told of, and both are gone when the provider is closed.
//
// What is not seen: a provider that ignores HTTPS_PROXY, traffic that is not HTTP, and, where a provider does not trust the
// authority (a Go program on macOS reads the system's roots and not SSL_CERT_FILE), the requests themselves. The first
// connection to such a host fails the handshake; the next ones to it are tunnelled and recorded as a connection, with the
// host, the bytes and the time.
package netcap

import (
	"bufio"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"github.com/pravo-systems/pravo/internal/env"
	"github.com/pravo-systems/pravo/internal/telemetry"
)

// Enabled reports whether provider calls are to be recorded: the runs are recorded, and PRAVO_TRACES_NET is not off.
func Enabled() bool { return telemetry.RecordingNet() }

// Capture is the proxy of one provider process.
type Capture struct {
	// Parent returns the context whose span the calls of the provider go under: the call that is running. It may be nil.
	Parent func() context.Context
	// Upstream sends the requests on. It is the default transport unless a test sets it.
	Upstream http.RoundTripper

	ln      net.Listener
	srv     *http.Server
	ca      *x509.Certificate
	caKey   *ecdsa.PrivateKey
	caFile  string
	mu      sync.Mutex
	leaves  map[string]*tls.Certificate
	blind   map[string]bool // hosts whose clients did not trust the authority
	environ env.Env         // the environment of the run: the proxy settings the provider would have had
	closed  atomic.Bool
	started time.Time
}

// New makes the authority and starts the proxy on a port of the loopback address.
// e is the environment of the run: its SSL_CERT_FILE and NO_PROXY are kept.
func New(e env.Env) (*Capture, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	tmpl := &x509.Certificate{
		SerialNumber:          serial(),
		Subject:               pkix.Name{CommonName: "pravo trace capture (this run only)"},
		NotBefore:             time.Now().Add(-time.Minute),
		NotAfter:              time.Now().Add(24 * time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return nil, err
	}
	ca, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, err
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	c := &Capture{environ: e, ca: ca, caKey: key, ln: ln, leaves: map[string]*tls.Certificate{}, blind: map[string]bool{}, Upstream: http.DefaultTransport, started: time.Now()}
	pemCA := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	f, err := os.CreateTemp("", "pravo-capture-*.pem")
	if err != nil {
		ln.Close()
		return nil, err
	}
	// what the provider trusts is the roots it would have trusted anyway, and the authority
	if _, err := f.Write(append(systemRoots(e), pemCA...)); err != nil {
		f.Close()
		os.Remove(f.Name())
		ln.Close()
		return nil, err
	}
	f.Close()
	c.caFile = f.Name()
	c.srv = &http.Server{Handler: http.HandlerFunc(c.serve), ReadHeaderTimeout: 30 * time.Second}
	go func() { _ = c.srv.Serve(ln) }() // ends with ErrServerClosed when the capture stops
	return c, nil
}

func serial() *big.Int {
	n, _ := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 120))
	return n
}

// systemRoots is the PEM of the roots that a provider would trust without the authority, when this machine keeps them in a
// file: SSL_CERT_FILE, or the usual places. Where it finds none the bundle is the authority alone.
func systemRoots(e env.Env) []byte {
	files := []string{e.Get("SSL_CERT_FILE"), "/etc/ssl/certs/ca-certificates.crt", "/etc/pki/tls/certs/ca-bundle.crt", "/etc/ssl/cert.pem", "/etc/ssl/ca-bundle.pem"}
	for _, f := range files {
		if f == "" {
			continue
		}
		if b, err := os.ReadFile(f); err == nil && len(b) > 0 {
			if b[len(b)-1] != '\n' {
				b = append(b, '\n')
			}
			return b
		}
	}
	return nil
}

// Env is what the provider process is given: the proxy for HTTP and HTTPS, and the bundle that holds the authority. The
// loopback addresses are not proxied, so that the plugin's own connection to pravo is untouched.
func (c *Capture) Env() []string {
	p := "http://" + c.ln.Addr().String()
	np := "127.0.0.1,localhost,::1"
	if v := c.environ.Get("NO_PROXY"); v != "" {
		np = v + "," + np
	}
	return []string{
		"HTTPS_PROXY=" + p, "HTTP_PROXY=" + p, "https_proxy=" + p, "http_proxy=" + p, "NO_PROXY=" + np, "no_proxy=" + np,
		"SSL_CERT_FILE=" + c.caFile, "AWS_CA_BUNDLE=" + c.caFile, "CURL_CA_BUNDLE=" + c.caFile, "REQUESTS_CA_BUNDLE=" + c.caFile,
	}
}

// Close stops the proxy and removes the file of the authority.
func (c *Capture) Close() {
	if c == nil || !c.closed.CompareAndSwap(false, true) {
		return
	}
	c.srv.Close()
	c.ln.Close()
	os.Remove(c.caFile)
}

func (c *Capture) ctx() context.Context {
	if c.Parent != nil {
		if ctx := c.Parent(); ctx != nil {
			return ctx
		}
	}
	return context.Background()
}

// serve takes what the provider sends to its proxy: a CONNECT for HTTPS, a request with an absolute address for HTTP.
func (c *Capture) serve(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodConnect {
		c.connect(w, r)
		return
	}
	if !r.URL.IsAbs() {
		http.Error(w, "this is a proxy", http.StatusBadRequest)
		return
	}
	c.forward(w, r, "http", r.URL.Host)
}

func (c *Capture) connect(w http.ResponseWriter, r *http.Request) {
	host := r.Host
	hj, ok := w.(http.Hijacker)
	if !ok {
		http.Error(w, "cannot hijack", http.StatusInternalServerError)
		return
	}
	c.mu.Lock()
	blind := c.blind[host]
	c.mu.Unlock()
	if blind {
		c.tunnel(w, hj, host)
		return
	}
	conn, _, err := hj.Hijack()
	if err != nil {
		return
	}
	defer conn.Close()
	if _, err := io.WriteString(conn, "HTTP/1.1 200 Connection Established\r\n\r\n"); err != nil {
		return
	}
	hostname, _, _ := net.SplitHostPort(host)
	if hostname == "" {
		hostname = host
	}
	tc := tls.Server(conn, &tls.Config{
		NextProtos: []string{"http/1.1"},
		GetCertificate: func(hi *tls.ClientHelloInfo) (*tls.Certificate, error) {
			name := hi.ServerName
			if name == "" {
				name = hostname
			}
			return c.leaf(name)
		},
	})
	if err := tc.Handshake(); err != nil {
		// a client that does not trust the authority: its next connection to the host is tunnelled
		c.mu.Lock()
		c.blind[host] = true
		c.mu.Unlock()
		return
	}
	// one connection, any number of requests, each a span
	one := &oneConn{conn: tc, done: make(chan struct{})}
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { c.forward(w, r, "https", host) }), ReadHeaderTimeout: 30 * time.Second}
	go func() { _ = srv.Serve(one) }()
	<-one.done
	srv.Close()
}

// oneConn is a listener that hands out one connection and then waits for it to close.
type oneConn struct {
	conn net.Conn
	once sync.Once
	done chan struct{}
}

func (o *oneConn) Accept() (net.Conn, error) {
	var got net.Conn
	o.once.Do(func() { got = &closeNotify{Conn: o.conn, done: o.done} })
	if got != nil {
		return got, nil
	}
	<-o.done
	return nil, errors.New("closed")
}
func (o *oneConn) Close() error   { return nil }
func (o *oneConn) Addr() net.Addr { return o.conn.LocalAddr() }

type closeNotify struct {
	net.Conn
	done chan struct{}
	once sync.Once
}

func (c *closeNotify) Close() error {
	c.once.Do(func() { close(c.done) })
	return c.Conn.Close()
}

// leaf makes, and keeps, a certificate for a host, signed by the authority of the run.
func (c *Capture) leaf(name string) (*tls.Certificate, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if l, ok := c.leaves[name]; ok {
		return l, nil
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	tmpl := &x509.Certificate{
		SerialNumber: serial(),
		Subject:      pkix.Name{CommonName: name},
		NotBefore:    time.Now().Add(-time.Minute),
		NotAfter:     time.Now().Add(12 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	if ip := net.ParseIP(name); ip != nil {
		tmpl.IPAddresses = []net.IP{ip}
	} else {
		tmpl.DNSNames = []string{name}
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, c.ca, &key.PublicKey, c.caKey)
	if err != nil {
		return nil, err
	}
	l := &tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}
	c.leaves[name] = l
	return l, nil
}

// forward sends one request on and writes the answer back, and the span says what happened.
func (c *Capture) forward(w http.ResponseWriter, r *http.Request, scheme, host string) {
	start := time.Now()
	path := r.URL.EscapedPath()
	if path == "" {
		path = "/"
	}
	hostname := host
	if h, _, err := net.SplitHostPort(host); err == nil {
		hostname = h
	}
	ctx, span := telemetry.Tracer().Start(c.ctx(), "network "+r.Method+" "+hostname, trace.WithSpanKind(trace.SpanKindClient), trace.WithTimestamp(start), trace.WithAttributes(
		attribute.String("http.request.method", r.Method),
		attribute.String("server.address", hostname),
		attribute.String("url.scheme", scheme),
		attribute.String("url.path", clip(path, 300)),
		attribute.Bool("pravo.net.decrypted", scheme == "https"),
	))
	out := r.Clone(ctx)
	out.RequestURI = ""
	out.URL = &url.URL{Scheme: scheme, Host: host, Path: r.URL.Path, RawPath: r.URL.RawPath, RawQuery: r.URL.RawQuery}
	out.Host = r.Host
	if out.Host == "" {
		out.Host = host
	}
	out.Header = r.Header.Clone()
	for _, h := range []string{"Proxy-Connection", "Proxy-Authorization"} {
		out.Header.Del(h)
	}
	req := &countBody{rc: r.Body}
	out.Body = req
	resp, err := c.Upstream.RoundTrip(out)
	if err != nil {
		span.SetAttributes(attribute.Int64("pravo.net.request_bytes", req.n.Load()))
		telemetry.End(span, err)
		http.Error(w, "pravo capture: "+err.Error(), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()
	for k, v := range resp.Header {
		w.Header()[k] = v
	}
	w.WriteHeader(resp.StatusCode)
	n, _ := io.Copy(w, resp.Body)
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
	span.SetAttributes(attribute.Int("http.response.status_code", resp.StatusCode), attribute.Int64("pravo.net.request_bytes", req.n.Load()), attribute.Int64("pravo.net.response_bytes", n))
	var serr error
	if resp.StatusCode >= 500 {
		serr = errors.New("HTTP " + strconv.Itoa(resp.StatusCode))
	}
	telemetry.End(span, serr)
}

type countBody struct {
	rc io.ReadCloser
	n  atomic.Int64
}

func (b *countBody) Read(p []byte) (int, error) {
	if b.rc == nil {
		return 0, io.EOF
	}
	n, err := b.rc.Read(p)
	b.n.Add(int64(n))
	return n, err
}
func (b *countBody) Close() error {
	if b.rc == nil {
		return nil
	}
	return b.rc.Close()
}

// tunnel joins the provider to the host without opening what passes: a connection is a span with the bytes both ways.
func (c *Capture) tunnel(w http.ResponseWriter, hj http.Hijacker, host string) {
	start := time.Now()
	hostname := host
	if h, _, err := net.SplitHostPort(host); err == nil {
		hostname = h
	}
	_, span := telemetry.Tracer().Start(c.ctx(), "network tunnel "+hostname, trace.WithSpanKind(trace.SpanKindClient), trace.WithTimestamp(start), trace.WithAttributes(
		attribute.String("server.address", hostname), attribute.Bool("pravo.net.decrypted", false)))
	up, err := net.DialTimeout("tcp", host, 15*time.Second)
	if err != nil {
		telemetry.End(span, err)
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	conn, brw, err := hj.Hijack()
	if err != nil {
		up.Close()
		telemetry.End(span, err)
		return
	}
	_, _ = io.WriteString(conn, "HTTP/1.1 200 Connection Established\r\n\r\n")
	var sent, got int64
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); sent = copyThen(up, bufio.NewReader(io.MultiReader(brw.Reader, conn))) }()
	go func() { defer wg.Done(); got = copyThen(conn, up) }()
	wg.Wait()
	conn.Close()
	up.Close()
	span.SetAttributes(attribute.Int64("pravo.net.request_bytes", sent), attribute.Int64("pravo.net.response_bytes", got))
	telemetry.End(span, nil)
}

func copyThen(dst net.Conn, src io.Reader) int64 {
	n, _ := io.Copy(dst, src)
	if cw, ok := dst.(interface{ CloseWrite() error }); ok {
		_ = cw.CloseWrite()
	}
	return n
}

func clip(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}
