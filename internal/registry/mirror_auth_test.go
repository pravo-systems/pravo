package registry

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"

	"cuelabs.dev/go/oci/ociregistry/ocimem"
	"cuelabs.dev/go/oci/ociregistry/ociserver"
)

// A credHelpers entry of the docker configuration runs docker-credential-NAME, as it does for
// docker, so docker-credential-ecr-login is enough to read a mirror in Amazon ECR.
func TestMirrorAuthenticatesWithACredentialHelper(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the helper is a shell script")
	}
	var authed, denied atomic.Int32
	inner := ociserver.New(ocimem.New(), nil)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if u, p, ok := r.BasicAuth(); !ok || u != "AWS" || p != "token-from-the-helper" {
			denied.Add(1)
			w.Header().Set("WWW-Authenticate", `Basic realm="ecr"`)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		authed.Add(1)
		inner.ServeHTTP(w, r)
	}))
	defer srv.Close()
	host := strings.TrimPrefix(srv.URL, "http://")

	bin := t.TempDir()
	helper := "#!/bin/sh\nread server\ncase \"$server\" in " + host + ") echo '{\"ServerURL\":\"'$server'\",\"Username\":\"AWS\",\"Secret\":\"token-from-the-helper\"}';; *) echo 'credentials not found in native keychain'; exit 1;; esac\n"
	if err := os.WriteFile(filepath.Join(bin, "docker-credential-ecr-login"), []byte(helper), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := t.TempDir()
	if err := os.WriteFile(filepath.Join(cfg, "config.json"), []byte(`{"credHelpers": {"`+host+`": "ecr-login"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("DOCKER_CONFIG", cfg)
	t.Setenv("DOCKER_AUTH_CONFIG", "")

	cl, err := ociClient(host)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := listTags(context.Background(), cl, "pravo-providers/hashicorp/random"); err != nil && authed.Load() == 0 {
		t.Fatalf("the helper's credentials were not sent: %v (denied %d)", err, denied.Load())
	}
	if authed.Load() == 0 {
		t.Fatal("no request was authenticated")
	}
}
