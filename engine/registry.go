package engine

import "sort"

// drivers holds every registered in-tree engine driver, keyed by Type().
var drivers = map[string]Driver{}

// pluginResolver, when installed, supplies out-of-process engine modules. It is
// consulted before the in-tree set so a plugin (e.g. one pinned by config or an
// override) takes precedence as engines migrate out of the tree.
var pluginResolver func(engineType string) (Driver, bool)

// Register makes a driver available under its Type(). Engine packages call this
// from an init function; cmd/doze blank-imports them to wire the set.
func Register(d Driver) {
	drivers[d.Type()] = d
}

// SetPluginResolver installs the resolver for plugin-backed engines (the daemon/CLI
// wire it to the plugin manager). A nil resolver disables plugin resolution.
func SetPluginResolver(r func(engineType string) (Driver, bool)) { pluginResolver = r }

// Lookup returns the driver for an engine type: a plugin if one is available for it,
// otherwise the in-tree registration.
func Lookup(engineType string) (Driver, bool) {
	if pluginResolver != nil {
		if d, ok := pluginResolver(engineType); ok {
			return d, true
		}
	}
	d, ok := drivers[engineType]
	return d, ok
}

// Types returns the registered in-tree engine types, sorted (used to build the
// config block schema; plugin engine types are added separately).
func Types() []string {
	out := make([]string, 0, len(drivers))
	for t := range drivers {
		out = append(out, t)
	}
	sort.Strings(out)
	return out
}
