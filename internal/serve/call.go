package serve

import (
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
	r := httptest.NewRequest(http.MethodGet, path+"?"+q.Encode(), nil)
	r.Header.Set("Authorization", "Bearer "+s.token)
	w := httptest.NewRecorder()
	s.api(w, r)
	return w.Code, w.Body.Bytes()
}
