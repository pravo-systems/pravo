package registry

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"sort"
	"strings"

	"cuelabs.dev/go/oci/ociregistry"
	"cuelabs.dev/go/oci/ociregistry/ociauth"
	"cuelabs.dev/go/oci/ociregistry/ociclient"
	"github.com/hashicorp/go-version"
	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

// A provider kept in a container registry is a repository named
// <path>/<type>. Each version is a tag holding an image index with one
// manifest per platform; the manifest's only layer is the zip archive that the
// provider registry protocol would serve.
const (
	ProviderArtifactType = "application/vnd.pravo.provider.v1"
	providerLayerType    = "application/vnd.pravo.provider.layer.v1.zip"
	emptyConfigType      = "application/vnd.oci.empty.v1+json"
)

// DefaultPlatforms are the platforms "providers copy" and "providers lock" use unless told
// otherwise.
var DefaultPlatforms = []Platform{
	{OS: "linux", Arch: "amd64"}, {OS: "linux", Arch: "arm64"},
	{OS: "darwin", Arch: "arm64"}, {OS: "darwin", Arch: "amd64"},
}

func (s Source) repo() string { return s.Namespace + "/" + s.Type }

// ociClient connects to a container registry using the credentials of the
// docker configuration. Plain HTTP is used for localhost only.
func ociClient(host string) (ociregistry.Interface, error) {
	cfg, err := ociauth.Load(nil)
	if err != nil {
		return nil, fmt.Errorf("reading container registry credentials: %w", err)
	}
	name := host
	if h, _, err := net.SplitHostPort(host); err == nil {
		name = h
	}
	insecure := name == "localhost" || name == "127.0.0.1" || name == "::1"
	return ociclient.New(host, &ociclient.Options{
		Insecure:  insecure,
		Transport: ociauth.NewStdTransport(ociauth.StdTransportParams{Config: cfg}),
	})
}

func readAll(r ociregistry.BlobReader, err error) ([]byte, error) {
	if err != nil {
		return nil, err
	}
	defer r.Close()
	return io.ReadAll(r)
}

// ociIndex fetches the index tagged ver.
func ociIndex(ctx context.Context, cl ociregistry.Interface, src Source, ver string) (*ocispec.Index, error) {
	data, err := readAll(cl.GetTag(ctx, src.repo(), ver))
	if err != nil {
		return nil, err
	}
	var idx ocispec.Index
	if err := json.Unmarshal(data, &idx); err != nil {
		return nil, fmt.Errorf("the manifest tagged %s is not an image index: %w", ver, err)
	}
	return &idx, nil
}

func hasOCIPlatform(idx *ocispec.Index, p Platform) (ocispec.Descriptor, bool) {
	for _, d := range idx.Manifests {
		if d.Platform != nil && d.Platform.OS == p.OS && d.Platform.Architecture == p.Arch {
			return d, true
		}
	}
	return ocispec.Descriptor{}, false
}

// resolveOCI picks the newest tag satisfying constraint that has a manifest
// for the current platform.
func (c *Client) resolveOCI(ctx context.Context, src Source, constraint string) (string, error) {
	var cons version.Constraints
	if strings.TrimSpace(constraint) != "" {
		var err error
		cons, err = version.NewConstraint(constraint)
		if err != nil {
			return "", fmt.Errorf("invalid version constraint %q for %s: %w", constraint, src.Display(), err)
		}
	}
	cl, err := ociClient(src.Host)
	if err != nil {
		return "", err
	}
	tags, err := ociregistry.All(cl.Tags(ctx, src.repo(), ""))
	if err != nil {
		return "", fmt.Errorf("listing versions of %s: %w", src.Display(), err)
	}
	type cand struct {
		v   *version.Version
		raw string
	}
	var cands []cand
	for _, t := range tags {
		v, err := version.NewVersion(t)
		if err != nil || (cons != nil && !cons.Check(v)) {
			continue
		}
		cands = append(cands, cand{v, t})
	}
	sort.Slice(cands, func(i, j int) bool { return cands[i].v.GreaterThan(cands[j].v) })
	plat := CurrentPlatform()
	for _, cd := range cands {
		idx, err := ociIndex(ctx, cl, src, cd.raw)
		if err != nil {
			continue
		}
		if _, ok := hasOCIPlatform(idx, plat); ok {
			return cd.raw, nil
		}
	}
	return "", fmt.Errorf("no version of %s matches %q for %s", src.Display(), constraint, plat)
}

// ociLayer finds the archive layer of one platform's manifest, checking the
// manifest against the digest its index names.
func ociLayer(ctx context.Context, cl ociregistry.Interface, src Source, ver string, plat Platform) (ocispec.Descriptor, error) {
	idx, err := ociIndex(ctx, cl, src, ver)
	if err != nil {
		return ocispec.Descriptor{}, fmt.Errorf("resolving %s %s: %w", src.Display(), ver, err)
	}
	md, ok := hasOCIPlatform(idx, plat)
	if !ok {
		return ocispec.Descriptor{}, fmt.Errorf("%s %s has no build for %s", src.Display(), ver, plat)
	}
	data, err := readAll(cl.GetManifest(ctx, src.repo(), md.Digest))
	if err != nil {
		return ocispec.Descriptor{}, fmt.Errorf("reading the manifest of %s %s: %w", src.Display(), ver, err)
	}
	if digest.FromBytes(data) != md.Digest {
		return ocispec.Descriptor{}, fmt.Errorf("the manifest of %s %s does not match the digest %s that its index names", src.Display(), ver, md.Digest)
	}
	var m ocispec.Manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return ocispec.Descriptor{}, err
	}
	if len(m.Layers) != 1 {
		return ocispec.Descriptor{}, fmt.Errorf("the manifest of %s %s has %d layers; want one archive", src.Display(), ver, len(m.Layers))
	}
	return m.Layers[0], nil
}

// archiveOCI downloads the archive layer of one platform's manifest and checks
// it against the digest the manifest names.
func (c *Client) archiveOCI(ctx context.Context, src Source, ver string, plat Platform, dir string, progress func(string)) (string, string, error) {
	cl, err := ociClient(src.Host)
	if err != nil {
		return "", "", err
	}
	layer, err := ociLayer(ctx, cl, src, ver, plat)
	if err != nil {
		return "", "", err
	}
	if progress != nil {
		progress(fmt.Sprintf("Downloading %s v%s (%s)", src.Display(), ver, plat))
	}
	r, err := cl.GetBlob(ctx, src.repo(), layer.Digest)
	if err != nil {
		return "", "", fmt.Errorf("downloading %s %s: %w", src.Display(), ver, err)
	}
	defer r.Close()
	sum, path, err := spool(dir, r)
	if err != nil {
		return "", "", fmt.Errorf("downloading %s %s: %w", src.Display(), ver, err)
	}
	if want := layer.Digest.Encoded(); want != sum {
		os.Remove(path)
		return "", "", fmt.Errorf("digest mismatch for %s %s: the manifest says %s, archive is %s", src.Display(), ver, want, sum)
	}
	return path, sum, nil
}

// checksumOCI is the sha256 the manifest of one platform names for its archive.
func (c *Client) checksumOCI(ctx context.Context, src Source, ver string, plat Platform) (string, error) {
	cl, err := ociClient(src.Host)
	if err != nil {
		return "", err
	}
	layer, err := ociLayer(ctx, cl, src, ver, plat)
	if err != nil {
		return "", err
	}
	return layer.Digest.Encoded(), nil
}

// Copy publishes version ver of the provider src to the container registry
// repository dst, for each platform in plats the source has. src may be a
// provider registry or another container registry. It refuses to replace a
// version that is already published.
func (c *Client) Copy(ctx context.Context, src, dst Source, ver string, plats []Platform, progress func(string)) error {
	if !dst.OCI {
		return fmt.Errorf("the destination %s is not an oci:// address", dst.String())
	}
	if _, err := version.NewVersion(ver); err != nil {
		return fmt.Errorf("version %q: %w", ver, err)
	}
	cl, err := ociClient(dst.Host)
	if err != nil {
		return err
	}
	if _, err := cl.ResolveTag(ctx, dst.repo(), ver); err == nil {
		return fmt.Errorf("%s already has version %s; a published version must not change", dst.Display(), ver)
	} else if !errors.Is(err, ociregistry.ErrManifestUnknown) && !errors.Is(err, ociregistry.ErrNameUnknown) {
		return err
	}
	have := plats
	if !src.OCI {
		infos, err := c.Versions(ctx, src)
		if err != nil {
			return err
		}
		have = nil
		for _, vi := range infos {
			if vi.Version == ver {
				for _, p := range plats {
					if hasPlatform(vi.Platforms, p) && len(vi.Platforms) > 0 {
						have = append(have, p)
					}
				}
			}
		}
	}
	if len(have) == 0 {
		return fmt.Errorf("%s %s has none of the requested platforms", src.Display(), ver)
	}
	tmp, err := os.MkdirTemp("", "pravo-copy-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)

	var built []archive
	for _, p := range have {
		if progress != nil {
			progress(fmt.Sprintf("Copying %s v%s (%s)", src.Display(), ver, p))
		}
		path, sum, err := c.Archive(ctx, src, ver, p, tmp, nil)
		if err != nil {
			return err
		}
		built = append(built, archive{plat: p, path: path, sum: sum})
	}
	return pushVersion(ctx, cl, dst, ver, built)
}

// archive is a provider zip ready to be published for one platform.
type archive struct {
	plat      Platform
	path, sum string
}

// pushVersion publishes archives as the tagged version ver of dst.
func pushVersion(ctx context.Context, cl ociregistry.Interface, dst Source, ver string, archives []archive) error {
	empty := ocispec.Descriptor{MediaType: emptyConfigType, Digest: digest.FromBytes([]byte("{}")), Size: 2, Data: []byte("{}")}
	if _, err := cl.PushBlob(ctx, dst.repo(), empty, bytes.NewReader([]byte("{}"))); err != nil {
		return fmt.Errorf("pushing to %s: %w", dst.Display(), err)
	}
	idx := ocispec.Index{MediaType: ocispec.MediaTypeImageIndex, ArtifactType: ProviderArtifactType}
	idx.SchemaVersion = 2
	for _, a := range archives {
		f, err := os.Open(a.path)
		if err != nil {
			return err
		}
		st, _ := f.Stat()
		layer := ocispec.Descriptor{MediaType: providerLayerType, Digest: digest.NewDigestFromEncoded(digest.SHA256, a.sum), Size: st.Size()}
		_, err = cl.PushBlob(ctx, dst.repo(), layer, f)
		f.Close()
		os.Remove(a.path)
		if err != nil {
			return fmt.Errorf("pushing to %s: %w", dst.Display(), err)
		}
		m := ocispec.Manifest{MediaType: ocispec.MediaTypeImageManifest, ArtifactType: ProviderArtifactType, Config: empty, Layers: []ocispec.Descriptor{layer}}
		m.SchemaVersion = 2
		data, _ := json.Marshal(m)
		md, err := cl.PushManifest(ctx, dst.repo(), "", data, ocispec.MediaTypeImageManifest)
		if err != nil {
			return fmt.Errorf("pushing to %s: %w", dst.Display(), err)
		}
		md.Platform = &ocispec.Platform{OS: a.plat.OS, Architecture: a.plat.Arch}
		idx.Manifests = append(idx.Manifests, md)
	}
	data, _ := json.Marshal(idx)
	if _, err := cl.PushManifest(ctx, dst.repo(), ver, data, ocispec.MediaTypeImageIndex); err != nil {
		return fmt.Errorf("pushing to %s: %w", dst.Display(), err)
	}
	return nil
}
