package state

import (
	"context"
	"net/http"
)

// Prober is a backend that can test whether its store honours the conditional
// requests that the locks need: the S3 and http backends.
type Prober interface {
	Backend
	Probe(ctx context.Context) error
	ProbeSteps(ctx context.Context, report func(check string, err error)) error
	ProbeStrict(ctx context.Context, report func(check string, err error)) error
}

var (
	_ Prober = (*S3Backend)(nil)
	_ Prober = (*HTTPBackend)(nil)
)

// Probe is the check of ProbeChecks, made on scratch objects under probe/; see
// S3Backend.Probe.
func (b *HTTPBackend) Probe(ctx context.Context) error { return b.ProbeSteps(ctx, nil) }

// ProbeSteps checks, on scratch objects under probe/, that the server honours
// create only (If-None-Match: *), replace (If-Match on a put) and delete
// (If-Match on a delete), each with a tag that must be refused and one that must
// be accepted, and that eight concurrent creates of one key leave one winner. It
// returns a *ProbeError for a server that ignores or rejects a condition, and
// another error when the server cannot be reached. report is called with the name
// of each check from ProbeChecks as it ends.
func (b *HTTPBackend) ProbeSteps(ctx context.Context, report func(check string, err error)) error {
	return runProbe(ctx, httpProbe{b}, report)
}

// ProbeStrict checks what the immutable records of a sealed state need beyond
// ProbeSteps: of eight concurrent replaces with one entity tag exactly one wins,
// and a key that has just been written is in the listing at once.
func (b *HTTPBackend) ProbeStrict(ctx context.Context, report func(check string, err error)) error {
	return runStrict(ctx, httpProbe{b}, report)
}

// httpProbe is the requests of the probes, made against a server.
type httpProbe struct{ b *HTTPBackend }

func (p httpProbe) storeName() string { return p.b.name }

// answer sorts a status into the answers a conditional request may get; any other
// status is a fault of the connection or the credentials.
func (p httpProbe) answer(status int, err error) probeAnswer {
	switch {
	case err != nil:
		return probeAnswer{err: err}
	case status/100 == 2:
		return probeAnswer{accepted: true}
	case status == http.StatusPreconditionFailed:
		return probeAnswer{}
	case status == http.StatusNotImplemented:
		return probeAnswer{err: errProbeUnsupported}
	}
	return probeAnswer{err: &httpStatusError{op: "probe", key: "probe", status: status}}
}

// put writes a scratch object with the conditions given, as a lock does. A
// refusal of a request that was sent more than once counts as accepted when the
// object holds what was sent.
func (p httpProbe) put(ctx context.Context, key, body, ifNone, ifMatch string) probeAnswer {
	b := p.b
	h := map[string]string{}
	if ifNone != "" {
		h["If-None-Match"] = ifNone
	}
	if ifMatch != "" {
		h["If-Match"] = ifMatch
	}
	r, err := b.do(ctx, http.MethodPut, b.objectURL(key), []byte(body), h)
	if err != nil {
		return probeAnswer{err: err}
	}
	if r.status == http.StatusPreconditionFailed {
		landed, lerr := b.landed(ctx, r.sent, "put", key, []byte(body))
		switch {
		case lerr != nil:
			return probeAnswer{err: lerr}
		case landed:
			return probeAnswer{accepted: true}
		}
	}
	return p.answer(r.status, nil)
}

func (p httpProbe) del(ctx context.Context, key, ifMatch string) probeAnswer {
	r, err := p.b.do(ctx, http.MethodDelete, p.b.objectURL(key), nil, map[string]string{"If-Match": ifMatch})
	return p.answer(r.status, err)
}

func (p httpProbe) get(ctx context.Context, key string) (string, string, error) {
	data, tag, err := p.b.getTagged(ctx, key)
	return string(data), tag, err
}

func (p httpProbe) list(ctx context.Context, prefix string) ([]string, error) {
	return p.b.List(ctx, prefix)
}

func (p httpProbe) cleanup(ctx context.Context, keys ...string) {
	for _, k := range keys {
		_ = p.b.Delete(context.WithoutCancel(ctx), k)
	}
}
