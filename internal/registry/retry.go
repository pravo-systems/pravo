package registry

import (
	"errors"
	"io"
	"math/rand/v2"
	"net"
	"net/http"
	"strings"
	"syscall"
	"time"
)

// retryAttempts is the number of times a request that failed for a reason that
// passes is made before its failure is returned.
const retryAttempts = 4

// retryTransport makes the requests that read from a registry again when the
// connection is reset or dropped, or when the registry answers that it is busy
// or unavailable. A registry that several machines query at once, as the CI of a
// project does, drops a few connections now and then, and a command that gives
// up at the first one fails for nothing. Requests that change something are not
// repeated.
type retryTransport struct {
	base http.RoundTripper
	// wait is the pause before the n-th new try, n from 1; tests replace it.
	wait func(n int) time.Duration
}

func (t *retryTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	base := t.base
	if base == nil {
		base = http.DefaultTransport
	}
	if req.Method != http.MethodGet && req.Method != http.MethodHead {
		return base.RoundTrip(req)
	}
	for attempt := 1; ; attempt++ {
		resp, err := base.RoundTrip(req)
		if attempt >= retryAttempts || !retryable(resp, err) || req.Context().Err() != nil {
			return resp, err
		}
		if resp != nil {
			_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<16))
			_ = resp.Body.Close()
		}
		pause := t.pause(attempt)
		select {
		case <-time.After(pause):
		case <-req.Context().Done():
			return nil, req.Context().Err()
		}
	}
}

func (t *retryTransport) pause(n int) time.Duration {
	if t.wait != nil {
		return t.wait(n)
	}
	d := 500 * time.Millisecond << (n - 1) // 0.5 s, 1 s, 2 s
	return d/2 + time.Duration(rand.Int64N(int64(d)))
}

// retryable reports whether a request that ended like this may succeed when it
// is made again.
func retryable(resp *http.Response, err error) bool {
	if err != nil {
		if errors.Is(err, syscall.ECONNRESET) || errors.Is(err, syscall.ECONNABORTED) || errors.Is(err, syscall.EPIPE) || errors.Is(err, io.ErrUnexpectedEOF) {
			return true
		}
		var ne net.Error
		if errors.As(err, &ne) {
			return true
		}
		return strings.Contains(err.Error(), "connection reset") || strings.Contains(err.Error(), "EOF")
	}
	switch resp.StatusCode {
	case http.StatusTooManyRequests, http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		return true
	}
	return false
}
