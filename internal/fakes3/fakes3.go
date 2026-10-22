// Package fakes3 is a minimal in-memory S3-compatible server: GET/PUT/DELETE
// objects with entity tags, PUT with If-None-Match: *, PUT and DELETE with
// If-Match, and ListObjectsV2 with a prefix, pages of at most 1,000 keys and
// URL-encoded keys. It can answer a request with an error that a real store
// gives under load or under a conflict (see Server.Fault). It exists to test
// and try the s3 state backend without a real bucket.
package fakes3

import (
	"crypto/md5"
	"encoding/base64"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
)

// MaxKeys is the largest number of keys in a page of a listing, as in S3.
const MaxKeys = 1000

// New returns an empty server.
func New() *Server { return &Server{Objects: map[string][]byte{}} }

// Server is a minimal S3-compatible server: GET/PUT/DELETE objects, PUT with
// If-None-Match: *, PUT and DELETE with If-Match, and ListObjectsV2 with a
// prefix. Enough to drive the backend through the real SDK client.
type Server struct {
	mu      sync.Mutex
	Objects map[string][]byte
	Puts    int
	// Fault, if it is set, is called with each request before the server serves
	// it, with the objects locked. A status other than 0 answers the request with
	// that status and error code, and nothing else happens: 409 with
	// ConditionalRequestConflict, 503 with SlowDown and 500 with InternalError are
	// what a real store answers under a conflict or a load. See FailFirst.
	Fault func(r *http.Request) (status int, code string)
	// Lost is like Fault, except that the request is served first and its effects
	// stay: only the answer is replaced. It is what a client sees when the
	// connection fails, or the store answers 500, after the store has committed
	// the write.
	Lost func(r *http.Request) (status int, code string)
	// PageSize is the number of keys in a page of a listing; 0 means MaxKeys.
	PageSize int
	// DropTokens leaves the NextContinuationToken out of a page that is not the
	// last, as some stores do; RepeatToken gives every page the token of the first.
	DropTokens, RepeatToken bool
	// UnquotedTags makes the server send entity tags without the quotes that the
	// standard puts around them, and refuse an If-Match that has them.
	UnquotedTags bool
}

// FailFirst returns a Fault that answers the first n requests for which match
// reports true (every request, if match is nil) with the status and error code,
// and serves the rest.
func FailFirst(n int, status int, code string, match func(r *http.Request) bool) func(r *http.Request) (int, string) {
	var mu sync.Mutex
	return func(r *http.Request) (int, string) {
		if match != nil && !match(r) {
			return 0, ""
		}
		mu.Lock()
		defer mu.Unlock()
		if n <= 0 {
			return 0, ""
		}
		n--
		return status, code
	}
}

// Conditional reports whether r is a write that carries a condition.
func Conditional(r *http.Request) bool {
	return (r.Method == http.MethodPut || r.Method == http.MethodDelete) && (r.Header.Get("If-Match") != "" || r.Header.Get("If-None-Match") != "")
}

// tag is the entity tag of an object as the server sends it.
func (f *Server) tag(data []byte) string {
	if f.UnquotedTags {
		return strings.Trim(etag(data), `"`)
	}
	return etag(data)
}

// etag is the entity tag of an object: the digest of its contents.
func etag(data []byte) string { return fmt.Sprintf(`"%x"`, md5.Sum(data)) }

// ifMatch checks the If-Match header of a PUT or DELETE against the object at
// key. It answers the request and returns false when the condition is not met:
// 404 for an object that does not exist, 412 for one with another entity tag.
func (f *Server) ifMatch(w http.ResponseWriter, r *http.Request, key string) bool {
	want := r.Header.Get("If-Match")
	if want == "" {
		return true
	}
	cur, ok := f.Objects[key]
	switch {
	case !ok:
		w.WriteHeader(http.StatusNotFound)
		fmt.Fprint(w, `<Error><Code>NoSuchKey</Code></Error>`)
	case want != "*" && (strings.Trim(want, `"`) != strings.Trim(etag(cur), `"`) || (f.UnquotedTags && strings.HasPrefix(want, `"`))):
		w.WriteHeader(http.StatusPreconditionFailed)
		fmt.Fprint(w, `<Error><Code>PreconditionFailed</Code></Error>`)
	default:
		return true
	}
	return false
}

func (f *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	fail := func(status int, code string) {
		w.WriteHeader(status)
		fmt.Fprintf(w, `<Error><Code>%s</Code><Message>injected by the test</Message></Error>`, code)
	}
	if f.Fault != nil {
		if status, code := f.Fault(r); status != 0 {
			fail(status, code)
			return
		}
	}
	if f.Lost != nil {
		if status, code := f.Lost(r); status != 0 {
			f.serve(httptest.NewRecorder(), r)
			fail(status, code)
			return
		}
	}
	f.serve(w, r)
}

// serve serves r, with the objects locked.
func (f *Server) serve(w http.ResponseWriter, r *http.Request) {
	// path-style: /bucket/key
	parts := strings.SplitN(strings.TrimPrefix(r.URL.Path, "/"), "/", 2)
	key := ""
	if len(parts) == 2 {
		key = parts[1]
	}
	switch {
	case r.Method == http.MethodGet && key == "" && r.URL.Query().Get("list-type") == "2":
		f.list(w, r)
	case r.Method == http.MethodGet:
		data, ok := f.Objects[key]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			fmt.Fprint(w, `<Error><Code>NoSuchKey</Code></Error>`)
			return
		}
		w.Header().Set("ETag", f.tag(data))
		_, _ = w.Write(data)
	case r.Method == http.MethodHead:
		if _, ok := f.Objects[key]; !ok {
			w.WriteHeader(http.StatusNotFound)
			return
		}
	case r.Method == http.MethodPut:
		if r.Header.Get("If-None-Match") == "*" {
			if _, ok := f.Objects[key]; ok {
				w.WriteHeader(http.StatusPreconditionFailed)
				fmt.Fprint(w, `<Error><Code>PreconditionFailed</Code></Error>`)
				return
			}
		}
		if !f.ifMatch(w, r, key) {
			return
		}
		data, _ := io.ReadAll(r.Body)
		f.Objects[key] = data
		f.Puts++
		w.Header().Set("ETag", f.tag(data))
	case r.Method == http.MethodDelete:
		if !f.ifMatch(w, r, key) {
			return
		}
		delete(f.Objects, key)
		w.WriteHeader(http.StatusNoContent)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

// list answers a ListObjectsV2 request: the keys under the prefix in byte order,
// after the continuation token or start-after, in pages of at most PageSize and
// MaxKeys keys. A page that is not the last has a NextContinuationToken. With
// encoding-type=url the keys are URL-encoded, as S3 does.
func (f *Server) list(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	prefix := q.Get("prefix")
	after := q.Get("start-after")
	if tok := q.Get("continuation-token"); tok != "" {
		raw, err := base64.RawURLEncoding.DecodeString(tok)
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			fmt.Fprint(w, `<Error><Code>InvalidArgument</Code><Message>bad continuation token</Message></Error>`)
			return
		}
		after = string(raw)
	}
	size := f.PageSize
	if size <= 0 || size > MaxKeys {
		size = MaxKeys
	}
	if n, err := strconv.Atoi(q.Get("max-keys")); err == nil && n >= 0 && n < size {
		size = n
	}
	var keys []string
	for k := range f.Objects {
		if strings.HasPrefix(k, prefix) && k > after {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	truncated := len(keys) > size
	if truncated {
		keys = keys[:size]
	}
	enc := func(k string) string {
		if q.Get("encoding-type") == "url" {
			return url.QueryEscape(k)
		}
		return k
	}
	type obj struct {
		Key  string `xml:"Key"`
		ETag string `xml:"ETag"`
		Size int    `xml:"Size"`
	}
	res := struct {
		XMLName               xml.Name `xml:"ListBucketResult"`
		Prefix                string   `xml:"Prefix"`
		KeyCount              int      `xml:"KeyCount"`
		MaxKeys               int      `xml:"MaxKeys"`
		EncodingType          string   `xml:"EncodingType,omitempty"`
		IsTruncated           bool     `xml:"IsTruncated"`
		NextContinuationToken string   `xml:"NextContinuationToken,omitempty"`
		Contents              []obj    `xml:"Contents"`
	}{Prefix: enc(prefix), KeyCount: len(keys), MaxKeys: size, EncodingType: q.Get("encoding-type"), IsTruncated: truncated}
	for _, k := range keys {
		res.Contents = append(res.Contents, obj{Key: enc(k), ETag: f.tag(f.Objects[k]), Size: len(f.Objects[k])})
	}
	switch {
	case truncated && f.DropTokens:
	case truncated && f.RepeatToken:
		res.NextContinuationToken = base64.RawURLEncoding.EncodeToString([]byte(prefix))
	case truncated:
		res.NextContinuationToken = base64.RawURLEncoding.EncodeToString([]byte(keys[len(keys)-1]))
	}
	w.Header().Set("Content-Type", "application/xml")
	_ = xml.NewEncoder(w).Encode(res)
}
