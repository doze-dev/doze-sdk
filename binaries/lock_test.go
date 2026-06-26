package binaries

import (
	"path/filepath"
	"testing"

	"github.com/nerdmenot/doze-sdk/engine"
)

var (
	linux = engine.Platform{OS: "linux", Arch: "amd64", Triple: "x86_64-unknown-linux-gnu"}
	mac   = engine.Platform{OS: "darwin", Arch: "arm64", Triple: "aarch64-apple-darwin"}
)

func pin(resolved, triple, sha string) engine.Pin {
	return engine.Pin{Resolved: resolved, Source: "mirror", Hashes: map[string]string{triple: sha}}
}

func TestLockRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "doze.lock")
	lock, err := LoadLock(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(lock.Specs("postgres")) != 0 {
		t.Fatal("fresh lock should be empty")
	}

	// Same (engine, spec) across two platforms merges hashes.
	lock.Record("postgres", "16", linux, pin("16.14.0", linux.Triple, "sha256:abc"))
	lock.Record("postgres", "16", mac, pin("16.14.0", mac.Triple, "sha256:def"))
	lock.Record("valkey", "9", linux, pin("9.1.0", linux.Triple, "sha256:ghi"))
	if err := lock.Save(); err != nil {
		t.Fatal(err)
	}

	reloaded, err := LoadLock(path)
	if err != nil {
		t.Fatal(err)
	}
	p, ok := reloaded.Get("postgres", "16", linux)
	if !ok || p.Resolved != "16.14.0" || p.Source != "mirror" {
		t.Fatalf("pin = %+v", p)
	}
	if p.Hashes[linux.Triple] != "sha256:abc" || p.Hashes[mac.Triple] != "sha256:def" {
		t.Fatalf("hashes not preserved: %+v", p.Hashes)
	}
	if vp, ok := reloaded.Get("valkey", "9", linux); !ok || vp.Resolved != "9.1.0" {
		t.Fatalf("valkey pin = %+v", vp)
	}
	if got := reloaded.Specs("postgres"); len(got) != 1 || got[0] != "16" {
		t.Fatalf("specs = %v", got)
	}
}

func TestLockMissingFile(t *testing.T) {
	lock, err := LoadLock(filepath.Join(t.TempDir(), "nope.lock"))
	if err != nil {
		t.Fatalf("missing lockfile should be ok: %v", err)
	}
	if _, ok := lock.Get("postgres", "16", linux); ok {
		t.Fatal("missing lock should have no pins")
	}
	if err := lock.Save(); err != nil {
		t.Fatal(err)
	}
}

func TestLockResolvedChangeResetsHashes(t *testing.T) {
	lock, _ := LoadLock(filepath.Join(t.TempDir(), "doze.lock"))
	lock.Record("postgres", "16", linux, pin("16.13.0", linux.Triple, "sha256:old"))
	lock.Record("postgres", "16", linux, pin("16.14.0", linux.Triple, "sha256:new"))
	p, _ := lock.Get("postgres", "16", linux)
	if p.Resolved != "16.14.0" || p.Hashes[linux.Triple] != "sha256:new" {
		t.Fatalf("changing resolved version should reset hashes: %+v", p)
	}
	if len(p.Hashes) != 1 {
		t.Fatalf("stale hash should be gone: %+v", p.Hashes)
	}
}
