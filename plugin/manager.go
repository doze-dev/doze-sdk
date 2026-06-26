package plugin

import (
	"fmt"
	"os"
	"strings"
	"sync"

	"github.com/nerdmenot/doze-sdk/engine"
)

// EnvResolver resolves an engine type to a plugin binary via DOZE_<TYPE>_PLUGIN —
// the v1 local override for developing a module before the doze-modules monorepo
// (Phase 5) supplies it. Engines without the env var are treated as compiled-in.
func EnvResolver() Resolver {
	return func(engineType string) (string, []string, bool) {
		p := os.Getenv("DOZE_" + strings.ToUpper(engineType) + "_PLUGIN")
		if p == "" {
			return "", nil, false
		}
		if fi, err := os.Stat(p); err != nil || fi.IsDir() {
			return "", nil, false
		}
		return p, nil, true // inherit the daemon's env (DOZE_HOME, PATH, …)
	}
}

// Resolver locates the plugin binary for an engine type, returning ok=false when
// that engine is compiled in (not a plugin). A path comes from a local override
// (DOZE_<TYPE>_PLUGIN) or the doze-modules cache (fetched + pinned).
type Resolver func(engineType string) (path string, env []string, ok bool)

// Chain returns a Resolver that tries each in order and returns the first hit —
// e.g. the local DOZE_<TYPE>_PLUGIN override before a fetched-from-doze-modules one.
func Chain(resolvers ...Resolver) Resolver {
	return func(engineType string) (string, []string, bool) {
		for _, r := range resolvers {
			if r == nil {
				continue
			}
			if path, env, ok := r(engineType); ok {
				return path, env, true
			}
		}
		return "", nil, false
	}
}

// Manager owns the launched engine-plugin processes for a daemon: it resolves an
// engine type to a plugin binary, launches it on first use, keeps it warm (config
// eval and every boot reuse the one process), and reaps them all on Close. A
// plugin that has exited is relaunched on the next request.
type Manager struct {
	resolve Resolver
	mu      sync.Mutex
	hosts   map[string]*Host
}

// NewManager builds a Manager backed by resolve.
func NewManager(resolve Resolver) *Manager {
	return &Manager{resolve: resolve, hosts: map[string]*Host{}}
}

// Driver returns the warm plugin driver for engineType, launching it if needed.
// found is false when the engine is not a plugin (the caller falls back to the
// in-tree registry).
func (m *Manager) Driver(engineType string) (drv engine.Driver, found bool, err error) {
	path, env, ok := m.resolve(engineType)
	if !ok {
		return nil, false, nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if h := m.hosts[engineType]; h != nil {
		if h.Alive() {
			return h.Driver(), true, nil
		}
		h.Close() // exited/crashed — drop and relaunch
		delete(m.hosts, engineType)
	}
	h, err := Launch(path, env)
	if err != nil {
		return nil, true, fmt.Errorf("launching %s plugin: %w", engineType, err)
	}
	m.hosts[engineType] = h
	return h.Driver(), true, nil
}

// Lookup adapts Driver to engine.SetPluginResolver's signature: a launch failure
// is surfaced (to stderr) and reported as "not a plugin" so the caller falls back
// to any in-tree registration rather than hanging.
func (m *Manager) Lookup(engineType string) (engine.Driver, bool) {
	drv, found, err := m.Driver(engineType)
	if err != nil {
		fmt.Fprintf(os.Stderr, "doze: %v\n", err)
		return nil, false
	}
	return drv, found
}

// Close reaps every launched plugin.
func (m *Manager) Close() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for name, h := range m.hosts {
		h.Close()
		delete(m.hosts, name)
	}
}
