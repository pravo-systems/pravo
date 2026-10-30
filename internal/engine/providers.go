package engine

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"cuelang.org/go/cue"
	"github.com/hashicorp/go-version"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
	"go.opentelemetry.io/otel/attribute"

	"github.com/pravo-systems/pravo/internal/config"
	"github.com/pravo-systems/pravo/internal/convert"
	"github.com/pravo-systems/pravo/internal/env"
	"github.com/pravo-systems/pravo/internal/plog"
	"github.com/pravo-systems/pravo/internal/plugin"
	"github.com/pravo-systems/pravo/internal/plugin/netcap"
	"github.com/pravo-systems/pravo/internal/registry"
	"github.com/pravo-systems/pravo/internal/schema"
	"github.com/pravo-systems/pravo/internal/telemetry"
)

// ResolvedProvider is a provider requirement resolved to a binary on disk.
type ResolvedProvider struct {
	Req     *config.ProviderReq
	Version string
	Path    string
	Hash    string
	// Protocol is how pravo talks to the provider (config.ProtocolTfplugin
	// or config.ProtocolReattach); Reattach is set for the latter.
	Protocol string
	Reattach *plugin.Reattachment
}

// Reattached reports whether the provider is a process pravo did not start.
func (r *ResolvedProvider) Reattached() bool { return r.Protocol == config.ProtocolReattach }

// resolveReattach decides whether the provider is reached by reattaching:
// either its declaration says so, or TF_REATTACH_PROVIDERS names it (the
// way debuggers and provider test harnesses hand a running provider over).
func (m *Providers) resolveReattach(local string, req *config.ProviderReq) (*ResolvedProvider, error) {
	reattach, err := plugin.ReattachFromEnv(m.Env, req.Source.String(), req.Source.Namespace+"/"+req.Source.Type)
	if err != nil {
		return nil, fmt.Errorf("provider %s: %w", local, err)
	}
	var ra *plugin.Reattachment
	switch {
	case reattach != nil:
		ra = reattach
		m.Log(fmt.Sprintf("provider %s: reattaching to %s (TF_REATTACH_PROVIDERS)", local, ra))
	case req.Protocol == config.ProtocolReattach:
		if req.Address == "" {
			return nil, fmt.Errorf("provider %s: protocol %q needs an address (unix:///path/to/socket or tcp://127.0.0.1:port)", local, config.ProtocolReattach)
		}
		parsed, err := plugin.ParseAddress(req.Address)
		if err != nil {
			return nil, fmt.Errorf("provider %s: %w", local, err)
		}
		ra = &parsed
	default:
		return nil, nil
	}
	return &ResolvedProvider{Req: req, Version: "reattach", Protocol: config.ProtocolReattach, Reattach: ra}, nil
}

// Providers manages provider processes for one run.
type Providers struct {
	Root     *config.Root
	Registry *registry.Client
	Resolved map[string]*ResolvedProvider // by local name
	Log      func(string)
	// Env is the environment the providers run in and are looked for by:
	// TF_REATTACH_PROVIDERS, PRAVO_RECORD, PRAVO_REPLAY, PRAVO_LOG and the rest of
	// what a provider process is given. It is empty unless the caller sets it.
	Env env.Env

	mu         sync.Mutex
	schemas    map[string]*schema.ProviderSchema // by local name
	instances  map[string]*instance              // by provider key
	configured map[string]error
	// ConfigValue returns the configuration value used for provider blocks.
	ConfigValue cue.Value
	// ConfigConvert, when set, converts the current (filled) configuration
	// of provider.<key> against its block schema; the engine provides it so
	// that provider configuration can reference applied resources and
	// ephemeral values.
	ConfigConvert func(key string, blk *schema.Block) (tftypes.Value, error)

	// launch, when set, starts every provider in place of the binary on disk.
	launch func(local string, rp *ResolvedProvider) (plugin.Provider, error)
}

type instance struct {
	p          plugin.Provider
	reattached bool
	key        string
	mu         sync.Mutex
	cfg        bool
}

// recording is how the providers are recorded or replayed, from the environment.
func (m *Providers) recording() plugin.Recording { return plugin.RecordingFrom(m.Env) }

// NewProviders creates an empty manager.
func NewProviders(root *config.Root, reg *registry.Client) *Providers {
	return &Providers{
		Root: root, Registry: reg, Resolved: map[string]*ResolvedProvider{},
		schemas: map[string]*schema.ProviderSchema{}, instances: map[string]*instance{},
		configured: map[string]error{}, Log: func(string) {},
	}
}

// EnsureRequirement adds an implied requirement for a provider local name
// (used for resources that exist only in state).
func (m *Providers) EnsureRequirement(local string) {
	if _, ok := m.Root.Providers[local]; !ok {
		m.Root.Providers[local] = &config.ProviderReq{
			LocalName: local, Implied: true, Protocol: config.ProtocolTfplugin,
			Source: registry.Source{Host: registry.DefaultHost, Namespace: "hashicorp", Type: local},
		}
	}
}

// Resolve selects versions and makes sure binaries are installed. With
// upgrade set, the lock file is ignored when choosing versions. install
// controls whether missing providers may be downloaded.
func (m *Providers) Resolve(ctx context.Context, lock *Lock, upgrade, install bool) error {
	locals := make([]string, 0, len(m.Root.Providers))
	for l := range m.Root.Providers {
		locals = append(locals, l)
	}
	sort.Strings(locals)
	for _, local := range locals {
		req := m.Root.Providers[local]
		if m.recording().Replaying() {
			ver := "replay"
			if e, ok := lock.Providers[req.Source.String()]; ok {
				ver = e.Version
			}
			m.Resolved[local] = &ResolvedProvider{Req: req, Version: ver, Path: "(replay)", Protocol: config.ProtocolTfplugin}
			continue
		}
		if req.Protocol == "" {
			req.Protocol = config.ProtocolTfplugin
		}
		rr, err := m.resolveReattach(local, req)
		if err != nil {
			return err
		}
		if rr != nil {
			m.Resolved[local] = rr
			continue
		}
		if req.Path != "" {
			plog.Debug(ctx, "provider from a path", "provider", local, "path", req.Path)
			p := req.Path
			if st, err := os.Stat(p); err == nil && st.IsDir() {
				bin, ok := registry.FindBinary(p, req.Source.Type)
				if !ok {
					return fmt.Errorf("provider %s: no terraform-provider-%s executable in %s", local, req.Source.Type, p)
				}
				p = bin
			} else if err != nil {
				return fmt.Errorf("provider %s: %w", local, err)
			}
			m.Resolved[local] = &ResolvedProvider{Req: req, Version: "dev", Path: p, Protocol: config.ProtocolTfplugin}
			continue
		}
		ver := ""
		if e, ok := lock.Providers[req.Source.String()]; ok && !upgrade {
			if req.Version == "" || satisfies(e.Version, req.Version) {
				ver = e.Version
				plog.Debug(ctx, "provider version from the lock file", "provider", local, "version", ver, "constraint", req.Version)
			} else {
				plog.Debug(ctx, "locked version does not satisfy the constraint", "provider", local, "locked", e.Version, "constraint", req.Version)
			}
		}
		if ver == "" {
			if !install {
				return fmt.Errorf("provider %s (%s) is not locked; run \"pravo init\"", local, req.Source.Display())
			}
			rctx, end := setup(ctx, "resolve provider "+local, attribute.String("pravo.provider.source", req.Source.Display()))
			v, err := m.Registry.Resolve(rctx, req.Source, req.Version)
			end(err)
			if err != nil {
				return err
			}
			ver = v
			plog.Debug(ctx, "provider version resolved", "provider", local, "source", req.Source.String(), "constraint", req.Version, "version", ver)
		}
		rp := &ResolvedProvider{Req: req, Version: ver, Protocol: config.ProtocolTfplugin}
		if p, ok := m.Registry.FindInstalled(req.Source, ver); ok {
			rp.Path = p
			plog.Debug(ctx, "provider found installed", "provider", local, "source", req.Source.String(), "version", ver, "path", p, "constraint", req.Version)
		} else {
			plog.Debug(ctx, "provider not installed", "provider", local, "source", req.Source.String(), "version", ver)
			if !install {
				return fmt.Errorf("provider %s v%s is not installed; run \"pravo init\"", req.Source.Display(), ver)
			}
			ictx, end := setup(ctx, "install provider "+local, attribute.String("pravo.provider.source", req.Source.Display()), attribute.String("pravo.provider.version", ver))
			res, err := m.Registry.Install(ictx, req.Source, ver, m.Log)
			end(err)
			if err != nil {
				return err
			}
			rp.Path, rp.Hash = res.Path, res.Hash
			plat := registry.CurrentPlatform().String()
			if e, ok := lock.Providers[req.Source.String()]; ok && e.Version == ver {
				if want, ok := e.Hashes[plat]; ok && want != res.Hash {
					_ = os.RemoveAll(filepath.Dir(res.Path))
					return fmt.Errorf("provider %s v%s: the downloaded archive for %s is %s, but %s pins %s; a published version must not change. If this is expected, remove the hash from the lock file and run \"pravo init\"", req.Source.Display(), ver, plat, res.Hash, LockFileName, want)
				}
			}
		}
		if rp.Hash == "" && install {
			// Found in the provider store, so nothing was downloaded. Pin what
			// the source states for this platform when the lock has no hash.
			plat := registry.CurrentPlatform()
			if e, ok := lock.Providers[req.Source.String()]; !ok || e.Version != ver || e.Hashes[plat.String()] == "" {
				cctx, end := setup(ctx, "checksum provider "+local)
				sum, err := m.Registry.Checksum(cctx, req.Source, ver, plat)
				end(err)
				if err == nil {
					rp.Hash = "sha256:" + sum
				}
			}
		}
		m.Resolved[local] = rp
	}
	return nil
}

func satisfies(ver, constraint string) bool {
	v, err := version.NewVersion(ver)
	if err != nil {
		return false
	}
	c, err := version.NewConstraint(constraint)
	if err != nil {
		return false
	}
	return c.Check(v)
}

// UpdateLock records the resolved versions into lock.
func (m *Providers) UpdateLock(lock *Lock) {
	for _, rp := range m.Resolved {
		if rp.Req.Path != "" || rp.Reattached() {
			continue
		}
		key := rp.Req.Source.String()
		e := lock.Providers[key]
		if e == nil || e.Version != rp.Version {
			e = &LockEntry{Version: rp.Version}
			lock.Providers[key] = e
		}
		e.Constraints = rp.Req.Version
		if rp.Hash != "" {
			if e.Hashes == nil {
				e.Hashes = map[string]string{}
			}
			if _, ok := e.Hashes[registry.CurrentPlatform().String()]; !ok {
				e.Hashes[registry.CurrentPlatform().String()] = rp.Hash
			}
		}
	}
}

// Schema returns the provider schema for a local name, using the on-disk
// cache when possible.
func (m *Providers) Schema(ctx context.Context, local string) (*schema.ProviderSchema, error) {
	m.mu.Lock()
	if s, ok := m.schemas[local]; ok {
		m.mu.Unlock()
		return s, nil
	}
	m.mu.Unlock()
	ctx, end := setup(ctx, "load schema "+local, attribute.String("pravo.provider", local))
	s, err := m.loadSchema(ctx, local)
	end(err)
	return s, err
}

func (m *Providers) loadSchema(ctx context.Context, local string) (*schema.ProviderSchema, error) {
	rp, ok := m.Resolved[local]
	if !ok {
		return nil, fmt.Errorf("provider %q is not resolved", local)
	}
	var packPath string
	// Recording and replaying must go through the provider interface: the
	// on-disk schema cache would hide GetSchema from the recorder.
	rec := m.recording()
	useCache := rec.Record == "" && !rec.Replaying()
	if !rp.Reattached() && useCache {
		packPath = m.Registry.SchemaPackPath(rp.Req.Source, rp.Version)
	}
	// A provider from a path is asked every run: its binary can change without
	// its version changing. Its schema is still written to the cache, as "dev",
	// for the viewer, which never starts a provider and reads only the cache.
	if rp.Req.Path == "" && packPath != "" {
		jsonPath := m.Registry.SchemaCachePath(rp.Req.Source, rp.Version)
		began := time.Now()
		// The pack is opened and its types are read when they are first asked for.
		if s, err := schema.OpenPack(packPath); err == nil {
			plog.Debug(ctx, "schema from the cache", "provider", local, "path", packPath, "took", time.Since(began).Round(time.Millisecond))
			m.mu.Lock()
			m.schemas[local] = s
			m.mu.Unlock()
			return s, nil
		}
		// A cache that an earlier pravo wrote is a JSON file. It is read whole, and
		// written as a pack so that the next run does not.
		if data, err := os.ReadFile(jsonPath); err == nil {
			if s, err := schema.Unmarshal(data); err == nil {
				plog.Debug(ctx, "schema from the cache", "provider", local, "path", jsonPath, "bytes", len(data), "took", time.Since(began).Round(time.Millisecond))
				writePack(packPath, s)
				m.mu.Lock()
				m.schemas[local] = s
				m.mu.Unlock()
				return s, nil
			}
		}
	}
	p, err := m.start(ctx, local, rp)
	if err != nil {
		return nil, err
	}
	defer p.Close()
	s, diags, err := p.GetSchema(ctx)
	if err != nil {
		return nil, fmt.Errorf("fetching schema of provider %s: %w", local, err)
	}
	if err := diags.Err(); err != nil {
		return nil, fmt.Errorf("fetching schema of provider %s: %w", local, err)
	}
	s.Sort()
	if packPath != "" {
		writePack(packPath, s)
	}
	m.mu.Lock()
	m.schemas[local] = s
	m.mu.Unlock()
	return s, nil
}

// Schemas fetches schemas for all resolved providers.
func (m *Providers) Schemas(ctx context.Context) (map[string]*schema.ProviderSchema, error) {
	out := map[string]*schema.ProviderSchema{}
	for local := range m.Resolved {
		s, err := m.Schema(ctx, local)
		if err != nil {
			return nil, err
		}
		out[local] = s
	}
	return out, nil
}

// instance returns the running process for a provider key, starting it if
// needed, without configuring it.
func (m *Providers) instance(ctx context.Context, key string) (*instance, error) {
	local := config.LocalName(key)
	waiting := time.Now()
	m.mu.Lock()
	defer m.mu.Unlock()
	// A task that comes while another starts a provider waits here, for any provider.
	telemetry.Waited(ctx, "wait for a provider to start", waiting, attribute.String("pravo.provider", local))
	inst, ok := m.instances[key]
	if ok {
		return inst, nil
	}
	rp, ok := m.Resolved[local]
	if !ok {
		return nil, fmt.Errorf("provider %q is not resolved", local)
	}
	p, err := m.start(ctx, local, rp)
	if err != nil {
		return nil, err
	}
	plugin.SetInstance(p, key)
	plog.Debug(ctx, "provider started", "provider", key, "version", rp.Version, "path", rp.Path)
	inst = &instance{p: p, key: key, reattached: rp.Reattached()}
	m.instances[key] = inst
	return inst, nil
}

// start launches a provider binary, or in replay mode serves recordings
// without one, and applies recording when requested.
func (m *Providers) start(ctx context.Context, local string, rp *ResolvedProvider) (plugin.Provider, error) {
	_, end := setup(ctx, "start provider "+local, attribute.String("pravo.provider", local), attribute.String("pravo.provider.version", rp.Version))
	p, err := m.startProvider(local, rp)
	end(err)
	return p, err
}

func (m *Providers) startProvider(local string, rp *ResolvedProvider) (plugin.Provider, error) {
	if m.launch != nil {
		return m.launch(local, rp)
	}
	rec := m.recording()
	if rec.Replaying() {
		return plugin.Wrap(nil, local, rec)
	}
	var p plugin.Provider
	var err error
	var capture *netcap.Capture
	if rp.Reattached() {
		p, err = plugin.Reattach(*rp.Reattach, plugin.Logger(m.Env.Get("PRAVO_LOG")))
	} else {
		var extra []string
		if netcap.Enabled() {
			// A capture that cannot start is no reason to stop the run: the provider goes without one.
			if capture, err = netcap.New(m.Env); err == nil {
				extra = capture.Env()
			} else {
				capture = nil
			}
		}
		p, err = plugin.StartWith(rp.Path, plugin.Logger(m.Env.Get("PRAVO_LOG")), m.Env, extra)
		if err != nil && capture != nil {
			capture.Close()
		}
	}
	if err != nil {
		return nil, err
	}
	w, err := plugin.Wrap(p, local, rec)
	if capture != nil {
		if err != nil {
			capture.Close()
		} else {
			plugin.AttachCapture(w, capture)
		}
	}
	return w, err
}

// Unconfigured returns a running provider without configuring it, which is
// all a provider-defined function needs.
func (m *Providers) Unconfigured(ctx context.Context, key string) (plugin.Provider, error) {
	inst, err := m.instance(ctx, key)
	if err != nil {
		return nil, err
	}
	return inst.p, nil
}

// Provider returns a running, configured provider for the given key
// ("aws" or "aws.west"), starting and configuring it on first use.
func (m *Providers) Provider(ctx context.Context, key string) (plugin.Provider, error) {
	local := config.LocalName(key)
	inst, err := m.instance(ctx, key)
	if err != nil {
		return nil, err
	}

	waiting := time.Now()
	inst.mu.Lock()
	defer inst.mu.Unlock()
	telemetry.Waited(ctx, "wait for provider "+key+" to be configured", waiting, attribute.String("pravo.provider", local))
	if inst.cfg {
		// Another provider may be recording its configuration just now.
		m.mu.Lock()
		err := m.configured[key]
		m.mu.Unlock()
		return inst.p, err
	}
	inst.cfg = true
	plog.Debug(ctx, "configuring provider", "provider", key)
	cctx, end := setup(ctx, "configure provider "+key, attribute.String("pravo.provider", local), attribute.String("pravo.provider.instance", key))
	err = m.configure(cctx, inst, local)
	end(err)
	m.mu.Lock()
	m.configured[key] = err
	m.mu.Unlock()
	return inst.p, err
}

func (m *Providers) configure(ctx context.Context, inst *instance, local string) error {
	s, err := m.Schema(ctx, local)
	if err != nil {
		return err
	}
	var cfg tftypes.Value
	if m.ConfigConvert != nil {
		var err error
		cfg, err = m.ConfigConvert(inst.key, s.Provider.Block)
		if err != nil {
			return fmt.Errorf("provider %s configuration: %w", inst.key, err)
		}
	} else {
		cv := m.ConfigValue.LookupPath(cue.MakePath(cue.Str("provider"), cue.Str(inst.key)))
		conv := &convert.Converter{AllowEphemeral: true}
		cfg = conv.Block("provider."+inst.key, s.Provider.Block, cv)
		if !cv.Exists() {
			cfg = conv.Block("provider."+inst.key, s.Provider.Block, m.Root.Ctx.CompileString("{}"))
		}
		if err := conv.Err(); err != nil {
			return fmt.Errorf("provider %s configuration: %w", inst.key, err)
		}
	}
	diags, err := inst.p.ValidateProviderConfig(ctx, cfg)
	if err != nil {
		return fmt.Errorf("validating provider %s configuration: %w", inst.key, err)
	}
	if err := diags.Err(); err != nil {
		return fmt.Errorf("provider %s configuration is invalid: %w", inst.key, err)
	}
	diags, err = inst.p.Configure(ctx, cfg)
	if err != nil {
		return fmt.Errorf("configuring provider %s: %w", inst.key, err)
	}
	if err := diags.Err(); err != nil {
		return fmt.Errorf("configuring provider %s: %w", inst.key, err)
	}
	return nil
}

// StopAll asks every provider that pravo started to stop what it is doing, as
// an interrupt does. A provider stops the calls it is serving and returns the
// state it had reached. A provider that pravo reattached to is left alone, as
// Close leaves it: a stop can leave a provider unable to serve the next call, and
// it is not pravo's to end.
func (m *Providers) StopAll(ctx context.Context) {
	m.mu.Lock()
	var started []*instance
	for _, inst := range m.instances {
		if !inst.reattached {
			started = append(started, inst)
		}
	}
	m.mu.Unlock()
	var wg sync.WaitGroup
	for _, inst := range started {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = inst.p.Stop(ctx)
		}()
	}
	wg.Wait()
}

// Close stops all provider processes.
func (m *Providers) Close() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, inst := range m.instances {
		if !inst.reattached {
			// A reattached provider is not ours to stop; closing only drops
			// the connection.
			_ = inst.p.Stop(context.Background())
		}
		inst.p.Close()
	}
	m.instances = map[string]*instance{}
	plugin.CleanupClients()
}

// writePack caches a schema as a pack. A failure only means that the next run
// asks the provider again, so it is not reported. The file is written aside and
// moved into place, so that a run that reads it never sees half of it.
func writePack(path string, s *schema.ProviderSchema) {
	data, err := s.MarshalPack()
	if err != nil {
		return
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".tmp*")
	if err != nil {
		return
	}
	_, werr := tmp.Write(data)
	cerr := tmp.Close()
	if werr != nil || cerr != nil || os.Rename(tmp.Name(), path) != nil {
		_ = os.Remove(tmp.Name())
	}
}
