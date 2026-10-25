// Package fakehttp is an in-memory server for the http state backend: GET, PUT
// and DELETE of objects with entity tags, PUT with If-None-Match: * and PUT and
// DELETE with If-Match, and a JSON listing by prefix. It is the reference for the
// protocol described at state.HTTPConfig, and exists to test and try the backend
// without a real server.
package fakehttp

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"sort"
	"strings"
	"sync"
)

// New returns an empty server.
func New() *Server { return &Server{Objects: map[string][]byte{}} }

// Server is the handler of an in-memory store.
type Server struct {
	mu      sync.Mutex
	Objects map[string][]byte
	// Token, when set, is the bearer token that every request must carry.
	Token string
	// Fault, if set, is called with each request before the server serves it. A
	// status other than 0 answers the request with it, and nothing else happens.
	Fault func(r *http.Request) (status int)
	// Lost is like Fault, except that the request is served first and its effects
	// stay: only the answer is replaced.
	Lost func(r *http.Request) (status int)
	// IgnoreConditions makes the server accept every write, as a plain file
	// server would.
	IgnoreConditions bool
}

// Tag is the entity tag of the contents.
func Tag(data []byte) string {
	sum := sha256.Sum256(data)
	return `"` + hex.EncodeToString(sum[:8]) + `"`
}

// FailFirst returns a Fault that answers the first n requests for which match
// reports true (every request, if match is nil) with status, and serves the rest.
func FailFirst(n, status int, match func(r *http.Request) bool) func(r *http.Request) int {
	var mu sync.Mutex
	return func(r *http.Request) int {
		if match != nil && !match(r) {
			return 0
		}
		mu.Lock()
		defer mu.Unlock()
		if n <= 0 {
			return 0
		}
		n--
		return status
	}
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if s.Token != "" && r.Header.Get("Authorization") != "Bearer "+s.Token {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.Fault != nil {
		if st := s.Fault(r); st != 0 {
			http.Error(w, "fault", st)
			return
		}
	}
	rec := &recorder{header: http.Header{}, status: http.StatusOK}
	s.serve(rec, r)
	if s.Lost != nil {
		if st := s.Lost(r); st != 0 {
			http.Error(w, "lost", st)
			return
		}
	}
	for k, v := range rec.header {
		w.Header()[k] = v
	}
	w.WriteHeader(rec.status)
	_, _ = w.Write(rec.body)
}

// recorder holds an answer until the server knows whether to send it.
type recorder struct {
	header http.Header
	status int
	body   []byte
}

func (r *recorder) Header() http.Header         { return r.header }
func (r *recorder) WriteHeader(status int)      { r.status = status }
func (r *recorder) Write(p []byte) (int, error) { r.body = append(r.body, p...); return len(p), nil }

func (s *Server) serve(w http.ResponseWriter, r *http.Request) {
	key := strings.TrimPrefix(r.URL.Path, "/")
	if key == "" || strings.HasSuffix(key, "/") {
		// The listing of the directory at the URL: the keys under key+prefix, named
		// relative to key.
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		prefix := r.URL.Query().Get("prefix")
		keys := []string{}
		for k := range s.Objects {
			if rel, ok := strings.CutPrefix(k, key); ok && strings.HasPrefix(rel, prefix) {
				keys = append(keys, rel)
			}
		}
		sort.Strings(keys)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"keys": keys})
		return
	}
	cur, exists := s.Objects[key]
	switch r.Method {
	case http.MethodGet:
		if !exists {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		w.Header().Set("ETag", Tag(cur))
		_, _ = w.Write(cur)
	case http.MethodPut:
		body, _ := io.ReadAll(r.Body)
		if !s.IgnoreConditions {
			if r.Header.Get("If-None-Match") == "*" && exists {
				http.Error(w, "exists", http.StatusPreconditionFailed)
				return
			}
			if tag := r.Header.Get("If-Match"); tag != "" {
				if !exists {
					http.Error(w, "not found", http.StatusNotFound)
					return
				}
				if tag != Tag(cur) {
					http.Error(w, "changed", http.StatusPreconditionFailed)
					return
				}
			}
		}
		s.Objects[key] = body
		w.Header().Set("ETag", Tag(body))
		w.WriteHeader(http.StatusNoContent)
	case http.MethodDelete:
		if tag := r.Header.Get("If-Match"); tag != "" && !s.IgnoreConditions {
			if !exists {
				http.Error(w, "not found", http.StatusNotFound)
				return
			}
			if tag != Tag(cur) {
				http.Error(w, "changed", http.StatusPreconditionFailed)
				return
			}
		}
		if !exists {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		delete(s.Objects, key)
		w.WriteHeader(http.StatusNoContent)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}
