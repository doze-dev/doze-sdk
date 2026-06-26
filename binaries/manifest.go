// Package binaries resolves an (engine, version) to a directory of executables,
// fetching from the doze-binaries mirror when needed and verifying checksums.
//
// Resolution order, cheapest first:
//
//  1. DOZE_<ENGINE>_BINDIR — an explicit bin dir for that engine (CI, local builds).
//  2. A content-addressed cache under <home>/<engine>/<full>-<triple>/bin.
//  3. A verified download from the mirror (its index.json + archives).
//
// The exact version each spec resolves to, and the checksum it was verified
// against, are recorded in doze.lock so every machine gets identical binaries.
package binaries

// DefaultMirrorRoot is the doze-binaries release-download root. Each engine has
// its own rolling release (tag = engine name), so the per-engine base is
// "<root>/<engine>", serving that engine's index.json and archives. Override per
// engine with DOZE_<ENGINE>_MIRROR, or set DOZE_MIRROR to a root that the engine
// name is joined to.
const DefaultMirrorRoot = "https://github.com/doze-dev/doze-binaries/releases/download"

// Manifest is the multi-engine index the mirror serves at <base>/index.yaml.
type Manifest struct {
	Engines map[string]EngineManifest `yaml:"engines"`
}

// EngineManifest is one engine's slice of the manifest: a major->full version
// map plus, per full version, a triple->artifact map.
type EngineManifest struct {
	Versions  map[string]string                      `yaml:"versions"`
	Artifacts map[string]map[string]ManifestArtifact `yaml:"artifacts"`
}

// ManifestArtifact describes one downloadable archive.
type ManifestArtifact struct {
	URL    string `yaml:"url"`
	SHA256 string `yaml:"sha256"`
	// Sig is a base64 ed25519 signature over the lowercase-hex SHA256, made with
	// the publisher's key. Verified against a Manager.SigningKey when one is set
	// (the signed module-registry path); ignored for unsigned mirrors (the engine
	// binaries from doze-binaries).
	Sig string `yaml:"sig,omitempty"`
}
