package binaries

import (
	"encoding/json"
	"testing"
)

func TestManifestParse(t *testing.T) {
	data := `{
	  "engines": {
	    "postgres": {
	      "versions": {"16": "16.14.0"},
	      "artifacts": {
	        "16.14.0": {
	          "x86_64-unknown-linux-gnu": {"url": "postgresql-16.14.0-x86_64-unknown-linux-gnu.tar.gz", "sha256": "deadbeef"}
	        }
	      }
	    },
	    "valkey": {
	      "versions": {"9": "9.1.0"},
	      "artifacts": {"9.1.0": {"aarch64-apple-darwin": {"url": "valkey-9.1.0-aarch64-apple-darwin.tar.gz", "sha256": "cafe"}}}
	    }
	  }
	}`
	var man Manifest
	if err := json.Unmarshal([]byte(data), &man); err != nil {
		t.Fatal(err)
	}
	if man.Engines["postgres"].Versions["16"] != "16.14.0" {
		t.Errorf("postgres versions = %v", man.Engines["postgres"].Versions)
	}
	art := man.Engines["postgres"].Artifacts["16.14.0"]["x86_64-unknown-linux-gnu"]
	if art.URL == "" || art.SHA256 != "deadbeef" {
		t.Errorf("postgres artifact = %+v", art)
	}
	if man.Engines["valkey"].Versions["9"] != "9.1.0" {
		t.Errorf("valkey versions = %v", man.Engines["valkey"].Versions)
	}
}
