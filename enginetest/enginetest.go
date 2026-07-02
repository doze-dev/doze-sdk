// Package enginetest is an acceptance-test harness for doze engine modules. It
// boots a REAL backend straight from a Driver — resolve, provision, run the
// SpawnPlan, wait for readiness — so a module can assert that each config it
// offers actually converges against the real engine, without needing doze core,
// its daemon, or its proxy. It is the doze analog of Terraform's helper/resource:
// module authors (first- or third-party) write acceptance tests against it.
//
// Boot requires a local backend binary via DOZE_<ENGINE>_BINDIR (acceptance tests
// need a real binary; this keeps them off the network) and SKIPS when it is unset,
// so `go test` stays green on a machine without the toolchain. Gate the tests
// themselves behind an `acceptance` build tag so they never run by accident.
package enginetest

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/hashicorp/hcl/v2/hclparse"

	"github.com/doze-dev/doze-sdk/binaries"
	"github.com/doze-dev/doze-sdk/engine"
)

// Options configures a Boot.
type Options struct {
	Version string   // engine version (e.g. "16"); "" for a versionless engine
	HCL     string   // the engine block BODY to decode (e.g. `role "app" {}`)
	Name    string   // instance name (default "acc")
	Port    int      // nominal port for socket naming (default: a free port)
	Env     []string // extra environment for spawned processes
	Timeout time.Duration
}

// Backend is a live engine backend for acceptance assertions.
type Backend struct {
	t       *testing.T
	drv     engine.Driver
	tc      engine.Toolchain
	inst    engine.Instance
	baseDir string
	mu      sync.Mutex
	procs   []*exec.Cmd
	logs    []*bytes.Buffer
}

// Boot resolves the toolchain (via DOZE_<ENGINE>_BINDIR), provisions a fresh data
// dir, runs the driver's SpawnPlan to readiness, and converges the initial config.
// Cleanup (stop processes) is registered on t. The test is skipped if the engine's
// BINDIR env is unset.
func Boot(t *testing.T, drv engine.Driver, opts Options) *Backend {
	t.Helper()
	engineType := drv.Type()
	binEnv := "DOZE_" + strings.ToUpper(engineType) + "_BINDIR"
	if os.Getenv(binEnv) == "" {
		t.Skipf("%s not set — acceptance tests need a local %s backend binary", binEnv, engineType)
	}
	if opts.Name == "" {
		opts.Name = "acc"
	}
	if opts.Port == 0 {
		opts.Port = freePort(t)
	}
	if opts.Timeout == 0 {
		opts.Timeout = 90 * time.Second
		if sb, ok := drv.(engine.SlowBooter); ok && sb.BootBudget() > opts.Timeout {
			opts.Timeout = sb.BootBudget()
		}
	}

	plat, err := binaries.HostPlatform()
	if err != nil {
		t.Fatalf("host platform: %v", err)
	}
	ctx := context.Background()
	tc, err := drv.Resolve(ctx, engine.VersionSpec(opts.Version), plat, noopLocker{}, errFetcher{})
	if err != nil {
		t.Fatalf("resolve %s: %v", engineType, err)
	}

	base := t.TempDir()
	b := &Backend{
		t: t, drv: drv, tc: tc, baseDir: base,
		inst: engine.Instance{
			Name:      opts.Name,
			Type:      engineType,
			Version:   engine.VersionSpec(opts.Version),
			DataDir:   filepath.Join(base, "data"),
			SocketDir: filepath.Join(base, "sock"),
			Port:      opts.Port,
		},
	}
	b.inst.Endpoint = engine.Endpoint{Backend: drv.BackendSocket(b.inst.SocketDir, b.inst.Port)}
	if err := os.MkdirAll(b.inst.SocketDir, 0o700); err != nil {
		t.Fatalf("socket dir: %v", err)
	}
	b.inst.Spec = b.decode(opts.HCL)

	if err := drv.Provision(ctx, b.inst, tc); err != nil {
		t.Fatalf("provision %s: %v", engineType, err)
	}
	t.Cleanup(b.stop)
	b.spawn(opts.Env, opts.Timeout)

	// Apply the initial config so the instance's structure (e.g. its database)
	// exists for subsequent per-case asserts. Bare engines (no Converger) and
	// flag-configured ones (their config was already baked into the SpawnPlan)
	// skip this — the Spec is set pre-spawn regardless.
	if _, ok := drv.(engine.Converger); ok {
		b.Converge(opts.HCL)
	}
	return b
}

// Instance exposes the (possibly re-converged) instance for building client
// connection strings in assertions.
func (b *Backend) Instance() engine.Instance { return b.inst }

// SocketDir is the backend's socket directory; Port its nominal port.
func (b *Backend) SocketDir() string { return b.inst.SocketDir }
func (b *Backend) Port() int         { return b.inst.Port }

// Converge decodes hcl as the instance's new config and runs the driver's
// Converger against the live backend — the operation acceptance matrices drive.
func (b *Backend) Converge(hcl string) {
	b.t.Helper()
	cv, ok := b.drv.(engine.Converger)
	if !ok {
		b.t.Fatalf("%s does not implement Converger", b.drv.Type())
	}
	b.inst.Spec = b.decode(hcl)
	if err := cv.Converge(context.Background(), b.inst, b.tc, b.inst.Endpoint); err != nil {
		b.t.Fatalf("converge failed for config:\n%s\nerror: %v", hcl, err)
	}
}

// Objects returns the instance's declared inventory (for prune assertions).
func (b *Backend) Objects() []engine.Object {
	if inv, ok := b.drv.(engine.Inventory); ok {
		return inv.Objects(b.inst)
	}
	return nil
}

// Prune drops the given previously-applied objects via the driver's Pruner.
func (b *Backend) Prune(removed []engine.Object) {
	b.t.Helper()
	pr, ok := b.drv.(engine.Pruner)
	if !ok {
		b.t.Fatalf("%s does not implement Pruner", b.drv.Type())
	}
	if err := pr.Prune(context.Background(), b.inst, b.tc, b.inst.Endpoint, removed); err != nil {
		b.t.Fatalf("prune failed: %v", err)
	}
}

// decode parses the engine block body and runs the driver's ConfigDecoder.
func (b *Backend) decode(hcl string) engine.EngineConfig {
	b.t.Helper()
	cd, ok := b.drv.(engine.ConfigDecoder)
	if !ok {
		b.t.Fatalf("%s does not implement ConfigDecoder", b.drv.Type())
	}
	f, diags := hclparse.NewParser().ParseHCL([]byte(hcl), "acc.hcl")
	if diags.HasErrors() {
		b.t.Fatalf("parsing acceptance HCL: %s", diags.Error())
	}
	spec, err := cd.DecodeConfig(f.Body, nil, b.baseDir, b.inst.Version)
	if err != nil {
		b.t.Fatalf("decoding config: %v", err)
	}
	return spec
}

// spawn runs the driver's SpawnPlan: it starts specs in dependency order, gates
// each on its readiness probe, and runs any post-ready hooks. A minimal stand-in
// for core's supervisor, sufficient for a single-shot acceptance boot.
func (b *Backend) spawn(extraEnv []string, timeout time.Duration) {
	b.t.Helper()
	sp, ok := b.drv.(engine.Spawner)
	if !ok {
		b.t.Fatalf("%s does not implement Spawner", b.drv.Type())
	}
	plan, err := sp.Plan(context.Background(), b.inst, b.tc)
	if err != nil {
		b.t.Fatalf("plan: %v", err)
	}
	ready := map[string]bool{}
	remaining := append([]engine.SpawnSpec(nil), plan.Specs...)
	deadline := time.Now().Add(timeout)
	for len(remaining) > 0 {
		progressed := false
		for i := 0; i < len(remaining); i++ {
			spec := remaining[i]
			if !depsReady(spec, ready) {
				continue
			}
			b.startSpec(spec, extraEnv)
			if err := b.waitReady(spec, deadline); err != nil {
				b.t.Fatalf("spec %q never became ready: %v\n%s", spec.Name, err, b.tailLogs())
			}
			b.runHooks(spec, extraEnv)
			ready[spec.Name] = true
			remaining = append(remaining[:i], remaining[i+1:]...)
			progressed = true
			break
		}
		if !progressed {
			b.t.Fatalf("spawn plan deadlock: unmet dependencies among %v", specNames(remaining))
		}
	}
}

func (b *Backend) startSpec(spec engine.SpawnSpec, extraEnv []string) {
	cmd := exec.Command(spec.Bin, spec.Args...)
	if spec.Dir != "" {
		cmd.Dir = spec.Dir
	}
	env := spec.Env
	if env == nil {
		env = os.Environ()
	}
	cmd.Env = append(append([]string(nil), env...), extraEnv...)
	if spec.Tree {
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	}
	var log bytes.Buffer
	cmd.Stdout = &log
	cmd.Stderr = &log
	if err := cmd.Start(); err != nil {
		b.t.Fatalf("starting %q (%s): %v", spec.Name, spec.Bin, err)
	}
	b.mu.Lock()
	b.procs = append(b.procs, cmd)
	b.logs = append(b.logs, &log)
	b.mu.Unlock()
}

// waitReady polls a spec's readiness probe until it passes or the deadline hits.
func (b *Backend) waitReady(spec engine.SpawnSpec, deadline time.Time) error {
	if spec.Ready == nil {
		time.Sleep(200 * time.Millisecond) // liveness-only: give it a moment
		return nil
	}
	r := spec.Ready
	for time.Now().Before(deadline) {
		if b.probe(r) {
			return nil
		}
		time.Sleep(250 * time.Millisecond)
	}
	return fmt.Errorf("readiness probe (%s %q) timed out", r.Kind, r.Target)
}

func (b *Backend) probe(r *engine.Ready) bool {
	switch r.Kind {
	case "exec":
		return exec.Command("sh", "-c", r.Target).Run() == nil
	case "tcp":
		c, err := net.DialTimeout("tcp", r.Target, 2*time.Second)
		if err == nil {
			_ = c.Close()
		}
		return err == nil
	case "socket":
		c, err := net.DialTimeout("unix", r.Target, 2*time.Second)
		if err == nil {
			_ = c.Close()
		}
		return err == nil
	case "http":
		resp, err := http.Get(r.Target)
		if err != nil {
			return false
		}
		_ = resp.Body.Close()
		return true
	case "log_line":
		return b.logsContain(r.Target)
	default:
		return false
	}
}

func (b *Backend) runHooks(spec engine.SpawnSpec, extraEnv []string) {
	for _, h := range spec.Hooks {
		cmd := exec.Command("sh", "-c", h)
		if spec.Dir != "" {
			cmd.Dir = spec.Dir
		}
		env := spec.Env
		if env == nil {
			env = os.Environ()
		}
		cmd.Env = append(append([]string(nil), env...), extraEnv...)
		if out, err := cmd.CombinedOutput(); err != nil {
			b.t.Fatalf("hook %q failed: %v\n%s", h, err, out)
		}
	}
}

// stop terminates every spawned process (its whole group when Tree was set).
func (b *Backend) stop() {
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, cmd := range b.procs {
		if cmd.Process == nil {
			continue
		}
		if cmd.SysProcAttr != nil && cmd.SysProcAttr.Setpgid {
			_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
		}
		_ = cmd.Process.Signal(syscall.SIGTERM)
	}
	// Give them a moment, then hard-kill stragglers.
	done := make(chan struct{})
	go func() {
		for _, cmd := range b.procs {
			_ = cmd.Wait()
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		for _, cmd := range b.procs {
			if cmd.Process != nil {
				_ = cmd.Process.Kill()
			}
		}
	}
}

func (b *Backend) logsContain(sub string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, l := range b.logs {
		if strings.Contains(l.String(), sub) {
			return true
		}
	}
	return false
}

func (b *Backend) tailLogs() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	var sb strings.Builder
	for i, l := range b.logs {
		fmt.Fprintf(&sb, "--- spec %d log ---\n", i)
		sc := bufio.NewScanner(bytes.NewReader(l.Bytes()))
		for sc.Scan() {
			sb.WriteString("  " + sc.Text() + "\n")
		}
	}
	return sb.String()
}

func depsReady(spec engine.SpawnSpec, ready map[string]bool) bool {
	for _, d := range spec.After {
		if !ready[d] {
			return false
		}
	}
	return true
}

func specNames(specs []engine.SpawnSpec) []string {
	out := make([]string, len(specs))
	for i, s := range specs {
		out[i] = s.Name
	}
	return out
}

func freePort(t *testing.T) int {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("free port: %v", err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

// noopLocker/errFetcher satisfy Resolve's signature. Resolve short-circuits on the
// BINDIR override (required by Boot), so neither is actually exercised — but the
// fetcher fails loudly if an engine ever ignores the override.
type noopLocker struct{}

func (noopLocker) Get(string, engine.VersionSpec, engine.Platform) (engine.Pin, bool) {
	return engine.Pin{}, false
}
func (noopLocker) Record(string, engine.VersionSpec, engine.Platform, engine.Pin) {}

type errFetcher struct{}

func (errFetcher) ResolveMajor(string, string) (string, error) {
	return "", fmt.Errorf("enginetest: no network fetch — set DOZE_<ENGINE>_BINDIR")
}
func (errFetcher) Ensure(context.Context, string, string, engine.Platform, string) (string, string, error) {
	return "", "", fmt.Errorf("enginetest: no network fetch — set DOZE_<ENGINE>_BINDIR")
}
