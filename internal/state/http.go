package state

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/pravo-systems/pravo/internal/plugin/netcap"
	"io"
	"math/rand/v2"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

// HTTPConfig describes a server that keeps a state graph as objects under a URL.
//
// The server speaks a small object protocol, the subset of S3 that the locks of
// the store need, without the signing:
//
//	GET    <url>/<key>            the object, with an ETag header; 404 if absent
//	PUT    <url>/<key>            writes the object; with "If-None-Match: *" only if
//	                              there is none, with "If-Match: <tag>" only if it
//	                              has that tag (404 if absent); 412 refuses a condition
//	DELETE <url>/<key>            deletes the object; "If-Match" as for PUT
//	GET    <url>/?prefix=<dir>/   {"keys": ["<dir>/a.json", ...]}, relative to <url>
//
// A listing that is cut short is not part of the protocol: the server sends all
// the keys under the prefix.
type HTTPConfig struct {
	// URL is the base URL of the state, such as https://state.example.com/team/app.
	URL string
	// Token, when set, is sent as a bearer token.
	Token string
	// Username and Password, when Username is set and Token is not, are sent as
	// basic authentication.
	Username, Password string
	// Client replaces the default HTTP client; the tests set it.
	Client *http.Client
}

// httpAttempts is how many times a request is sent when the server is busy or the
// connection fails. httpBackoffBase is the first upper limit of the random wait
// between attempts, a variable so that the tests need not wait for it.
const (
	httpAttempts   = 6
	httpMaxBackoff = 5 * time.Second
)

var httpBackoffBase = 100 * time.Millisecond

// HTTPBackend stores objects on a server that speaks the protocol of HTTPConfig.
type HTTPBackend struct {
	base   *url.URL
	name   string
	token  string
	user   string
	pass   string
	client *http.Client
}

// NewHTTPBackend prepares the backend for the server. It does not contact it.
func NewHTTPBackend(cfg HTTPConfig) (*HTTPBackend, error) {
	if cfg.URL == "" {
		return nil, errors.New("$settings.state.url is required for the http backend")
	}
	u, err := url.Parse(cfg.URL)
	if err != nil {
		return nil, fmt.Errorf("$settings.state.url: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" || u.Host == "" {
		return nil, fmt.Errorf("$settings.state.url %q: want an http:// or https:// URL", cfg.URL)
	}
	if u.User != nil {
		return nil, errors.New("$settings.state.url must not hold a user name or password: set username and password, or token")
	}
	if u.RawQuery != "" || u.Fragment != "" {
		return nil, errors.New("$settings.state.url must not have a query or a fragment")
	}
	u.Path = strings.TrimRight(u.Path, "/")
	u.RawPath = ""
	client := cfg.Client
	if client == nil {
		tr := http.DefaultTransport.(*http.Transport).Clone()
		tr.MaxIdleConns, tr.MaxIdleConnsPerHost = idleConns, idleConns
		client = &http.Client{Transport: tr}
	}
	return &HTTPBackend{base: u, name: u.String(), token: cfg.Token, user: cfg.Username, pass: cfg.Password, client: client}, nil
}

func (b *HTTPBackend) Name() string { return b.name }
func (b *HTTPBackend) Local() bool  { return false }

// httpStatusError is the answer of the server to a request it did not serve.
type httpStatusError struct {
	op, key string
	status  int
	body    string
}

func (e *httpStatusError) Error() string {
	msg := fmt.Sprintf("http %s %s: %d %s", e.op, e.key, e.status, http.StatusText(e.status))
	if e.body != "" {
		msg += ": " + e.body
	}
	return msg
}

func httpStatus(err error) int {
	var se *httpStatusError
	if errors.As(err, &se) {
		return se.status
	}
	return 0
}

func (b *HTTPBackend) objectURL(key string) string {
	u := *b.base
	u.Path = b.base.Path + "/" + key
	return u.String()
}

// httpRequestTimeout is how long one attempt, from sending the request to reading
// the whole answer, may take before it is given up and tried again. It is a var
// for tests.
var httpRequestTimeout = 2 * time.Minute

// httpMaxBody is the most a server may answer with: a record or a listing is far
// smaller, and a server that sends more is not one of ours.
const httpMaxBody = 256 << 20

// reply is the answer to a request, with the number of requests it took.
type reply struct {
	status int
	body   []byte
	etag   string
	sent   int
}

// busy reports whether a status says to ask again.
func busy(status int) bool {
	switch status {
	case http.StatusTooManyRequests, http.StatusInternalServerError, http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		return true
	}
	return false
}

// do sends the request, again when the server is busy or the connection fails, up
// to httpAttempts times. The answer is returned whatever its status; the error is
// that of the last connection failure, or the context.
func (b *HTTPBackend) do(ctx context.Context, method, target string, body []byte, header map[string]string) (reply, error) {
	var r reply
	var lastErr error
	for attempt := 0; attempt < httpAttempts; attempt++ {
		if attempt > 0 {
			limit := httpMaxBackoff
			if attempt < 16 && httpBackoffBase<<attempt < limit {
				limit = httpBackoffBase << attempt
			}
			select {
			case <-ctx.Done():
				return r, ctx.Err()
			case <-time.After(time.Duration(rand.Int64N(int64(limit) + 1))):
			}
		}
		var rd io.Reader
		if body != nil {
			rd = bytes.NewReader(body)
		}
		actx, cancel := context.WithTimeout(ctx, httpRequestTimeout)
		req, err := http.NewRequestWithContext(actx, method, target, rd)
		if err != nil {
			cancel()
			return r, err
		}
		for k, v := range header {
			req.Header.Set(k, v)
		}
		switch {
		case b.token != "":
			req.Header.Set("Authorization", "Bearer "+b.token)
		case b.user != "":
			req.SetBasicAuth(b.user, b.pass)
		}
		r.sent++
		resp, err := netcap.Trace(req, b.client.Do)
		if err != nil {
			cancel()
			if ctx.Err() != nil {
				return r, ctx.Err()
			}
			lastErr = err
			continue
		}
		data, rerr := io.ReadAll(io.LimitReader(resp.Body, httpMaxBody+1))
		resp.Body.Close()
		cancel()
		if rerr == nil && len(data) > httpMaxBody {
			return r, fmt.Errorf("%s %s: the answer is over %d MiB", method, target, httpMaxBody>>20)
		}
		if rerr != nil {
			if ctx.Err() != nil {
				return r, ctx.Err()
			}
			lastErr = rerr
			continue
		}
		lastErr = nil
		r.status, r.body, r.etag = resp.StatusCode, data, resp.Header.Get("ETag")
		if busy(r.status) {
			lastErr = &httpStatusError{status: r.status, body: snippet(data)}
			continue
		}
		return r, nil
	}
	if lastErr != nil {
		var se *httpStatusError
		if errors.As(lastErr, &se) {
			return r, nil // the busy answer of the last attempt
		}
	}
	return r, lastErr
}

// snippet is the start of an error body, for a message.
func snippet(data []byte) string {
	s := strings.TrimSpace(string(data))
	if len(s) > 200 {
		s = s[:200] + "..."
	}
	return s
}

func (b *HTTPBackend) fail(op, key string, r reply) error {
	return &httpStatusError{op: op, key: key, status: r.status, body: snippet(r.body)}
}

func (b *HTTPBackend) Get(ctx context.Context, key string) ([]byte, error) {
	data, _, err := b.getTagged(ctx, key)
	return data, err
}

func (b *HTTPBackend) getTagged(ctx context.Context, key string) ([]byte, string, error) {
	r, err := b.do(ctx, http.MethodGet, b.objectURL(key), nil, nil)
	if err != nil {
		return nil, "", fmt.Errorf("http get %s: %w", key, err)
	}
	switch {
	case r.status == http.StatusNotFound:
		return nil, "", ErrNotFound
	case r.status/100 != 2:
		return nil, "", b.fail("get", key, r)
	}
	return r.body, r.etag, nil
}

func (b *HTTPBackend) Put(ctx context.Context, key string, data []byte) error {
	r, err := b.do(ctx, http.MethodPut, b.objectURL(key), data, map[string]string{"Content-Type": "application/json"})
	if err != nil {
		return fmt.Errorf("http put %s: %w", key, err)
	}
	if r.status/100 != 2 {
		return b.fail("put", key, r)
	}
	return nil
}

// landed reports whether a conditional write that was sent more than once was
// applied by an earlier attempt whose answer was lost: the server refuses the
// condition because the object holds the bytes that were written. See
// S3Backend.landed.
func (b *HTTPBackend) landed(ctx context.Context, sent int, op, key string, data []byte) (bool, error) {
	if sent < 2 {
		return false, nil
	}
	got, err := b.Get(ctx, key)
	switch {
	case err == nil:
		return bytes.Equal(got, data), nil
	case errors.Is(err, ErrNotFound):
		return false, nil
	}
	return false, fmt.Errorf("http conditional %s %s: %w: %w", op, key, ErrUnknownOutcome, err)
}

// unknown is the error for a conditional write whose answer was lost for good.
func unknown(op, key string, err error) error {
	return fmt.Errorf("http conditional %s %s: %w: %w", op, key, ErrUnknownOutcome, err)
}

// PutIfAbsent relies on "If-None-Match: *". A server that ignores it would let
// two applies hold the same lock; Probe checks for that.
func (b *HTTPBackend) PutIfAbsent(ctx context.Context, key string, data []byte) (bool, error) {
	r, err := b.do(ctx, http.MethodPut, b.objectURL(key), data, map[string]string{"Content-Type": "application/json", "If-None-Match": "*"})
	switch {
	case err != nil && r.sent > 0 && ctx.Err() == nil:
		return false, unknown("put", key, err)
	case err != nil:
		return false, fmt.Errorf("http conditional put %s: %w", key, err)
	case busy(r.status):
		return false, unknown("put", key, b.fail("put", key, r))
	case r.status == http.StatusPreconditionFailed:
		return b.landed(ctx, r.sent, "put", key, data)
	case r.status/100 != 2:
		return false, b.fail("conditional put", key, r)
	}
	return true, nil
}

func noTag(op, key string) error {
	return fmt.Errorf("http conditional %s %s: the server sent no ETag, so the write cannot be made conditional", op, key)
}

// DeleteIf reads the object and deletes the version it read with an If-Match
// condition. See S3Backend.DeleteIf for the meaning of a 404 after a resend.
func (b *HTTPBackend) DeleteIf(ctx context.Context, key string, match func(current []byte) bool) (bool, error) {
	data, tag, err := b.getTagged(ctx, key)
	if err != nil {
		return false, err
	}
	if tag == "" {
		return false, noTag("delete", key)
	}
	if !match(data) {
		return false, nil
	}
	r, err := b.do(ctx, http.MethodDelete, b.objectURL(key), nil, map[string]string{"If-Match": tag})
	switch {
	case err != nil:
		return false, fmt.Errorf("http conditional delete %s: %w", key, err)
	case r.status == http.StatusNotFound:
		return false, ErrNotFound
	case r.status == http.StatusPreconditionFailed:
		return false, nil
	case r.status/100 != 2:
		return false, b.fail("conditional delete", key, r)
	}
	return true, nil
}

// ReplaceIf reads the object and writes the new contents over the version it
// read, with an If-Match condition.
func (b *HTTPBackend) ReplaceIf(ctx context.Context, key string, update func(current []byte) ([]byte, bool)) (bool, error) {
	data, tag, err := b.getTagged(ctx, key)
	if err != nil {
		return false, err
	}
	if tag == "" {
		return false, noTag("put", key)
	}
	next, ok := update(data)
	if !ok {
		return false, nil
	}
	r, err := b.do(ctx, http.MethodPut, b.objectURL(key), next, map[string]string{"Content-Type": "application/json", "If-Match": tag})
	switch {
	case err != nil && r.sent > 0 && ctx.Err() == nil:
		return false, unknown("put", key, err)
	case err != nil:
		return false, fmt.Errorf("http conditional put %s: %w", key, err)
	case r.status == http.StatusNotFound:
		return false, ErrNotFound
	case busy(r.status):
		return false, unknown("put", key, b.fail("put", key, r))
	case r.status == http.StatusPreconditionFailed:
		return b.landed(ctx, r.sent, "put", key, next)
	case r.status/100 != 2:
		return false, b.fail("conditional put", key, r)
	}
	return true, nil
}

// Delete removes the object; one that is not there is not an error.
func (b *HTTPBackend) Delete(ctx context.Context, key string) error {
	r, err := b.do(ctx, http.MethodDelete, b.objectURL(key), nil, nil)
	if err != nil {
		return fmt.Errorf("http delete %s: %w", key, err)
	}
	if r.status/100 != 2 && r.status != http.StatusNotFound {
		return b.fail("delete", key, r)
	}
	return nil
}

// List asks for the keys under prefix and returns them sorted.
func (b *HTTPBackend) List(ctx context.Context, prefix string) ([]string, error) {
	dir := strings.TrimSuffix(prefix, "/") + "/"
	u := *b.base
	u.Path = b.base.Path + "/"
	u.RawQuery = url.Values{"prefix": {dir}}.Encode()
	r, err := b.do(ctx, http.MethodGet, u.String(), nil, nil)
	if err != nil {
		return nil, fmt.Errorf("http list %s: %w", prefix, err)
	}
	if r.status/100 != 2 {
		return nil, b.fail("list", prefix, r)
	}
	var doc struct {
		Keys []string `json:"keys"`
	}
	if err := json.Unmarshal(r.body, &doc); err != nil {
		return nil, fmt.Errorf("http list %s: the listing is not {\"keys\": [...]}: %w", prefix, err)
	}
	out := make([]string, 0, len(doc.Keys))
	for _, k := range doc.Keys {
		if !strings.HasPrefix(k, dir) || strings.HasSuffix(k, "/") {
			continue
		}
		out = append(out, k)
	}
	if !sort.StringsAreSorted(out) {
		sort.Strings(out)
	}
	return out, nil
}
