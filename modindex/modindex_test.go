package modindex

import (
	"crypto/ed25519"
	"errors"
	"strings"
	"testing"
)

func testIndex() *Index {
	return &Index{
		Schema:    1,
		Module:    "postgres",
		Namespace: "doze",
		Releases: map[string]Release{
			"0.1.0": {
				Protocol: 1,
				Engines:  []string{"14", "15", "16", "17"},
				Artifacts: map[string]Artifact{
					"aarch64-apple-darwin": {URL: "https://x/postgres-plugin-0.1.0.tar.gz", SHA256: "aa"},
				},
			},
			"0.2.0": {
				Protocol: 1,
				Engines:  []string{"14", "15", "16", "17", "18"},
				Artifacts: map[string]Artifact{
					"aarch64-apple-darwin": {URL: "https://x/postgres-plugin-0.2.0.tar.gz", SHA256: "bb"},
				},
			},
			"0.3.0": {
				Protocol: 2,
				Engines:  []string{"14", "15", "16", "17", "18", "19"},
				Artifacts: map[string]Artifact{
					"aarch64-apple-darwin": {URL: "https://x/postgres-plugin-0.3.0.tar.gz", SHA256: "cc"},
				},
			},
		},
		Channels: map[string]string{"stable": "0.3.0"},
	}
}

func TestSelect(t *testing.T) {
	idx := testIndex()

	tests := []struct {
		name    string
		proto   int
		majors  []string
		want    string
		wantErr string
	}{
		{name: "channel head wins when compatible", proto: 2, majors: []string{"18"}, want: "0.3.0"},
		{name: "falls back to newest compatible when head speaks newer protocol", proto: 1, majors: []string{"16"}, want: "0.2.0"},
		{name: "engine gate picks release that supports the major", proto: 1, majors: []string{"18"}, want: "0.2.0"},
		{name: "unsupported major names latest and its support", proto: 1, majors: []string{"19"}, wantErr: "no release of postgres supports postgres 19; latest (0.2.0) supports 14, 15, 16, 17, 18"},
		{name: "no protocol-compatible release asks to upgrade doze", proto: 0, majors: nil, wantErr: "plugin protocol"},
		{name: "no declared majors -> newest, gate open", proto: 1, majors: nil, want: "0.2.0"},
		{name: "exact spec reduced to major by caller", proto: 1, majors: []string{Major("16.14")}, want: "0.2.0"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v, _, err := Select(idx, tt.proto, tt.majors, "stable")
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("want error containing %q, got %v", tt.wantErr, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if v != tt.want {
				t.Fatalf("selected %s, want %s", v, tt.want)
			}
		})
	}
}

func TestSelectEmptyEnginesNoGate(t *testing.T) {
	idx := &Index{
		Schema: 1, Module: "s3", Namespace: "doze",
		Releases: map[string]Release{
			"0.2.0": {Protocol: 1, Artifacts: map[string]Artifact{"t": {URL: "u", SHA256: "s"}}},
		},
		Channels: map[string]string{"stable": "0.2.0"},
	}
	v, _, err := Select(idx, 1, []string{"anything"}, "stable")
	if err != nil || v != "0.2.0" {
		t.Fatalf("versionless module must not gate: got %s, %v", v, err)
	}
}

func TestSelectErrorTypes(t *testing.T) {
	idx := testIndex()
	var pe *ProtocolError
	_, _, err := Select(idx, 9, nil, "")
	if !errors.As(err, &pe) {
		t.Fatalf("want ProtocolError, got %T: %v", err, err)
	}
	var ee *EngineSupportError
	_, _, err = Select(idx, 1, []string{"19"}, "")
	if !errors.As(err, &ee) {
		t.Fatalf("want EngineSupportError, got %T: %v", err, err)
	}
	if ee.Latest != "0.2.0" {
		t.Fatalf("EngineSupportError.Latest = %s, want 0.2.0", ee.Latest)
	}
}

func TestParseRejectsOldFormat(t *testing.T) {
	old := []byte("engines:\n  postgres:\n    versions:\n      default: 0.1.1\n")
	if _, err := Parse(old); err == nil || !strings.Contains(err.Error(), "re-published") {
		t.Fatalf("old-format index must be rejected with a re-publish hint, got %v", err)
	}
}

func TestParseRoundTrip(t *testing.T) {
	yaml := []byte(`schema: 1
module: valkey
namespace: doze
releases:
  "0.2.0":
    protocol: 1
    engines: ["8", "9"]
    artifacts:
      aarch64-apple-darwin:
        url: https://x/valkey-plugin-0.2.0.tar.gz
        sha256: abc
        sig: c2ln
channels:
  stable: "0.2.0"
signature: aWdub3JlZA==
`)
	idx, err := Parse(yaml)
	if err != nil {
		t.Fatal(err)
	}
	if idx.Module != "valkey" || idx.Releases["0.2.0"].Protocol != 1 || idx.Channels["stable"] != "0.2.0" {
		t.Fatalf("bad parse: %+v", idx)
	}
}

func TestSignVerify(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	idx := testIndex()
	if err := Sign(idx, priv); err != nil {
		t.Fatal(err)
	}
	if err := Verify(idx, pub); err != nil {
		t.Fatalf("signed index must verify: %v", err)
	}

	// Any metadata mutation invalidates the signature.
	idx.Channels["stable"] = "0.1.0"
	if err := Verify(idx, pub); err == nil {
		t.Fatal("mutated channels must fail verification")
	}
	idx.Channels["stable"] = "0.3.0"
	if err := Verify(idx, pub); err != nil {
		t.Fatalf("restored index must verify again: %v", err)
	}
	rel := idx.Releases["0.2.0"]
	rel.Engines = append(rel.Engines, "19")
	idx.Releases["0.2.0"] = rel
	if err := Verify(idx, pub); err == nil {
		t.Fatal("mutated engine support must fail verification")
	}

	// Unsigned and wrong-key both fail.
	idx2 := testIndex()
	if err := Verify(idx2, pub); err == nil {
		t.Fatal("unsigned index must fail verification")
	}
	otherPub, _, _ := ed25519.GenerateKey(nil)
	idx3 := testIndex()
	_ = Sign(idx3, priv)
	if err := Verify(idx3, otherPub); err == nil {
		t.Fatal("wrong key must fail verification")
	}
}

func TestCanonicalPayloadStable(t *testing.T) {
	a, err := CanonicalPayload(testIndex())
	if err != nil {
		t.Fatal(err)
	}
	b, err := CanonicalPayload(testIndex())
	if err != nil {
		t.Fatal(err)
	}
	if string(a) != string(b) {
		t.Fatal("canonical payload must be deterministic")
	}
	if strings.Contains(string(a), "\n") || strings.Contains(string(a), "signature") {
		t.Fatalf("payload must be single-line and exclude the signature: %s", a)
	}
}

func TestCompareVersions(t *testing.T) {
	cases := [][2]string{{"0.10.0", "0.9.1"}, {"1.0.0", "0.9.9"}, {"0.2.0", "0.1.1"}}
	for _, c := range cases {
		if CompareVersions(c[0], c[1]) <= 0 {
			t.Fatalf("%s must sort above %s", c[0], c[1])
		}
	}
	if CompareVersions("1.2.3", "1.2.3") != 0 {
		t.Fatal("equal versions must compare 0")
	}
}
