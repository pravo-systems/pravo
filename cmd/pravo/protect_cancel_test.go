package main

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/pravo-systems/pravo/internal/fakes3"
	"github.com/pravo-systems/pravo/internal/state"
)

// runPravoCtx is runPravo for a command whose context the test cancels, as a
// signal does in main.
func runPravoCtx(t *testing.T, ctx context.Context, dir string, args ...string) (string, int) {
	t.Helper()
	wd, _ := os.Getwd()
	defer os.Chdir(wd) //nolint:usetesting // the directory is put back after each call, so that a relative path in the next call means what it meant in the first
	r, w, _ := os.Pipe()
	stdout := os.Stdout
	os.Stdout = w
	done := make(chan []byte)
	go func() { b, _ := io.ReadAll(r); done <- b }()
	root := newRootCmd()
	root.SetArgs(normaliseArgs(append([]string{"pravo", "-C", dir}, args...))[1:])
	err := root.ExecuteContext(ctx)
	w.Close()
	os.Stdout = stdout
	text := string(<-done)
	if err != nil {
		return text + "Error: " + err.Error() + "\n", exitCodeOf(err)
	}
	return text, 0
}

// 'pravo protect init' that is interrupted leaves a state that the command
// finishes when it is run again, and writers meanwhile are refused. The command
// is cancelled when the bucket has taken some of the records.
func TestProtectInitInterruptedIsResumed(t *testing.T) {
	requireSealing(t)
	ids := isolateIdentities(t)
	t.Setenv("PRAVO_HOME", t.TempDir())
	t.Setenv("PRAVO_S3_PROBE", "off")

	const records, interruptAfter = 300, 40
	f := fakes3.New()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var recPuts atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPut && strings.Contains(r.URL.Path, "/rec/") && recPuts.Add(1) == interruptAfter {
			cancel()
		}
		f.ServeHTTP(w, r)
	}))
	t.Cleanup(srv.Close)
	dir := specDir(t, map[string]string{"main.cue": "package t\n\n" +
		`$settings: state: {backend: "s3", bucket: "northwind-state", prefix: "prod", region: "eu-west-1", endpoint: "` + srv.URL + `", path_style: true, access_key: "test", secret_key: "test"}` + "\n"})
	e := &s3Env{dir: dir, f: f, url: srv.URL}
	st := e.store(t)
	for i := range records {
		e.record(t, st, "r"+string(rune('a'+i%26))+strings.Repeat("x", i/26), secretA)
	}
	_, ada := person(t, ids, "ada")
	e.readers(t, ada)
	puts := recPuts.Load()

	text, code := runPravoCtx(t, ctx, e.dir, "protect", "init", "--encrypt", "--yes")
	if code == 0 || !strings.Contains(text, "interrupted after") || !strings.Contains(text, "Run the command again to finish") {
		t.Fatalf("an interrupted init: exit %d\n%s", code, text)
	}
	if n := recPuts.Load() - puts; n >= records {
		t.Fatalf("the interrupted init wrote %d of %d records", n, records)
	}

	status, code := runPravo(t, e.dir, "protect", "status")
	if code != 0 || !strings.Contains(status, "Sealed       migrating · ") || !strings.Contains(status, "run pravo protect init --encrypt to finish") {
		t.Fatalf("status after the interrupt: exit %d\n%s", code, status)
	}
	if text, code := runPravo(t, e.dir, "state", "rm", "ra"); code == 0 || !strings.Contains(text, "a seal is in progress; run pravo protect init --encrypt to finish") {
		t.Errorf("a writer during the migration: exit %d\n%s", code, text)
	}

	text, code = runPravo(t, e.dir, "protect", "init", "--encrypt", "--yes")
	if code != 0 || !strings.Contains(text, "The state is sealed to ada.") {
		t.Fatalf("init run again: exit %d\n%s", code, text)
	}
	status, code = runPravo(t, e.dir, "protect", "status")
	if code != 0 || !strings.Contains(status, "Sealed       on · format 3 · 1 reader (ada)") || strings.Contains(status, "Untrusted") {
		t.Fatalf("status after the second run: exit %d\n%s", code, status)
	}
	if strings.Contains(e.bucketText(), secretA) {
		t.Error("the secret is still in the bucket")
	}
	b, err := state.OpenBackendWith(context.Background(), e.backend(t), state.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if n := b.Len(); n != records {
		t.Errorf("the sealed state lists %d records, want %d", n, records)
	}
}
