package plugin

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/gohcl"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/doze-dev/doze-sdk/engine"
	"github.com/doze-dev/doze-sdk/plugin/proto"
)

// diagDriver decodes `color`, and refuses a block without one with an error it
// cannot place.
type diagDriver struct{ engine.Driver }

func (diagDriver) DecodeConfig(body hcl.Body, ctx *hcl.EvalContext, _ string, _ engine.VersionSpec) (engine.EngineConfig, error) {
	var c struct {
		Color string `hcl:"color,optional"`
	}
	if d := gohcl.DecodeBody(body, ctx, &c); d.HasErrors() {
		return nil, d
	}
	if c.Color == "" {
		return nil, errors.New("diag needs a color")
	}
	return &c, nil
}

func decodeVia(t *testing.T, src string) error {
	t.Helper()
	s := newEngineServer(diagDriver{})
	_, err := s.DecodeConfig(context.Background(), &proto.DecodeRequest{
		File: []byte(src), Filename: "doze.hcl", BlockType: "diag", BlockLabel: "x",
	})
	if err == nil {
		t.Fatal("expected the decode to fail")
	}
	return err
}

// A rejected block crosses the wire as the same flat text as before, with its
// diagnostics, positions and all, as a status detail the client hands back
// under a *DecodeError.
func TestDecodeDiagnosticsCrossTheWire(t *testing.T) {
	wire := decodeVia(t, "diag \"x\" {\n  version = 1\n  colour  = \"red\"\n  shade   = 2\n}\n")
	st, _ := status.FromError(wire)
	if st.Code() != codes.Unknown {
		t.Errorf("code = %v, want Unknown (what a plain error always was)", st.Code())
	}
	// The text an older host shows: every diagnostic, one per line.
	if want := "doze.hcl:3,3-9: Unsupported argument;"; !strings.HasPrefix(st.Message(), want) || strings.Count(st.Message(), "\n") != 1 {
		t.Errorf("message = %q, want two lines starting %q", st.Message(), want)
	}

	err := decodeErrorFrom(wire)
	var de *DecodeError
	if !errors.As(err, &de) {
		t.Fatalf("want a *DecodeError, got %T: %v", err, err)
	}
	if err.Error() != st.Message() {
		t.Errorf("Error() = %q, want the status message %q", err.Error(), st.Message())
	}
	var dd hcl.Diagnostics
	if !errors.As(err, &dd) || len(dd) != 2 {
		t.Fatalf("want two hcl.Diagnostics under the error, got %v", dd)
	}
	d := dd[0]
	if d.Severity != hcl.DiagError || d.Summary != "Unsupported argument" || !strings.Contains(d.Detail, `"colour"`) {
		t.Errorf("diagnostic = %+v", d)
	}
	want := hcl.Range{Filename: "doze.hcl", Start: hcl.Pos{Line: 3, Column: 3, Byte: 27}, End: hcl.Pos{Line: 3, Column: 9, Byte: 33}}
	if d.Subject == nil || *d.Subject != want {
		t.Errorf("subject = %v, want %v", d.Subject, want)
	}
}

// An error the driver can't place is pinned to the block's header.
func TestUnplacedDecodeErrorPointsAtTheBlock(t *testing.T) {
	err := decodeErrorFrom(decodeVia(t, "\ndiag \"x\" {\n  version = 1\n}\n"))
	if err.Error() != "diag needs a color" {
		t.Errorf("Error() = %q, want the driver's own text", err.Error())
	}
	var dd hcl.Diagnostics
	if !errors.As(err, &dd) || len(dd) != 1 {
		t.Fatalf("want one diagnostic, got %v", dd)
	}
	if s := dd[0].Subject; s == nil || s.Start.Line != 2 || dd[0].Summary != "diag needs a color" {
		t.Errorf("diagnostic = %+v, subject %v", dd[0], s)
	}
}

// A module built before diagnostics crossed the wire sends only the text: the
// client returns it as a plain error, as it always did.
func TestDecodeErrorWithoutDiagnosticsIsPlain(t *testing.T) {
	err := decodeErrorFrom(status.Error(codes.Unknown, "kv config: doze.hcl:2,3-9: Unsupported argument"))
	var de *DecodeError
	if errors.As(err, &de) {
		t.Fatalf("no diagnostics were sent, got a *DecodeError: %v", de)
	}
	if err.Error() != "kv config: doze.hcl:2,3-9: Unsupported argument" {
		t.Errorf("Error() = %q", err.Error())
	}
}

// End to end through a real plugin process.
func TestRemoteDecodeDiagnostics(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "kv")
	build := exec.Command("go", "build", "-o", bin, "./kvtest")
	build.Stderr = os.Stderr
	if err := build.Run(); err != nil {
		t.Fatalf("building kv plugin: %v", err)
	}
	h, err := Launch(bin, nil)
	if err != nil {
		t.Fatalf("launch: %v", err)
	}
	defer h.Close()
	rd := h.Driver().(engine.RemoteDecoder)
	file := []byte("kv \"cache\" {\n  version = 9\n  bogus   = 1\n}\n")
	_, err = rd.DecodeRemote(file, "doze.hcl", "kv", "cache", nil, ".", "9")
	var dd hcl.Diagnostics
	if !errors.As(err, &dd) || len(dd) != 1 || dd[0].Subject == nil {
		t.Fatalf("want one positioned diagnostic, got %v", err)
	}
	if s := dd[0].Subject; s.Filename != "doze.hcl" || s.Start.Line != 3 || s.Start.Column != 3 {
		t.Errorf("subject = %v, want doze.hcl line 3 column 3", s)
	}
	if !strings.Contains(err.Error(), "bogus") {
		t.Errorf("Error() = %q, want the module's text", err.Error())
	}
}
