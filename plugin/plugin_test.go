package plugin

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/nerdmenot/doze-sdk/engine"
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
