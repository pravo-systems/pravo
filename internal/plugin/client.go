package plugin

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"

	"github.com/hashicorp/go-hclog"
	goplugin "github.com/hashicorp/go-plugin"
	"google.golang.org/grpc"

	"github.com/pravo-systems/pravo/internal/tfplugin/tfplugin5"
	"github.com/pravo-systems/pravo/internal/tfplugin/tfplugin6"
)

// Handshake is the go-plugin handshake OpenTofu providers expect.
var Handshake = goplugin.HandshakeConfig{
	MagicCookieKey:   "TF_PLUGIN_MAGIC_COOKIE",
	MagicCookieValue: "d602bf8f470bc67ca7faa0386276bbdd4330efaf76d1a219cb4d6991ca9872b2",
}

const maxMessageSize = 256 << 20

type grpcPlugin struct {
	goplugin.NetRPCUnsupportedPlugin
	version int
}

func (g *grpcPlugin) GRPCServer(*goplugin.GRPCBroker, *grpc.Server) error {
	return errors.New("pravo is a plugin client only")
}

func (g *grpcPlugin) GRPCClient(ctx context.Context, broker *goplugin.GRPCBroker, conn *grpc.ClientConn) (interface{}, error) {
	switch g.version {
	case 5:
		return &v5{c: tfplugin5.NewProviderClient(conn)}, nil
	case 6:
		return &v6{c: tfplugin6.NewProviderClient(conn)}, nil
	}
	return nil, fmt.Errorf("unsupported protocol version %d", g.version)
}

// Logger returns the hclog logger used for plugin processes. PRAVO_LOG
// selects the level (trace, debug, info, warn, error); by default provider
// logs are not shown, since diagnostics are reported through the CLI.
func Logger() hclog.Logger {
	level := hclog.LevelFromString(os.Getenv("PRAVO_LOG"))
	if level == hclog.NoLevel {
		level = hclog.Off
	}
	return hclog.New(&hclog.LoggerOptions{Name: "plugin", Level: level, Output: os.Stderr, Color: hclog.AutoColor})
}

// Start launches the provider binary at path and negotiates the protocol.
// The returned Provider must be closed with Close.
func Start(path string, logger hclog.Logger) (Provider, error) {
	if logger == nil {
		logger = Logger()
	}
	cmd := exec.Command(path)
	cmd.Env = append(os.Environ(), "TF_PLUGIN_MAGIC_COOKIE="+Handshake.MagicCookieValue)
	client := goplugin.NewClient(&goplugin.ClientConfig{
		HandshakeConfig: Handshake,
		VersionedPlugins: map[int]goplugin.PluginSet{
			5: {"provider": &grpcPlugin{version: 5}},
			6: {"provider": &grpcPlugin{version: 6}},
		},
		Cmd:              cmd,
		AllowedProtocols: []goplugin.Protocol{goplugin.ProtocolGRPC},
		AutoMTLS:         true,
		Managed:          true,
		Logger:           logger,
		GRPCDialOptions: []grpc.DialOption{
			grpc.WithDefaultCallOptions(grpc.MaxCallRecvMsgSize(maxMessageSize), grpc.MaxCallSendMsgSize(maxMessageSize)),
		},
	})
	rpc, err := client.Client()
	if err != nil {
		client.Kill()
		return nil, fmt.Errorf("starting provider %s: %w", path, err)
	}
	raw, err := rpc.Dispense("provider")
	if err != nil {
		client.Kill()
		return nil, fmt.Errorf("dispensing provider %s: %w", path, err)
	}
	switch p := raw.(type) {
	case *v5:
		p.close = client.Kill
		return p, nil
	case *v6:
		p.close = client.Kill
		return p, nil
	}
	client.Kill()
	return nil, fmt.Errorf("unexpected plugin type %T", raw)
}

// CleanupClients kills all managed plugin processes.
func CleanupClients() { goplugin.CleanupClients() }
