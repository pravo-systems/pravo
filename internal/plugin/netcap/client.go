package netcap

import (
	"errors"
	"io"
	"net/http"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"github.com/pravo-systems/pravo/internal/telemetry"
)

// Trace sends r with send and records it as a network span under the span of r's context, for the requests that pravo makes
// itself, such as the ones to a remote state. It is the same span as the one of a provider's request, from the other side:
// the method, host, path without its query, status and sizes, never a header or a body. It sends the request as it is when
// recording is off.
func Trace(r *http.Request, send func(*http.Request) (*http.Response, error)) (*http.Response, error) {
	if !Enabled() {
		return send(r)
	}
	start := time.Now()
	host := r.URL.Hostname()
	path := r.URL.EscapedPath()
	if path == "" {
		path = "/"
	}
	ctx, span := telemetry.Tracer().Start(r.Context(), "network "+r.Method+" "+host, trace.WithSpanKind(trace.SpanKindClient), trace.WithTimestamp(start), trace.WithAttributes(
		attribute.String("http.request.method", r.Method),
		attribute.String("server.address", host),
		attribute.String("url.scheme", r.URL.Scheme),
		attribute.String("url.path", clip(path, 300)),
		attribute.Bool("pravo.net.decrypted", true),
	))
	if p := r.URL.Port(); p != "" {
		span.SetAttributes(attribute.String("server.port", p))
	}
	out := r.Clone(ctx)
	req := &countBody{rc: r.Body}
	if r.Body != nil {
		out.Body = req
	}
	resp, err := send(out)
	if err != nil {
		span.SetAttributes(attribute.Int64("pravo.net.request_bytes", req.n.Load()))
		telemetry.End(span, err)
		return resp, err
	}
	resp.Body = &spanBody{rc: resp.Body, span: span, req: req, status: resp.StatusCode}
	return resp, nil
}

// spanBody ends the span of a request when its answer has been read or closed, so that the span lasts as long as the transfer.
type spanBody struct {
	rc     io.ReadCloser
	span   trace.Span
	req    *countBody
	status int
	once   sync.Once
	n2     atomic.Int64
}

func (b *spanBody) Read(p []byte) (int, error) {
	n, err := b.rc.Read(p)
	b.n2.Add(int64(n))
	if err != nil {
		b.end(err)
	}
	return n, err
}

func (b *spanBody) Close() error {
	err := b.rc.Close()
	b.end(nil)
	return err
}

func (b *spanBody) end(readErr error) {
	b.once.Do(func() {
		b.span.SetAttributes(attribute.Int("http.response.status_code", b.status), attribute.Int64("pravo.net.request_bytes", b.req.n.Load()), attribute.Int64("pravo.net.response_bytes", b.n2.Load()))
		var err error
		if b.status >= 500 {
			err = errors.New("HTTP " + strconv.Itoa(b.status))
		} else if readErr != nil && readErr != io.EOF {
			err = readErr
		}
		telemetry.End(b.span, err)
	})
}
