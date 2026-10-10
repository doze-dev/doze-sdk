package binaries

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/doze-dev/doze-sdk/engine"
)

// seedCache lays down a cache entry the way ensureDownload leaves one, with
// the given recorded digest ("" for an entry from before digests were kept).
func seedCache(t *testing.T, home, name, full string, plat engine.Platform, digest string) {
	t.Helper()
	contentDir := filepath.Join(home, name, full+"-"+plat.Triple)
	if err := os.MkdirAll(filepath.Join(contentDir, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(contentDir, "bin", "tool"), []byte("x"), 0o755); err != nil {
		t.Fatal(err)
	}
	if digest != "" {
		writeShaMeta(contentDir, digest)
	}
}

func TestCacheHitHonoursTheLock(t *testing.T) {
	plat := engine.Platform{Triple: "aarch64-apple-darwin"}
	for _, tc := range []struct {
		name, recorded, locked string
		wantErr                bool
	}{
		{"matching pin", "sha256:aaa", "sha256:aaa", false},
		{"no pin yet", "sha256:aaa", "", false},
		{"entry predates digests", "", "sha256:aaa", false},
		{"lock pins something else", "sha256:aaa", "sha256:bbb", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := &Manager{Home: t.TempDir()}
			seedCache(t, m.Home, "valkey", "9.0.1", plat, tc.recorded)

			// The URL is never fetched: a cache hit must answer first.
			_, digest, err := m.EnsureArtifact(context.Background(), "valkey", "9.0.1", plat, "http://unused.invalid/x.tgz", "", "", tc.locked)
			if tc.wantErr {
				if err == nil || !strings.Contains(err.Error(), "the lock pins sha256:bbb") {
					t.Fatalf("want a lock mismatch, got %v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("cache hit failed: %v", err)
			}
			if digest != tc.recorded {
				t.Fatalf("digest = %q, want %q", digest, tc.recorded)
			}
		})
	}
}
