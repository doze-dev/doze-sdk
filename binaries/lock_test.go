package binaries

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/doze-dev/doze-sdk/engine"
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

func TestLockModulePinRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "doze.lock")
	lock, _ := LoadLock(path)

	mp := ModulePin{
		Version: "0.2.0", Protocol: 1, Engines: []string{"14", "15", "16"},
		Hashes: map[string]string{linux.Triple: "sha256:abc", mac.Triple: "sha256:def"},
	}
	lock.RecordModule("doze/postgres", mp)
	if err := lock.Save(); err != nil {
		t.Fatal(err)
	}

	reloaded, err := LoadLock(path)
	if err != nil {
		t.Fatal(err)
	}
	got, ok := reloaded.GetModule("doze/postgres")
	if !ok || got.Version != "0.2.0" || got.Protocol != 1 {
		t.Fatalf("module pin = %+v", got)
	}
	if len(got.Engines) != 3 || got.Engines[2] != "16" {
		t.Fatalf("engines not preserved: %+v", got.Engines)
	}
	if got.Hashes[mac.Triple] != "sha256:def" {
		t.Fatalf("hashes not preserved: %+v", got.Hashes)
	}

	// RecordModule replaces the pin wholesale — that is how upgrade moves it.
	lock.RecordModule("doze/postgres", ModulePin{Version: "0.3.0", Protocol: 1, Engines: []string{"14", "15", "16", "17"}, Hashes: map[string]string{linux.Triple: "sha256:xyz"}})
	got, _ = lock.GetModule("doze/postgres")
	if got.Version != "0.3.0" || len(got.Hashes) != 1 || got.Hashes[linux.Triple] != "sha256:xyz" {
		t.Fatalf("upgrade must replace the pin: %+v", got)
	}

	lock.DropModule("doze/postgres")
	if _, ok := lock.GetModule("doze/postgres"); ok {
		t.Fatal("dropped pin must be gone")
	}
}

func TestLockDropsPreRedesignModuleEntries(t *testing.T) {
	// The pre-redesign shape nested a channel map under each source; its keys are
	// unknown to ModulePin, so the entry parses to a versionless pin and is dropped.
	path := filepath.Join(t.TempDir(), "doze.lock")
	old := `engines: {}
modules:
    doze/postgres:
        default:
            resolved: 0.1.0
            source: doze/postgres
            hashes:
                aarch64-apple-darwin: sha256:aaa
keys:
    doze: c29tZWtleQ==
`
	if err := os.WriteFile(path, []byte(old), 0o644); err != nil {
		t.Fatal(err)
	}
	lock, err := LoadLock(path)
	if err != nil {
		t.Fatalf("old-format module entries must not fail the load: %v", err)
	}
	if _, ok := lock.GetModule("doze/postgres"); ok {
		t.Fatal("old-format entry must be treated as absent")
	}
	if k, ok := lock.GetKey("doze"); !ok || k != "c29tZWtleQ==" {
		t.Fatal("keys layer must survive")
	}
	// A save after the scrub writes a clean file that re-pins on next resolve.
	lock.RecordModule("doze/postgres", ModulePin{Version: "0.2.0", Protocol: 1})
	if err := lock.Save(); err != nil {
		t.Fatal(err)
	}
	reloaded, _ := LoadLock(path)
	if got, ok := reloaded.GetModule("doze/postgres"); !ok || got.Version != "0.2.0" {
		t.Fatalf("re-pin after scrub = %+v", got)
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
