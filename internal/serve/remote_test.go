package serve

import (
	"context"
	"fmt"
	"net/http/httptest"
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
