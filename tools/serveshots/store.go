package main

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/pravo-systems/pravo/internal/fakes3"
)

// storeAddr is where the team's bucket is served. The pictures show it, so
// it is the port that the documentation uses for the in-memory server, and
// not a free one.
const storeAddr = "127.0.0.1:9000"

// A store is the S3-compatible server that holds the state of the team
// situation, in memory, for as long as the program runs.
type store struct {
	// Endpoint is the address, as the specification names it.
	Endpoint string
	srv      *http.Server
	done     chan struct{}
}

// startStore serves an empty bucket on storeAddr.
func startStore() (*store, error) {
	ln, err := net.Listen("tcp", storeAddr)
	if err != nil {
		return nil, fmt.Errorf("the team situation serves its bucket on %s, which is in use: %w", storeAddr, err)
	}
	s := &store{Endpoint: "http://" + storeAddr, srv: &http.Server{Handler: fakes3.New()}, done: make(chan struct{})}
	go func() {
		defer close(s.done)
		_ = s.srv.Serve(ln)
	}()
	return s, nil
}

// stop ends the server.
func (s *store) stop() {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_ = s.srv.Shutdown(ctx)
	<-s.done
}

// storeEnv is the environment that a command needs to reach the store: the
// server accepts any credentials, and the specification names the endpoint.
var storeEnv = []string{"AWS_ACCESS_KEY_ID=test", "AWS_SECRET_ACCESS_KEY=test"}

// fakeProviderPkg is the provider that lists objects without a cloud.
const fakeProviderPkg = "github.com/pravo-systems/pravo/tools/fakeprovider"

// buildFakeProvider builds the fake provider into dir, with the go tool, and
// returns its path. The program is run from a checkout of the module, as
// "go run ./tools/serveshots", so the tool finds the package there.
func buildFakeProvider(ctx context.Context, dir string) (string, error) {
	name := "terraform-provider-demo"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	out := filepath.Join(dir, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	cmd := exec.CommandContext(ctx, "go", "build", "-o", out, fakeProviderPkg)
	if b, err := exec.CommandContext(ctx, "go", "env", "GOMOD").Output(); err != nil || strings.TrimSpace(string(b)) == "" || strings.TrimSpace(string(b)) == os.DevNull {
		return "", fmt.Errorf("building the fake provider needs the pravo module: run the program from a checkout of it, or give -fakeprovider")
	}
	if b, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("building the fake provider: %v\n%s", err, indent(strings.TrimSpace(string(b))))
	}
	return out, nil
}
