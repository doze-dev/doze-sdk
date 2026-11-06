package plugin

import (
	"github.com/hashicorp/hcl/v2"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/doze-dev/doze-sdk/plugin/proto"
)

// DecodeError is what DecodeRemote returns when a module built with this SDK
// (or newer) rejects its block. Error() is the flat text every module sends,
// unchanged; Diagnostics carries the same errors with their positions, so the
// host can render them as it renders its own (errors.As with an
// hcl.Diagnostics target finds them, through Unwrap). A module built before
// diagnostics crossed the wire sends only the text, and DecodeRemote returns a
// plain error.
type DecodeError struct {
	Message     string
	Diagnostics hcl.Diagnostics
}

func (e *DecodeError) Error() string { return e.Message }
func (e *DecodeError) Unwrap() error { return e.Diagnostics }

// decodeFailure is a failed DecodeConfig on the wire: the status message is
// msg, exactly what a module sent before diagnostics crossed the wire, and the
// diagnostics ride along as a status detail an older host ignores.
func decodeFailure(msg string, diags hcl.Diagnostics) error {
	st := status.New(codes.Unknown, msg)
	if len(diags) == 0 {
		return st.Err()
	}
	if withDiags, err := st.WithDetails(diagsToProto(diags)); err == nil {
		st = withDiags
	}
	return st.Err()
}

// decodeErrorFrom turns a failed DecodeConfig back into an error: a
// *DecodeError when the module sent diagnostics, else the module's text.
func decodeErrorFrom(err error) error {
	if err == nil {
		return nil
	}
	if st, ok := status.FromError(err); ok {
		for _, d := range st.Details() {
			if pd, ok := d.(*proto.DecodeDiagnostics); ok && len(pd.Diagnostics) > 0 {
				return &DecodeError{Message: st.Message(), Diagnostics: diagsFromProto(pd)}
			}
		}
	}
	return userError(err)
}

func diagsToProto(diags hcl.Diagnostics) *proto.DecodeDiagnostics {
	out := &proto.DecodeDiagnostics{}
	for _, d := range diags {
		if d == nil {
			continue
		}
		out.Diagnostics = append(out.Diagnostics, &proto.Diagnostic{
			Warning: d.Severity == hcl.DiagWarning,
			Summary: d.Summary,
			Detail:  d.Detail,
			Subject: rangeToProto(d.Subject),
			Context: rangeToProto(d.Context),
		})
	}
	return out
}

func diagsFromProto(pd *proto.DecodeDiagnostics) hcl.Diagnostics {
	out := make(hcl.Diagnostics, 0, len(pd.GetDiagnostics()))
	for _, d := range pd.GetDiagnostics() {
		sev := hcl.DiagError
		if d.GetWarning() {
			sev = hcl.DiagWarning
		}
		out = append(out, &hcl.Diagnostic{
			Severity: sev,
			Summary:  d.GetSummary(),
			Detail:   d.GetDetail(),
			Subject:  rangeFromProto(d.GetSubject()),
			Context:  rangeFromProto(d.GetContext()),
		})
	}
	return out
}

func rangeToProto(r *hcl.Range) *proto.SourceRange {
	if r == nil {
		return nil
	}
	return &proto.SourceRange{Filename: r.Filename, Start: posToProto(r.Start), End: posToProto(r.End)}
}

func posToProto(p hcl.Pos) *proto.SourcePos {
	return &proto.SourcePos{Line: int64(p.Line), Column: int64(p.Column), Byte: int64(p.Byte)}
}

func rangeFromProto(r *proto.SourceRange) *hcl.Range {
	if r == nil {
		return nil
	}
	return &hcl.Range{Filename: r.GetFilename(), Start: posFromProto(r.GetStart()), End: posFromProto(r.GetEnd())}
}

func posFromProto(p *proto.SourcePos) hcl.Pos {
	return hcl.Pos{Line: int(p.GetLine()), Column: int(p.GetColumn()), Byte: int(p.GetByte())}
}
