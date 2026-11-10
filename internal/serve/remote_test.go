package serve

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/pravo-systems/pravo/internal/config"
	"github.com/pravo-systems/pravo/internal/fakes3"
	"github.com/pravo-systems/pravo/internal/state"
)

// A state in a bucket is read the same way, and its changes are found by listing it.
func TestStateInABucket(t *testing.T) {
	f := fakes3.New()
	ts := httptest.NewServer(f)
	t.Cleanup(ts.Close)
	b, err := state.NewS3Backend(context.Background(), state.S3Config{Bucket: "state", Prefix: "team/app", Region: "us-east-1", Endpoint: ts.URL,
		PathStyle: true, AccessKey: "test", SecretKey: "test"})
	if err != nil {
		t.Fatal(err)
	}
	st, err := state.OpenBackend(context.Background(), b)
	if err != nil {
		t.Fatal(err)
	}
	seed(t, st)
	dir := t.TempDir()
	writeSpec(t, dir, specCUE+fmt.Sprintf(`
$settings: state: {backend: "s3", bucket: "state", prefix: "team/app", region: "us-east-1", endpoint: %q, path_style: true, access_key: "test", secret_key: "test"}
`, ts.URL))
	opts := Options{OpenState: func(ctx context.Context, root *config.Root) (*state.Store, error) {
		return state.OpenBackend(ctx, ReadOnly(b))
	}}
	before := len(f.Objects)
	p := start(t, dir, st, opts)
	g := p.graph()
	if g.Project.State.Backend != "s3" || g.Project.State.Records != 5 || len(resourceNodes(g)) != 8 || g.Project.SpecError != "" {
		t.Fatalf("project %+v nodes %d, spec error %q", g.Project.State, len(g.Nodes), g.Project.SpecError)
	}
	noSecrets(t, "graph", p.get("/api/graph"))
	for _, n := range g.Nodes {
		noSecrets(t, "detail of "+n.Addr, p.get("/api/detail?addr="+queryEscape(n.Addr)))
	}
	if len(f.Objects) != before {
		t.Errorf("the bucket holds %d objects, it held %d before the viewer", len(f.Objects), before)
	}
	if err := st.Set(record("hosts.x", "fake_db", `{"name":"x"}`), ""); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the new record to show", func() bool { n := nodeOf(p.graph(), "hosts.x"); return n != nil && n.Presence == "both" })
}

// A look at a state in a bucket reads none of the objects of the stored plans and of the
// discoveries: the listing of a bucket says which version of each it holds, and a progress
// object or a run that was replaced has another. The look is repeated every few seconds for as
// long as the viewer runs, so a read for each object was a read for each object every time.
func TestALookAtABucketReadsNoPlanAndNoDiscovery(t *testing.T) {
	f := fakes3.New()
	ts := httptest.NewServer(f)
	t.Cleanup(ts.Close)
	ctx := context.Background()
	b, err := state.NewS3Backend(ctx, state.S3Config{Bucket: "state", Prefix: "team/app", Region: "us-east-1", Endpoint: ts.URL,
		PathStyle: true, AccessKey: "test", SecretKey: "test"})
	if err != nil {
		t.Fatal(err)
	}
	st, err := state.OpenBackend(ctx, b)
	if err != nil {
		t.Fatal(err)
	}
	seed(t, st)
	progress, run := "plans/plan-0a1b2c3d.progress.json", state.DiscoveryRunDir+"/20261110T101500Z-0a1b2c.json"
	for _, key := range []string{progress, "plans/plan-0a1b2c3d.json", run} {
		if err := b.Put(ctx, key, []byte(`{"n":1}`)); err != nil {
			t.Fatal(err)
		}
	}
	var mu sync.Mutex
	reads := map[string]int{}
	f.Fault = func(r *http.Request) (int, string) {
		if r.Method == http.MethodGet && !r.URL.Query().Has("list-type") {
			mu.Lock()
			reads[strings.TrimPrefix(r.URL.Path, "/state/team/app/")]++
			mu.Unlock()
		}
		return 0, ""
	}
	view := ReadOnly(b)
	stateSig, _, plansSig, discoverySig := remoteStateSignature(view)
	mu.Lock()
	for key, n := range reads {
		if key != "meta.json" && key != "outputs.json" {
			t.Errorf("the look read %s %d times, want only meta.json and outputs.json to be read", key, n)
		}
	}
	mu.Unlock()
	// What is replaced under its key is still seen.
	if err := b.Put(ctx, progress, []byte(`{"n":2}`)); err != nil {
		t.Fatal(err)
	}
	if err := b.Put(ctx, run, []byte(`{"n":2}`)); err != nil {
		t.Fatal(err)
	}
	if err := st.Set(record("db.main", "fake_db", `{"name":"other"}`), ""); err != nil {
		t.Fatal(err)
	}
	stateSig2, _, plansSig2, discoverySig2 := remoteStateSignature(view)
	if plansSig2 == plansSig {
		t.Error("a progress object that was replaced leaves the signature of the plans as it was")
	}
	if discoverySig2 == discoverySig {
		t.Error("a run that was replaced leaves the signature of the discoveries as it was")
	}
	if stateSig2 == stateSig {
		t.Error("a record that was written again leaves the signature of the state as it was")
	}
}

// plainBackend is a backend whose listing says nothing of versions, as one behind an HTTP
// server is.
type plainBackend struct{ state.Backend }

// A backend that lists no versions is still signed by reading the progress objects of its
// plans, and only those.
func TestABackendWithoutVersionsIsSignedByReadingProgress(t *testing.T) {
	f := fakes3.New()
	ts := httptest.NewServer(f)
	t.Cleanup(ts.Close)
	ctx := context.Background()
	s3, err := state.NewS3Backend(ctx, state.S3Config{Bucket: "state", Prefix: "team/app", Region: "us-east-1", Endpoint: ts.URL,
		PathStyle: true, AccessKey: "test", SecretKey: "test"})
	if err != nil {
		t.Fatal(err)
	}
	b := plainBackend{s3}
	if _, ok := state.VersionsOf(b); ok {
		t.Fatal("the plain backend lists versions")
	}
	progress := "plans/plan-0a1b2c3d.progress.json"
	for _, key := range []string{progress, "plans/plan-0a1b2c3d.json", "plans/other.progress.json"} {
		if err := b.Put(ctx, key, []byte(`{"n":1}`)); err != nil {
			t.Fatal(err)
		}
	}
	var mu sync.Mutex
	reads := map[string]int{}
	f.Fault = func(r *http.Request) (int, string) {
		if r.Method == http.MethodGet && !r.URL.Query().Has("list-type") {
			mu.Lock()
			reads[strings.TrimPrefix(r.URL.Path, "/state/team/app/")]++
			mu.Unlock()
		}
		return 0, ""
	}
	sig := remotePlansSignature(ctx, b)
	mu.Lock()
	if len(reads) != 1 || reads[progress] != 1 {
		t.Errorf("read %v, want only %s once", reads, progress)
	}
	mu.Unlock()
	if err := b.Put(ctx, progress, []byte(`{"n":2}`)); err != nil {
		t.Fatal(err)
	}
	if remotePlansSignature(ctx, b) == sig {
		t.Error("a progress object that was replaced leaves the signature as it was")
	}
}
