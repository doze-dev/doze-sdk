package plugin

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/doze-dev/doze-sdk/engine"
)

// TestEchoRoundTrip builds the echo plugin, launches it over go-plugin/gRPC, and
// drives it through the pluginDriver adapter — proving the host ↔ plugin protocol
// end to end (Type, ConnString, the SpawnPlan, and an advertised capability).
func TestEchoRoundTrip(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "echo")
	build := exec.Command("go", "build", "-o", bin, "./echo")
	build.Stderr = os.Stderr
	if err := build.Run(); err != nil {
		t.Fatalf("building echo plugin: %v", err)
	}

	h, err := Launch(bin, nil)
	if err != nil {
		t.Fatalf("launch: %v", err)
	}
	defer h.Close()
	drv := h.Driver()

	if got := drv.Type(); got != "echo" {
		t.Fatalf("Type() = %q, want echo", got)
	}
	if v, u := drv.ConnString(engine.Instance{Name: "e1"}, engine.Endpoint{}); v != "ECHO_URL" || u != "echo://local" {
		t.Fatalf("ConnString = %q,%q", v, u)
	}

	// SpawnPlan round-trips.
	sp, ok := drv.(engine.Spawner)
	if !ok {
		t.Fatal("adapter is not a Spawner")
	}
	plan, err := sp.Plan(context.Background(), engine.Instance{Name: "e1"}, engine.Toolchain{})
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if len(plan.Specs) != 1 || plan.Specs[0].Name != "e1" || plan.Specs[0].Bin != "sh" {
		t.Fatalf("plan = %+v", plan)
	}

	// An advertised capability dispatches; an unadvertised one safely no-ops.
	if lc, ok := drv.(engine.Lifecycle); !ok || !lc.Supervised(engine.Instance{Name: "e1"}) {
		t.Fatal("expected the echo plugin to advertise Supervised=true")
	}
	if c, ok := drv.(engine.Converger); ok {
		if err := c.Converge(context.Background(), engine.Instance{Name: "e1"}, engine.Toolchain{}, engine.Endpoint{}); err != nil {
			t.Fatalf("unadvertised Converge should no-op, got %v", err)
		}
	}
}

// TestConsoleRoundTrip proves the console capability end to end: the spec
// crosses the wire intact, a stream delivers its rows and stops when the host
// does, and the admin half (resources, actions) is there beside it.
func TestConsoleRoundTrip(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "echo")
	build := exec.Command("go", "build", "-o", bin, "./echo")
	build.Stderr = os.Stderr
	if err := build.Run(); err != nil {
		t.Fatalf("building echo plugin: %v", err)
	}
	h, err := Launch(bin, nil)
	if err != nil {
		t.Fatalf("launch: %v", err)
	}
	defer h.Close()

	c, ok := h.Driver().(engine.Console)
	if !ok {
		t.Fatalf("a module that advertises a console should be an engine.Console, got %T", h.Driver())
	}
	spec := c.DescribeConsole()
	if spec.Front != "console" || spec.WebURL != "http://{address}/_console" || len(spec.Panels) != 2 {
		t.Fatalf("spec = %+v", spec)
	}
	if p := spec.Panels[0]; p.Kind != "table" || p.Source != "echo" || len(p.Columns) != 2 || p.Columns[1].Role != "count" || p.Columns[1].Align != "right" {
		t.Fatalf("table panel = %+v", p)
	}
	if p := spec.Panels[1]; p.Kind != "stream" || p.Select != "echoes" {
		t.Fatalf("stream panel = %+v", p)
	}

	ctx, cancel := context.WithCancel(context.Background())
	out := make(chan engine.Row, 8)
	done := make(chan error, 1)
	go func() {
		done <- c.Tail(ctx, engine.Instance{Name: "e1", Type: "echo"}, engine.Endpoint{}, "wire", "hello", out)
	}()
	for i := 1; i <= 3; i++ {
		select {
		case row := <-out:
			if want := "wire/hello #" + string(rune('0'+i)); row.Cells["text"] != want || row.Tone != "ok" {
				t.Fatalf("row %d = %+v, want %q", i, row, want)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("row %d never arrived", i)
		}
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("a tail stopped by the host returned %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the tail did not stop when the host hung up")
	}

	a, ok := h.Driver().(engine.Admin)
	if !ok {
		t.Fatalf("a console module is an admin module too, got %T", h.Driver())
	}
	rs, err := a.Resources(context.Background(), engine.Instance{Name: "e1", Type: "echo"}, engine.Endpoint{})
	if err != nil || len(rs) != 2 || rs[0].Info["sent"] != "2" {
		t.Fatalf("resources = %+v, %v", rs, err)
	}
}

// A module built before consoles existed still loads and simply has none: it
// never advertises the capability, so the host never asks, and its admin driver
// answers an empty spec without a call. consoles.md measure "a version 1
// module still loads".
func TestAModuleWithoutAConsoleHasAnEmptyOne(t *testing.T) {
	d := &pluginDriver{caps: map[string]bool{capAdmin: true}} // no client: any RPC would panic
	var drv engine.Driver = adminDriver{d}
	c, ok := drv.(engine.Console)
	if !ok {
		t.Fatalf("an admin driver is an engine.Console, with an empty spec when the module has none")
	}
	if spec := c.DescribeConsole(); spec.Front != "" || len(spec.Panels) != 0 || spec.WebURL != "" {
		t.Fatalf("spec = %+v", spec)
	}
	if err := c.Tail(context.Background(), engine.Instance{}, engine.Endpoint{}, "p", "", make(chan engine.Row)); err == nil {
		t.Fatal("tailing a module with no console is an error")
	}

	// And the echo module's pre-console sibling: a plain driver is not one.
	bin := filepath.Join(t.TempDir(), "kv")
	build := exec.Command("go", "build", "-o", bin, "./kvtest")
	build.Stderr = os.Stderr
	if err := build.Run(); err != nil {
		t.Fatalf("building kvtest: %v", err)
	}
	h, err := Launch(bin, nil)
	if err != nil {
		t.Fatalf("a module without a console must still load: %v", err)
	}
	defer h.Close()
	if _, ok := h.Driver().(engine.Console); ok {
		t.Fatalf("a module that is not an admin module should not look like a console, got %T", h.Driver())
	}
}
