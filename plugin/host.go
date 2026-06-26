package plugin

import (
	"fmt"
	"os/exec"

	hclog "github.com/hashicorp/go-hclog"
	goplugin "github.com/hashicorp/go-plugin"

	"github.com/doze-dev/doze-sdk/engine"
)

// Host is a launched engine plugin: Driver adapts it to the in-tree engine.Driver
// (+ capabilities), and Close terminates the plugin process.
type Host struct {
	client *goplugin.Client
	driver engine.Driver
}

// Launch starts the plugin binary at path (with env, or the parent's when nil),
// handshakes over gRPC, and returns a Host whose Driver behaves like any in-tree
// engine. The caller must Close it to reap the plugin process.
func Launch(path string, env []string) (*Host, error) {
	cmd := exec.Command(path)
	if env != nil {
		cmd.Env = env
	}
	client := goplugin.NewClient(&goplugin.ClientConfig{
		HandshakeConfig:  Handshake,
		Plugins:          pluginSet(nil),
		Cmd:              cmd,
		AllowedProtocols: []goplugin.Protocol{goplugin.ProtocolGRPC},
		Logger:           hclog.NewNullLogger(),
	})
	rpc, err := client.Client()
	if err != nil {
		client.Kill()
		return nil, fmt.Errorf("connecting to plugin %s: %w", path, err)
	}
	raw, err := rpc.Dispense(pluginKey)
	if err != nil {
		client.Kill()
		return nil, fmt.Errorf("dispensing plugin %s: %w", path, err)
	}
	drv, ok := raw.(engine.Driver)
	if !ok {
		client.Kill()
		return nil, fmt.Errorf("plugin %s did not yield an engine.Driver", path)
	}
	return &Host{client: client, driver: drv}, nil
}

// Driver returns the adapted driver.
func (h *Host) Driver() engine.Driver { return h.driver }

// Alive reports whether the plugin process is still running.
func (h *Host) Alive() bool { return !h.client.Exited() }

// Close terminates the plugin process.
func (h *Host) Close() { h.client.Kill() }
