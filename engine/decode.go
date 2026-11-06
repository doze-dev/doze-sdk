package engine

import (
	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/gohcl"
)

// DecodeStrict decodes an HCL config block body into target (a pointer to a
// struct with `hcl:` tags) and returns any diagnostics as the error.
// gohcl is strict — arguments and blocks the struct doesn't declare are
// rejected — so typos in a user's config surface as decode errors rather than
// being silently ignored. It is the standard first line of a driver's
// DecodeConfig:
//
//	var raw myBody
//	if err := engine.DecodeStrict(body, ctx, &raw); err != nil {
//		return nil, err
//	}
//
// The error is the hcl.Diagnostics value itself: its text is the diagnostics'
// rendering (source ranges included), and returned as is (or wrapped with %w)
// it reaches doze with its positions, so doze shows the user the offending
// line. Flattening it into a string (fmt.Errorf with %s) loses them.
func DecodeStrict(body hcl.Body, ctx *hcl.EvalContext, target any) error {
	if d := gohcl.DecodeBody(body, ctx, target); d.HasErrors() {
		return d
	}
	return nil
}
