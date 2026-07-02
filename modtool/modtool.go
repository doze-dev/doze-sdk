// Package modtool builds and packages doze engine modules into the release
// layout a registry publishes: per-platform plugin archives, the schema-1
// module index (doze-sdk/modindex), and the generated meta.yaml docs.
//
// It is the library behind doze-modules' dzm AND the way third-party module
// repos release — a module repo's cmd/release is ~10 lines:
//
//	m := modtool.Module{
//	    Name: "httpd", Version: "0.1.0", Namespace: "acme",
//	    PluginPath: "./plugin", Driver: httpd.Driver{},
//	}
//	if err := modtool.Release(m, "dist", modtool.AllTriples()); err != nil { … }
//
// Because the caller compiles against its own driver, Describe() is a direct
// call — the engine-support list and config docs are generated from the code
// that actually decodes, and can't drift.
package modtool

import (
	"archive/tar"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/doze-dev/doze-sdk/engine"
	"github.com/doze-dev/doze-sdk/modindex"
	dozeplugin "github.com/doze-dev/doze-sdk/plugin"
)

// Module describes one engine module to build and package.
type Module struct {
	Name      string // engine type / registry name, e.g. "httpd"
	Version   string // MODULE version (semver) — not the engine version
	Namespace string // publisher namespace stamped into the index, e.g. "acme"
	// PluginPath is the package path of the plugin main (relative to RepoDir),
	// e.g. "./plugin".
	PluginPath string
	// RepoDir is the module repo root the build runs in ("." when empty).
	RepoDir string
	// Driver supplies Describe(): the engine-support list for the signed index
	// and the meta.yaml documentation. Mandatory — an undescribed module cannot
	// be published.
	Driver engine.Describer
	// Logf receives progress lines; fmt.Printf-style. Defaults to stdout.
	Logf func(format string, args ...any)
}

// triples maps a platform triple to its GOOS/GOARCH — the supported set:
// Apple Silicon mac + 64-bit Linux (Intel Mac is intentionally unsupported).
var triples = map[string][2]string{
	"aarch64-apple-darwin":      {"darwin", "arm64"},
	"aarch64-unknown-linux-gnu": {"linux", "arm64"},
	"x86_64-unknown-linux-gnu":  {"linux", "amd64"},
}

// AllTriples returns the supported platform triples, sorted.
func AllTriples() []string {
	out := make([]string, 0, len(triples))
	for t := range triples {
		out = append(out, t)
	}
	sort.Strings(out)
	return out
}

// Release builds the module for every triple and writes the full release
// layout under outDir/<name>: archives + index.yaml + meta.yaml.
func Release(m Module, outDir string, buildTriples []string) error {
	for _, triple := range buildTriples {
		if err := Build(m, outDir, triple); err != nil {
			return err
		}
	}
	return WriteMeta(m, outDir)
}

// Build cross-compiles the plugin for one triple (pure Go, CGO off,
// -trimpath -buildvcs=false so identical source builds identical bytes),
// packages it as bin/<name>-plugin in a tar.gz, and merges the schema-1
// index.yaml.
//
// Published artifacts are IMMUTABLE and never rebuilt: when the (possibly
// pre-downloaded) index already carries this (version, triple), the build is
// skipped — a changed published sha would strand every doze.lock pinning it.
func Build(m Module, outDir, triple string) error {
	if err := m.validate(); err != nil {
		return err
	}
	logf := m.logf()
	plat, ok := triples[triple]
	if !ok {
		return fmt.Errorf("unknown triple %q (supported: %s)", triple, strings.Join(AllTriples(), ", "))
	}
	moduleDir := filepath.Join(outDir, m.Name)
	if err := os.MkdirAll(moduleDir, 0o755); err != nil {
		return err
	}
	archiveName := fmt.Sprintf("%s-plugin-%s-%s.tar.gz", m.Name, m.Version, triple)
	archivePath := filepath.Join(moduleDir, archiveName)

	if published(filepath.Join(moduleDir, "index.yaml"), m.Version, triple) {
		logf("  skip %s (already published)", triple)
		return nil
	}

	tmp, err := os.MkdirTemp("", "modtool-"+m.Name)
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	binPath := filepath.Join(tmp, "bin", m.Name+"-plugin")
	if err := os.MkdirAll(filepath.Dir(binPath), 0o755); err != nil {
		return err
	}
	logf("  build %s", triple)
	cmd := exec.Command("go", "build", "-trimpath", "-buildvcs=false", "-o", binPath, m.PluginPath)
	cmd.Dir = m.repoDir()
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0", "GOOS="+plat[0], "GOARCH="+plat[1])
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("building %s for %s: %w\n%s", m.Name, triple, err, out)
	}

	sha, err := writeTarGz(archivePath, tmp, "bin/"+m.Name+"-plugin")
	if err != nil {
		return err
	}
	return mergeIndex(filepath.Join(moduleDir, "index.yaml"), m, triple, archiveName, sha, logf)
}

// WriteMeta generates outDir/<name>/meta.yaml from the driver's Describe() —
// the docs the registry site and `doze modules docs` render.
func WriteMeta(m Module, outDir string) error {
	if err := m.validate(); err != nil {
		return err
	}
	mf := toMetaFile(m.Name, m.Driver.Describe())
	data, err := yaml.Marshal(mf)
	if err != nil {
		return err
	}
	dir := filepath.Join(outDir, m.Name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "meta.yaml"), data, 0o644)
}

func (m Module) validate() error {
	switch {
	case m.Name == "":
		return fmt.Errorf("modtool: Module.Name is required")
	case m.Version == "":
		return fmt.Errorf("modtool: Module.Version is required")
	case m.Namespace == "":
		return fmt.Errorf("modtool: Module.Namespace is required")
	case m.PluginPath == "":
		return fmt.Errorf("modtool: Module.PluginPath is required")
	case m.Driver == nil:
		return fmt.Errorf("modtool: Module.Driver is required — implement Describe() on the driver; the index's engine-support list and the docs are generated from it")
	}
	return nil
}

func (m Module) repoDir() string {
	if m.RepoDir == "" {
		return "."
	}
	return m.RepoDir
}

func (m Module) logf() func(string, ...any) {
	if m.Logf != nil {
		return m.Logf
	}
	return func(format string, args ...any) { fmt.Printf(format+"\n", args...) }
}

// published reports whether the index at path already carries an artifact for
// (version, triple) — i.e. it was released before and must not be rebuilt.
func published(indexPath, version, triple string) bool {
	b, err := os.ReadFile(indexPath)
	if err != nil {
		return false
	}
	idx, err := modindex.Parse(b)
	if err != nil {
		return false
	}
	_, ok := idx.Releases[version].Artifacts[triple]
	return ok
}

// writeTarGz tars the single member (relative to root) into dest.tar.gz and
// returns its sha256 hex.
func writeTarGz(dest, root, member string) (string, error) {
	f, err := os.Create(dest)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	gz := gzip.NewWriter(io.MultiWriter(f, h))
	tw := tar.NewWriter(gz)
	body, err := os.ReadFile(filepath.Join(root, member))
	if err != nil {
		return "", err
	}
	hdr := &tar.Header{Name: member, Mode: 0o755, Size: int64(len(body)), Typeflag: tar.TypeReg}
	if err := tw.WriteHeader(hdr); err != nil {
		return "", err
	}
	if _, err := tw.Write(body); err != nil {
		return "", err
	}
	if err := tw.Close(); err != nil {
		return "", err
	}
	if err := gz.Close(); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// mergeIndex updates the per-module schema-1 index.yaml in place: it adds this
// (release, triple) artifact, stamps the release's plugin protocol and engine
// -support list (from Describe()), and points channels.stable at the highest
// release. Existing releases are preserved (publishing is cumulative); a
// pre-schema index is discarded and rebuilt. The index is written UNSIGNED —
// the registry's publish step signs it.
func mergeIndex(path string, m Module, triple, archiveName, sha string, logf func(string, ...any)) error {
	var idx *modindex.Index
	if b, err := os.ReadFile(path); err == nil {
		if parsed, perr := modindex.Parse(b); perr == nil {
			idx = parsed
		} else {
			logf("  note: discarding pre-schema index at %s (%v)", path, perr)
		}
	}
	if idx == nil {
		idx = &modindex.Index{Schema: modindex.Schema, Module: m.Name, Namespace: m.Namespace, Releases: map[string]modindex.Release{}, Channels: map[string]string{}}
	}
	if idx.Releases == nil {
		idx.Releases = map[string]modindex.Release{}
	}
	if idx.Channels == nil {
		idx.Channels = map[string]string{}
	}

	rel, exists := idx.Releases[m.Version]
	// A published (release, triple) artifact is immutable: rebuilding the same
	// version with different bytes silently swaps binaries under a semver —
	// force a version bump instead.
	if exists {
		if prev, ok := rel.Artifacts[triple]; ok && !strings.EqualFold(prev.SHA256, sha) {
			return fmt.Errorf("%s %s (%s) is already published with a different sha256 — bump the module version instead of rebuilding it", m.Name, m.Version, triple)
		}
	}
	rel.Protocol = dozeplugin.ProtocolVersion
	rel.Engines = engineMajors(m.Driver.Describe())
	if rel.Artifacts == nil {
		rel.Artifacts = map[string]modindex.Artifact{}
	}
	rel.Artifacts[triple] = modindex.Artifact{URL: archiveName, SHA256: sha}
	idx.Releases[m.Version] = rel

	stable := m.Version
	for v := range idx.Releases {
		if modindex.CompareVersions(v, stable) > 0 {
			stable = v
		}
	}
	idx.Channels["stable"] = stable
	idx.Signature = "" // never carry a stale signature past a mutation

	b, err := yaml.Marshal(idx)
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o644)
}

// engineMajors reduces Describe().Versions to unique engine majors for the
// index's engine-support gate. Versionless engines (no Versions) return nil —
// no gate.
func engineMajors(d engine.Description) []string {
	var out []string
	seen := map[string]bool{}
	for _, v := range d.Versions {
		major := modindex.Major(v)
		if major != "" && !seen[major] {
			seen[major] = true
			out = append(out, major)
		}
	}
	return out
}
