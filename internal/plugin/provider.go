// Package plugin launches OpenTofu provider binaries (the same binaries legacy
// Terraform uses) and talks to them over the plugin protocol (versions 5 and
// 6), exposing a single Provider interface.
package plugin

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/hashicorp/terraform-plugin-go/tftypes"

	"github.com/pravo-systems/pravo/internal/schema"
)

// CoreVersion is reported to providers in the protocol's terraform_version
// field of ConfigureProvider. Some providers gate behaviour on it, so it is a
// plausible recent core version.
const CoreVersion = "1.9.8"

// The client capabilities pravo announces in every request that carries them.
// A write-only attribute is a configuration value that the provider reads and
// does not store. pravo handles one like any other attribute, so the
// capability is always announced; a provider built on the plugin framework
// refuses a non-null write-only attribute without it. pravo does not act on a
// deferred response, so it never allows deferral.
const (
	writeOnlyAttributesAllowed = true
	deferralAllowed            = false
)

// Diagnostic mirrors a provider diagnostic.
type Diagnostic struct {
	Severity string // "error" or "warning"
	Summary  string
	Detail   string
	Path     *tftypes.AttributePath
}

// Diagnostics is a list of provider diagnostics.
type Diagnostics []Diagnostic

// HasErrors reports whether any diagnostic is an error.
func (d Diagnostics) HasErrors() bool {
	for _, x := range d {
		if x.Severity == "error" {
			return true
		}
	}
	return false
}

// Err returns the diagnostics as an error if any is an error.
func (d Diagnostics) Err() error {
	if !d.HasErrors() {
		return nil
	}
	return d
}

func (d Diagnostics) Error() string {
	var parts []string
	for _, x := range d {
		if x.Severity != "error" {
			continue
		}
		s := x.Summary
		if x.Path != nil && len(x.Path.Steps()) > 0 {
			s = fmt.Sprintf("%s (at %s)", s, pathString(x.Path))
		}
		if x.Detail != "" {
			s += ": " + strings.TrimSpace(x.Detail)
		}
		parts = append(parts, s)
	}
	return strings.Join(parts, "\n")
}

// Warnings returns only the warning diagnostics.
func (d Diagnostics) Warnings() Diagnostics {
	var out Diagnostics
	for _, x := range d {
		if x.Severity == "warning" {
			out = append(out, x)
		}
	}
	return out
}

func pathString(p *tftypes.AttributePath) string {
	var b strings.Builder
	for _, s := range p.Steps() {
		switch st := s.(type) {
		case tftypes.AttributeName:
			if b.Len() > 0 {
				b.WriteByte('.')
			}
			b.WriteString(string(st))
		case tftypes.ElementKeyString:
			fmt.Fprintf(&b, "[%q]", string(st))
		case tftypes.ElementKeyInt:
			fmt.Fprintf(&b, "[%d]", int64(st))
		case tftypes.ElementKeyValue:
			b.WriteString("[*]")
		}
	}
	return b.String()
}

// PlanResult is the outcome of PlanResourceChange.
type PlanResult struct {
	PlannedState     tftypes.Value
	RequiresReplace  []*tftypes.AttributePath
	PlannedPrivate   []byte
	LegacyTypeSystem bool
}

// ApplyResult is the outcome of ApplyResourceChange.
type ApplyResult struct {
	NewState         tftypes.Value
	Private          []byte
	LegacyTypeSystem bool
}

// EphemeralResult is the outcome of opening or renewing an ephemeral resource.
type EphemeralResult struct {
	Result  tftypes.Value
	Private []byte
	RenewAt *time.Time
	// Deferred is set when the provider cannot open the resource yet.
	Deferred bool
}

// FunctionError is an error returned by a provider function.
type FunctionError struct {
	Text     string
	Argument *int64
}

func (e *FunctionError) Error() string {
	if e.Argument != nil {
		return fmt.Sprintf("argument %d: %s", *e.Argument, e.Text)
	}
	return e.Text
}

// ImportedResource is one resource returned by ImportResourceState.
type ImportedResource struct {
	TypeName string
	State    tftypes.Value
	Private  []byte
}

// Provider is the protocol-independent view of a running provider plugin.
// All values are typed by the schema the caller obtained from GetSchema.
type Provider interface {
	ProtocolVersion() int
	GetSchema(ctx context.Context) (*schema.ProviderSchema, Diagnostics, error)
	ValidateProviderConfig(ctx context.Context, cfg tftypes.Value) (Diagnostics, error)
	Configure(ctx context.Context, cfg tftypes.Value) (Diagnostics, error)
	ValidateResourceConfig(ctx context.Context, typ string, cfg tftypes.Value) (Diagnostics, error)
	ValidateDataSourceConfig(ctx context.Context, typ string, cfg tftypes.Value) (Diagnostics, error)
	UpgradeResourceState(ctx context.Context, typ string, version int64, jsonState []byte, want tftypes.Type) (tftypes.Value, Diagnostics, error)
	ReadResource(ctx context.Context, typ string, state tftypes.Value, private []byte) (tftypes.Value, []byte, Diagnostics, error)
	PlanResourceChange(ctx context.Context, typ string, prior, proposed, config tftypes.Value, priorPrivate []byte) (*PlanResult, Diagnostics, error)
	ApplyResourceChange(ctx context.Context, typ string, prior, planned, config tftypes.Value, plannedPrivate []byte) (*ApplyResult, Diagnostics, error)
	ImportResourceState(ctx context.Context, typ, id string, types map[string]tftypes.Type) ([]ImportedResource, Diagnostics, error)
	ReadDataSource(ctx context.Context, typ string, cfg tftypes.Value) (tftypes.Value, Diagnostics, error)
	ValidateEphemeralConfig(ctx context.Context, typ string, cfg tftypes.Value) (Diagnostics, error)
	OpenEphemeral(ctx context.Context, typ string, cfg tftypes.Value) (*EphemeralResult, Diagnostics, error)
	RenewEphemeral(ctx context.Context, typ string, private []byte) (*EphemeralResult, Diagnostics, error)
	CloseEphemeral(ctx context.Context, typ string, private []byte) (Diagnostics, error)
	// CallFunction invokes a provider-defined function. ret is the declared
	// return type used to decode the result.
	CallFunction(ctx context.Context, name string, args []tftypes.Value, argTypes []tftypes.Type, ret tftypes.Type) (tftypes.Value, *FunctionError, error)
	Stop(ctx context.Context) error
	// Close terminates the plugin process.
	Close()
}

func marshal(v tftypes.Value) ([]byte, error) {
	return v.MarshalMsgPack(v.Type())
}

func unmarshal(msgpack, jsonData []byte, t tftypes.Type) (tftypes.Value, error) {
	switch {
	case len(msgpack) > 0:
		return tftypes.ValueFromMsgPack(msgpack, t)
	case len(jsonData) > 0:
		return tftypes.ValueFromJSON(jsonData, t)
	default:
		return tftypes.NewValue(t, nil), nil
	}
}
