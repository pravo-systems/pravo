package registry

import (
	"fmt"
	"slices"
	"sort"
	"strings"
)

// MirrorVar is the environment variable that sets the provider mirror; the flag
// --provider-mirror sets it for one run.
const MirrorVar = "PRAVO_PROVIDER_MIRROR"

// A Mirror says where the archives of providers are downloaded from, in place of the
// provider registries their sources name. A provider keeps its address everywhere else:
// the import path, the key of the lock file and the provider recorded in the state are
// those of the source, and the provider is installed in the same place.
//
// Its setting is a list separated by commas. An entry that is an oci:// address is the
// prefix: a source namespace/type is read from <prefix>/<namespace>/<type>, and one
// prefix is allowed. An entry SOURCE=oci://host/path/type reads that source from that
// repository instead, as an exception to the prefix.
type Mirror struct {
	// Prefix is the repository path, with its host, that sources are placed under. Its
	// Type is empty. It is the zero Source when the setting has no prefix.
	Prefix Source
	// Overrides gives the repository of some sources, by Source.String of the source.
	Overrides map[string]Source
}

// ParseMirror parses the setting of a mirror. The empty setting is no mirror.
func ParseMirror(spec string) (*Mirror, error) {
	var m Mirror
	for _, part := range strings.Split(spec, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if left, right, ok := strings.Cut(part, "="); ok {
			src, err := ParseSource(left)
			if err != nil || src.OCI {
				return nil, fmt.Errorf("provider mirror %q: %q is not a provider source; write SOURCE=oci://host/path/type, as in hashicorp/aws=oci://registry.example.com/mirror/aws", part, strings.TrimSpace(left))
			}
			dst, err := ParseSource(strings.TrimSpace(right))
			if err != nil || !dst.OCI {
				return nil, fmt.Errorf("provider mirror %q: %q is not an oci://host/path/type address", part, strings.TrimSpace(right))
			}
			if m.Overrides == nil {
				m.Overrides = map[string]Source{}
			}
			if _, dup := m.Overrides[src.String()]; dup {
				return nil, fmt.Errorf("provider mirror: %s is given twice", src.Display())
			}
			m.Overrides[src.String()] = dst
			continue
		}
		prefix, err := ParseMirrorPrefix(part)
		if err != nil {
			return nil, fmt.Errorf("provider mirror %q: %w", part, err)
		}
		if m.Prefix.Host != "" {
			return nil, fmt.Errorf("provider mirror: two prefixes, %s and %s; give one prefix and the exceptions as SOURCE=oci://...", m.Prefix, prefix)
		}
		m.Prefix = prefix
	}
	if m.Prefix.Host == "" && len(m.Overrides) == 0 {
		return nil, nil
	}
	return &m, nil
}

// ParseMirrorPrefix parses oci://host/path, the repository path that providers are kept
// under in a mirror. The result has the host and the path as its Namespace, and no Type.
func ParseMirrorPrefix(s string) (Source, error) {
	rest, ok := strings.CutPrefix(strings.TrimSpace(s), OCIPrefix)
	if !ok {
		return Source{}, fmt.Errorf("want oci://host/path, as in oci://123456789012.dkr.ecr.us-east-2.amazonaws.com/pravo-providers")
	}
	parts := strings.Split(strings.TrimRight(rest, "/"), "/")
	if len(parts) < 2 || slices.Contains(parts, "") {
		return Source{}, fmt.Errorf("want oci://host/path, as in oci://123456789012.dkr.ecr.us-east-2.amazonaws.com/pravo-providers")
	}
	return Source{Host: strings.ToLower(parts[0]), Namespace: strings.Join(parts[1:], "/"), OCI: true}, nil
}

// Under is where a source is kept under the prefix p: <prefix>/<namespace>/<type>.
func (p Source) Under(src Source) Source {
	return Source{Host: p.Host, Namespace: p.Namespace + "/" + src.Namespace, Type: src.Type, OCI: true}
}

// For is the repository that holds src in the mirror. A source that is already in a
// container registry is not moved, unless it is named as an exception.
func (m *Mirror) For(src Source) (Source, bool) {
	if m == nil {
		return Source{}, false
	}
	if dst, ok := m.Overrides[src.String()]; ok {
		return dst, true
	}
	if src.OCI || m.Prefix.Host == "" {
		return Source{}, false
	}
	return m.Prefix.Under(src), true
}

// String is the setting that parses to m, the same way every time.
func (m *Mirror) String() string {
	var parts []string
	if m.Prefix.Host != "" {
		parts = append(parts, OCIPrefix+m.Prefix.Host+"/"+m.Prefix.Namespace)
	}
	keys := make([]string, 0, len(m.Overrides))
	for k := range m.Overrides {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		parts = append(parts, k+"="+m.Overrides[k].String())
	}
	return strings.Join(parts, ",")
}

// MirrorOf is the repository that the mirror of the client keeps src in, and whether src is
// read from the mirror.
func (c *Client) MirrorOf(src Source) (Source, bool) { return c.Mirror.For(src) }

// Direct returns a client that reads from the sources themselves, as one without a mirror
// does. A command that fills a mirror, or that records what a source states for the lock
// file, reads the sources.
func (c *Client) Direct() *Client {
	return &Client{HTTP: c.HTTP, Home: c.Home, Env: c.Env}
}

// RequireSigned reports whether PRAVO_REQUIRE_SIGNED is set.
func (c *Client) RequireSigned() bool { return truthy(c.Env.Get("PRAVO_REQUIRE_SIGNED")) }
