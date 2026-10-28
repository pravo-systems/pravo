package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"cuelang.org/go/cue"
	"cuelang.org/go/cue/cuecontext"
	"github.com/hashicorp/terraform-plugin-go/tftypes"

	"github.com/pravo-systems/pravo/internal/config"
	"github.com/pravo-systems/pravo/internal/plugin"
	"github.com/pravo-systems/pravo/internal/registry"
	"github.com/pravo-systems/pravo/internal/schema"
	"github.com/pravo-systems/pravo/internal/state"
	"github.com/pravo-systems/pravo/internal/state/statetest"
)

// fakeProvider is an in-process provider of one resource type, fake_thing,
// with a computed id, a required name that cannot change in place and an
// optional content. It lets a test run the engine without a plugin process.
// effect is what the apply of one resource does to another.
type effect struct{ target, content string }

type fakeProvider struct {
	mu   sync.Mutex
	next int
	// calls counts what reaches the provider, by name: the RPCs of the
	// protocol and "start" for each time the engine launches the provider.
	calls map[string]int
	// drifted and vanished change what ReadResource reports for the resource
	// whose name attribute is the key: another content, or no object at all.
	drifted  map[string]string
	vanished map[string]bool
	// sideEffect makes the apply of the resource with this name change another
	// resource that exists already: ReadResource reports the content that it names
	// for the target from then on, as a provider whose attachment changes the role.
	sideEffect map[string]effect
	// read lists the names of the resources that ReadResource was asked about,
	// in order.
	read []string
	// partial makes ApplyResourceChange of the resource with this name fail
	// after the object came to exist without its content.
	partial map[string]bool
	// replanned makes PlanResourceChange of the resource with this name plan this
	// content instead of the configured one, as a provider whose answer changed
	// since the plan was made.
	replanned map[string]string
	// data makes ReadDataSource answer this content for the data source whose name
	// attribute is the key, as a file that changed since the last read.
	data map[string]string
	// latency holds up every ReadResource and every destroy by this long, as a
	// provider that calls a remote API does.
	latency time.Duration
	// undestroyable makes the destroy of the resource with this name fail.
	undestroyable map[string]bool
	// upgradeJitter holds up every UpgradeResourceState by up to this long, by a
	// time that depends on the record, so that the calls end in no particular
	// order.
	upgradeJitter time.Duration
	// destroys lists the destroys in the order they started and ended: a start
	// and an end each take the next number. peak is the most destroys that ran at
	// the same time.
	destroys []destroyRecord
	running  int
	peak     int
	tick     int
	// holds makes ApplyResourceChange of the resource with this name wait; see
	// hold. stops counts the calls to Stop. A provider that is deaf does not hear
	// Stop: it goes on with what it does.
	holds map[string]*hold
	stops int
	deaf  bool
	stop  chan struct{}
	// configured holds the configuration of each call to Configure.
	configured []tftypes.Value
	// listed is what ListResource answers for a list type, in order; listFails
	// makes the call fail with this text. lists holds the scope of each call.
	listed    map[string][]plugin.ListedResource
	listFails string
	lists     []tftypes.Value
	// importable maps the ids that ImportResourceState finds to the content of the
	// object.
	importable map[string]string
	// importTargets are the targets that ImportResourceState was asked for.
	importTargets []plugin.ImportTarget
}

// canImport makes ImportResourceState find the object with this id.
func (f *fakeProvider) canImport(id, content string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.importable == nil {
		f.importable = map[string]string{}
	}
	f.importable[id] = content
}

// list sets the objects that ListResource answers for the list type typ.
func (f *fakeProvider) list(typ string, objects ...plugin.ListedResource) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.listed == nil {
		f.listed = map[string][]plugin.ListedResource{}
	}
	f.listed[typ] = objects
}

// ListResource answers the objects of the type that are in the scope: the
// scope attribute region keeps the objects of that region, name_prefix the
// ones whose display name starts with it.
func (f *fakeProvider) ListResource(_ context.Context, typ string, cfg tftypes.Value, limit int64) ([]plugin.ListedResource, plugin.Diagnostics, error) {
	f.count("ListResource")
	f.mu.Lock()
	defer f.mu.Unlock()
	f.lists = append(f.lists, cfg)
	if f.listFails != "" {
		return nil, nil, fmt.Errorf("%s", f.listFails)
	}
	var m map[string]tftypes.Value
	var region, prefix string
	if cfg.As(&m) == nil {
		_ = m["region"].As(&region)
		_ = m["name_prefix"].As(&prefix)
	}
	var out []plugin.ListedResource
	for _, o := range f.listed[typ] {
		if r, _ := o.Identity["region"].(string); region != "" && r != region {
			continue
		}
		if !strings.HasPrefix(o.DisplayName, prefix) {
			continue
		}
		out = append(out, o)
		if limit > 0 && int64(len(out)) >= limit {
			break
		}
	}
	return out, nil, nil
}

// hold is the apply of one resource that waits for the test. reached is closed
// when the apply has begun. The apply ends when the test calls let, which makes
// it succeed, or refuse, which makes the provider say it failed, or when the
// provider is stopped.
type hold struct {
	reached chan struct{}
	release chan error
}

// let ends the apply of the resource as if the provider had done its work.
func (h *hold) let() { h.release <- nil }

// refuse ends the apply of the resource with an error and no state.
func (h *hold) refuse() { h.release <- fmt.Errorf("the provider could not apply the change") }

// hold makes the apply of the resource named name wait for the test, from now on.
func (f *fakeProvider) hold(name string) *hold {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.holds == nil {
		f.holds = map[string]*hold{}
	}
	h := &hold{reached: make(chan struct{}), release: make(chan error, 1)}
	f.holds[name] = h
	return h
}

// waitFor blocks until the apply of every resource held is under way.
func waitFor(t *testing.T, holds ...*hold) {
	t.Helper()
	for _, h := range holds {
		select {
		case <-h.reached:
		case <-time.After(10 * time.Second):
			t.Fatal("the apply of a held resource did not begin")
		}
	}
}

// stopped returns how many times the provider was asked to stop.
func (f *fakeProvider) stopped() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.stops
}

// goDeaf makes the provider ignore Stop, from now on.
func (f *fakeProvider) goDeaf() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.deaf = true
}

// await makes the apply of name wait if the test holds it, and reports the error
// the apply ends with. A call whose context is cancelled ends at once, as a call
// over the wire does.
func (f *fakeProvider) await(ctx context.Context, name string) error {
	f.mu.Lock()
	h := f.holds[name]
	if f.stop == nil {
		f.stop = make(chan struct{})
	}
	stop := f.stop
	f.mu.Unlock()
	if h == nil {
		return nil
	}
	close(h.reached)
	select {
	case err := <-h.release:
		return err
	case <-stop:
		return fmt.Errorf("the provider was stopped")
	case <-ctx.Done():
		return ctx.Err()
	}
}

// destroyRecord is the destroy of the resource called name, between the numbers
// start and end.
type destroyRecord struct {
	name       string
	start, end int
}

// setLatency makes the provider slow, from now on.
func (f *fakeProvider) setLatency(d time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.latency = d
}

// jitterUpgrades makes the calls to UpgradeResourceState end in no particular order.
func (f *fakeProvider) jitterUpgrades(d time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.upgradeJitter = d
}

// refuseDestroy makes the destroy of the resource named name fail from now on.
func (f *fakeProvider) refuseDestroy(name string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.undestroyable == nil {
		f.undestroyable = map[string]bool{}
	}
	f.undestroyable[name] = true
}

func (f *fakeProvider) delay() time.Duration {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.latency
}

// destroyed returns the destroys that ended and the most that ran together.
func (f *fakeProvider) destroyed() ([]destroyRecord, int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]destroyRecord(nil), f.destroys...), f.peak
}

// readNames returns the names of the resources that were read, sorted.
func (f *fakeProvider) readNames() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := append([]string(nil), f.read...)
	sort.Strings(out)
	return out
}

func (f *fakeProvider) count(name string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.calls == nil {
		f.calls = map[string]int{}
	}
	f.calls[name]++
}

// called returns how many times the named call reached the provider.
func (f *fakeProvider) called(name string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls[name]
}

// resetCalls sets every count to zero.
func (f *fakeProvider) resetCalls() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls, f.read = map[string]int{}, nil
}

// drift makes the real world differ from the state: the resource named name
// now has this content.
func (f *fakeProvider) drift(name, content string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.drifted == nil {
		f.drifted = map[string]string{}
	}
	f.drifted[name] = content
}

// vanish removes the object of the resource named name from the real world.
// changeOnApply makes the apply of name change target, as in sideEffect.
func (f *fakeProvider) changeOnApply(name, target, content string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.sideEffect == nil {
		f.sideEffect = map[string]effect{}
	}
	f.sideEffect[name] = effect{target, content}
}

func (f *fakeProvider) vanish(name string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.vanished == nil {
		f.vanished = map[string]bool{}
	}
	f.vanished[name] = true
}

// replan makes the provider plan the resource named name with this content from
// now on, whatever the configuration says.
func (f *fakeProvider) replan(name, content string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.replanned == nil {
		f.replanned = map[string]string{}
	}
	f.replanned[name] = content
}

// setData makes the data source named name answer with this content from now on.
func (f *fakeProvider) setData(name, content string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.data == nil {
		f.data = map[string]string{}
	}
	f.data[name] = content
}

// restore ends the drift and the disappearance of the resource named name.
func (f *fakeProvider) restore(name string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.drifted, name)
	delete(f.vanished, name)
}

func (f *fakeProvider) ProtocolVersion() int { return 6 }
func (f *fakeProvider) GetSchema(context.Context) (*schema.ProviderSchema, plugin.Diagnostics, error) {
	return fakeSchema(), nil, nil
}
func (f *fakeProvider) ValidateProviderConfig(context.Context, tftypes.Value) (plugin.Diagnostics, error) {
	return nil, nil
}
func (f *fakeProvider) Configure(_ context.Context, cfg tftypes.Value) (plugin.Diagnostics, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.configured = append(f.configured, cfg)
	return nil, nil
}

// regions returns the region of each configuration the provider received, in
// order; "" for a null one.
func (f *fakeProvider) regions() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for _, cfg := range f.configured {
		var m map[string]tftypes.Value
		var region string
		if cfg.As(&m) == nil {
			_ = m["region"].As(&region)
		}
		out = append(out, region)
	}
	return out
}
func (f *fakeProvider) ValidateResourceConfig(context.Context, string, tftypes.Value) (plugin.Diagnostics, error) {
	f.count("ValidateResourceConfig")
	return nil, nil
}
func (f *fakeProvider) ValidateDataSourceConfig(context.Context, string, tftypes.Value) (plugin.Diagnostics, error) {
	return nil, nil
}
func (f *fakeProvider) UpgradeResourceState(_ context.Context, _ string, _ int64, js []byte, want tftypes.Type) (tftypes.Value, plugin.Diagnostics, error) {
	f.count("UpgradeResourceState")
	f.mu.Lock()
	jitter := f.upgradeJitter
	f.mu.Unlock()
	if jitter > 0 {
		h := fnv.New32a()
		_, _ = h.Write(js)
		time.Sleep(jitter * time.Duration(h.Sum32()%8) / 7)
	}
	v, err := tftypes.ValueFromJSON(js, want)
	return v, nil, err
}
func (f *fakeProvider) ReadResource(_ context.Context, _ string, st tftypes.Value, private []byte) (tftypes.Value, []byte, plugin.Diagnostics, error) {
	f.count("ReadResource")
	if d := f.delay(); d > 0 {
		time.Sleep(d)
	}
	var m map[string]tftypes.Value
	if err := st.As(&m); err != nil {
		return st, private, nil, nil
	}
	var name string
	_ = m["name"].As(&name)
	f.mu.Lock()
	f.read = append(f.read, name)
	vanished := f.vanished[name]
	content, drifted := f.drifted[name]
	f.mu.Unlock()
	switch {
	case vanished:
		return tftypes.NewValue(st.Type(), nil), nil, nil, nil
	case drifted:
		m["content"] = tftypes.NewValue(tftypes.String, content)
		return tftypes.NewValue(st.Type(), m), private, nil, nil
	}
	return st, private, nil, nil
}
func (f *fakeProvider) PlanResourceChange(_ context.Context, _ string, prior, proposed, _ tftypes.Value, _ []byte) (*plugin.PlanResult, plugin.Diagnostics, error) {
	f.count("PlanResourceChange")
	if proposed.IsNull() {
		return &plugin.PlanResult{PlannedState: proposed}, nil, nil
	}
	var m map[string]tftypes.Value
	if err := proposed.As(&m); err != nil {
		return nil, nil, err
	}
	res := &plugin.PlanResult{}
	var planned string
	_ = m["name"].As(&planned)
	f.mu.Lock()
	content, replanned := f.replanned[planned]
	f.mu.Unlock()
	if replanned {
		m["content"] = tftypes.NewValue(tftypes.String, content)
	}
	if prior.IsNull() {
		m["id"] = tftypes.NewValue(tftypes.String, tftypes.UnknownValue)
	} else {
		var pm map[string]tftypes.Value
		if err := prior.As(&pm); err != nil {
			return nil, nil, err
		}
		if !pm["name"].Equal(m["name"]) {
			res.RequiresReplace = []*tftypes.AttributePath{tftypes.NewAttributePath().WithAttributeName("name")}
		}
	}
	res.PlannedState = tftypes.NewValue(proposed.Type(), m)
	return res, nil, nil
}
func (f *fakeProvider) ApplyResourceChange(ctx context.Context, _ string, prior, planned, _ tftypes.Value, private []byte) (*plugin.ApplyResult, plugin.Diagnostics, error) {
	f.count("ApplyResourceChange")
	if planned.IsNull() {
		if f.destroy(prior) {
			return nil, plugin.Diagnostics{{Severity: "error", Summary: "the provider could not destroy the object"}}, nil
		}
		return &plugin.ApplyResult{NewState: planned}, nil, nil
	}
	var m map[string]tftypes.Value
	if err := planned.As(&m); err != nil {
		return nil, nil, err
	}
	if !m["id"].IsKnown() || m["id"].IsNull() {
		f.mu.Lock()
		f.next++
		n := f.next
		f.mu.Unlock()
		m["id"] = tftypes.NewValue(tftypes.String, fmt.Sprintf("id-%d", n))
	}
	var name string
	_ = m["name"].As(&name)
	if err := f.await(ctx, name); err != nil {
		return nil, plugin.Diagnostics{{Severity: "error", Summary: err.Error()}}, nil
	}
	f.mu.Lock()
	partial := f.partial[name]
	// The object is now what the apply wrote: what was changed outside, or taken
	// away, is not any more.
	delete(f.drifted, name)
	delete(f.vanished, name)
	if fx, ok := f.sideEffect[name]; ok {
		if f.drifted == nil {
			f.drifted = map[string]string{}
		}
		f.drifted[fx.target] = fx.content
	}
	f.mu.Unlock()
	if partial {
		m["content"] = tftypes.NewValue(tftypes.String, nil)
		return &plugin.ApplyResult{NewState: tftypes.NewValue(planned.Type(), m), Private: private},
			plugin.Diagnostics{{Severity: "error", Summary: "the provider stopped before it set the content"}}, nil
	}
	return &plugin.ApplyResult{NewState: tftypes.NewValue(planned.Type(), m), Private: private}, nil, nil
}

// destroy takes the time of a destroy of the object whose state is prior and
// notes when it started and ended. It reports whether the destroy fails.
func (f *fakeProvider) destroy(prior tftypes.Value) (fails bool) {
	var name string
	var m map[string]tftypes.Value
	if prior.As(&m) == nil {
		_ = m["name"].As(&name)
	}
	f.mu.Lock()
	f.tick++
	start := f.tick
	if f.running++; f.running > f.peak {
		f.peak = f.running
	}
	d := f.latency
	f.mu.Unlock()
	time.Sleep(d)
	f.mu.Lock()
	defer f.mu.Unlock()
	f.running--
	f.tick++
	f.destroys = append(f.destroys, destroyRecord{name: name, start: start, end: f.tick})
	return f.undestroyable[name]
}

func (f *fakeProvider) ImportResourceState(_ context.Context, typ string, target plugin.ImportTarget, types map[string]tftypes.Type) ([]plugin.ImportedResource, plugin.Diagnostics, error) {
	id := target.ID
	if target.Identity != nil {
		id, _ = target.Identity["id"].(string)
	}
	f.mu.Lock()
	f.importTargets = append(f.importTargets, target)
	content, ok := f.importable[id]
	f.mu.Unlock()
	if !ok {
		return nil, nil, fmt.Errorf("not supported")
	}
	t := types[typ]
	st := tftypes.NewValue(t, map[string]tftypes.Value{
		"id": tftypes.NewValue(tftypes.String, id), "name": tftypes.NewValue(tftypes.String, "obj-"+id),
		"content": tftypes.NewValue(tftypes.String, content),
	})
	return []plugin.ImportedResource{{TypeName: typ, State: st}}, nil, nil
}
func (f *fakeProvider) ReadDataSource(_ context.Context, _ string, cfg tftypes.Value) (tftypes.Value, plugin.Diagnostics, error) {
	f.count("ReadDataSource")
	var m map[string]tftypes.Value
	if err := cfg.As(&m); err != nil {
		return tftypes.Value{}, nil, err
	}
	var name string
	_ = m["name"].As(&name)
	content := "from " + name
	f.mu.Lock()
	if c, ok := f.data[name]; ok {
		content = c
	}
	f.mu.Unlock()
	m["id"] = tftypes.NewValue(tftypes.String, "data-"+name)
	m["content"] = tftypes.NewValue(tftypes.String, content)
	return tftypes.NewValue(cfg.Type(), m), nil, nil
}
func (f *fakeProvider) ValidateEphemeralConfig(context.Context, string, tftypes.Value) (plugin.Diagnostics, error) {
	return nil, nil
}
func (f *fakeProvider) OpenEphemeral(_ context.Context, _ string, cfg tftypes.Value) (*plugin.EphemeralResult, plugin.Diagnostics, error) {
	f.count("OpenEphemeral")
	var m map[string]tftypes.Value
	if err := cfg.As(&m); err != nil {
		return nil, nil, err
	}
	var name string
	_ = m["name"].As(&name)
	m["token"] = tftypes.NewValue(tftypes.String, "token-"+name)
	return &plugin.EphemeralResult{Result: tftypes.NewValue(cfg.Type(), m)}, nil, nil
}
func (f *fakeProvider) RenewEphemeral(context.Context, string, []byte) (*plugin.EphemeralResult, plugin.Diagnostics, error) {
	return nil, nil, fmt.Errorf("not supported")
}
func (f *fakeProvider) CloseEphemeral(context.Context, string, []byte) (plugin.Diagnostics, error) {
	f.count("CloseEphemeral")
	return nil, nil
}
func (f *fakeProvider) CallFunction(_ context.Context, _ string, args []tftypes.Value, _ []tftypes.Type, _ tftypes.Type) (tftypes.Value, *plugin.FunctionError, error) {
	f.count("CallFunction")
	var s string
	if err := args[0].As(&s); err != nil {
		return tftypes.Value{}, nil, err
	}
	return tftypes.NewValue(tftypes.String, strings.ToUpper(s)), nil, nil
}
func (f *fakeProvider) Stop(context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.stops++
	if f.deaf {
		return nil
	}
	if f.stop == nil {
		f.stop = make(chan struct{})
	}
	select {
	case <-f.stop:
	default:
		close(f.stop)
	}
	return nil
}
func (f *fakeProvider) Close() {}

func fakeSchema() *schema.ProviderSchema {
	attr := func(name string, req, opt, comp bool) *schema.Attribute {
		return &schema.Attribute{Name: name, TypeJSON: json.RawMessage(`"string"`), Required: req, Optional: opt, Computed: comp}
	}
	return &schema.ProviderSchema{
		Provider: &schema.Schema{Block: &schema.Block{Attributes: []*schema.Attribute{
			attr("region", false, true, false),
			{Name: "token", TypeJSON: json.RawMessage(`"string"`), Optional: true, Sensitive: true},
		}}},
		Resources: map[string]*schema.Schema{"fake_thing": {Block: &schema.Block{Attributes: []*schema.Attribute{
			attr("id", false, false, true), attr("name", true, false, false), attr("content", false, true, false),
		}}}},
		DataSources: map[string]*schema.Schema{"fake_data": {Block: &schema.Block{Attributes: []*schema.Attribute{
			attr("id", false, false, true), attr("name", true, false, false), attr("content", false, false, true),
		}}}},
		Ephemerals: map[string]*schema.Schema{"fake_session": {Block: &schema.Block{Attributes: []*schema.Attribute{
			attr("name", true, false, false), attr("token", false, false, true),
		}}}},
		ListResources: map[string]*schema.Schema{"fake_thing": {Block: &schema.Block{Attributes: []*schema.Attribute{
			attr("region", false, true, false), attr("name_prefix", false, true, false),
		}}}},
		Functions: map[string]*schema.Function{"fake_upper": {
			Parameters: []*schema.Parameter{{Name: "text", TypeJSON: json.RawMessage(`"string"`)}},
			ReturnJSON: json.RawMessage(`"string"`),
		}},
	}
}

// harness runs the engine against the fake provider, a state kept in a
// temporary directory and a specification given as CUE text.
type harness struct {
	t     *testing.T
	dir   string
	state *state.Store
	fake  *fakeProvider
	// backend counts the calls that the state makes to its backend.
	backend *statetest.Counting
}

func newHarness(t *testing.T) *harness { return newHarnessOver(t, nil) }

// newHarnessOver is newHarness with a backend that wrap makes from the local one
// and that the counts go through.
func newHarnessOver(t *testing.T, wrap func(state.Backend) state.Backend) *harness {
	t.Helper()
	dir := t.TempDir()
	lb, err := state.NewLocalBackend(filepath.Join(dir, "state"))
	if err != nil {
		t.Fatal(err)
	}
	var base state.Backend = lb
	if wrap != nil {
		base = wrap(lb)
	}
	backend := statetest.NewCounting(base)
	st, err := state.OpenBackend(context.Background(), backend)
	if err != nil {
		t.Fatal(err)
	}
	return &harness{t: t, dir: dir, state: st, fake: &fakeProvider{}, backend: backend}
}

// reopen opens the state again, as the next command would.
func (h *harness) reopen() {
	h.t.Helper()
	st, err := state.OpenBackend(context.Background(), h.backend)
	if err != nil {
		h.t.Fatal(err)
	}
	h.state = st
}

// spec is a specification: its CUE text, the addresses of its resources and
// of its outputs.
type spec struct {
	cue       string
	resources []string
	outputs   []string
	// data lists the data sources (fake_data) and builtins the built-in values
	// (pravo.#Env) of the specification.
	data     []string
	builtins []string
	// secrets lists the pravo.#Secret nodes and secretVersions the pravo.#SecretVersion
	// nodes of the specification.
	secrets        []string
	secretVersions []string
	// ephemerals lists the ephemeral resources (fake_session) and calls the
	// function calls (fake_upper) of the specification.
	ephemerals []string
	calls      []string
	// discoveries lists the addresses of the discoveries (of fake_thing) of the
	// specification.
	discoveries []string
	// providers maps the key of each provider instance to the path of its
	// configuration in the specification; keys maps a resource to the instance it
	// uses, "fake" when absent.
	providers map[string]string
	keys      map[string]string
}

func (s spec) keyOf(addr string) string {
	if k := s.keys[addr]; k != "" {
		return k
	}
	return "fake"
}

// engine returns an engine for the specification, as a new command makes.
func (h *harness) engine(s spec, opts Options) *Engine {
	h.t.Helper()
	if err := os.WriteFile(filepath.Join(h.dir, "main.cue"), []byte("package t\n\n"+s.cue), 0o644); err != nil {
		h.t.Fatal(err)
	}
	ctx := cuecontext.New()
	v := ctx.CompileString(s.cue)
	if err := v.Err(); err != nil {
		h.t.Fatal(err)
	}
	root := &config.Root{Dir: h.dir, Ctx: ctx, Value: v, Providers: map[string]*config.ProviderReq{}, Imports: map[string]*config.Import{}, ProviderKeys: []string{"fake"}}
	for _, addr := range s.resources {
		p := cue.ParsePath(addr)
		res := &config.Resource{Mode: "managed", Type: "fake_thing", Name: addr, Path: p, ProviderKey: s.keyOf(addr)}
		age := v.LookupPath(p).LookupPath(cue.MakePath(cue.Str("$lifecycle"), cue.Str("refresh")))
		res.RefreshPinned = age.Exists() && age.IsConcrete()
		root.Resources = append(root.Resources, res)
	}
	for _, addr := range s.data {
		root.Resources = append(root.Resources, &config.Resource{Mode: "data", Type: "fake_data", Name: addr, Path: cue.ParsePath(addr), ProviderKey: s.keyOf(addr)})
	}
	for _, addr := range s.ephemerals {
		root.Resources = append(root.Resources, &config.Resource{Mode: "ephemeral", Type: "fake_session", Name: addr, Path: cue.ParsePath(addr), ProviderKey: s.keyOf(addr)})
	}
	for _, addr := range s.calls {
		root.Resources = append(root.Resources, &config.Resource{Mode: "call", Type: "fake_upper", Name: addr, Path: cue.ParsePath(addr), ProviderKey: "fake"})
	}
	for _, addr := range s.builtins {
		root.Resources = append(root.Resources, &config.Resource{Mode: "builtin", Type: "env", Name: addr, Path: cue.ParsePath(addr), Builtin: true})
	}
	for _, addr := range s.secrets {
		root.Resources = append(root.Resources, &config.Resource{Mode: "ephemeral", Type: "secret", Name: addr, Path: cue.ParsePath(addr), Builtin: true})
	}
	for _, addr := range s.secretVersions {
		root.Resources = append(root.Resources, &config.Resource{Mode: "builtin", Type: "secret_version", Name: addr, Path: cue.ParsePath(addr), Builtin: true})
	}
	for _, name := range s.outputs {
		root.Outputs = append(root.Outputs, &config.Output{Name: name, Path: cue.ParsePath(name)})
	}
	for _, addr := range s.discoveries {
		p := cue.ParsePath(addr)
		root.Discoveries = append(root.Discoveries, config.NewDiscovery(v.LookupPath(p), p, "fake_thing", registry.Source{Host: "example.test", Namespace: "x", Type: "fake"}, s.keyOf(addr)))
	}
	for key, path := range s.providers {
		root.ProviderDecls = append(root.ProviderDecls, &config.ProviderDecl{Path: cue.ParsePath(path), Key: key, Local: "fake"})
		if key != "fake" {
			root.ProviderKeys = append(root.ProviderKeys, key)
		}
	}
	req := &config.ProviderReq{LocalName: "fake", Source: registry.Source{Host: "example.test", Namespace: "x", Type: "fake"}, Protocol: config.ProtocolTfplugin}
	root.Providers["fake"] = req
	provs := NewProviders(root, nil)
	provs.Resolved["fake"] = &ResolvedProvider{Req: req, Version: "1.0.0", Protocol: config.ProtocolTfplugin}
	provs.schemas["fake"] = fakeSchema()
	provs.launch = func(string, *ResolvedProvider) (plugin.Provider, error) {
		h.fake.count("start")
		return h.fake, nil
	}
	if opts.Progress == nil {
		opts.Progress = func(string, ...any) {}
	}
	return New(root, h.state, provs, map[string]*schema.ProviderSchema{"fake": fakeSchema()}, v, opts)
}

// apply plans and applies the specification and fails the test on an error.
func (h *harness) apply(s spec) *ApplyResult {
	h.t.Helper()
	e := h.engine(s, Options{})
	plan, err := e.Plan(context.Background())
	if err != nil {
		h.t.Fatalf("plan: %v", err)
	}
	res, err := e.Apply(context.Background(), plan)
	if err != nil {
		h.t.Fatalf("apply: %v", err)
	}
	return res
}

// source is the CUE of one fake_data data source.
func source(name string) string {
	return fmt.Sprintf("{name: %q, id: string | null, content: string | null}", name)
}

// thing is the CUE of one fake_thing resource; extra holds more fields.
func thing(name string, extra string) string {
	return fmt.Sprintf("{name: %q, id: string | null\n%s}", name, extra)
}

func (h *harness) revision(addr string) int {
	h.t.Helper()
	n := h.state.Get(addr)
	if n == nil {
		h.t.Fatalf("%s is not in the state", addr)
	}
	return n.Revision
}
