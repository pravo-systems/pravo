package engine

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// A root that pravo shadow wrote stands beside the Terraform root it was decompiled
// from, and Terraform still owns the objects. Until somebody takes the root over, an
// apply that would change an object is refused: the shadow can be planned as often as
// wanted and can do no harm.

// ShadowLockFile is the note that pravo shadow leaves beside the specification.
const ShadowLockFile = "shadow.lock.json"

// ShadowLock says whose the objects of a decompiled root are.
type ShadowLock struct {
	// Owner is "terraform" until the root is taken over, and "pravo" after.
	Owner string `json:"owner"`
	// Source is the absolute path or the s3:// URL of the state that was decompiled; it is
	// empty when that was standard input.
	Source string `json:"source,omitempty"`
	// Region, Endpoint and Profile are what an s3:// source was read with.
	Region   string `json:"region,omitempty"`
	Endpoint string `json:"endpoint,omitempty"`
	Profile  string `json:"profile,omitempty"`
	Lineage  string `json:"lineage,omitempty"`
	Serial   int64  `json:"serial"`
	SHA256   string `json:"sha256"`
	Taken    string `json:"taken"`
	// Addresses maps the address of each resource here to its Terraform address.
	Addresses map[string]string `json:"addresses,omitempty"`
}

// NewShadowLock describes the state that a shadow was taken from.
func NewShadowLock(src ShadowSource, data []byte) *ShadowLock {
	var head struct {
		Lineage string `json:"lineage"`
		Serial  int64  `json:"serial"`
	}
	_ = json.Unmarshal(data, &head)
	sum := sha256.Sum256(data)
	place := src.Place
	switch {
	case place == "-":
		place = ""
	case !src.IsS3():
		if abs, err := filepath.Abs(place); err == nil {
			place = abs
		}
	}
	l := &ShadowLock{Owner: "terraform", Source: place, Lineage: head.Lineage, Serial: head.Serial, SHA256: hex.EncodeToString(sum[:]), Taken: time.Now().UTC().Format(time.RFC3339)}
	if src.IsS3() {
		l.Region, l.Endpoint, l.Profile = src.Region, src.Endpoint, src.Profile
	}
	return l
}

// SourceOf is the place the state of the shadow is read from again.
func (l *ShadowLock) SourceOf() ShadowSource {
	return ShadowSource{Place: l.Source, Region: l.Region, Endpoint: l.Endpoint, Profile: l.Profile}
}

// ReadShadowLock reads the note of the root in dir. It is nil where there is none.
func ReadShadowLock(dir string) (*ShadowLock, error) {
	b, err := os.ReadFile(filepath.Join(dir, ShadowLockFile))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var l ShadowLock
	if err := json.Unmarshal(b, &l); err != nil {
		return nil, fmt.Errorf("%s: %w", ShadowLockFile, err)
	}
	return &l, nil
}

// Write writes the note into dir.
func (l *ShadowLock) Write(dir string) error {
	b, err := json.MarshalIndent(l, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, ShadowLockFile), append(b, '\n'), 0o644)
}

// Moved says how the state differs from what the shadow was taken from: "" when it is
// the same, byte for byte, or when it cannot be read from here.
func (l *ShadowLock) Moved(ctx context.Context) string {
	if l.Source == "" {
		return ""
	}
	data, err := l.SourceOf().Read(ctx)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(data)
	if hex.EncodeToString(sum[:]) == l.SHA256 {
		return ""
	}
	var head struct {
		Serial int64 `json:"serial"`
	}
	_ = json.Unmarshal(data, &head)
	return fmt.Sprintf("the Terraform state has moved since the shadow was taken (serial %d then, %d now): take the shadow again with pravo shadow --force", l.Serial, head.Serial)
}

// shadowGuard refuses a plan that changes an object of a root that Terraform owns.
func (e *Engine) shadowGuard(ctx context.Context, plan *Plan) error {
	if e.Root == nil || plan == nil {
		return nil
	}
	l, err := ReadShadowLock(e.Root.Dir)
	if err != nil || l == nil || l.Owner != "terraform" {
		return err
	}
	var touched []string
	for _, c := range plan.Changes {
		if c.Applied || c.Mode == "data" {
			continue
		}
		switch c.Action {
		case ActionCreate, ActionUpdate, ActionReplace, ActionDelete:
			touched = append(touched, fmt.Sprintf("%s (%s)", c.Addr, c.Action))
		}
	}
	if len(touched) == 0 {
		return nil
	}
	shown := touched
	if len(shown) > 5 {
		shown = append(append([]string(nil), shown[:5]...), fmt.Sprintf("and %d more", len(touched)-5))
	}
	msg := fmt.Sprintf("apply refused: this root is a shadow of the Terraform state %s (serial %d), and Terraform still owns its objects. The plan would change %s: %s. Nothing was changed.\nPlan here as often as you like. To change the objects, either apply the change with Terraform and take the shadow again, or hand the objects over to pravo with: pravo shadow take-over",
		l.Source, l.Serial, nodes(len(touched)), strings.Join(shown, ", "))
	if m := l.Moved(ctx); m != "" {
		msg += "\nAlso: " + m + "."
	}
	return WithCode(ExitRefused, errors.New(msg))
}
