// Package registry resolves, downloads and caches OpenTofu providers using
// the provider registry protocol, and locates provider binaries on disk.
package registry

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/hashicorp/go-version"

	"github.com/pravo-systems/pravo/internal/env"
)

// DefaultHost is used when a provider source has no host part: the OpenTofu
// registry. Any host implementing the provider registry protocol can be
// named explicitly in a source.
const DefaultHost = "registry.opentofu.org"

// Source identifies a provider: host/namespace/type. An OCI source names a
// repository in a container registry instead: Namespace is the repository
// path before the last element, which is the provider type.
type Source struct {
	Host      string
	Namespace string
	Type      string
	OCI       bool
}

// OCIPrefix starts the address of a provider kept in a container registry.
const OCIPrefix = "oci://"

// Limits on what a registry can make pravo read or write. A provider is a few
// hundred megabytes at most; the limits sit well above that and below what
// would fill a disk or the memory of a build machine.
var (
	// maxUnzippedFile is the most that one file of a provider package may unpack to.
	maxUnzippedFile int64 = 4 << 30
	// maxUnzippedTotal is the most that a whole package may unpack to.
	maxUnzippedTotal int64 = 6 << 30
	// maxZipEntries is the most entries that a provider package may hold.
	maxZipEntries = 1000
	// maxArchive is the most that one archive download may be.
	maxArchive int64 = 2 << 30
	// maxJSON is the most that one JSON answer of a registry may be.
	maxJSON int64 = 64 << 20
	// maxManifest is the most that an OCI manifest or index may be.
	maxManifest int64 = 4 << 20
	// maxTags is the most tags of one OCI repository that pravo lists.
	maxTags = 100000
	// idleTimeout is how long a registry may send nothing, while pravo waits for a
	// header or the next bytes of a body, before the request fails.
	idleTimeout = 60 * time.Second
)

// ParseSource parses "type", "namespace/type" or "host/namespace/type".
func ParseSource(s string) (Source, error) {
	if rest, ok := strings.CutPrefix(strings.TrimSpace(s), OCIPrefix); ok {
		parts := strings.Split(rest, "/")
		if len(parts) < 3 || slices.Contains(parts, "") {
			return Source{}, fmt.Errorf("invalid provider source %q: want oci://host/path/type", s)
		}
		return Source{Host: strings.ToLower(parts[0]), Namespace: strings.Join(parts[1:len(parts)-1], "/"), Type: parts[len(parts)-1], OCI: true}, nil
	}
	parts := strings.Split(strings.TrimSpace(s), "/")
	switch len(parts) {
	case 1:
		if parts[0] == "" {
			return Source{}, errors.New("empty provider source")
		}
		return Source{Host: DefaultHost, Namespace: "hashicorp", Type: parts[0]}, nil
	case 2:
		return Source{Host: DefaultHost, Namespace: parts[0], Type: parts[1]}, nil
	case 3:
		return Source{Host: strings.ToLower(parts[0]), Namespace: parts[1], Type: parts[2]}, nil
	}
	return Source{}, fmt.Errorf("invalid provider source %q: want [host/]namespace/type", s)
}

// String returns the fully qualified address host/namespace/type.
func (s Source) String() string {
	if s.OCI {
		return OCIPrefix + s.Host + "/" + s.Namespace + "/" + s.Type
	}
	return s.Host + "/" + s.Namespace + "/" + s.Type
}

// storeDir is the source's place below the provider and schema stores. A port
// in the host is written after an underscore, which is also how it appears in the
// generated package path.
func (s Source) storeDir() string {
	host := strings.ReplaceAll(s.Host, ":", "_")
	if s.OCI {
		return filepath.Join("oci", host, filepath.FromSlash(s.Namespace), s.Type)
	}
	return filepath.Join(host, s.Namespace, s.Type)
}

// Display returns the address without the default host.
func (s Source) Display() string {
	if s.Host == DefaultHost {
		return s.Namespace + "/" + s.Type
	}
	return s.String()
}

// Platform is an os/arch pair in registry notation.
type Platform struct {
	OS   string `json:"os"`
	Arch string `json:"arch"`
}

// CurrentPlatform is the platform pravo runs on.
func CurrentPlatform() Platform { return Platform{OS: runtime.GOOS, Arch: runtime.GOARCH} }

func (p Platform) String() string { return p.OS + "_" + p.Arch }

// VersionInfo is one entry of the registry's version list.
type VersionInfo struct {
	Version   string     `json:"version"`
	Protocols []string   `json:"protocols"`
	Platforms []Platform `json:"platforms"`
}

type downloadInfo struct {
	Protocols   []string `json:"protocols"`
	OS          string   `json:"os"`
	Arch        string   `json:"arch"`
	Filename    string   `json:"filename"`
	DownloadURL string   `json:"download_url"`
	Shasum      string   `json:"shasum"`

	ShasumsURL          string      `json:"shasums_url"`
	ShasumsSignatureURL string      `json:"shasums_signature_url"`
	SigningKeys         signingKeys `json:"signing_keys"`
}

// Client talks to provider registries and manages the local provider store.
type Client struct {
	HTTP *http.Client
	// Home is the pravo home directory ($PRAVO_HOME, default ~/.pravo).
	Home string
	// Env is the environment the client reads TF_PLUGIN_CACHE_DIR and the home of the
	// user from, to find providers that OpenTofu installed. Empty unless set.
	Env env.Env
	// Mirror, when set, says where the archives of providers are downloaded from, in place
	// of their sources (see Mirror). NewClient sets it from PRAVO_PROVIDER_MIRROR.
	Mirror    *Mirror
	mirrorErr error

	mu        sync.Mutex
	discovery map[string]string
}

// NewClient returns a client using the default home directory of e.
func NewClient(e env.Env) *Client {
	c := &Client{HTTP: newHTTPClient(), Home: Home(e), Env: e}
	// A setting that does not parse is reported by the first use of the client, not by
	// reading what is installed.
	c.Mirror, c.mirrorErr = ParseMirror(e.Get(MirrorVar))
	if c.mirrorErr != nil {
		c.mirrorErr = fmt.Errorf("%s: %w", MirrorVar, c.mirrorErr)
	}
	return c
}

// newHTTPClient is the client for registries. It retries what may pass, gives up
// on a registry that stops answering, and refuses a redirect from https to http,
// which would send the answer and the checksum in clear text. Loopback is
// exempt, as it is for the container registry client.
func newHTTPClient() *http.Client {
	base := http.DefaultTransport.(*http.Transport).Clone()
	base.ResponseHeaderTimeout = idleTimeout
	base.TLSHandshakeTimeout = 30 * time.Second
	return &http.Client{
		Transport: &retryTransport{base: base},
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 10 {
				return errors.New("stopped after 10 redirects")
			}
			if req.URL.Host != via[0].URL.Host {
				req.Header.Del("Authorization")
			}
			if req.URL.Scheme != "https" && via[len(via)-1].URL.Scheme == "https" && !loopback(req.URL.Hostname()) {
				return fmt.Errorf("refusing a redirect from https to %s", clean(req.URL.Redacted()))
			}
			return nil
		},
	}
}

// loopback reports whether host is the local machine, where plain http is allowed.
func loopback(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// secureURL reports whether u is https, or http to the local machine.
func secureURL(u string) bool {
	pu, err := url.Parse(u)
	if err != nil {
		return false
	}
	return pu.Scheme == "https" || (pu.Scheme == "http" && loopback(pu.Hostname()))
}

// clean makes text that a registry sent safe to print: control characters,
// which could rewrite a terminal or add lines to a CI log, become "?".
func clean(s string) string {
	return strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f || (r >= 0x80 && r < 0xa0) {
			return '?'
		}
		return r
	}, s)
}

// idleBody is a response body that fails when no byte arrives for idleTimeout.
type idleBody struct {
	io.ReadCloser
	timer  *time.Timer
	cancel context.CancelFunc
	start  time.Time
	read   int64
}

// minRate is the slowest average, in bytes a second, that a download may have
// once it has run for idleTimeout: a registry that sends one byte a second keeps
// the idle timer alive and holds a job forever.
const minRate = 1 << 10

func (b *idleBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	b.read += int64(n)
	if el := time.Since(b.start); err == nil && el > idleTimeout && float64(b.read)/el.Seconds() < minRate {
		return n, fmt.Errorf("the registry sends %d bytes a second; giving up", int64(float64(b.read)/el.Seconds()))
	}
	b.timer.Reset(idleTimeout)
	if err != nil && errors.Is(err, context.Canceled) {
		err = fmt.Errorf("the registry sent nothing for %s", idleTimeout)
	}
	return n, err
}

func (b *idleBody) Close() error {
	b.timer.Stop()
	err := b.ReadCloser.Close()
	b.cancel()
	return err
}

// idleTransport makes every response body of a round tripper an idleBody.
type idleTransport struct{ base http.RoundTripper }

func (t idleTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	ctx, cancel := context.WithCancel(req.Context())
	timer := time.AfterFunc(idleTimeout, cancel)
	resp, err := t.base.RoundTrip(req.WithContext(ctx))
	if err != nil {
		timer.Stop()
		cancel()
		return nil, err
	}
	resp.Body = &idleBody{ReadCloser: resp.Body, timer: timer, cancel: cancel, start: time.Now()}
	return resp, nil
}

// token is the credential for a registry host, read like OpenTofu reads it from
// TF_TOKEN_<host>, with "." written "_" and "-" written "__". It goes only to that
// exact host and only over https (or to the local machine): a redirect to another
// host drops it.
func (c *Client) token(u *url.URL) string {
	if !secureURL(u.String()) {
		return ""
	}
	name := "TF_TOKEN_" + strings.NewReplacer(".", "_", "-", "__", ":", "_").Replace(strings.ToLower(u.Host))
	return strings.TrimSpace(c.Env.Get(name))
}

// get starts a GET request whose body is read under the idle timeout. The caller closes the body.
func (c *Client) get(ctx context.Context, u string) (*http.Response, error) {
	ctx, cancel := context.WithCancel(ctx)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, http.NoBody)
	if err != nil {
		cancel()
		return nil, err
	}
	req.Header.Set("User-Agent", "pravo")
	if tok := c.token(req.URL); tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	timer := time.AfterFunc(idleTimeout, cancel)
	resp, err := c.HTTP.Do(req)
	if err != nil {
		timer.Stop()
		cancel()
		return nil, err
	}
	resp.Body = &idleBody{ReadCloser: resp.Body, timer: timer, cancel: cancel, start: time.Now()}
	return resp, nil
}

// Home returns $PRAVO_HOME or ~/.pravo, from the environment e.
func Home(e env.Env) string {
	if h := e.Get("PRAVO_HOME"); h != "" {
		return h
	}
	home, ok := e.UserHomeDir()
	if !ok {
		return ".pravo"
	}
	return filepath.Join(home, ".pravo")
}

// ProviderDir is where a provider version for the current platform is unpacked.
func (c *Client) ProviderDir(src Source, ver string) string {
	return filepath.Join(c.Home, "providers", src.storeDir(), ver, CurrentPlatform().String())
}

// SchemaCachePath is where the provider's schema JSON is cached. Newer versions
// of pravo keep it as a pack, at SchemaPackPath, and read this file only when
// the pack is not there.
func (c *Client) SchemaCachePath(src Source, ver string) string {
	return filepath.Join(c.Home, "schemas", src.storeDir(), ver+".json")
}

// SchemaPackPath is where the provider's schema is cached as a pack, an index
// and one document for each type, so that a run reads the types it uses.
func (c *Client) SchemaPackPath(src Source, ver string) string {
	return filepath.Join(c.Home, "schemas", src.storeDir(), ver+".pack")
}

func (c *Client) providersBase(ctx context.Context, host string) (string, error) {
	c.mu.Lock()
	if c.discovery == nil {
		c.discovery = map[string]string{}
	}
	if base, ok := c.discovery[host]; ok {
		c.mu.Unlock()
		return base, nil
	}
	c.mu.Unlock()

	u := "https://" + host + "/.well-known/terraform.json"
	var disc map[string]string
	if err := c.getJSON(ctx, u, &disc); err != nil {
		return "", fmt.Errorf("registry discovery for %s: %w", host, err)
	}
	p, ok := disc["providers.v1"]
	if !ok {
		return "", fmt.Errorf("registry %s does not offer the providers.v1 service", host)
	}
	base := p
	if !strings.HasPrefix(p, "http") {
		base = "https://" + host + p
	}
	if !secureURL(base) {
		return "", fmt.Errorf("registry %s names the providers.v1 service at %s; the address must be https", host, clean(base))
	}
	if !strings.HasSuffix(base, "/") {
		base += "/"
	}
	c.mu.Lock()
	c.discovery[host] = base
	c.mu.Unlock()
	return base, nil
}

func (c *Client) getJSON(ctx context.Context, u string, out any) error {
	resp, err := c.get(ctx, u)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return &statusError{URL: u, Code: resp.StatusCode, Status: resp.Status, Body: bodyLine(body, resp.Status)}
	}
	lr := &io.LimitedReader{R: resp.Body, N: maxJSON + 1}
	if err := json.NewDecoder(lr).Decode(out); err != nil {
		if lr.N <= 0 {
			return fmt.Errorf("the answer is larger than %d MB", maxJSON>>20)
		}
		return err
	}
	return nil
}

// statusError is a registry response that is not 200.
type statusError struct {
	URL    string
	Code   int
	Status string
	// Body is at most one short line of the response body.
	Body string
}

func (e *statusError) Error() string {
	if e.Body == "" {
		return fmt.Sprintf("GET %s: %s", e.URL, e.Status)
	}
	return fmt.Sprintf("GET %s: %s: %s", e.URL, e.Status, e.Body)
}

// notFound reports whether err is a registry's 404.
func notFound(err error) bool {
	var se *statusError
	return errors.As(err, &se) && se.Code == http.StatusNotFound
}

// maxBodyLine is the longest part of a response body that an error shows.
const maxBodyLine = 120

// bodyLine reduces the body of an error response to one short line: a JSON
// body compacted, the title of a web page, or the first line of text. A page
// without a title and a body that only repeats the status give nothing.
func bodyLine(body []byte, status string) string {
	text := strings.TrimSpace(string(body))
	switch {
	case text == "":
		return ""
	case json.Valid(body):
		var b bytes.Buffer
		if json.Compact(&b, body) == nil {
			text = b.String()
		}
	case strings.HasPrefix(text, "<"):
		lower := strings.ToLower(text)
		i := strings.Index(lower, "<title>")
		if i < 0 {
			return ""
		}
		j := strings.Index(lower[i:], "</title>")
		if j < 0 {
			return ""
		}
		text = strings.Join(strings.Fields(text[i+len("<title>"):i+j]), " ")
	default:
		text, _, _ = strings.Cut(text, "\n")
		text = strings.TrimSpace(text)
	}
	if text == "" || strings.Contains(strings.ToLower(status), strings.ToLower(text)) {
		return ""
	}
	if r := []rune(text); len(r) > maxBodyLine {
		text = string(r[:maxBodyLine]) + "..."
	}
	return clean(text)
}

// Versions lists the versions the registry offers for src.
func (c *Client) Versions(ctx context.Context, src Source) ([]VersionInfo, error) {
	base, err := c.providersBase(ctx, src.Host)
	if err != nil {
		return nil, err
	}
	var resp struct {
		Versions []VersionInfo `json:"versions"`
	}
	u := base + url.PathEscape(src.Namespace) + "/" + url.PathEscape(src.Type) + "/versions"
	if err := c.getJSON(ctx, u, &resp); err != nil {
		if notFound(err) {
			return nil, fmt.Errorf("provider %s was not found; check the name", src.String())
		}
		return nil, fmt.Errorf("listing versions of %s: %w", src.Display(), err)
	}
	return resp.Versions, nil
}

// Resolve picks the newest version satisfying constraint (empty = any) that is
// available for the current platform and speaks protocol 5 or 6.
func (c *Client) Resolve(ctx context.Context, src Source, constraint string) (string, error) {
	if c.mirrorErr != nil {
		return "", c.mirrorErr
	}
	if m, ok := c.MirrorOf(src); ok {
		return c.resolveOCI(ctx, m, constraint)
	}
	if src.OCI {
		return c.resolveOCI(ctx, src, constraint)
	}
	var cons version.Constraints
	if strings.TrimSpace(constraint) != "" {
		var err error
		cons, err = version.NewConstraint(constraint)
		if err != nil {
			return "", fmt.Errorf("invalid version constraint %q for %s: %w", constraint, src.Display(), err)
		}
	}
	infos, err := c.Versions(ctx, src)
	if err != nil {
		return "", err
	}
	plat := CurrentPlatform()
	type cand struct {
		v   *version.Version
		raw string
	}
	var cands []cand
	for _, vi := range infos {
		v, err := version.NewVersion(vi.Version)
		if err != nil {
			continue
		}
		if cons != nil && !cons.Check(v) {
			continue
		}
		if !supportsProtocol(vi.Protocols) {
			continue
		}
		if !hasPlatform(vi.Platforms, plat) {
			continue
		}
		cands = append(cands, cand{v, vi.Version})
	}
	if len(cands) == 0 {
		return "", fmt.Errorf("no version of %s matches %q for %s with plugin protocol 5 or 6", src.Display(), constraint, plat)
	}
	sort.Slice(cands, func(i, j int) bool { return cands[i].v.GreaterThan(cands[j].v) })
	return cands[0].raw, nil
}

func supportsProtocol(protocols []string) bool {
	if len(protocols) == 0 {
		return true
	}
	for _, p := range protocols {
		if strings.HasPrefix(p, "5") || strings.HasPrefix(p, "6") {
			return true
		}
	}
	return false
}

func hasPlatform(ps []Platform, want Platform) bool {
	if len(ps) == 0 {
		return true
	}
	for _, p := range ps {
		if p.OS == want.OS && p.Arch == want.Arch {
			return true
		}
	}
	return false
}

// FindInstalled looks for an already unpacked binary, in pravo's own store
// first and then in the OpenTofu plugin cache ($TF_PLUGIN_CACHE_DIR, or the
// default ~/.terraform.d/plugin-cache that OpenTofu inherited), keyed by the
// provider's own registry host.
func (c *Client) FindInstalled(src Source, ver string) (string, bool) {
	// pravo's own store counts only a finished install: a binary there
	// without the completion marker, or cut short, is a leftover of an
	// interrupted unpack and is installed again.
	if p, ok := findComplete(c.ProviderDir(src, ver), src.Type); ok {
		return p, true
	}
	var dirs []string
	plat := CurrentPlatform().String()
	var caches []string
	if src.OCI {
		// The OpenTofu plugin cache is keyed by registry address; it holds
		// nothing for a provider from a container registry.
		return "", false
	}
	if d := c.Env.Get("TF_PLUGIN_CACHE_DIR"); d != "" {
		caches = append(caches, d)
	}
	if home, ok := c.Env.UserHomeDir(); ok {
		caches = append(caches, filepath.Join(home, ".terraform.d", "plugin-cache"))
	}
	for _, cache := range caches {
		dirs = append(dirs, filepath.Join(cache, src.Host, src.Namespace, src.Type, ver, plat))
	}
	return findIn(dirs, src.Type)
}

// completeMarker names the file that Install writes into a finished provider
// directory. It holds the size of the binary.
const completeMarker = ".complete"

// findComplete finds the binary in an install directory that Install finished.
func findComplete(dir, typ string) (string, bool) {
	p, ok := FindBinary(dir, typ)
	if !ok {
		return "", false
	}
	want, err := os.ReadFile(filepath.Join(dir, completeMarker))
	if err != nil {
		return "", false
	}
	st, err := os.Stat(p)
	if err != nil || strings.TrimSpace(string(want)) != strconv.FormatInt(st.Size(), 10) {
		return "", false
	}
	return p, true
}

func findIn(dirs []string, typ string) (string, bool) {
	for _, d := range dirs {
		if p, ok := FindBinary(d, typ); ok {
			return p, true
		}
	}
	return "", false
}

// FindBinary finds the provider executable inside dir.
func FindBinary(dir, typ string) (string, bool) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", false
	}
	prefix := "terraform-provider-" + typ
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasPrefix(name, prefix) || strings.HasSuffix(name, ".zip") {
			continue
		}
		info, err := e.Info()
		if err != nil || !executable(name, info.Mode()) {
			continue
		}
		return filepath.Join(dir, name), true
	}
	return "", false
}

// executable reports whether a file of this name and mode is a program: on
// Windows a file named for an executable type, elsewhere a file with an
// execute bit.
func executable(name string, mode os.FileMode) bool {
	if runtime.GOOS == "windows" {
		return strings.HasSuffix(strings.ToLower(name), ".exe")
	}
	return mode&0o111 != 0
}

// InstallResult describes an installed provider binary.
type InstallResult struct {
	Path    string
	Version string
	// Hash is "sha256:<hex>" of the downloaded archive, or empty when the
	// binary was found locally.
	Hash string
	// Downloaded reports whether a download happened.
	Downloaded bool
	// Signer is the fingerprint of the key that signed the checksum list of the
	// download, or empty when the registry did not sign it.
	Signer string
}

func truthy(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

// staleStage is how old an unfinished staging directory must be before a
// later install removes it. A kill leaves one behind; a running install
// finishes well inside this.
const staleStage = 6 * time.Hour

// sweepStale removes staging directories that a killed install left in parent.
func sweepStale(parent string) {
	ents, err := os.ReadDir(parent)
	if err != nil {
		return
	}
	for _, e := range ents {
		if !e.IsDir() || !strings.HasPrefix(e.Name(), ".install-") {
			continue
		}
		if fi, err := e.Info(); err == nil && time.Since(fi.ModTime()) > staleStage {
			os.RemoveAll(filepath.Join(parent, e.Name()))
		}
	}
}

// Install makes sure the provider version is on disk and returns its binary.
func (c *Client) Install(ctx context.Context, src Source, ver string, progress func(string)) (*InstallResult, error) {
	if p, ok := c.FindInstalled(src, ver); ok {
		return &InstallResult{Path: p, Version: ver}, nil
	}
	dir := c.ProviderDir(src, ver)
	// Unpack beside the final directory and rename it into place, so that
	// the final directory is either absent or complete, whatever kills this
	// process or runs at the same time.
	parent := filepath.Dir(dir)
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return nil, err
	}
	sweepStale(parent)
	stage, err := os.MkdirTemp(parent, ".install-*")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(stage)
	archive, sum, signer, err := c.archive(ctx, src, ver, CurrentPlatform(), stage, progress)
	if err != nil {
		return nil, err
	}
	if err := unzip(archive, stage); err != nil {
		return nil, fmt.Errorf("unpacking %s %s: %w", src.Display(), ver, err)
	}
	os.Remove(archive)
	bin, ok := FindBinary(stage, src.Type)
	if !ok {
		return nil, fmt.Errorf("the archive of %s %s did not contain a terraform-provider-%s executable", src.Display(), ver, src.Type)
	}
	st, err := os.Stat(bin)
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(filepath.Join(stage, completeMarker), []byte(fmt.Sprintln(st.Size())), 0o644); err != nil {
		return nil, err
	}
	var last error
	for range 5 {
		err := os.Rename(stage, dir)
		if err == nil {
			p, _ := FindBinary(dir, src.Type)
			return &InstallResult{Path: p, Version: ver, Hash: "sha256:" + sum, Downloaded: true, Signer: signer}, nil
		}
		last = err
		// The directory is there: a concurrent install won, or an old,
		// unfinished one is in the way.
		if p, ok := findComplete(dir, src.Type); ok {
			return &InstallResult{Path: p, Version: ver, Hash: "sha256:" + sum, Downloaded: true, Signer: signer}, nil
		}
		os.RemoveAll(dir)
	}
	return nil, fmt.Errorf("installing %s %s into %s: %w", src.Display(), ver, dir, last)
}

// Checksum returns the sha256 of the archive of one provider version for plat,
// as the source states it, without downloading the archive.
func (c *Client) Checksum(ctx context.Context, src Source, ver string, plat Platform) (string, error) {
	if c.mirrorErr != nil {
		return "", c.mirrorErr
	}
	if m, ok := c.MirrorOf(src); ok {
		return c.checksumOCI(ctx, m, ver, plat)
	}
	if src.OCI {
		return c.checksumOCI(ctx, src, ver, plat)
	}
	dl, err := c.downloadInfo(ctx, src, ver, plat)
	if err != nil {
		return "", err
	}
	return statedSum(src, ver, plat, dl)
}

// ChecksumSigned is Checksum for a provider that is already on disk: it also checks the
// signature of the checksum list, as an install does, and returns the fingerprint of the key
// that made it ("" when the registry does not sign). A lock file written beside a provider
// that was installed by an earlier run then holds what an install would have written. A
// signature that does not verify is an error, and so is a missing one when
// PRAVO_REQUIRE_SIGNED is set; the checksum is not returned then.
func (c *Client) ChecksumSigned(ctx context.Context, src Source, ver string, plat Platform) (sum, signer string, err error) {
	if c.mirrorErr != nil {
		return "", "", c.mirrorErr
	}
	if m, ok := c.MirrorOf(src); ok {
		// What the mirror states is not a source of truth: the lock file is. The caller
		// compares the archive with the lock file; there is no signature to check here.
		sum, err = c.checksumOCI(ctx, m, ver, plat)
		return sum, "", err
	}
	if src.OCI {
		sum, err = c.checksumOCI(ctx, src, ver, plat)
		if err != nil {
			return "", "", err
		}
		signer, err = c.checkSigned(ctx, src, ver, nil, sum)
		if err != nil {
			return "", "", err
		}
		return sum, signer, nil
	}
	dl, err := c.downloadInfo(ctx, src, ver, plat)
	if err != nil {
		return "", "", err
	}
	if sum, err = statedSum(src, ver, plat, dl); err != nil {
		return "", "", err
	}
	if signer, err = c.checkSigned(ctx, src, ver, dl, sum); err != nil {
		return "", "", err
	}
	return sum, signer, nil
}

// downloadInfo asks the registry for the download of one provider version for plat. It is the
// only place that builds the address, words a version that is not there and refuses a build for
// another platform or protocol; Checksum, ChecksumSigned and archive all call it.
func (c *Client) downloadInfo(ctx context.Context, src Source, ver string, plat Platform) (*downloadInfo, error) {
	base, err := c.providersBase(ctx, src.Host)
	if err != nil {
		return nil, err
	}
	var dl downloadInfo
	u := fmt.Sprintf("%s%s/%s/%s/download/%s/%s", base, url.PathEscape(src.Namespace), url.PathEscape(src.Type), ver, plat.OS, plat.Arch)
	if err := c.getJSON(ctx, u, &dl); err != nil {
		if notFound(err) {
			return nil, fmt.Errorf("%s v%s was not found for %s", src.Display(), ver, plat)
		}
		return nil, fmt.Errorf("resolving download for %s %s: %w", src.Display(), ver, err)
	}
	if err := checkDownloadInfo(&dl, plat); err != nil {
		return nil, fmt.Errorf("%s %s: %w", src.Display(), ver, err)
	}
	return &dl, nil
}

// statedSum is the checksum the registry states for a download, lower-case.
func statedSum(src Source, ver string, plat Platform, dl *downloadInfo) (string, error) {
	if dl.Shasum == "" {
		return "", fmt.Errorf("%s does not state a checksum for %s %s (%s)", src.Host, src.Display(), ver, plat)
	}
	return strings.ToLower(dl.Shasum), nil
}

// checkSigned is the one rule for the signature of a checksum list: a signature that does
// not verify is an error, and an unsigned one is an error when PRAVO_REQUIRE_SIGNED is set.
// A provider from an OCI registry has no signed checksum list, so the setting refuses it as
// it refuses an unsigned HTTP registry. dl is nil for OCI.
func (c *Client) checkSigned(ctx context.Context, src Source, ver string, dl *downloadInfo, sum string) (signer string, err error) {
	if dl != nil && dl.signed() {
		signer, err = c.verifySignature(ctx, dl, sum)
		if err != nil {
			return "", fmt.Errorf("%s %s: %w", src.Display(), ver, err)
		}
		return signer, nil
	}
	if truthy(c.Env.Get("PRAVO_REQUIRE_SIGNED")) {
		return "", fmt.Errorf("%s %s is not signed by the registry (no signing keys or no signature of the checksum list), and PRAVO_REQUIRE_SIGNED is set", src.Display(), ver)
	}
	return "", nil
}

// Archive downloads the zip archive of one provider version for plat into
// dir, checks it against the checksum the registry gave, and returns its path
// and sha256.
func (c *Client) Archive(ctx context.Context, src Source, ver string, plat Platform, dir string, progress func(string)) (path, sum string, err error) {
	path, sum, _, err = c.archive(ctx, src, ver, plat, dir, progress)
	return path, sum, err
}

// archive is Archive that also returns the fingerprint of the key that signed
// the checksum list, or "" when the registry did not sign it.
func (c *Client) archive(ctx context.Context, src Source, ver string, plat Platform, dir string, progress func(string)) (path, sum, signer string, err error) {
	if c.mirrorErr != nil {
		return "", "", "", c.mirrorErr
	}
	if m, ok := c.MirrorOf(src); ok {
		// The archive is checked against the lock file by the caller, which refuses one that
		// differs; a container registry has no signed checksum list of its own.
		path, sum, err = c.archiveOCI(ctx, m, ver, plat, dir, progress)
		if err != nil {
			return "", "", "", err
		}
		return path, sum, "", nil
	}
	if src.OCI {
		path, sum, err = c.archiveOCI(ctx, src, ver, plat, dir, progress)
		if err != nil {
			return "", "", "", err
		}
		if signer, err = c.checkSigned(ctx, src, ver, nil, sum); err != nil {
			os.Remove(path)
			return "", "", "", err
		}
		return path, sum, signer, nil
	}
	dl, err := c.downloadInfo(ctx, src, ver, plat)
	if err != nil {
		return "", "", "", err
	}
	if progress != nil {
		progress(fmt.Sprintf("Downloading %s v%s (%s)", src.Display(), ver, clean(dl.Filename)))
	}
	if !secureURL(dl.DownloadURL) {
		return "", "", "", fmt.Errorf("the registry gave the download address %s for %s %s; it must be https", clean(dl.DownloadURL), src.Display(), ver)
	}
	resp, err := c.get(ctx, dl.DownloadURL)
	if err != nil {
		return "", "", "", fmt.Errorf("downloading %s: %w", clean(dl.DownloadURL), err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", "", "", fmt.Errorf("downloading %s: %s", clean(dl.DownloadURL), clean(resp.Status))
	}
	sum, path, err = spool(dir, resp.Body)
	if err != nil {
		return "", "", "", fmt.Errorf("downloading %s: %w", clean(dl.DownloadURL), err)
	}
	if dl.Shasum == "" {
		os.Remove(path)
		return "", "", "", fmt.Errorf("the registry gave no checksum for %s, so the archive cannot be checked", clean(dl.Filename))
	}
	if !strings.EqualFold(sum, dl.Shasum) {
		os.Remove(path)
		return "", "", "", fmt.Errorf("checksum mismatch for %s: registry says %s, archive is %s", clean(dl.Filename), clean(dl.Shasum), sum)
	}
	if signer, err = c.checkSigned(ctx, src, ver, dl, sum); err != nil {
		os.Remove(path)
		return "", "", "", err
	}
	return path, sum, signer, nil
}

// checkDownloadInfo refuses an answer that is for another platform than the one
// asked for, or for a plugin protocol that pravo does not speak.
func checkDownloadInfo(dl *downloadInfo, plat Platform) error {
	if (dl.OS != "" && dl.OS != plat.OS) || (dl.Arch != "" && dl.Arch != plat.Arch) {
		return fmt.Errorf("the registry answered with a build for %s_%s, but %s was asked for", clean(dl.OS), clean(dl.Arch), plat)
	}
	if !supportsProtocol(dl.Protocols) {
		return fmt.Errorf("the build speaks plugin protocol %s, and pravo speaks 5 and 6", clean(strings.Join(dl.Protocols, ", ")))
	}
	return nil
}

// spool writes r to a temporary file in dir and returns its sha256 and path.
func spool(dir string, r io.Reader) (sum, path string, err error) {
	tmp, err := os.CreateTemp(dir, ".download-*.zip")
	if err != nil {
		return "", "", err
	}
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(tmp, h), io.LimitReader(r, maxArchive+1))
	tmp.Close()
	if err == nil && n > maxArchive {
		err = fmt.Errorf("the archive is larger than %d MB", maxArchive>>20)
	}
	if err != nil {
		os.Remove(tmp.Name())
		return "", "", err
	}
	return hex.EncodeToString(h.Sum(nil)), tmp.Name(), nil
}

func unzip(archive, dir string) error {
	r, err := zip.OpenReader(archive)
	if err != nil {
		return err
	}
	defer r.Close()
	if len(r.File) > maxZipEntries {
		return fmt.Errorf("the archive has %d entries; a provider package has at most %d", len(r.File), maxZipEntries)
	}
	var total int64
	for _, f := range r.File {
		if !filepath.IsLocal(filepath.FromSlash(strings.TrimSuffix(f.Name, "/"))) {
			return fmt.Errorf("refusing to extract %q", clean(f.Name))
		}
		name := filepath.Clean(filepath.FromSlash(f.Name))
		if m := f.Mode(); !m.IsRegular() && !m.IsDir() {
			return fmt.Errorf("refusing to extract %q: it is a %s, and a provider package holds files and directories", clean(f.Name), kindOf(m))
		}
		dst := filepath.Join(dir, name)
		if f.FileInfo().IsDir() {
			if err := os.MkdirAll(dst, 0o755); err != nil {
				return err
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return err
		}
		rc, err := f.Open()
		if err != nil {
			return err
		}
		mode := f.Mode().Perm()
		if strings.HasPrefix(filepath.Base(name), "terraform-provider-") {
			mode |= 0o755
		}
		out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
		if err != nil {
			rc.Close()
			return err
		}
		// A provider is a few hundred megabytes at most. The caps keep a package
		// that lies about its size from filling the disk, and say so when they stop it.
		n, err := io.Copy(out, io.LimitReader(rc, maxUnzippedFile+1))
		rc.Close()
		if err == nil {
			err = out.Sync()
		}
		if cerr := out.Close(); err == nil {
			err = cerr
		}
		if err == nil && n > maxUnzippedFile {
			err = fmt.Errorf("%q unpacks to more than %d MB", clean(f.Name), maxUnzippedFile>>20)
		}
		if err == nil {
			total += n
			if total > maxUnzippedTotal {
				err = fmt.Errorf("the archive unpacks to more than %d MB", maxUnzippedTotal>>20)
			}
		}
		if err != nil {
			return err
		}
	}
	return nil
}

func kindOf(m os.FileMode) string {
	switch {
	case m&os.ModeSymlink != 0:
		return "symbolic link"
	case m&os.ModeDevice != 0, m&os.ModeCharDevice != 0:
		return "device"
	case m&os.ModeNamedPipe != 0:
		return "named pipe"
	case m&os.ModeSocket != 0:
		return "socket"
	}
	return "special file"
}
