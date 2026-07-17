package modindex

// Conformance test against the checked-in JS<->Go signing fixture.
//
// The fixture's byte-identical twin lives in the doze-registry repo at
// doze-registry/scripts/testdata/conformance/fixture.json, exercised by
// doze-registry/scripts/conformance-check.mjs (bun run test:conformance). The
// index was signed by the registry's JS signer (scripts/lib.mjs) with a
// throwaway keypair; this test proves the Go verification path accepts those
// exact bytes, and that Go's re-signing reproduces the JS signatures
// byte-for-byte (ed25519 is deterministic), pinning CanonicalPayload to the
// JS canonical form. If this fails after a signing-scheme change, the two
// implementations have drifted — fix the code, don't regenerate the fixture
// to paper over it.

import (
	"crypto/ed25519"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"os"
	"strings"
	"testing"
)

type conformanceFixture struct {
	PublicKey       string          `json:"publicKey"`       // raw 32-byte ed25519 key, base64 (keys.json format)
	PrivateKeyPkcs8 string          `json:"privateKeyPkcs8"` // matching PKCS8 DER, base64 (throwaway, test-only)
	Index           json.RawMessage `json:"index"`           // signed schema-1 index (JSON is valid YAML)
}

func loadConformanceFixture(t *testing.T) (conformanceFixture, *Index, ed25519.PublicKey, ed25519.PrivateKey) {
	t.Helper()
	data, err := os.ReadFile("testdata/conformance/fixture.json")
	if err != nil {
		t.Fatalf("reading conformance fixture: %v", err)
	}
	var fx conformanceFixture
	if err := json.Unmarshal(data, &fx); err != nil {
		t.Fatalf("parsing conformance fixture: %v", err)
	}
	idx, err := Parse(fx.Index) // JSON is a subset of YAML: the exact fixture bytes go through Parse
	if err != nil {
		t.Fatalf("parsing fixture index: %v", err)
	}
	rawPub, err := base64.StdEncoding.DecodeString(fx.PublicKey)
	if err != nil || len(rawPub) != ed25519.PublicKeySize {
		t.Fatalf("fixture public key: want raw %d-byte ed25519 key, got %d bytes (err %v)", ed25519.PublicKeySize, len(rawPub), err)
	}
	der, err := base64.StdEncoding.DecodeString(fx.PrivateKeyPkcs8)
	if err != nil {
		t.Fatalf("decoding fixture private key: %v", err)
	}
	keyAny, err := x509.ParsePKCS8PrivateKey(der)
	if err != nil {
		t.Fatalf("parsing fixture PKCS8 private key: %v", err)
	}
	priv, ok := keyAny.(ed25519.PrivateKey)
	if !ok {
		t.Fatalf("fixture private key is %T, want ed25519.PrivateKey", keyAny)
	}
	return fx, idx, ed25519.PublicKey(rawPub), priv
}

func TestConformanceFixtureVerifies(t *testing.T) {
	_, idx, pub, _ := loadConformanceFixture(t)

	if err := Verify(idx, pub); err != nil {
		t.Fatalf("JS-signed fixture index must verify in Go: %v", err)
	}

	artifacts := 0
	for v, r := range idx.Releases {
		for triple, a := range r.Artifacts {
			artifacts++
			sig, err := base64.StdEncoding.DecodeString(a.Sig)
			if err != nil {
				t.Fatalf("artifact %s %s: malformed sig: %v", v, triple, err)
			}
			// The per-artifact scheme shared with doze-sdk/binaries: ed25519
			// over the lowercase-hex SHA256 string of the archive.
			if !ed25519.Verify(pub, []byte(strings.ToLower(a.SHA256)), sig) {
				t.Fatalf("artifact %s %s: JS-made sig must verify in Go", v, triple)
			}
		}
	}
	if artifacts == 0 {
		t.Fatal("fixture has no artifacts")
	}
}

func TestConformanceResignReproducesJS(t *testing.T) {
	_, idx, _, priv := loadConformanceFixture(t)

	// ed25519 is deterministic: Go's Sign over Go's CanonicalPayload must
	// reproduce the JS signature exactly, or the canonical forms have drifted.
	jsSig := idx.Signature
	if err := Sign(idx, priv); err != nil {
		t.Fatal(err)
	}
	if idx.Signature != jsSig {
		payload, _ := CanonicalPayload(idx)
		t.Fatalf("Go re-sign differs from JS signature — canonical payloads drifted\n  go payload: %s", payload)
	}
}

func TestConformanceTamperFails(t *testing.T) {
	_, idx, pub, _ := loadConformanceFixture(t)
	idx.Channels["stable"] = "9.9.9"
	if err := Verify(idx, pub); err == nil {
		t.Fatal("tampered channels must fail verification")
	}
}
