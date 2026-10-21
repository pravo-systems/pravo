// Package fakes3 is a minimal in-memory S3-compatible server: GET/PUT/DELETE
// objects with entity tags, PUT with If-None-Match: *, PUT and DELETE with
// If-Match, and ListObjectsV2 with a prefix. It exists to test and try the s3
// state backend without a real bucket.
package fakes3

import (
	"crypto/md5"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"sync"
)

// New returns an empty server.
func New() *Server { return &Server{Objects: map[string][]byte{}} }

// Server is a minimal S3-compatible server: GET/PUT/DELETE objects, PUT with
// If-None-Match: *, PUT and DELETE with If-Match, and ListObjectsV2 with a
// prefix. Enough to drive the backend through the real SDK client.
type Server struct {
	mu      sync.Mutex
	Objects map[string][]byte
	Puts    int
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
	case want != "*" && strings.Trim(want, `"`) != strings.Trim(etag(cur), `"`):
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
	// path-style: /bucket/key
	parts := strings.SplitN(strings.TrimPrefix(r.URL.Path, "/"), "/", 2)
	key := ""
	if len(parts) == 2 {
		key = parts[1]
	}
	switch {
	case r.Method == http.MethodGet && key == "" && r.URL.Query().Get("list-type") == "2":
		prefix := r.URL.Query().Get("prefix")
		var keys []string
		for k := range f.Objects {
			if strings.HasPrefix(k, prefix) {
				keys = append(keys, k)
			}
		}
		sort.Strings(keys)
		type obj struct {
			Key string `xml:"Key"`
		}
		res := struct {
			XMLName     xml.Name `xml:"ListBucketResult"`
			IsTruncated bool     `xml:"IsTruncated"`
			Contents    []obj    `xml:"Contents"`
		}{}
		for _, k := range keys {
			res.Contents = append(res.Contents, obj{Key: k})
		}
		w.Header().Set("Content-Type", "application/xml")
		_ = xml.NewEncoder(w).Encode(res)
	case r.Method == http.MethodGet:
		data, ok := f.Objects[key]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			fmt.Fprint(w, `<Error><Code>NoSuchKey</Code></Error>`)
			return
		}
		w.Header().Set("ETag", etag(data))
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
		w.Header().Set("ETag", etag(data))
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
