// Package plugin launches OpenTofu provider binaries (the same binaries legacy
// Terraform uses) and talks to them over the plugin protocol (versions 5 and
// 6), exposing a single Provider interface.
package plugin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/hashicorp/terraform-plugin-go/tftypes"
	"github.com/vmihailenco/msgpack/v5"

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

// ListedResource is one object that a provider listed.
type ListedResource struct {
	// Identity is the identity of the object as the provider gave it. It is decoded
	// without the identity schema, so a number is an int64 or a float64, a list is a
	// []any and an object a map[string]any.
	Identity map[string]any
	// DisplayName is what the provider calls the object in a list, such as its name.
	DisplayName string
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
	// ImportResourceState asks the provider for the object that target names: by id,
	// or by the identity of the resource type.
	ImportResourceState(ctx context.Context, typ string, target ImportTarget, types map[string]tftypes.Type) ([]ImportedResource, Diagnostics, error)
	ReadDataSource(ctx context.Context, typ string, cfg tftypes.Value) (tftypes.Value, Diagnostics, error)
	ValidateEphemeralConfig(ctx context.Context, typ string, cfg tftypes.Value) (Diagnostics, error)
	OpenEphemeral(ctx context.Context, typ string, cfg tftypes.Value) (*EphemeralResult, Diagnostics, error)
	RenewEphemeral(ctx context.Context, typ string, private []byte) (*EphemeralResult, Diagnostics, error)
	CloseEphemeral(ctx context.Context, typ string, private []byte) (Diagnostics, error)
	// ListResource lists the objects of a list resource type in the scope that cfg
	// sets, at most limit of them (0 is no limit of pravo's: the provider's own
	// bound holds). A diagnostic of an object, or of the stream, is in the
	// diagnostics.
	ListResource(ctx context.Context, typ string, cfg tftypes.Value, limit int64) ([]ListedResource, Diagnostics, error)
	// CallFunction invokes a provider-defined function. ret is the declared
	// return type used to decode the result.
	CallFunction(ctx context.Context, name string, args []tftypes.Value, argTypes []tftypes.Type, ret tftypes.Type) (tftypes.Value, *FunctionError, error)
	Stop(ctx context.Context) error
	// Close terminates the plugin process.
	Close()
}

// decodeIdentity reads the identity of a listed object. The identity schema is
// not asked for: the value is decoded as MessagePack is, which gives the names
// and the values that a provider sent.
func decodeIdentity(msgpackData, jsonData []byte) (map[string]any, error) {
	var raw any
	switch {
	case len(msgpackData) > 0:
		if err := msgpack.Unmarshal(msgpackData, &raw); err != nil {
			return nil, fmt.Errorf("decoding the identity of a listed object: %w", err)
		}
	case len(jsonData) > 0:
		if err := json.Unmarshal(jsonData, &raw); err != nil {
			return nil, fmt.Errorf("decoding the identity of a listed object: %w", err)
		}
	default:
		return nil, nil
	}
	m, ok := normalize(raw).(map[string]any)
	if !ok && raw != nil {
		return nil, errors.New("the identity of a listed object is not an object")
	}
	return m, nil
}

// normalize turns what MessagePack decodes into the values that encoding/json
// writes the way a person expects: every integer an int64, every map keyed by
// strings.
func normalize(v any) any {
	switch x := v.(type) {
	case int8:
		return int64(x)
	case int16:
		return int64(x)
	case int32:
		return int64(x)
	case int:
		return int64(x)
	case uint8:
		return int64(x)
	case uint16:
		return int64(x)
	case uint32:
		return int64(x)
	case uint64:
		return int64(x)
	case uint:
		return int64(x)
	case float32:
		return float64(x)
	case []any:
		out := make([]any, len(x))
		for i, e := range x {
			out[i] = normalize(e)
		}
		return out
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, e := range x {
			out[k] = normalize(e)
		}
		return out
	case map[any]any:
		out := make(map[string]any, len(x))
		for k, e := range x {
			out[fmt.Sprint(k)] = normalize(e)
		}
		return out
	}
	return v
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
