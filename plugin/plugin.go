// Package plugin is doze core's host for out-of-process engine modules. An engine
// plugin is a separate binary that implements the Engine gRPC service (see
// proto/); core launches it via HashiCorp go-plugin and adapts it back to the
// in-tree engine.Driver + capability interfaces through pluginDriver, so the rest
// of the runtime treats a plugin exactly like a compiled-in engine.
package plugin

import (
	"context"

	goplugin "github.com/hashicorp/go-plugin"
	"google.golang.org/grpc"

	"github.com/doze-dev/doze-sdk/engine"
	"github.com/doze-dev/doze-sdk/plugin/proto"
)

// Handshake gates host↔plugin compatibility; a mismatched ProtocolVersion or
// cookie aborts the launch cleanly.
var Handshake = goplugin.HandshakeConfig{
	ProtocolVersion:  1,
	MagicCookieKey:   "DOZE_PLUGIN",
	MagicCookieValue: "engine",
}

// pluginKey is the dispensed plugin name.
const pluginKey = "engine"

// pluginSet maps the dispensed name to the gRPC plugin. The server side wraps a
// driver (set on the plugin's Impl); the client side leaves it nil.
func pluginSet(drv engine.Driver) goplugin.PluginSet {
	return goplugin.PluginSet{pluginKey: &enginePlugin{impl: drv}}
}

// enginePlugin bridges go-plugin to the Engine gRPC service in both directions.
type enginePlugin struct {
	goplugin.NetRPCUnsupportedPlugin
	impl engine.Driver // server side only
}

// GRPCServer registers the engine implementation (server side, in the plugin).
func (p *enginePlugin) GRPCServer(_ *goplugin.GRPCBroker, s *grpc.Server) error {
	proto.RegisterEngineServer(s, newEngineServer(p.impl))
	return nil
}

// GRPCClient returns the host-side adapter wrapping the gRPC client.
func (p *enginePlugin) GRPCClient(_ context.Context, _ *goplugin.GRPCBroker, c *grpc.ClientConn) (any, error) {
	return newPluginDriver(proto.NewEngineClient(c)), nil
}
