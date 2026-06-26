package plugin

import (
	goplugin "github.com/hashicorp/go-plugin"

	"github.com/nerdmenot/doze-sdk/engine"
)

// Serve runs drv as a doze engine plugin. An engine module's main() implements the
// in-tree engine.Driver (+ capability) interfaces and calls Serve — go-plugin then
// speaks the Engine gRPC service to doze core. (Pending the public dozeplugin SDK
// module in Phase 5; for now plugins live in-tree and import this package.)
func Serve(drv engine.Driver) {
	goplugin.Serve(&goplugin.ServeConfig{
		HandshakeConfig: Handshake,
		Plugins:         pluginSet(drv),
		GRPCServer:      goplugin.DefaultGRPCServer,
	})
}
