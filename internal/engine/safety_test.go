package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hashicorp/terraform-plugin-go/tftypes"

	"pravo.dev/pravo/internal/schema"
	"pravo.dev/pravo/internal/state"
)

// targetSpec is a specification with two provider instances. The instance fake
// manages a, b and the data source s; the instance fake.docs manages d.
func targetSpec(region, token string) spec {
	return spec{
		cue: fmt.Sprintf("prov: {region: %q, token: %q}\nprov2: {region: \"us-1\"}\n", region, token) +
			"a: " + thing("a", "content: \"x\"") + "\n" +
			"b: " + thing("b", "content: \"x\"") + "\n" +
			"d: " + thing("d", "content: \"x\"") + "\n" +
			"s: " + source("s") + "\n",
		resources: []string{"a", "b", "d"},
		data:      []string{"s"},
		providers: map[string]string{"fake": "prov", "fake.docs": "prov2"},
		keys:      map[string]string{"d": "fake.docs"},
	}
}

const targetMoved = "the non-sensitive configuration of its provider changed"

// A plan reads the records of a provider instance whose configuration points
// somewhere else, whatever their refresh age, and does not plan them unless the
// read finds drift. A credential that rotates changes nothing, a record that
// carries no digest is not read, and the records of another instance are left
// alone.
func TestProviderTargetChangeReadsItsRecords(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t)
	h.apply(targetSpec("eu-1", "t1"))
	stamp := func(addr string) string { return h.state.Get(addr).ProviderConfig }
	for _, addr := range []string{"a", "b", "d", "s"} {
		if stamp(addr) == "" {
			t.Fatalf("%s carries no digest of its provider's configuration after an apply", addr)
		}
	}
	if stamp("a") != stamp("b") || stamp("a") != stamp("s") || stamp("a") == stamp("d") {
		t.Errorf("digests: a %s, b %s, s %s, d %s; want the instance's records to share one and the other instance to have another", stamp("a"), stamp("b"), stamp("s"), stamp("d"))
	}

	// plan makes a plan against records that are far inside their refresh age.
	plan := func(s spec) *Plan {
		t.Helper()
		h.fake.resetCalls()
		p, err := h.engine(s, Options{DefaultRefresh: "6h"}).Plan(ctx)
		if err != nil {
			t.Fatal(err)
		}
		return p
	}
	quiet := func(what string, p *Plan) {
		t.Helper()
		if got := h.fake.readNames(); len(got) != 0 {
			t.Errorf("%s: read %v, want nothing", what, got)
		}
		for _, call := range []string{"ReadResource", "ReadDataSource", "UpgradeResourceState", "PlanResourceChange", "start"} {
			if n := h.fake.called(call); n != 0 {
				t.Errorf("%s: %s was called %d times, want 0", what, call, n)
			}
		}
		if p.HasChanges() {
			t.Errorf("%s: the plan has changes", what)
		}
	}

	quiet("nothing changed", plan(targetSpec("eu-1", "t1")))
	quiet("a credential rotated", plan(targetSpec("eu-1", "t2")))

	p := plan(targetSpec("eu-2", "t2"))
	if got := h.fake.readNames(); !slices.Equal(got, []string{"a", "b"}) {
		t.Errorf("a changed region read %v, want a and b", got)
	}
	if n := h.fake.called("ReadDataSource"); n != 1 {
		t.Errorf("a changed region read the data source %d times, want 1", n)
	}
	if n := h.fake.called("PlanResourceChange"); n != 0 || p.HasChanges() {
		t.Errorf("a changed region planned %d resources (changes: %v); it only reads", n, p.HasChanges())
	}
	if !hasReason(reasonsOf(p, "a"), targetMoved) {
		t.Errorf("a: reasons = %q, want %q", reasonsOf(p, "a"), targetMoved)
	}
	if hasReason(reasonsOf(p, "d"), targetMoved) {
		t.Errorf("d is managed by another instance and was not retargeted: %q", reasonsOf(p, "d"))
	}
	if p.Stats.Refreshed != 3 {
		t.Errorf("refreshed = %d, want 3 (a, b and s)", p.Stats.Refreshed)
	}
	if stamp("a") == stamp("b") && stamp("a") == stamp("s") && stamp("a") != "" {
		// The read wrote the digest of the new configuration.
	} else {
		t.Errorf("after the read: a %s, b %s, s %s; want one new digest", stamp("a"), stamp("b"), stamp("s"))
	}
	quiet("the plan after the read", plan(targetSpec("eu-2", "t2")))

	// A record that carries no digest is not read, whatever the configuration.
	bare := *h.state.Get("a")
	bare.ProviderConfig = ""
	if err := h.state.Set(&bare, "test"); err != nil {
		t.Fatal(err)
	}
	h.reopen()
	plan(targetSpec("eu-3", "t2"))
	if got := h.fake.readNames(); !slices.Equal(got, []string{"b"}) {
		t.Errorf("read %v, want b alone: a has no digest", got)
	}
	if stamp("a") != "" {
		t.Errorf("a was not read and was stamped: %s", stamp("a"))
	}

	// The next write stamps it.
	s := targetSpec("eu-3", "t2")
	s.cue = strings.Replace(s.cue, "a: "+thing("a", "content: \"x\""), "a: "+thing("a", "content: \"y\""), 1)
	h.apply(s)
	if stamp("a") == "" || stamp("a") != stamp("b") {
		t.Errorf("after an apply: a %s, b %s; want the same digest", stamp("a"), stamp("b"))
	}
}

// A plan that walks again, because a record it read as an input differs from
// its state, still says why it read a node whatever the age of the node.
func TestForcedReadKeepsItsReasonWhenThePlanWalksAgain(t *testing.T) {
	src := func(region, tag string) spec {
		return spec{
			cue: fmt.Sprintf("prov: {region: %q}\nprov2: {region: \"us-1\"}\n", region) +
				"in: " + thing("in", "") + "\n" +
				"top: " + thing("top", `content: "\(in.id)-`+tag+`"`+"\n$lifecycle: refresh: \"1h\"") + "\n",
			resources: []string{"in", "top"},
			providers: map[string]string{"fake": "prov", "fake.docs": "prov2"},
			keys:      map[string]string{"in": "fake.docs"},
		}
	}
	h := newHarness(t)
	h.apply(src("eu-1", "1"))
	time.Sleep(5 * time.Millisecond)
	h.fake.drift("in", "changed")
	h.fake.resetCalls()

	p, err := h.engine(src("eu-2", "2"), Options{DefaultRefresh: "1ms"}).Plan(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got := h.fake.readNames(); !slices.Equal(got, []string{"in", "top"}) {
		t.Errorf("read %v, want each once", got)
	}
	if ch := changeOf(t, p, "in"); !ch.Drifted || ch.Action != ActionUpdate {
		t.Fatalf("in: %s, drifted %v; the plan did not walk again from the drift", ch.Action, ch.Drifted)
	}
	if got := reasonsOf(p, "top"); !hasReason(got, targetMoved) {
		t.Errorf("top: reasons = %q, want %q", got, targetMoved)
	}
}

// A provider with no attribute that says where it points leaves no digest, and
// costs nothing.
func TestProviderWithoutTargetAttributesIsNotStamped(t *testing.T) {
	h := newHarness(t)
	s := spec{cue: "prov: {token: \"secret\"}\na: " + thing("a", "") + "\n", resources: []string{"a"}, providers: map[string]string{"fake": "prov"}}
	h.apply(s)
	if got := h.state.Get("a").ProviderConfig; got != "" {
		t.Errorf("a configuration with a sensitive attribute alone left the digest %q", got)
	}
	h2 := newHarness(t)
	h2.apply(spec{cue: "a: " + thing("a", "") + "\n", resources: []string{"a"}})
	if got := h2.state.Get("a").ProviderConfig; got != "" {
		t.Errorf("a provider with no configuration left the digest %q", got)
	}
}

// The digest leaves out what is not known, and what is made of an ephemeral
// value. A configuration with an unknown value has no digest to compare yet.
func TestProviderTargetLeavesOutWhatIsNotStable(t *testing.T) {
	h := newHarness(t)
	digest := func(s spec) (string, bool) {
		t.Helper()
		e := h.engine(s, Options{})
		if _, err := e.buildGraph(); err != nil {
			t.Fatal(err)
		}
		return e.providerTarget("fake")
	}
	fixed := spec{cue: "prov: {region: \"eu-1\"}\n", providers: map[string]string{"fake": "prov"}}
	if d, ok := digest(fixed); !ok || d == "" {
		t.Errorf("a fixed region: digest %q, ok %v", d, ok)
	}
	// A region that comes from a resource not yet created is unknown.
	unknown := spec{
		cue:       "prov: {region: a.content}\na: " + thing("a", "content: \"\\(b.id)\"") + "\nb: " + thing("b", "") + "\n",
		resources: []string{"a", "b"}, providers: map[string]string{"fake": "prov", "fake.docs": "unset"},
		keys: map[string]string{"a": "fake.docs", "b": "fake.docs"},
	}
	if d, ok := digest(unknown); ok {
		t.Errorf("an unknown region: digest %q, want none", d)
	}
	// A region that comes from an ephemeral value changes on every run.
	ephemeral := spec{
		cue:        "prov: {region: sess.token}\nsess: {name: \"sess\", token: string | null}\n",
		ephemerals: []string{"sess"}, providers: map[string]string{"fake": "prov"},
	}
	if d, ok := digest(ephemeral); !ok || d != "" {
		t.Errorf("an ephemeral region: digest %q, ok %v, want an empty digest", d, ok)
	}
}

func TestTargetBlock(t *testing.T) {
	str := func(name string, sensitive bool) *schema.Attribute {
		return &schema.Attribute{Name: name, TypeJSON: json.RawMessage(`"string"`), Optional: true, Sensitive: sensitive}
	}
	inner := &schema.Block{Attributes: []*schema.Attribute{str("role", false), str("external_id", true)}}
	blk := &schema.Block{
		Attributes: []*schema.Attribute{
			str("region", false), str("token", true), str("profile", false),
			{Name: "tags", TypeJSON: json.RawMessage(`["map","string"]`), Optional: true},
			{Name: "nested", Optional: true, NestedType: &schema.Object{Nesting: schema.NestingSingle, Attributes: []*schema.Attribute{str("host", false), str("key", true)}}},
		},
		BlockTypes: []*schema.NestedBlock{{TypeName: "assume_role", Block: inner, Nesting: schema.NestingList}},
	}
	typ := blk.Type()
	nestedT := blk.Attributes[4].Type()
	roleT := inner.Type()
	obj := func(region, token, profile any, role any, nested any) tftypes.Value {
		val := func(t tftypes.Type, v any) tftypes.Value { return tftypes.NewValue(t, v) }
		roles := []tftypes.Value{}
		if role != nil {
			roles = append(roles, role.(tftypes.Value))
		}
		n := val(nestedT, nil)
		if nested != nil {
			n = nested.(tftypes.Value)
		}
		return tftypes.NewValue(typ, map[string]tftypes.Value{
			"region": val(tftypes.String, region), "token": val(tftypes.String, token), "profile": val(tftypes.String, profile),
			"tags": val(tftypes.Map{ElementType: tftypes.String}, nil), "nested": n,
			"assume_role": tftypes.NewValue(tftypes.List{ElementType: roleT}, roles),
		})
	}
	role := func(r, ext any) tftypes.Value {
		return tftypes.NewValue(roleT, map[string]tftypes.Value{"role": tftypes.NewValue(tftypes.String, r), "external_id": tftypes.NewValue(tftypes.String, ext)})
	}
	nested := func(host, key any) tftypes.Value {
		return tftypes.NewValue(nestedT, map[string]tftypes.Value{"host": tftypes.NewValue(tftypes.String, host), "key": tftypes.NewValue(tftypes.String, key)})
	}
	unk := tftypes.UnknownValue
	cases := []struct {
		name    string
		v       tftypes.Value
		skip    map[string]bool
		want    string
		unknown bool
	}{
		{"a region", obj("eu-1", nil, nil, nil, nil), nil, `{"region":"eu-1"}`, false},
		{"a sensitive attribute is left out", obj("eu-1", "s3cret", nil, nil, nil), nil, `{"region":"eu-1"}`, false},
		{"a null attribute is an absent one", obj("eu-1", nil, nil, nil, nil), nil, `{"region":"eu-1"}`, false},
		{"nothing", obj(nil, "s3cret", nil, nil, nil), nil, `{}`, false},
		{"a block", obj("eu-1", nil, nil, role("r1", "x"), nil), nil, `{"assume_role":[{"role":"r1"}],"region":"eu-1"}`, false},
		{"a sensitive attribute of a block", obj("eu-1", nil, nil, role("r1", "y"), nil), nil, `{"assume_role":[{"role":"r1"}],"region":"eu-1"}`, false},
		{"a nested attribute", obj(nil, nil, nil, nil, nested("h", "k")), nil, `{"nested":{"host":"h"}}`, false},
		{"an unknown attribute", obj("eu-1", nil, unk, nil, nil), nil, `{"region":"eu-1"}`, true},
		{"an unknown attribute in a block", obj("eu-1", nil, nil, role(unk, nil), nil), nil, `{"assume_role":[{}],"region":"eu-1"}`, true},
		{"an unknown sensitive attribute", obj("eu-1", unk, nil, nil, nil), nil, `{"region":"eu-1"}`, false},
		{"an attribute that is skipped", obj("eu-1", nil, unk, nil, nil), map[string]bool{"profile": true}, `{"region":"eu-1"}`, false},
	}
	for _, c := range cases {
		unknown := false
		got, _ := json.Marshal(targetBlock(blk, c.v, c.skip, &unknown))
		if string(got) != c.want || unknown != c.unknown {
			t.Errorf("%s: %s (unknown %v), want %s (unknown %v)", c.name, got, unknown, c.want, c.unknown)
		}
	}
}

// plantStaleLock leaves the lock of an apply that was killed: its process is gone
// and nothing has renewed the lock for an hour.
func plantStaleLock(t *testing.T, st *state.Store, addr, txn string) state.Lock {
	t.Helper()
	host, _ := os.Hostname()
	old := time.Now().Add(-time.Hour).UTC()
	l := state.Lock{Addr: addr, PID: 999999, Host: host, User: "alice", Actor: "alice", Command: "pravo apply", Txn: txn, Nonce: "dead", Since: old, RenewedAt: old, Kind: "lock"}
	data, _ := json.Marshal(l)
	if err := st.Backend().Put(context.Background(), "locks/"+addr+".lock", data); err != nil {
		t.Fatal(err)
	}
	return l
}

func locksHeld(t *testing.T, st *state.Store) []string {
	t.Helper()
	locks, err := st.Locks()
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, l := range locks {
		out = append(out, l.Addr)
	}
	return out
}

// A node whose lock an apply left behind is read by the next plan whatever its
// refresh age, and the read removes the lock. Nothing else is read.
func TestStaleLockForcesAReadRegardlessOfAge(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t)
	s := spec{cue: "a: " + thing("a", "content: \"x\"") + "\nb: " + thing("b", "content: \"x\"") + "\nc: " + thing("c", "content: \"x\"") + "\n", resources: []string{"a", "b", "c"}}
	h.apply(s)
	host, _ := os.Hostname()
	want := fmt.Sprintf("an apply (txn T1, alice on host %s) stopped while this node was locked; its real state is read again whatever the refresh age", host)

	// With no stale lock, a plan inside the refresh age reads nothing, and looks at
	// the locks once.
	h.fake.resetCalls()
	h.backend.Reset()
	if _, err := h.engine(s, Options{DefaultRefresh: "6h"}).Plan(ctx); err != nil {
		t.Fatal(err)
	}
	if got := h.fake.readNames(); len(got) != 0 {
		t.Fatalf("read %v with no stale lock", got)
	}
	if gets := h.backend.Count("Get", "locks"); gets != 0 {
		t.Errorf("the plan read %d locks of a store that has none", gets)
	}
	h.backend.Reset()
	if _, err := h.state.Suspects(); err != nil {
		t.Fatal(err)
	}
	if lists, gets := h.backend.Count("List", "locks"), h.backend.Count("Get", "locks"); lists != 1 || gets != 0 {
		t.Errorf("looking for suspects listed the locks %d times and read %d of them, want 1 and 0", lists, gets)
	}

	// A lock that is not stale is a running apply, and says nothing.
	live := state.Lock{Addr: "c", PID: os.Getpid(), Host: host, Command: "pravo apply", Txn: "T0", Nonce: "other", Since: time.Now().UTC(), RenewedAt: time.Now().UTC(), Kind: "lock"}
	data, _ := json.Marshal(live)
	if err := h.state.Backend().Put(ctx, "locks/c.lock", data); err != nil {
		t.Fatal(err)
	}
	plantStaleLock(t, h.state, "b", "T1")
	h.fake.resetCalls()
	plan, err := h.engine(s, Options{DefaultRefresh: "6h"}).Plan(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got := h.fake.readNames(); !slices.Equal(got, []string{"b"}) {
		t.Errorf("read %v, want b alone", got)
	}
	if !hasReason(reasonsOf(plan, "b"), want) {
		t.Errorf("b: reasons = %q, want %q", reasonsOf(plan, "b"), want)
	}
	if plan.Stats.Refreshed != 1 || plan.HasChanges() {
		t.Errorf("refreshed %d, changes %v; want 1 refreshed and no changes", plan.Stats.Refreshed, plan.HasChanges())
	}
	if got := locksHeld(t, h.state); !slices.Equal(got, []string{"c"}) {
		t.Errorf("locks after the plan: %v, want the running apply's lock on c alone", got)
	}

	// The next plan has no stale lock left, and reads nothing.
	h.fake.resetCalls()
	if _, err := h.engine(s, Options{DefaultRefresh: "6h"}).Plan(ctx); err != nil {
		t.Fatal(err)
	}
	if got := h.fake.readNames(); len(got) != 0 {
		t.Errorf("the next plan read %v", got)
	}

	// The modes that read by age read the node. -refresh=false reads nothing, says
	// so, and leaves the lock.
	for _, c := range []struct {
		mode  RefreshMode
		reads []string
	}{{RefreshAge, []string{"b"}}, {RefreshExpired, []string{"b"}}, {RefreshNone, nil}} {
		plantStaleLock(t, h.state, "b", "T1")
		var mu sync.Mutex
		var said []string
		h.fake.resetCalls()
		opts := Options{Refresh: c.mode, DefaultRefresh: "6h", Progress: func(f string, a ...any) {
			mu.Lock()
			defer mu.Unlock()
			said = append(said, fmt.Sprintf(f, a...))
		}}
		if _, err := h.engine(s, opts).Plan(ctx); err != nil {
			t.Fatal(err)
		}
		if got := h.fake.readNames(); !slices.Equal(got, c.reads) {
			t.Errorf("mode %d read %v, want %v", c.mode, got, c.reads)
		}
		warned := false
		for _, m := range said {
			warned = warned || (strings.HasPrefix(m, "Warning: b: an apply (txn T1") && strings.Contains(m, "-refresh=false"))
		}
		if warned != (c.mode == RefreshNone) {
			t.Errorf("mode %d: warned %v, messages %q", c.mode, warned, said)
		}
		if c.mode == RefreshNone {
			if got := locksHeld(t, h.state); !slices.Contains(got, "b") {
				t.Errorf("-refresh=false removed the stale lock")
			}
			if _, err := h.state.ForceUnlock("b"); err != nil {
				t.Fatal(err)
			}
		}
	}
}
