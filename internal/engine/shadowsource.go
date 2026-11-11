package engine

import (
	"context"
	"fmt"
	"io"
	"net/url"
	"os"
	"strings"

	"github.com/pravo-systems/pravo/internal/state"
)

// ShadowSource says where the Terraform or OpenTofu state of a shadow is read from.
// The place is a file, "-" for standard input, or s3://bucket/key for a state that an
// S3 backend holds: the rest of the fields only matter for the last.
type ShadowSource struct {
	// Place is the file, "-" or the s3:// URL.
	Place string
	// Stdin is read for "-".
	Stdin io.Reader
	// Region, Endpoint and Profile configure the S3 client as they do for the state of
	// a root; the credentials come from the usual chain.
	Region, Endpoint, Profile string
}

// IsS3 says that the state sits in an S3 bucket.
func (s ShadowSource) IsS3() bool { return strings.HasPrefix(s.Place, "s3://") }

// Name is how the source is written in the notes and the specification: the base name of
// a file, and the URL of an object.
func (s ShadowSource) Name() string {
	if s.Place == "-" {
		return "standard input"
	}
	if s.IsS3() {
		return s.Place
	}
	return baseName(s.Place)
}

func baseName(p string) string {
	if i := strings.LastIndexAny(p, `/\`); i >= 0 {
		return p[i+1:]
	}
	return p
}

// Read returns the bytes of the state.
func (s ShadowSource) Read(ctx context.Context) ([]byte, error) {
	switch {
	case s.Place == "-":
		if s.Stdin == nil {
			return nil, fmt.Errorf("no standard input to read the state from")
		}
		return io.ReadAll(s.Stdin)
	case s.IsS3():
		u, err := url.Parse(s.Place)
		if err != nil || u.Host == "" || strings.Trim(u.Path, "/") == "" {
			return nil, fmt.Errorf("%s: want s3://bucket/key", s.Place)
		}
		b, err := state.NewS3Backend(ctx, state.S3Config{Bucket: u.Host, Region: s.Region, Endpoint: s.Endpoint, Profile: s.Profile})
		if err != nil {
			return nil, err
		}
		data, err := b.Get(ctx, strings.TrimPrefix(u.Path, "/"))
		if err != nil {
			return nil, fmt.Errorf("%s: %w", s.Place, err)
		}
		return data, nil
	}
	return os.ReadFile(s.Place)
}
