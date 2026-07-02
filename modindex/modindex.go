// Package modindex defines the signed module index a doze registry serves for
// each engine module — the schema, its canonical signing payload, and the
// release-selection policy doze core applies.
//
// The index versions the *module* (the plugin binary), which is a different
// axis from the *engine* version a user declares in doze.hcl: `version = 18`
// names the software to run; the module release that runs it is chosen here,
// automatically, as the newest release compatible with the host's plugin
// protocol and the declared engine majors. binaries.Manifest remains the
// engine-binaries mirror format; modules never reuse it.
package modindex

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"slices"
	"sort"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// Schema is the index schema version this package reads and writes.
const Schema = 1

// Index is one module's signed registry index (<registry>/<ns>/<name>/index.yaml).
type Index struct {
	Schema    int    `yaml:"schema" json:"schema"`
	Module    string `yaml:"module" json:"module"`       // must equal the registry directory name
	Namespace string `yaml:"namespace" json:"namespace"` // publisher namespace, e.g. "doze"
	// Releases maps a module release version (semver) to its record.
	Releases map[string]Release `yaml:"releases" json:"releases"`
	// Channels are npm-dist-tag-style pointers into Releases ("stable" -> "0.2.0").
	Channels map[string]string `yaml:"channels" json:"channels"`
	// Signature is a base64 ed25519 signature by the namespace key over the
	// lowercase-hex SHA256 of CanonicalPayload — it covers the release metadata
	// (protocol, engine support, channels), not just the artifacts, so a
	// compromised host can't lie about compatibility or roll a channel back.
	Signature string `yaml:"signature,omitempty" json:"-"`
}

// Release is one published module version.
type Release struct {
	// Protocol is the doze plugin protocol this binary speaks (plugin.ProtocolVersion).
	Protocol int `yaml:"protocol" json:"protocol"`
	// Engines lists the engine MAJORS this release supports (from the driver's
	// Describe().Versions). Empty means unversioned/no gate (s3, sqs, sns).
	Engines []string `yaml:"engines,omitempty" json:"engines,omitempty"`
	// Artifacts maps a platform triple to its downloadable archive.
	Artifacts map[string]Artifact `yaml:"artifacts" json:"artifacts"`
}

// Artifact is one downloadable module archive.
type Artifact struct {
	URL    string `yaml:"url" json:"url"`
	SHA256 string `yaml:"sha256" json:"sha256"`
	// Sig is a base64 ed25519 signature over the lowercase-hex SHA256 — the
	// per-artifact execution gate, unchanged from the original scheme.
	Sig string `yaml:"sig,omitempty" json:"sig,omitempty"`
}

// Parse reads a schema-1 index. An index without `schema: 1` — including the
// pre-schema format that reused the engine-mirror manifest — is rejected.
func Parse(data []byte) (*Index, error) {
	var idx Index
	if err := yaml.Unmarshal(data, &idx); err != nil {
		return nil, fmt.Errorf("parsing module index: %w", err)
	}
	if idx.Schema != Schema {
		return nil, fmt.Errorf("module index is not schema %d (got %d): the module must be re-published for this doze", Schema, idx.Schema)
	}
	if idx.Module == "" {
		return nil, fmt.Errorf("module index has no module name")
	}
	return &idx, nil
}

// CanonicalPayload renders the signed portion of the index — module, namespace,
// releases, channels — as canonical JSON: object keys sorted, no insignificant
// whitespace, no HTML escaping, empty optional fields omitted. The registry's
// signer (scripts/lib.mjs) produces byte-identical output.
func CanonicalPayload(idx *Index) ([]byte, error) {
	releases := map[string]any{}
	for v, r := range idx.Releases {
		rel := map[string]any{"protocol": r.Protocol}
		if len(r.Engines) > 0 {
			rel["engines"] = r.Engines
		}
		arts := map[string]any{}
		for triple, a := range r.Artifacts {
			art := map[string]any{"url": a.URL, "sha256": strings.ToLower(a.SHA256)}
			if a.Sig != "" {
				art["sig"] = a.Sig
			}
			arts[triple] = art
		}
		rel["artifacts"] = arts
		releases[v] = rel
	}
	payload := map[string]any{
		"module":    idx.Module,
		"namespace": idx.Namespace,
		"releases":  releases,
		"channels":  idx.Channels,
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(payload); err != nil {
		return nil, err
	}
	// Encoder appends a newline; the canonical form has none.
	return bytes.TrimRight(buf.Bytes(), "\n"), nil
}

// Verify checks the index-level signature against the namespace's public key.
func Verify(idx *Index, key ed25519.PublicKey) error {
	if idx.Signature == "" {
		return fmt.Errorf("module index is unsigned")
	}
	sig, err := base64.StdEncoding.DecodeString(idx.Signature)
	if err != nil {
		return fmt.Errorf("malformed index signature: %w", err)
	}
	payload, err := CanonicalPayload(idx)
	if err != nil {
		return err
	}
	sum := sha256.Sum256(payload)
	if !ed25519.Verify(key, []byte(hex.EncodeToString(sum[:])), sig) {
		return fmt.Errorf("index signature does not match the publisher key")
	}
	return nil
}

// Sign computes the index-level signature with the publisher's private key and
// stores it on the index. The registry normally signs in JS (scripts/lib.mjs);
// this exists for tests and local file:// registries.
func Sign(idx *Index, key ed25519.PrivateKey) error {
	payload, err := CanonicalPayload(idx)
	if err != nil {
		return err
	}
	sum := sha256.Sum256(payload)
	idx.Signature = base64.StdEncoding.EncodeToString(ed25519.Sign(key, []byte(hex.EncodeToString(sum[:]))))
	return nil
}

// ProtocolError reports that no release of a module speaks the host's plugin
// protocol.
type ProtocolError struct {
	Module string
	Host   int // the protocol this doze speaks
	Min    int // lowest protocol any release speaks
	Max    int // highest protocol any release speaks
}

func (e *ProtocolError) Error() string {
	if e.Min > e.Host {
		return fmt.Sprintf("every release of %s requires plugin protocol >= %d; this doze speaks %d — upgrade doze", e.Module, e.Min, e.Host)
	}
	return fmt.Sprintf("no release of %s speaks plugin protocol %d (releases span %d-%d)", e.Module, e.Host, e.Min, e.Max)
}

// EngineSupportError reports that no protocol-compatible release supports the
// declared engine major(s).
type EngineSupportError struct {
	Module    string
	Majors    []string // the declared engine majors that went unsatisfied
	Latest    string   // newest protocol-compatible release
	Supported []string // that release's supported majors
}

func (e *EngineSupportError) Error() string {
	return fmt.Sprintf("no release of %s supports %s %s; latest (%s) supports %s",
		e.Module, e.Module, strings.Join(e.Majors, ", "), e.Latest, strings.Join(e.Supported, ", "))
}

// Select picks the module release for a host: the channel head when it is
// compatible with hostProtocol and requiredMajors, else the newest compatible
// release (so an older doze keeps resolving after the channel moves to a newer
// protocol). requiredMajors are engine majors ("18"); a release with an empty
// Engines list satisfies any of them. channel defaults to "stable".
func Select(idx *Index, hostProtocol int, requiredMajors []string, channel string) (string, Release, error) {
	if channel == "" {
		channel = "stable"
	}
	if len(idx.Releases) == 0 {
		return "", Release{}, fmt.Errorf("module index for %s has no releases", idx.Module)
	}

	minP, maxP := 0, 0
	var protoOK []string // protocol-compatible release versions
	for v, r := range idx.Releases {
		if minP == 0 || r.Protocol < minP {
			minP = r.Protocol
		}
		if r.Protocol > maxP {
			maxP = r.Protocol
		}
		if r.Protocol == hostProtocol {
			protoOK = append(protoOK, v)
		}
	}
	if len(protoOK) == 0 {
		return "", Release{}, &ProtocolError{Module: idx.Module, Host: hostProtocol, Min: minP, Max: maxP}
	}
	sort.Slice(protoOK, func(i, j int) bool { return CompareVersions(protoOK[i], protoOK[j]) > 0 })

	var candidates []string
	for _, v := range protoOK {
		if Supports(idx.Releases[v], requiredMajors) {
			candidates = append(candidates, v)
		}
	}
	if len(candidates) == 0 {
		latest := protoOK[0]
		return "", Release{}, &EngineSupportError{
			Module: idx.Module, Majors: requiredMajors,
			Latest: latest, Supported: idx.Releases[latest].Engines,
		}
	}

	// Prefer the channel head when it is itself a candidate.
	if head, ok := idx.Channels[channel]; ok && slices.Contains(candidates, head) {
		return head, idx.Releases[head], nil
	}
	return candidates[0], idx.Releases[candidates[0]], nil
}

// Supports reports whether a release covers every required engine major. An
// empty Engines list means the module is unversioned — no gate.
func Supports(r Release, requiredMajors []string) bool {
	if len(r.Engines) == 0 {
		return true
	}
	for _, m := range requiredMajors {
		if m != "" && !slices.Contains(r.Engines, m) {
			return false
		}
	}
	return true
}

// Major returns the major segment of a version spec ("16.14" -> "16").
func Major(spec string) string {
	if i := strings.IndexByte(spec, '.'); i > 0 {
		return spec[:i]
	}
	return spec
}

// CompareVersions orders dotted version strings numerically per segment
// (falling back to string order for non-numeric segments): 0.10.0 > 0.9.1.
func CompareVersions(a, b string) int {
	as, bs := strings.Split(a, "."), strings.Split(b, ".")
	for i := 0; i < len(as) || i < len(bs); i++ {
		var sa, sb string
		if i < len(as) {
			sa = as[i]
		}
		if i < len(bs) {
			sb = bs[i]
		}
		na, ea := strconv.Atoi(sa)
		nb, eb := strconv.Atoi(sb)
		switch {
		case ea == nil && eb == nil:
			if na != nb {
				if na > nb {
					return 1
				}
				return -1
			}
		default:
			if c := strings.Compare(sa, sb); c != 0 {
				return c
			}
		}
	}
	return 0
}
