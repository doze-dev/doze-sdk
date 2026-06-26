// Command kvtest is a config-bearing engine plugin used to prove config-decode
// over the wire: it decodes its own HCL block (with a var reference) via gohcl —
// exactly as an in-tree engine would — and reflects the decoded values into its
// SpawnPlan so a test can confirm the config round-tripped.
package main

import (
	"context"
	"encoding/gob"
	"fmt"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/gohcl"

	"github.com/doze-dev/doze-sdk/engine"
	dozeplugin "github.com/doze-dev/doze-sdk/plugin"
)

// Config is the kv engine's typed config — gob-registered so it round-trips as the
// opaque instance Spec.
type Config struct {
	Password  string `hcl:"password,optional"`
	MaxMemory string `hcl:"maxmemory,optional"`
}

func init() { gob.Register(&Config{}) }

type driver struct{}

func (driver) Type() string { return "kv" }

// DecodeConfig is the plugin's own gohcl decode — unchanged from in-tree.
func (driver) DecodeConfig(body hcl.Body, ctx *hcl.EvalContext, _ string) (engine.EngineConfig, error) {
	var c Config
	if d := gohcl.DecodeBody(body, ctx, &c); d.HasErrors() {
		return nil, fmt.Errorf("kv config: %s", d)
	}
	return &c, nil
}

func (driver) Resolve(context.Context, engine.VersionSpec, engine.Platform, engine.Locker, engine.Fetcher) (engine.Toolchain, error) {
	return engine.Toolchain{Engine: "kv", Full: "builtin"}, nil
}
func (driver) Provision(context.Context, engine.Instance, engine.Toolchain) error { return nil }
func (driver) Provisioned(string) bool                                            { return true }
func (driver) BackendSocket(socketDir string, _ int) string                       { return socketDir + "/kv.sock" }
func (driver) ConnString(engine.Instance, engine.Endpoint) (string, string) {
	return "KV_URL", "kv://local"
}

func (driver) Plan(_ context.Context, inst engine.Instance, _ engine.Toolchain) (engine.SpawnPlan, error) {
	cfg, ok := inst.Spec.(*Config)
	if !ok {
		return engine.SpawnPlan{}, fmt.Errorf("kv %q: spec did not round-trip (%T)", inst.Name, inst.Spec)
	}
	return engine.SpawnPlan{Specs: []engine.SpawnSpec{{
		Name: inst.Name, Bin: "sh",
		Args: []string{"-c", fmt.Sprintf("password=%s maxmemory=%s", cfg.Password, cfg.MaxMemory)},
	}}}, nil
}

func main() { dozeplugin.Serve(driver{}) }
