package engine

import (
	"errors"
	"testing"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclparse"
)

// DecodeStrict's error is the diagnostics, positions and all, so they can
// cross the plugin wire; its text is what it always was.
func TestDecodeStrictKeepsDiagnostics(t *testing.T) {
	f, diags := hclparse.NewParser().ParseHCL([]byte("colour = \"red\"\n"), "doze.hcl")
	if diags.HasErrors() {
		t.Fatal(diags)
	}
	var raw struct {
		Color string `hcl:"color,optional"`
	}
	err := DecodeStrict(f.Body, nil, &raw)
	var dd hcl.Diagnostics
	if !errors.As(err, &dd) || len(dd) != 1 || dd[0].Subject == nil || dd[0].Subject.Start.Line != 1 {
		t.Fatalf("want one positioned diagnostic, got %#v", err)
	}
	if want := dd.Error(); err.Error() != want {
		t.Errorf("Error() = %q, want %q", err.Error(), want)
	}
}
