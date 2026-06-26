// Command echo is a trivial engine plugin used to prove the host↔plugin gRPC
// round-trip end to end: it implements the minimal engine.Driver + Spawner +
// Lifecycle surface and serves it over go-plugin.
package main

import (
	"context"

	"github.com/doze-dev/doze-sdk/engine"
	dozeplugin "github.com/doze-dev/doze-sdk/plugin"
)

type driver struct{}

func (driver) Type() string { return "echo" }

func (driver) Resolve(context.Context, engine.VersionSpec, engine.Platform, engine.Locker, engine.Fetcher) (engine.Toolchain, error) {
	return engine.Toolchain{Engine: "echo", Full: "builtin"}, nil
}
func (driver) Provision(context.Context, engine.Instance, engine.Toolchain) error { return nil }
func (driver) Provisioned(string) bool                                            { return true }
func (driver) BackendSocket(socketDir string, _ int) string                       { return socketDir + "/echo.sock" }
func (driver) ConnString(engine.Instance, engine.Endpoint) (string, string) {
	return "ECHO_URL", "echo://local"
}

// Plan implements engine.Spawner.
func (driver) Plan(_ context.Context, inst engine.Instance, _ engine.Toolchain) (engine.SpawnPlan, error) {
	return engine.SpawnPlan{Specs: []engine.SpawnSpec{{
		Name: inst.Name, Bin: "sh", Args: []string{"-c", "echo hello && sleep 3600"},
	}}}, nil
}

// Supervised implements engine.Lifecycle.
func (driver) Supervised(engine.Instance) bool { return true }

func main() { dozeplugin.Serve(driver{}) }
