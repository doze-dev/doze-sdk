package modtool

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/doze-dev/doze-sdk/engine"
	"github.com/doze-dev/doze-sdk/modindex"
	dozeplugin "github.com/doze-dev/doze-sdk/plugin"
)

type fakeDescriber struct{}

func (fakeDescriber) Describe() engine.Description {
	return engine.Description{
		Title: "Echo", Tagline: "test engine", Category: "test",
		Versions: []string{"1", "2"},
		Source:   "test/echo",
		Example:  `echo "e" {}`,
		Config:   []engine.ConfigArg{{Name: "x", Type: "string", Desc: "an arg", Since: "2"}},
	}
}

func hostTriple(t *testing.T) string {
	t.Helper()
	for triple, plat := range triples {
		if plat[0] == runtime.GOOS && plat[1] == runtime.GOARCH {
			return triple
		}
	}
	t.Skipf("no supported triple for %s/%s", runtime.GOOS, runtime.GOARCH)
	return ""
}

func TestReleaseRoundTrip(t *testing.T) {
	triple := hostTriple(t)
	out := t.TempDir()
	m := Module{
		Name: "echo", Version: "0.1.0", Namespace: "test",
		PluginPath: "./plugin/echo", RepoDir: "..",
		Driver: fakeDescriber{},
		Logf:   t.Logf,
	}
	if err := Release(m, out, []string{triple}); err != nil {
		t.Fatal(err)
	}

	// The archive exists and the index parses as signed-schema-1-minus-signature.
	if _, err := os.Stat(filepath.Join(out, "echo", "echo-plugin-0.1.0-"+triple+".tar.gz")); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(out, "echo", "index.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	idx, err := modindex.Parse(b)
	if err != nil {
		t.Fatal(err)
	}
	rel := idx.Releases["0.1.0"]
	if rel.Protocol != dozeplugin.ProtocolVersion || len(rel.Engines) != 2 || idx.Channels["stable"] != "0.1.0" {
		t.Fatalf("bad release record: %+v channels=%v", rel, idx.Channels)
	}
	if idx.Namespace != "test" {
		t.Fatalf("namespace = %q", idx.Namespace)
	}

	// meta.yaml carries the described args (with since) and no versions field.
	meta, err := os.ReadFile(filepath.Join(out, "echo", "meta.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(meta), "since: \"2\"") || strings.Contains(string(meta), "\nversions:") {
		t.Fatalf("meta.yaml wrong shape:\n%s", meta)
	}

	// A second build of the same (version, triple) skips — published is immutable.
	skipped := false
	m.Logf = func(format string, args ...any) {
		if strings.Contains(format, "skip") {
			skipped = true
		}
	}
	if err := Build(m, out, triple); err != nil {
		t.Fatal(err)
	}
	if !skipped {
		t.Fatal("second build must skip the published artifact")
	}
}

func TestValidate(t *testing.T) {
	err := Build(Module{Name: "x", Version: "1", Namespace: "n", PluginPath: "./p"}, t.TempDir(), "aarch64-apple-darwin")
	if err == nil || !strings.Contains(err.Error(), "Describe()") {
		t.Fatalf("missing driver must name Describe(): %v", err)
	}
}

// Ported dzm guarantees: immutability guard, versionless gate, pre-schema discard.
func TestMergeIndexGuarantees(t *testing.T) {
	logf := func(string, ...any) {}
	dir := t.TempDir()
	path := filepath.Join(dir, "index.yaml")
	m := Module{Name: "valkey", Version: "0.2.0", Namespace: "doze", PluginPath: "./p", Driver: fakeDescriber{}}

	if err := mergeIndex(path, m, "aarch64-apple-darwin", "a.tar.gz", "aaa", logf); err != nil {
		t.Fatal(err)
	}
	// Same version + different bytes -> hard error.
	if err := mergeIndex(path, m, "aarch64-apple-darwin", "a.tar.gz", "TAMPERED", logf); err == nil {
		t.Fatal("re-publishing a version with a different sha must fail")
	}
	// Same bytes -> idempotent.
	if err := mergeIndex(path, m, "aarch64-apple-darwin", "a.tar.gz", "aaa", logf); err != nil {
		t.Fatal(err)
	}

	// Versionless driver -> no engine gate.
	type versionless struct{ fakeDescriber }
	vd := Module{Name: "s3", Version: "0.3.0", Namespace: "doze", PluginPath: "./p",
		Driver: describerFunc(func() engine.Description { return engine.Description{Title: "S3"} })}
	vp := filepath.Join(dir, "s3.yaml")
	if err := mergeIndex(vp, vd, "aarch64-apple-darwin", "s.tar.gz", "bbb", logf); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(vp)
	idx, err := modindex.Parse(b)
	if err != nil {
		t.Fatal(err)
	}
	if got := idx.Releases["0.3.0"].Engines; len(got) != 0 {
		t.Fatalf("versionless module must have no engine gate, got %v", got)
	}
	_ = versionless{}

	// A pre-schema index is discarded and rebuilt.
	pre := filepath.Join(dir, "pre.yaml")
	os.WriteFile(pre, []byte("engines:\n  valkey:\n    versions:\n      default: 0.1.0\n"), 0o644)
	if err := mergeIndex(pre, m, "aarch64-apple-darwin", "a.tar.gz", "aaa", logf); err != nil {
		t.Fatal(err)
	}
	b, _ = os.ReadFile(pre)
	if idx, err := modindex.Parse(b); err != nil || len(idx.Releases) != 1 {
		t.Fatalf("pre-schema index must be rebuilt fresh: %v %+v", err, idx)
	}
}

type describerFunc func() engine.Description

func (f describerFunc) Describe() engine.Description { return f() }
