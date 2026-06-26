package plugin

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/nerdmenot/doze-sdk/engine"
)

// TestRemoteDecode proves config-decode over the wire end to end: a plugin parses
// its own HCL block (resolving a var reference) out-of-process, the decoded config
// returns as an opaque RawSpec, and that spec survives a later Plan call where the
// plugin reads it back — so both the cty serialization and the opaque-spec
// round-trip are exercised.
func TestRemoteDecode(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "kv")
	build := exec.Command("go", "build", "-o", bin, "./kvtest")
	build.Stderr = os.Stderr
	if err := build.Run(); err != nil {
		t.Fatalf("building kv plugin: %v", err)
	}
	h, err := Launch(bin, nil)
	if err != nil {
		t.Fatalf("launch: %v", err)
	}
	defer h.Close()
	drv := h.Driver()

	rd, ok := drv.(engine.RemoteDecoder)
	if !ok {
		t.Fatal("plugin driver is not a RemoteDecoder")
	}
	file := []byte(`kv "cache" {
  version   = 9
  password  = "secret"
  maxmemory = var.cap
}`)
	vars := map[string]cty.Value{
		"var": cty.ObjectVal(map[string]cty.Value{"cap": cty.StringVal("256mb")}),
	}
	spec, err := rd.DecodeRemote(file, "kv", "cache", vars, ".")
	if err != nil {
		t.Fatalf("DecodeRemote: %v", err)
	}
	rs, ok := spec.(*RawSpec)
	if !ok || len(rs.Bytes) == 0 {
		t.Fatalf("expected a non-empty RawSpec, got %T", spec)
	}

	// The opaque spec round-trips: Plan reads it back, and the decoded values
	// (including the resolved var) reflect into the SpawnPlan.
	sp := drv.(engine.Spawner)
	plan, err := sp.Plan(context.Background(), engine.Instance{Name: "cache", Spec: rs}, engine.Toolchain{})
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	args := strings.Join(plan.Specs[0].Args, " ")
	if !strings.Contains(args, "password=secret") {
		t.Fatalf("password did not round-trip: %q", args)
	}
	if !strings.Contains(args, "maxmemory=256mb") {
		t.Fatalf("var reference did not resolve/round-trip: %q", args)
	}
}
