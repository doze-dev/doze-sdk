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

// Admin and Console, so the round trip also proves a console: one table of
// "echoes", a counter row and a stream that says what it was asked to tail.

func (driver) Actions() []engine.Action {
	return []engine.Action{{ID: "ping", Label: "Ping", Kind: "echo"}}
}

func (driver) Resources(context.Context, engine.Instance, engine.Endpoint) ([]engine.Resource, error) {
	return []engine.Resource{
		{Kind: "echo", Name: "hello", Status: "2 sent", Info: map[string]string{"sent": "2"}},
		{Kind: "echo", Name: "world", Status: "0 sent", Info: map[string]string{"sent": "0"}},
	}, nil
}

func (driver) Run(_ context.Context, _ engine.Instance, _ engine.Endpoint, action, resource, _ string) (string, error) {
	return action + " " + resource, nil
}

func (driver) Describe() engine.ConsoleSpec {
	return engine.ConsoleSpec{
		Front:  "console",
		WebURL: "http://{address}/_console",
		Panels: []engine.Panel{
			{ID: "echoes", Title: "Echoes", Kind: "table", Source: "echo", Columns: []engine.Column{
				{Key: "name", Title: "Name"},
				{Key: "sent", Title: "Sent", Align: "right", Role: "count"},
			}},
			{ID: "wire", Title: "Wire", Kind: "stream", Select: "echoes", Columns: []engine.Column{{Key: "text", Title: "Text"}}},
		},
	}
}

func (driver) Tail(ctx context.Context, _ engine.Instance, _ engine.Endpoint, panel, selector string, out chan<- engine.Row) error {
	for i := 1; i <= 3; i++ {
		select {
		case out <- engine.Row{Cells: map[string]string{"text": panel + "/" + selector + " #" + string(rune('0'+i))}, Tone: "ok"}:
		case <-ctx.Done():
			return nil
		}
	}
	<-ctx.Done() // a stream stays open until the host hangs up
	return nil
}
