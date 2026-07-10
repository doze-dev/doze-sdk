package plugin

import (
	"encoding/json"
	"os"

	goplugin "github.com/hashicorp/go-plugin"

	"github.com/doze-dev/doze-sdk/engine"
)

// DescribeArg is the subcommand that makes a plugin print its engine.Description
// as JSON and exit, instead of speaking the plugin protocol. It lets tooling
// (the doze library's EngineSchema, dzm) discover a module's config schema
// locally by running its binary — no registry round-trip. Plugins that don't
// implement engine.Describer print an empty object.
const DescribeArg = "__describe"

// Serve runs drv as a doze engine plugin. An engine module's main() implements the
// in-tree engine.Driver (+ capability) interfaces and calls Serve — go-plugin then
// speaks the Engine gRPC service to doze core. (Pending the public dozeplugin SDK
// module in Phase 5; for now plugins live in-tree and import this package.)
//
// Invoked as `<plugin> __describe`, it instead prints the driver's
// engine.Description as JSON and exits (see DescribeArg).
func Serve(drv engine.Driver) {
	if len(os.Args) > 1 && os.Args[1] == DescribeArg {
		var desc engine.Description
		if d, ok := drv.(engine.Describer); ok {
			desc = d.Describe()
		}
		_ = json.NewEncoder(os.Stdout).Encode(desc)
		return
	}
	goplugin.Serve(&goplugin.ServeConfig{
		HandshakeConfig: Handshake,
		Plugins:         pluginSet(drv),
		GRPCServer:      goplugin.DefaultGRPCServer,
	})
}
