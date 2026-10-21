package plugin

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/hashicorp/go-hclog"
	goplugin "github.com/hashicorp/go-plugin"
	"github.com/hashicorp/go-plugin/runner"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Reattachment says where an already running provider listens. pravo
// connects to it with the plugin protocol and never starts or stops it.
type Reattachment struct {
	// Network is "unix" or "tcp"; Address is a socket path or host:port.
	Network string
	Address string
	// ProtocolVersion is the plugin protocol version (5 or 6); 0 means
	// probe: try 6, then 5.
	ProtocolVersion int
}

func (r Reattachment) String() string {
	return r.Network + "://" + r.Address
}

// ParseAddress parses "unix:///path/to/socket" or "tcp://host:port". Traffic
// to a provider includes configuration, state and write-only values, so TCP
// is accepted only on the loopback interface; use a unix socket or a tunnel
// for anything else.
func ParseAddress(s string) (Reattachment, error) {
	u, err := url.Parse(s)
	if err != nil || u.Scheme == "" {
		return Reattachment{}, fmt.Errorf("invalid provider address %q: want unix:///path/to/socket or tcp://127.0.0.1:port", s)
	}
	switch u.Scheme {
	case "unix":
		path := u.Path
		if path == "" {
			path = u.Opaque
		}
		if path == "" {
			return Reattachment{}, fmt.Errorf("invalid provider address %q: no socket path", s)
		}
		return Reattachment{Network: "unix", Address: path}, nil
	case "tcp":
		host, port, err := net.SplitHostPort(u.Host)
		if err != nil {
			return Reattachment{}, fmt.Errorf("invalid provider address %q: want tcp://host:port", s)
		}
		if err := requireLoopback(host); err != nil {
			return Reattachment{}, fmt.Errorf("provider address %q: %w", s, err)
		}
		return Reattachment{Network: "tcp", Address: net.JoinHostPort(host, port)}, nil
	}
	return Reattachment{}, fmt.Errorf("invalid provider address %q: scheme %q is not supported (unix, tcp)", s, u.Scheme)
}

func requireLoopback(host string) error {
	if host == "localhost" {
		return nil
	}
	if ip := net.ParseIP(host); ip != nil && ip.IsLoopback() {
		return nil
	}
	return fmt.Errorf("tcp is accepted on the loopback interface only, because the connection carries configuration, state and secrets in clear; use a unix socket or an encrypted tunnel")
}

type reattachEntry struct {
	Protocol        string
	ProtocolVersion int
	Pid             int
	Test            bool
	Addr            struct{ Network, String string }
}

// ReattachFromEnv looks the provider up in TF_REATTACH_PROVIDERS, the
// variable debuggers and provider test harnesses print and that OpenTofu
// honours: a JSON map from provider address to where it listens. The key
// must match the full source, or its namespace/type (providers print the
// registry host they were built for, which need not be the one pravo uses).
func ReattachFromEnv(candidates ...string) (*Reattachment, error) {
	raw := os.Getenv("TF_REATTACH_PROVIDERS")
	if raw == "" {
		return nil, nil
	}
	var m map[string]reattachEntry
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		return nil, fmt.Errorf("TF_REATTACH_PROVIDERS is not valid: %w", err)
	}
	for _, want := range candidates {
		for key, e := range m {
			if key != want && !strings.HasSuffix(key, "/"+want) && !strings.HasSuffix(want, "/"+key) {
				continue
			}
			if e.Protocol != "" && e.Protocol != "grpc" {
				return nil, fmt.Errorf("TF_REATTACH_PROVIDERS[%s]: protocol %q is not supported (grpc)", key, e.Protocol)
			}
			r := Reattachment{Network: e.Addr.Network, Address: e.Addr.String, ProtocolVersion: e.ProtocolVersion}
			switch r.Network {
			case "unix":
			case "tcp":
				host, _, err := net.SplitHostPort(r.Address)
				if err != nil {
					return nil, fmt.Errorf("TF_REATTACH_PROVIDERS[%s]: %w", key, err)
				}
				if err := requireLoopback(host); err != nil {
					return nil, fmt.Errorf("TF_REATTACH_PROVIDERS[%s]: %w", key, err)
				}
			default:
				return nil, fmt.Errorf("TF_REATTACH_PROVIDERS[%s]: network %q is not supported (unix, tcp)", key, r.Network)
			}
			return &r, nil
		}
	}
	return nil, nil
}

// attached is the runner for a process pravo did not start: it waits until
// the connection is closed and kills nothing.
type attached struct {
	done chan struct{}
	once sync.Once
}

func (a *attached) Wait(ctx context.Context) error {
	select {
	case <-a.done:
	case <-ctx.Done():
	}
	return nil
}
func (a *attached) Kill(context.Context) error { return nil }
func (a *attached) ID() string                 { return "reattached" }
func (a *attached) PluginToHost(n, ad string) (string, string, error) {
	return n, ad, nil
}
func (a *attached) HostToPlugin(n, ad string) (string, string, error) {
	return n, ad, nil
}
func (a *attached) release() { a.once.Do(func() { close(a.done) }) }

type addr struct{ network, str string }

func (a addr) Network() string { return a.network }
func (a addr) String() string  { return a.str }

// Reattach connects to a running provider. It never starts, stops or
// signals the process; closing the returned Provider only drops the
// connection.
func Reattach(r Reattachment, logger hclog.Logger) (Provider, error) {
	if logger == nil {
		logger = Logger()
	}
	versions := []int{6, 5}
	if r.ProtocolVersion != 0 {
		versions = []int{r.ProtocolVersion}
	}
	var last error
	for _, v := range versions {
		p, err := reattachVersion(r, v, logger)
		if err == nil {
			return p, nil
		}
		last = err
		if st, ok := status.FromError(unwrapStatus(err)); !ok || st.Code() != codes.Unimplemented {
			break // not a version mismatch: no point probing further
		}
	}
	if st, ok := status.FromError(unwrapStatus(last)); ok && st.Code() == codes.Unavailable {
		return nil, fmt.Errorf("reattaching to provider at %s: nothing is listening there; is the provider running? (%w)", r, last)
	}
	return nil, fmt.Errorf("reattaching to provider at %s: %w", r, last)
}

// statusError carries a gRPC status through fmt.Errorf wrapping.
type statusError struct{ err error }

func (s statusError) Error() string { return s.err.Error() }
func (s statusError) Unwrap() error { return s.err }

func unwrapStatus(err error) error {
	for e := err; e != nil; {
		if se, ok := e.(statusError); ok {
			return se.err
		}
		u, ok := e.(interface{ Unwrap() error })
		if !ok {
			break
		}
		e = u.Unwrap()
	}
	return err
}

func reattachVersion(r Reattachment, version int, logger hclog.Logger) (Provider, error) {
	att := &attached{done: make(chan struct{})}
	client := goplugin.NewClient(&goplugin.ClientConfig{
		HandshakeConfig: Handshake,
		// Reattaching in test mode skips the handshake that normally
		// selects the plugin set, so give it directly.
		Plugins:          goplugin.PluginSet{"provider": &grpcPlugin{version: version}},
		VersionedPlugins: map[int]goplugin.PluginSet{version: {"provider": &grpcPlugin{version: version}}},
		AllowedProtocols: []goplugin.Protocol{goplugin.ProtocolGRPC},
		Logger:           logger,
		Managed:          false,
		Reattach: &goplugin.ReattachConfig{
			Protocol:        goplugin.ProtocolGRPC,
			ProtocolVersion: version,
			Addr:            addr{r.Network, r.Address},
			// Test mode: Kill never touches the process, which is not ours.
			Test:         true,
			ReattachFunc: func() (runner.AttachedRunner, error) { return att, nil },
		},
		GRPCDialOptions: []grpc.DialOption{
			grpc.WithDefaultCallOptions(grpc.MaxCallRecvMsgSize(maxMessageSize), grpc.MaxCallSendMsgSize(maxMessageSize)),
		},
	})
	closeAll := func() { att.release(); client.Kill() }
	rpc, err := client.Client()
	if err != nil {
		closeAll()
		return nil, err
	}
	raw, err := rpc.Dispense("provider")
	if err != nil {
		closeAll()
		return nil, err
	}
	var p Provider
	switch x := raw.(type) {
	case *v5:
		x.close = closeAll
		p = x
	case *v6:
		x.close = closeAll
		p = x
	default:
		closeAll()
		return nil, fmt.Errorf("unexpected plugin type %T", raw)
	}
	// Probe: a provider that speaks the other version answers Unimplemented.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if _, _, err := p.GetSchema(ctx); err != nil {
		closeAll()
		if st, ok := status.FromError(err); ok && st.Code() == codes.Unimplemented {
			return nil, statusError{err}
		}
		return nil, err
	}
	return p, nil
}
