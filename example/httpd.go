// Command httpd is a complete, minimal example doze engine — copy it as the
// starting point for your own. It serves a directory of static files; doze
// declares it as:
//
//	httpd "site" { root = "./public" }
//
// and routes client connections on the instance's port to the server. The engine
// has no external binary: it self-execs (os.Executable + a hidden "__serve" arg)
// to run a Go http.FileServer on a unix socket, the same pattern doze's local-AWS
// engines use. Build it and point doze at it for local development:
//
//	go build -o /tmp/httpd-plugin ./example
//	DOZE_HTTPD_PLUGIN=/tmp/httpd-plugin doze up
//
// To distribute it, publish the binary in a doze-modules-style mirror and set
// DOZE_MODULES_MIRROR (or a `modules { mirror = "…" }` block).
package main

import (
	"context"
	"encoding/gob"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/gohcl"

	"github.com/doze-dev/doze-sdk/engine"
	dozeplugin "github.com/doze-dev/doze-sdk/plugin"
)

// Config is the decoded body of an `httpd "name" { … }` block. doze hands the
// driver the block's HCL; gohcl tags map fields to attributes.
type Config struct {
	Root string `hcl:"root,optional"` // directory to serve (default ".")
}

// Driver implements engine.Driver plus the few capabilities this engine needs.
// A Driver is stateless — one value handles every instance of this type.
type Driver struct{}

// Compile-time proof the driver satisfies the contract + the capabilities doze
// discovers by type assertion.
var (
	_ engine.Driver        = Driver{}
	_ engine.Spawner       = Driver{} // how to run (a declarative SpawnPlan)
	_ engine.ConfigDecoder = Driver{} // how to decode its HCL block
	_ engine.Versionless   = Driver{} // no doze-managed binary version to pick
)

// Type is the config block keyword and the engine's identity. A `httpd` block in
// doze.hcl resolves to this driver.
func (Driver) Type() string { return "httpd" }

// Versionless marks the engine as having no doze-managed toolchain version (we
// ship our own server via self-exec), so instances need no `version =`.
func (Driver) Versionless() {}

// Resolve would fetch the engine's backing binary for the host platform (using
// the supplied Fetcher + recording the pin in the Locker). This engine bundles its
// server, so there's nothing to fetch — return a synthetic toolchain.
func (Driver) Resolve(context.Context, engine.VersionSpec, engine.Platform, engine.Locker, engine.Fetcher) (engine.Toolchain, error) {
	return engine.Toolchain{Engine: "httpd", Full: "builtin"}, nil
}

// Provision prepares the instance's data dir before first boot (run initdb, lay
// down a template, …). Static file serving needs no store; just ensure the dir.
func (Driver) Provision(_ context.Context, inst engine.Instance, _ engine.Toolchain) error {
	return os.MkdirAll(inst.DataDir, 0o755)
}

// Provisioned reports whether the data dir is already initialized (so doze can
// skip Provision on later boots).
func (Driver) Provisioned(dataDir string) bool {
	fi, err := os.Stat(dataDir)
	return err == nil && fi.IsDir()
}

// DecodeConfig (engine.ConfigDecoder) decodes the engine-specific HCL body into a
// Config. doze strips the common fields (version, listen) first and hands over the
// rest, already resolved against variables/locals via ctx. The declared engine
// version also arrives here — a versioned engine would gate version-specific
// arguments with engine.RequireVersion; httpd is versionless, so it ignores it.
func (Driver) DecodeConfig(body hcl.Body, ctx *hcl.EvalContext, _ string, _ engine.VersionSpec) (engine.EngineConfig, error) {
	var c Config
	if d := gohcl.DecodeBody(body, ctx, &c); d.HasErrors() {
		return nil, fmt.Errorf("%s", d.Error())
	}
	if c.Root == "" {
		c.Root = "."
	}
	return &c, nil
}

// Plan (engine.Spawner) returns a declarative plan core executes + supervises: one
// spec that self-execs this binary as the file server, gated on its socket
// accepting connections. Returning a plan (rather than spawning yourself) keeps
// doze's hardened restart/reap/log handling in charge.
func (Driver) Plan(_ context.Context, inst engine.Instance, _ engine.Toolchain) (engine.SpawnPlan, error) {
	// The proxy dials a unix socket in inst.SocketDir; make sure the dir exists
	// before the server tries to bind it.
	if err := os.MkdirAll(inst.SocketDir, 0o700); err != nil {
		return engine.SpawnPlan{}, err
	}
	socket := Driver{}.BackendSocket(inst.SocketDir, inst.Port)
	root := "."
	if c, ok := inst.Spec.(*Config); ok && c != nil {
		root = c.Root
	}
	self, err := os.Executable()
	if err != nil {
		return engine.SpawnPlan{}, err
	}
	return engine.SpawnPlan{Specs: []engine.SpawnSpec{{
		Name:  inst.Name,
		Bin:   self,
		Args:  []string{"__serve", "--socket", socket, "--root", root},
		Ready: &engine.Ready{Kind: "socket", Target: socket},
	}}}, nil
}

// BackendSocket is the address doze's proxy dials to reach the running server.
func (Driver) BackendSocket(socketDir string, _ int) string {
	return filepath.Join(socketDir, "httpd.sock")
}

// ConnString is the connection URL doze surfaces (via `doze output` and the
// instance's `httpd.<name>.url` attribute), pointed at the doze-owned endpoint.
func (Driver) ConnString(_ engine.Instance, ep engine.Endpoint) (envVar, url string) {
	host := ep.TCPAddr
	if host == "" {
		host = "127.0.0.1"
	}
	return "HTTPD_URL", "http://" + host
}

func main() {
	// Dual-purpose binary: the hidden "__serve" mode runs the actual server (what
	// Plan self-execs); otherwise speak the engine plugin protocol to doze.
	if len(os.Args) > 1 && os.Args[1] == "__serve" {
		if err := serve(os.Args[2:]); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}
	gob.Register(&Config{}) // the config crosses the wire as gob
	dozeplugin.Serve(Driver{})
}

// serve runs the file server on the unix socket until killed (doze supervises it).
func serve(args []string) error {
	fs := flag.NewFlagSet("__serve", flag.ContinueOnError)
	socket := fs.String("socket", "", "unix socket to listen on")
	root := fs.String("root", ".", "directory to serve")
	if err := fs.Parse(args); err != nil {
		return err
	}
	_ = os.Remove(*socket)
	ln, err := net.Listen("unix", *socket)
	if err != nil {
		return err
	}
	return http.Serve(ln, http.FileServer(http.Dir(*root)))
}
