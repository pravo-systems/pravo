package netcap

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

func attrs(kvs []attribute.KeyValue) map[string]attribute.Value {
	m := map[string]attribute.Value{}
	for _, kv := range kvs {
		m[string(kv.Key)] = kv.Value
	}
	return m
}

func setup(t *testing.T) (*Capture, *tracetest.SpanRecorder, *httptest.Server, context.Context) {
	t.Helper()
	rec := tracetest.NewSpanRecorder()
	prev := otel.GetTracerProvider()
	otel.SetTracerProvider(sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(rec)))
	t.Cleanup(func() { otel.SetTracerProvider(prev) })

	up := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body)
		if r.URL.Path == "/boom" {
			http.Error(w, "no", http.StatusInternalServerError)
			return
		}
		io.WriteString(w, "hello")
	}))
	t.Cleanup(up.Close)
	c, err := New()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(c.Close)
	c.Upstream = up.Client().Transport
	ctx, root := otel.Tracer("test").Start(context.Background(), "provider ReadResource")
	t.Cleanup(func() { root.End() })
	c.Parent = func() context.Context { return ctx }
	return c, rec, up, ctx
}

func client(c *Capture, trust bool) *http.Client {
	proxy, _ := url.Parse("http://" + c.ln.Addr().String())
	pool := x509.NewCertPool()
	if trust {
		b, _ := os.ReadFile(c.caFile)
		pool.AppendCertsFromPEM(b)
	}
	return &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(proxy), TLSClientConfig: &tls.Config{RootCAs: pool}}}
}

// A request through the proxy is a span under the running call, with the method, host, path and status, and without the
// query, the headers and the body.
func TestRequestsAreSpans(t *testing.T) {
	t.Setenv("PRAVO_TRACES", "on")
	t.Setenv("OTEL_SDK_DISABLED", "")
	c, rec, up, _ := setup(t)
	cl := client(c, true)
	resp, err := cl.Post(up.URL+"/things/1?token=secret", "text/plain", strings.NewReader("body-secret"))
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if string(b) != "hello" {
		t.Fatalf("body %q", b)
	}
	if r, err := cl.Get(up.URL + "/boom"); err == nil {
		r.Body.Close()
	}
	var found, failed bool
	for _, sp := range rec.Ended() {
		if !strings.HasPrefix(sp.Name(), "network ") {
			continue
		}
		a := attrs(sp.Attributes())
		if !sp.Parent().IsValid() {
			t.Errorf("%q has no parent", sp.Name())
		}
		for k, v := range a {
			if strings.Contains(v.Emit(), "secret") {
				t.Errorf("%s holds %q", k, v.Emit())
			}
		}
		switch a["url.path"].AsString() {
		case "/things/1":
			found = true
			if a["http.request.method"].AsString() != "POST" || a["http.response.status_code"].AsInt64() != 200 || !a["pravo.net.decrypted"].AsBool() {
				t.Errorf("attributes %v", a)
			}
			if a["pravo.net.request_bytes"].AsInt64() != int64(len("body-secret")) || a["pravo.net.response_bytes"].AsInt64() != 5 {
				t.Errorf("sizes %v", a)
			}
		case "/boom":
			failed = sp.Status().Code != 0
		}
	}
	if !found || !failed {
		t.Errorf("found=%v failed=%v in %d spans", found, failed, len(rec.Ended()))
	}
}

// A client that does not trust the authority fails the handshake once; the next connection to the host is tunnelled and
// recorded as a connection.
func TestUntrustingClientIsTunnelled(t *testing.T) {
	t.Setenv("PRAVO_TRACES", "on")
	t.Setenv("OTEL_SDK_DISABLED", "")
	c, rec, up, _ := setup(t)
	// the client trusts the upstream's certificate and not the authority's
	proxy, _ := url.Parse("http://" + c.ln.Addr().String())
	cl := &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(proxy), TLSClientConfig: &tls.Config{RootCAs: up.Client().Transport.(*http.Transport).TLSClientConfig.RootCAs}, DisableKeepAlives: true}}
	if r, err := cl.Get(up.URL + "/x"); err == nil {
		r.Body.Close()
		t.Fatal("the first connection should fail the handshake")
	}
	// the proxy marks the host a moment after the client sees the failure
	var err error
	for i := 0; i < 50; i++ {
		var r *http.Response
		if r, err = cl.Get(up.URL + "/x"); err == nil {
			r.Body.Close()
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err != nil {
		t.Fatal(err)
	}
	var tunnel bool
	for i := 0; i < 100 && !tunnel; i++ {
		for _, sp := range rec.Ended() {
			if strings.HasPrefix(sp.Name(), "network tunnel ") {
				tunnel = true
				a := attrs(sp.Attributes())
				if a["pravo.net.decrypted"].AsBool() || a["pravo.net.response_bytes"].AsInt64() == 0 {
					t.Errorf("tunnel attributes %v", a)
				}
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !tunnel {
		t.Error("no tunnel span")
	}
}

// Closing removes the file of the authority, and the environment names the proxy, the bundle and the loopback exception.
func TestEnvAndClose(t *testing.T) {
	t.Setenv("PRAVO_TRACES", "on")
	c, err := New()
	if err != nil {
		t.Fatal(err)
	}
	env := strings.Join(c.Env(), "\n")
	for _, want := range []string{"HTTPS_PROXY=http://127.0.0.1:", "SSL_CERT_FILE=" + c.caFile, "AWS_CA_BUNDLE=", "NO_PROXY=", "127.0.0.1"} {
		if !strings.Contains(env, want) {
			t.Errorf("env lacks %q", want)
		}
	}
	if fi, err := os.Stat(c.caFile); err != nil || fi.Mode().Perm()&0o077 != 0 {
		t.Errorf("bundle %v %v", fi, err)
	}
	c.Close()
	c.Close()
	if _, err := os.Stat(c.caFile); !os.IsNotExist(err) {
		t.Error("the bundle is still there")
	}
}

func TestEnabled(t *testing.T) {
	t.Setenv("OTEL_SDK_DISABLED", "")
	t.Setenv("PRAVO_TRACES", "")
	if Enabled() {
		t.Error("on without PRAVO_TRACES")
	}
	t.Setenv("PRAVO_TRACES", "on")
	if !Enabled() {
		t.Error("off with PRAVO_TRACES=on")
	}
	t.Setenv("PRAVO_TRACES_NET", "off")
	if Enabled() {
		t.Error("on with PRAVO_TRACES_NET=off")
	}
}
