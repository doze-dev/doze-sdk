package plugin

import (
	"fmt"
	"os/exec"
	"strings"

	hclog "github.com/hashicorp/go-hclog"
	goplugin "github.com/hashicorp/go-plugin"

	"github.com/doze-dev/doze-sdk/engine"
)

// ProtocolMismatchError reports that a plugin binary speaks a different doze
// plugin protocol than this host. It is the launch-time backstop behind the
// registry index's protocol gate — it also covers DOZE_<TYPE>_PLUGIN local
// overrides, which never pass through the registry.
type ProtocolMismatchError struct {
	Path string // the plugin binary
	Host int    // the protocol this doze speaks
	Err  error  // go-plugin's underlying handshake error
}

func (e *ProtocolMismatchError) Error() string {
	return fmt.Sprintf("plugin %s speaks an incompatible doze plugin protocol (this doze speaks %d) — run 'doze modules upgrade', or rebuild the plugin against a matching doze-sdk", e.Path, e.Host)
}

func (e *ProtocolMismatchError) Unwrap() error { return e.Err }

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
		// go-plugin reports a protocol-version mismatch with this text; surface
		// it as a typed, actionable error instead of the raw handshake failure.
		if strings.Contains(err.Error(), "Incompatible API version") {
			return nil, &ProtocolMismatchError{Path: path, Host: ProtocolVersion, Err: err}
		}
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
