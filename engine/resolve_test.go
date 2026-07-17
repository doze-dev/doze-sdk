package engine

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

type fakeLock struct {
	pins map[string]Pin
	rec  []LockEntry
}

func (f *fakeLock) Get(engine string, spec VersionSpec, _ Platform) (Pin, bool) {
	p, ok := f.pins[engine+"/"+spec.String()]
	return p, ok
}
func (f *fakeLock) Record(engine string, spec VersionSpec, _ Platform, pin Pin) {
	f.rec = append(f.rec, LockEntry{Engine: engine, Spec: spec, Pin: pin})
}

type fakeFetch struct {
	majors  map[string]string
	ensured []string // "eng/full/expectedSHA"
}

func (f *fakeFetch) ResolveMajor(eng, major string) (string, error) {
	if v, ok := f.majors[eng+"/"+major]; ok {
		return v, nil
	}
	return "", os.ErrNotExist
}
func (f *fakeFetch) Ensure(_ context.Context, eng, full string, _ Platform, expectedSHA string) (string, string, error) {
	f.ensured = append(f.ensured, eng+"/"+full+"/"+expectedSHA)
	return "/bin", "sha256:abc", nil
}

func TestResolveViaExactMajorAndPin(t *testing.T) {
	plat := Platform{Triple: "aarch64-apple-darwin"}
	fetch := &fakeFetch{majors: map[string]string{"valkey/9": "9.1.0"}}
	lk := &fakeLock{}

	// Major spec resolves through the mirror.
	tc, err := ResolveVia(context.Background(), lk, fetch, plat, "valkey", "9", ExactDots(2))
	if err != nil || tc.Full != "9.1.0" {
		t.Fatalf("major: got (%+v, %v), want Full 9.1.0", tc, err)
	}
	// Exact spec skips the mirror entirely.
	tc, err = ResolveVia(context.Background(), lk, fetch, plat, "valkey", "8.0.9", ExactDots(2))
	if err != nil || tc.Full != "8.0.9" {
		t.Fatalf("exact: got (%+v, %v), want Full 8.0.9", tc, err)
	}
	// A two-part spec under ExactDots(2) is a major -> mirror miss surfaces.
	if _, err = ResolveVia(context.Background(), lk, fetch, plat, "valkey", "8.0", ExactDots(2)); err == nil {
		t.Fatal("two-part spec with two-dot exactness should have consulted the mirror and failed")
	}
	// A lockfile pin wins over both and threads its sha into Ensure.
	lk.pins = map[string]Pin{"valkey/9": {Resolved: "9.0.4", Hashes: map[string]string{plat.Triple: "sha256:pinned"}}}
	tc, err = ResolveVia(context.Background(), lk, fetch, plat, "valkey", "9", ExactDots(2))
	if err != nil || tc.Full != "9.0.4" {
		t.Fatalf("pinned: got (%+v, %v), want Full 9.0.4", tc, err)
	}
	last := fetch.ensured[len(fetch.ensured)-1]
	if last != "valkey/9.0.4/sha256:pinned" {
		t.Fatalf("pinned Ensure = %q, want expectedSHA threaded through", last)
	}
	if len(lk.rec) == 0 || lk.rec[len(lk.rec)-1].Pin.Resolved != "9.0.4" {
		t.Fatalf("pin not re-recorded: %+v", lk.rec)
	}
}

func TestExactDots(t *testing.T) {
	if _, ok := ExactDots(1)("16"); ok {
		t.Error(`ExactDots(1)("16") should be a major`)
	}
	if _, ok := ExactDots(1)("16.14"); !ok {
		t.Error(`ExactDots(1)("16.14") should be exact`)
	}
	if _, ok := ExactDots(2)("11.4"); ok {
		t.Error(`ExactDots(2)("11.4") should be a major (a mariadb line)`)
	}
	if full, ok := ExactDots(2)("11.4.5"); !ok || full != "11.4.5" {
		t.Error(`ExactDots(2)("11.4.5") should be exact`)
	}
}

func TestClearStaleLock(t *testing.T) {
	dir := t.TempDir()
	lock := filepath.Join(dir, "pid")

	// Missing lock: success.
	if err := ClearStaleLock("x", lock); err != nil {
		t.Fatalf("missing lock: %v", err)
	}
	// Stale pid (long dead): cleared.
	if err := os.WriteFile(lock, []byte("999999999\nrest\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := ClearStaleLock("x", lock); err != nil {
		t.Fatalf("stale pid: %v", err)
	}
	if _, err := os.Stat(lock); !os.IsNotExist(err) {
		t.Fatal("stale lock not removed")
	}
	// Live pid (ourselves): refuse.
	if err := os.WriteFile(lock, []byte(strconv.Itoa(os.Getpid())+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := ClearStaleLock("x", lock); err == nil {
		t.Fatal("live pid should refuse to clear")
	}
}
