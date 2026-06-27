package binaries

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/doze-dev/doze-sdk/engine"
)

// Manager fetches and caches engine toolchains from the mirror. It holds no
// lock state; callers pass an engine.Locker to Resolve.
type Manager struct {
	Home string // doze home; toolchains cache under <home>/<engine>/<full>-<triple>
	HTTP *http.Client
	Logf func(format string, args ...any)

	// MirrorRoot, when set, is the base every artifact mirror is joined under
	// (<root>/<name>), bypassing the DOZE_MIRROR/DOZE_<ENGINE>_MIRROR env lookups.
	// The modules fetcher sets it so plugin modules resolve against doze-modules
	// instead of doze-binaries while reusing this fetch/verify/cache machinery.
	MirrorRoot string

	// SigningKey, when set, requires every fetched artifact to carry a valid
	// ed25519 signature (ManifestArtifact.Sig) over its SHA256, made with this key.
	// The module registry fetcher sets it to the publisher's namespace key, so an
	// unsigned or wrongly-signed plugin is rejected. Nil ⇒ checksum-only (engine
	// binaries).
	SigningKey ed25519.PublicKey

	manifests map[string]*Manifest // memoized per engine (each has its own release/index.json)
}

// NewManager returns a Manager caching under home.
func NewManager(home string) *Manager {
	return &Manager{
		Home:      home,
		HTTP:      &http.Client{Timeout: 10 * time.Minute},
		Logf:      func(string, ...any) {},
		manifests: map[string]*Manifest{},
	}
}

func (m *Manager) logf(format string, args ...any) {
	if m.Logf != nil {
		m.Logf(format, args...)
	}
}

// HostPlatform returns the running host's platform descriptor.
func HostPlatform() (engine.Platform, error) {
	triple, err := targetTriple(runtime.GOOS, runtime.GOARCH)
	if err != nil {
		return engine.Platform{}, err
	}
	return engine.Platform{OS: runtime.GOOS, Arch: runtime.GOARCH, Triple: triple}, nil
}

func targetTriple(goos, goarch string) (string, error) {
	switch goos {
	case "linux":
		switch goarch {
		case "amd64":
			return "x86_64-unknown-linux-gnu", nil
		case "arm64":
			return "aarch64-unknown-linux-gnu", nil
		}
	case "darwin":
		switch goarch {
		case "arm64":
			return "aarch64-apple-darwin", nil
			// Intel Mac (darwin/amd64) is intentionally unsupported — Apple Silicon only.
		}
	}
	return "", fmt.Errorf("unsupported platform %s/%s", goos, goarch)
}

// mirrorBase returns the mirror base URL for an engine. Each engine has its own
// rolling release (tag = engine name), so the default is per-engine. A Manager
// with MirrorRoot set joins the name under it (the modules fetcher's path);
// otherwise override with DOZE_<ENGINE>_MIRROR, or DOZE_MIRROR (name joined).
func (m *Manager) mirrorBase(eng string) string {
	if m.MirrorRoot != "" {
		return strings.TrimRight(m.MirrorRoot, "/") + "/" + eng
	}
	if v := os.Getenv("DOZE_" + strings.ToUpper(eng) + "_MIRROR"); v != "" {
		return v
	}
	if v := os.Getenv("DOZE_MIRROR"); v != "" {
		return strings.TrimRight(v, "/") + "/" + eng
	}
	return DefaultMirrorRoot + "/" + eng
}

// fetchManifest fetches and memoizes the engine's index.json (each engine's
// release serves its own).
func (m *Manager) fetchManifest(eng string) (*Manifest, error) {
	if man, ok := m.manifests[eng]; ok {
		return man, nil
	}
	url := strings.TrimRight(m.mirrorBase(eng), "/") + "/index.yaml"
	body, err := m.get(url)
	if err != nil {
		return nil, fmt.Errorf("fetching mirror manifest %s: %w", url, err)
	}
	var man Manifest
	if err := yaml.Unmarshal(body, &man); err != nil {
		return nil, fmt.Errorf("parsing mirror manifest: %w", err)
	}
	m.manifests[eng] = &man
	return &man, nil
}

// Manifest returns the mirror's index.json (memoized). engineHint selects a
// per-engine mirror override (DOZE_<ENGINE>_MIRROR) when set; pass "" for the
// default mirror.
func (m *Manager) Manifest(engineHint string) (*Manifest, error) {
	return m.fetchManifest(engineHint)
}

// ResolveMajor returns the full version the mirror maps a major to.
func (m *Manager) ResolveMajor(eng, major string) (string, error) {
	man, err := m.fetchManifest(eng)
	if err != nil {
		return "", err
	}
	em, ok := man.Engines[eng]
	if !ok {
		return "", fmt.Errorf("mirror has no engine %q", eng)
	}
	full, ok := em.Versions[major]
	if !ok {
		return "", fmt.Errorf("mirror has no %s version for major %q", eng, major)
	}
	return full, nil
}

// Ensure makes the toolchain for (engine, full) present and returns its bin dir
// and verified "sha256:<hex>" digest. It checks the content-addressed cache
// first, then downloads and verifies from the mirror. expectedSHA, when set
// (from the lockfile), must match.
func (m *Manager) Ensure(ctx context.Context, eng, full string, plat engine.Platform, expectedSHA string) (binDir, digest string, err error) {
	contentDir := filepath.Join(m.Home, eng, full+"-"+plat.Triple)
	binDir = filepath.Join(contentDir, "bin")
	if dirHasFiles(binDir) {
		return binDir, readShaMeta(contentDir), nil
	}

	man, err := m.fetchManifest(eng)
	if err != nil {
		return "", "", err
	}
	art, ok := man.Engines[eng].Artifacts[full][plat.Triple]
	if !ok {
		return "", "", fmt.Errorf("mirror has no %s %s artifact for %s", eng, full, plat.Triple)
	}
	url := art.URL
	if !strings.Contains(url, "://") {
		url = strings.TrimRight(m.mirrorBase(eng), "/") + "/" + strings.TrimLeft(url, "/")
	}

	m.logf("downloading %s %s (%s)…", eng, full, plat.Triple)
	archive, err := m.get(url)
	if err != nil {
		return "", "", fmt.Errorf("downloading %s: %w", url, err)
	}
	gotSum := hex.EncodeToString(sum256(archive))
	digest = "sha256:" + gotSum
	switch {
	case expectedSHA != "":
		if digest != expectedSHA {
			return "", "", fmt.Errorf("checksum mismatch for %s %s (%s): locked %s, got %s", eng, full, plat.Triple, expectedSHA, digest)
		}
	case art.SHA256 != "":
		if !strings.EqualFold(gotSum, art.SHA256) {
			return "", "", fmt.Errorf("checksum mismatch for %s %s (%s): manifest %s, got %s", eng, full, plat.Triple, art.SHA256, gotSum)
		}
	default:
		return "", "", fmt.Errorf("no checksum available to verify %s %s (%s)", eng, full, plat.Triple)
	}

	// Signed registry path: the artifact must carry a valid publisher signature
	// over its checksum. Rejects unsigned or tampered modules.
	if m.SigningKey != nil {
		if err := verifySig(m.SigningKey, gotSum, art.Sig); err != nil {
			return "", "", fmt.Errorf("signature check failed for %s %s (%s): %w", eng, full, plat.Triple, err)
		}
	}

	tmp := contentDir + ".tmp"
	_ = os.RemoveAll(tmp)
	if err := extractTarGz(archive, tmp); err != nil {
		return "", "", fmt.Errorf("extracting: %w", err)
	}
	found := locateBinDir(tmp)
	if found == "" {
		return "", "", fmt.Errorf("extracted %s archive has no bin/ directory", eng)
	}
	// Normalize so the layout is always <contentDir>/bin.
	if filepath.Dir(found) != tmp {
		inner := filepath.Dir(found)
		entries, _ := os.ReadDir(inner)
		for _, e := range entries {
			_ = os.Rename(filepath.Join(inner, e.Name()), filepath.Join(tmp, e.Name()))
		}
	}
	writeShaMeta(tmp, digest)
	_ = os.RemoveAll(contentDir)
	if err := os.MkdirAll(filepath.Dir(contentDir), 0o755); err != nil {
		return "", "", err
	}
	if err := os.Rename(tmp, contentDir); err != nil {
		return "", "", err
	}
	if !dirHasFiles(binDir) {
		return "", "", fmt.Errorf("downloaded %s toolchain has no executables", eng)
	}
	m.logf("cached %s %s at %s", eng, full, contentDir)
	return binDir, digest, nil
}

// Fetch retrieves a URL through the same transport Ensure uses, including the
// file:// shortcut for local/dev mirrors. The registry fetcher uses it to pull a
// namespace's keys.json before any signed artifact is downloaded.
func (m *Manager) Fetch(url string) ([]byte, error) { return m.get(url) }

func (m *Manager) get(url string) ([]byte, error) {
	// A file:// mirror (or a self-hosted mirror on a local path) is read directly.
	if path, ok := strings.CutPrefix(url, "file://"); ok {
		return os.ReadFile(path)
	}
	resp, err := m.HTTP.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET %s: %s", url, resp.Status)
	}
	return io.ReadAll(resp.Body)
}

func sum256(b []byte) []byte { s := sha256.Sum256(b); return s[:] }

const shaMetaFile = ".doze-archive-sha"

func writeShaMeta(dir, digest string) {
	_ = os.WriteFile(filepath.Join(dir, shaMetaFile), []byte(digest+"\n"), 0o644)
}

func readShaMeta(dir string) string {
	data, err := os.ReadFile(filepath.Join(dir, shaMetaFile))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

// dirHasFiles reports whether dir exists and contains at least one entry.
func dirHasFiles(dir string) bool {
	entries, err := os.ReadDir(dir)
	return err == nil && len(entries) > 0
}

// locateBinDir finds the first directory named "bin" under root.
func locateBinDir(root string) string {
	var found string
	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil || found != "" {
			return nil
		}
		if d.IsDir() && d.Name() == "bin" {
			found = path
		}
		return nil
	})
	return found
}

func extractTarGz(data []byte, dest string) error {
	gz, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return err
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		target := filepath.Join(dest, hdr.Name)
		if !strings.HasPrefix(target, filepath.Clean(dest)+string(os.PathSeparator)) && target != filepath.Clean(dest) {
			return fmt.Errorf("unsafe path in archive: %q", hdr.Name)
		}
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			f, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, os.FileMode(hdr.Mode)&0o777)
			if err != nil {
				return err
			}
			if _, err := io.Copy(f, tr); err != nil {
				f.Close()
				return err
			}
			f.Close()
		case tar.TypeSymlink:
			_ = os.Symlink(hdr.Linkname, target)
		}
	}
	return nil
}

// verifySig checks a base64 ed25519 signature over the lowercase-hex sha against
// key. A missing signature is a failure when a key is configured.
func verifySig(key ed25519.PublicKey, shaHex, sigB64 string) error {
	if sigB64 == "" {
		return fmt.Errorf("artifact is unsigned")
	}
	sig, err := base64.StdEncoding.DecodeString(sigB64)
	if err != nil {
		return fmt.Errorf("malformed signature: %w", err)
	}
	if !ed25519.Verify(key, []byte(shaHex), sig) {
		return fmt.Errorf("signature does not match the publisher key")
	}
	return nil
}

// ParsePublicKey decodes a base64 ed25519 public key (the form a registry's
// keys.json carries). Callers set it as Manager.SigningKey.
func ParsePublicKey(b64 string) (ed25519.PublicKey, error) {
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(b64))
	if err != nil {
		return nil, fmt.Errorf("decoding public key: %w", err)
	}
	if len(raw) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("public key is %d bytes, want %d", len(raw), ed25519.PublicKeySize)
	}
	return ed25519.PublicKey(raw), nil
}
