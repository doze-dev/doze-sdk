// Package engine is the doze engine contract: the Driver interface plus the
// optional capability interfaces (Spawner, ConfigDecoder, Converger, Templater,
// ProxyFilter, …) an engine implements, and the value types they exchange with the
// host (Instance, Toolchain, SpawnPlan, Ready, Endpoint, Pin, …).
//
// An engine implements Driver (+ whatever capabilities it needs) and is served to
// doze over the plugin protocol with plugin.Serve. doze discovers capabilities by
// type assertion, so a driver only implements what it supports. See the package
// README and example/ for a complete worked engine.
package engine
