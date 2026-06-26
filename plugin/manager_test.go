package plugin

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func buildEcho(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "echo")
	build := exec.Command("go", "build", "-o", bin, "./echo")
	build.Stderr = os.Stderr
	if err := build.Run(); err != nil {
		t.Fatalf("building echo plugin: %v", err)
	}
	return bin
}

func TestManager(t *testing.T) {
	echo := buildEcho(t)
	m := NewManager(func(engineType string) (string, []string, bool) {
		if engineType == "echo" {
			return echo, nil, true
		}
		return "", nil, false // not a plugin
	})
	defer m.Close()

	d1, found, err := m.Driver("echo")
	if err != nil || !found {
		t.Fatalf("Driver(echo) found=%v err=%v", found, err)
	}
	if d1.Type() != "echo" {
		t.Fatalf("Type = %q", d1.Type())
	}

	// A second request reuses the warm plugin (same adapter instance).
	d2, _, _ := m.Driver("echo")
	if d1 != d2 {
		t.Fatal("expected the warm plugin to be reused, got a fresh launch")
	}

	// A compiled-in engine is not found (caller falls back to the in-tree registry).
	if _, found, _ := m.Driver("postgres"); found {
		t.Fatal("postgres should not resolve to a plugin")
	}
}
