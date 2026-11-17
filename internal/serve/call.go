package serve

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
)

// Call answers one read path of the template catalog in process, with the status and the body that the viewer gets
// for the same request, so that "pravo template" and the wizard are the same code. It takes the path of the API, such
// as "/api/templates/try", and its query. No other path is served this way.
func (s *Server) Call(path string, q url.Values) (int, []byte) {
	if !strings.HasPrefix(path, "/api/templates") {
		return http.StatusNotFound, []byte(`{"error":"not found"}`)
	}
	r := httptest.NewRequestWithContext(context.Background(), http.MethodGet, path+"?"+q.Encode(), nil)
	r.Header.Set("Authorization", "Bearer "+s.token)
	w := httptest.NewRecorder()
	s.api(w, r)
	return w.Code, w.Body.Bytes()
}

// Draw answers one path of the drawing board in process, with the status and the body that the page gets for the
// same request, so that an agent draws with the code a person draws with: the types and the form of a type, a
// resource as the pen can change it, the check of a drawing, and, when the server was made with board options, the
// save and the plan of a drawing. It never applies, never starts from a Terraform state and never asks the guide. It
// looks at the files first, as the watcher of a running server does, so that a call after a save reads what was
// written. A body is sent as JSON; a GET takes the query.
func (s *Server) Draw(path string, q url.Values, body any) (int, []byte) {
	method := http.MethodGet
	switch path {
	case "/api/board", "/api/board/types", "/api/board/type", "/api/board/edit":
	case "/api/board/check", "/api/board/save", "/api/board/plan":
		method = http.MethodPost
	default:
		return http.StatusNotFound, []byte(`{"error":"not a path an agent draws with"}`)
	}
	s.watch.sample()
	var rd io.Reader
	if method == http.MethodPost {
		b, err := json.Marshal(body)
		if err != nil {
			return http.StatusBadRequest, []byte(`{"error":"the drawing is not JSON"}`)
		}
		rd = bytes.NewReader(b)
	}
	r := httptest.NewRequestWithContext(context.Background(), method, path+"?"+q.Encode(), rd)
	r.Header.Set("Authorization", "Bearer "+s.token)
	if rd != nil {
		r.Header.Set("Content-Type", "application/json")
	}
	w := httptest.NewRecorder()
	s.api(w, r)
	return w.Code, w.Body.Bytes()
}
