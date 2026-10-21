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
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"sort"
	"strings"
	"sync"

	"github.com/hashicorp/go-version"
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
			return Source{}, fmt.Errorf("empty provider source")
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
}

// Client talks to provider registries and manages the local provider store.
type Client struct {
	HTTP *http.Client
	// Home is the pravo home directory ($PRAVO_HOME, default ~/.pravo).
	Home string

	mu        sync.Mutex
	discovery map[string]string
}

// NewClient returns a client using the default home directory.
func NewClient() *Client {
	return &Client{HTTP: &http.Client{Transport: &retryTransport{}}, Home: Home()}
}

// Home returns $PRAVO_HOME or ~/.pravo.
func Home() string {
	if h := os.Getenv("PRAVO_HOME"); h != "" {
		return h
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ".pravo"
	}
	return filepath.Join(home, ".pravo")
}

// ProviderDir is where a provider version for the current platform is unpacked.
func (c *Client) ProviderDir(src Source, ver string) string {
	return filepath.Join(c.Home, "providers", src.storeDir(), ver, CurrentPlatform().String())
}

// SchemaCachePath is where the provider's schema JSON is cached.
func (c *Client) SchemaCachePath(src Source, ver string) string {
	return filepath.Join(c.Home, "schemas", src.storeDir(), ver+".json")
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
	if !strings.HasSuffix(base, "/") {
		base += "/"
	}
	c.mu.Lock()
	c.discovery[host] = base
	c.mu.Unlock()
	return base, nil
}

func (c *Client) getJSON(ctx context.Context, u string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "pravo")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return &statusError{URL: u, Code: resp.StatusCode, Status: resp.Status, Body: bodyLine(body, resp.Status)}
	}
	return json.NewDecoder(resp.Body).Decode(out)
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
	return text
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
	dirs := []string{c.ProviderDir(src, ver)}
	plat := CurrentPlatform().String()
	var caches []string
	if src.OCI {
		// The OpenTofu plugin cache is keyed by registry address; it holds
		// nothing for a provider from a container registry.
		return findIn(dirs, src.Type)
	}
	if d := os.Getenv("TF_PLUGIN_CACHE_DIR"); d != "" {
		caches = append(caches, d)
	}
	if home, err := os.UserHomeDir(); err == nil {
		caches = append(caches, filepath.Join(home, ".terraform.d", "plugin-cache"))
	}
	for _, cache := range caches {
		dirs = append(dirs, filepath.Join(cache, src.Host, src.Namespace, src.Type, ver, plat))
	}
	return findIn(dirs, src.Type)
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
}

// Install makes sure the provider version is on disk and returns its binary.
func (c *Client) Install(ctx context.Context, src Source, ver string, progress func(string)) (*InstallResult, error) {
	if p, ok := c.FindInstalled(src, ver); ok {
		return &InstallResult{Path: p, Version: ver}, nil
	}
	dir := c.ProviderDir(src, ver)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	archive, sum, err := c.Archive(ctx, src, ver, CurrentPlatform(), dir, progress)
	if err != nil {
		return nil, err
	}
	defer os.Remove(archive)
	if err := unzip(archive, dir); err != nil {
		return nil, fmt.Errorf("unpacking %s %s: %w", src.Display(), ver, err)
	}
	p, ok := FindBinary(dir, src.Type)
	if !ok {
		return nil, fmt.Errorf("the archive of %s %s did not contain a terraform-provider-%s executable", src.Display(), ver, src.Type)
	}
	return &InstallResult{Path: p, Version: ver, Hash: "sha256:" + sum, Downloaded: true}, nil
}

// Checksum returns the sha256 of the archive of one provider version for plat,
// as the source states it, without downloading the archive.
func (c *Client) Checksum(ctx context.Context, src Source, ver string, plat Platform) (string, error) {
	if src.OCI {
		return c.checksumOCI(ctx, src, ver, plat)
	}
	base, err := c.providersBase(ctx, src.Host)
	if err != nil {
		return "", err
	}
	var dl downloadInfo
	u := fmt.Sprintf("%s%s/%s/%s/download/%s/%s", base, url.PathEscape(src.Namespace), url.PathEscape(src.Type), ver, plat.OS, plat.Arch)
	if err := c.getJSON(ctx, u, &dl); err != nil {
		if notFound(err) {
			return "", fmt.Errorf("%s v%s was not found for %s", src.Display(), ver, plat)
		}
		return "", fmt.Errorf("resolving download for %s %s: %w", src.Display(), ver, err)
	}
	if dl.Shasum == "" {
		return "", fmt.Errorf("%s does not state a checksum for %s %s (%s)", src.Host, src.Display(), ver, plat)
	}
	return strings.ToLower(dl.Shasum), nil
}

// Archive downloads the zip archive of one provider version for plat into
// dir, checks it against the checksum the registry gave, and returns its path
// and sha256.
func (c *Client) Archive(ctx context.Context, src Source, ver string, plat Platform, dir string, progress func(string)) (path, sum string, err error) {
	if src.OCI {
		return c.archiveOCI(ctx, src, ver, plat, dir, progress)
	}
	base, err := c.providersBase(ctx, src.Host)
	if err != nil {
		return "", "", err
	}
	var dl downloadInfo
	u := fmt.Sprintf("%s%s/%s/%s/download/%s/%s", base, url.PathEscape(src.Namespace), url.PathEscape(src.Type), ver, plat.OS, plat.Arch)
	if err := c.getJSON(ctx, u, &dl); err != nil {
		if notFound(err) {
			return "", "", fmt.Errorf("%s v%s was not found for %s", src.Display(), ver, plat)
		}
		return "", "", fmt.Errorf("resolving download for %s %s: %w", src.Display(), ver, err)
	}
	if progress != nil {
		progress(fmt.Sprintf("Downloading %s v%s (%s)", src.Display(), ver, dl.Filename))
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, dl.DownloadURL, nil)
	if err != nil {
		return "", "", err
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return "", "", fmt.Errorf("downloading %s: %w", dl.DownloadURL, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", "", fmt.Errorf("downloading %s: %s", dl.DownloadURL, resp.Status)
	}
	sum, path, err = spool(dir, resp.Body)
	if err != nil {
		return "", "", fmt.Errorf("downloading %s: %w", dl.DownloadURL, err)
	}
	if dl.Shasum != "" && !strings.EqualFold(sum, dl.Shasum) {
		os.Remove(path)
		return "", "", fmt.Errorf("checksum mismatch for %s: registry says %s, archive is %s", dl.Filename, dl.Shasum, sum)
	}
	return path, sum, nil
}

// spool writes r to a temporary file in dir and returns its sha256 and path.
func spool(dir string, r io.Reader) (sum, path string, err error) {
	tmp, err := os.CreateTemp(dir, ".download-*.zip")
	if err != nil {
		return "", "", err
	}
	h := sha256.New()
	_, err = io.Copy(io.MultiWriter(tmp, h), r)
	tmp.Close()
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
	for _, f := range r.File {
		name := filepath.Clean(f.Name)
		if strings.HasPrefix(name, "..") || filepath.IsAbs(name) {
			return fmt.Errorf("refusing to extract %q", f.Name)
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
		_, err = io.Copy(out, rc)
		rc.Close()
		out.Close()
		if err != nil {
			return err
		}
	}
	return nil
}
