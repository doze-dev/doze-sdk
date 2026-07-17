package plugin

import (
	"encoding/gob"

	"github.com/doze-dev/doze-sdk/engine"
)

// Main is the whole main() of a typical engine plugin: it registers the
// module's concrete config type with gob (engine configs cross the plugin wire
// gob-encoded — see conv.go) and then serves drv over the plugin protocol via
// Serve, including the `__describe` mode. A module's plugin/main.go collapses
// to a one-liner:
//
//	func main() { dozeplugin.Main(postgres.Driver{}, &postgres.Config{}) }
//
// config is the value the driver's DecodeConfig returns, normally a pointer to
// the zero config (e.g. &postgres.Config{}); pass nil for a driver whose config
// never crosses the wire. Plugins that need extra argv modes (e.g. a `__serve`
// self-exec dispatch) handle those first and fall through to Main.
func Main(drv engine.Driver, config any) {
	if config != nil {
		gob.Register(config)
	}
	Serve(drv)
}
