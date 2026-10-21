package registry

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const notFoundPage = `<!doctype html>
<html lang="en">
  <head>
    <title>Not Found</title>
    <style>body { font-family: system-ui; }</style>
  </head>
  <body><main>Not Found</main></body>
</html>`

// registryServer answers the discovery document and the version list the way a
// provider registry does, and everything else with a web page.
func registryServer(t *testing.T, versions http.HandlerFunc) (*Client, Source) {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/terraform.json", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"providers.v1": "/v1/providers/"}`))
	})
	mux.HandleFunc("/v1/providers/", versions)
	srv := httptest.NewTLSServer(mux)
	t.Cleanup(srv.Close)
	host := strings.TrimPrefix(srv.URL, "https://")
	return &Client{HTTP: srv.Client(), Home: t.TempDir()}, Source{Host: host, Namespace: "hashicorp", Type: "ramdom"}
}

func TestUnknownProviderIsOneLine(t *testing.T) {
	c, src := registryServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte(notFoundPage))
	})
	_, err := c.Resolve(context.Background(), src, "")
	if err == nil {
		t.Fatal("no error")
	}
	want := "provider " + src.String() + " was not found; check the name"
	if err.Error() != want {
		t.Errorf("error = %q, want %q", err, want)
	}
	if strings.ContainsAny(err.Error(), "\n<") {
		t.Errorf("the page leaked into the error: %q", err)
	}
}

func TestUnknownVersionIsOneLine(t *testing.T) {
	c, src := registryServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte(notFoundPage))
	})
	_, err := c.Checksum(context.Background(), src, "9.9.9", Platform{OS: "linux", Arch: "amd64"})
	if err == nil || err.Error() != src.Display()+" v9.9.9 was not found for linux_amd64" {
		t.Errorf("error = %v", err)
	}
}

func TestOtherErrorsShowAtMostOneShortLine(t *testing.T) {
	long := strings.Repeat("x", 500)
	for name, tc := range map[string]struct {
		code int
		body string
		want string // the end of the message
	}{
		"web page with a title": {500, notFoundPage, "500 Internal Server Error: Not Found"},
		"web page, no title":    {502, "<html><body>" + strings.Repeat("bad gateway ", 50) + "</body></html>", "502 Bad Gateway"},
		"json":                  {500, "{\n  \"errors\": [\"upstream is down\"]\n}\n", `500 Internal Server Error: {"errors":["upstream is down"]}`},
		"text, many lines":      {503, "try again later\nrequest 4711\nmore detail\n", "503 Service Unavailable: try again later"},
		"empty":                 {500, "", "500 Internal Server Error"},
		"long line":             {500, long, "500 Internal Server Error: " + strings.Repeat("x", maxBodyLine) + "..."},
	} {
		c, src := registryServer(t, func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(tc.code)
			w.Write([]byte(tc.body))
		})
		src.Type = "random"
		_, err := c.Resolve(context.Background(), src, "")
		if err == nil {
			t.Fatalf("%s: no error", name)
		}
		if strings.Contains(err.Error(), "\n") {
			t.Errorf("%s: the error has more than one line: %q", name, err)
		}
		if !strings.HasSuffix(err.Error(), tc.want) {
			t.Errorf("%s: error = %q, want it to end with %q", name, err, tc.want)
		}
	}
}

func TestBodyLine(t *testing.T) {
	for name, tc := range map[string]struct{ body, status, want string }{
		"the status again":     {"Not Found", "404 Not Found", ""},
		"a title that repeats": {notFoundPage, "404 Not Found", ""},
		"a title":              {notFoundPage, "500 Internal Server Error", "Not Found"},
		"spaces in a title":    {"<html><title>\n  Bad\n  gateway </title></html>", "500 Internal Server Error", "Bad gateway"},
		"unclosed title":       {"<html><title>Oops", "500", ""},
		"json":                 {`{"a": [1, 2]}`, "500", `{"a":[1,2]}`},
		"empty":                {"  \n", "500", ""},
	} {
		if got := bodyLine([]byte(tc.body), tc.status); got != tc.want {
			t.Errorf("%s: bodyLine = %q, want %q", name, got, tc.want)
		}
	}
}
