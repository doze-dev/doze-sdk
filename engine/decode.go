package engine

import (
	"fmt"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/gohcl"
)

// DecodeStrict decodes an HCL config block body into target (a pointer to a
// struct with `hcl:` tags) and flattens any diagnostics into a single error.
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
// The returned error carries the diagnostics' full rendering (source ranges
// included), matching what a hand-rolled gohcl.DecodeBody + diags.Error()
// would produce.
func DecodeStrict(body hcl.Body, ctx *hcl.EvalContext, target any) error {
	if d := gohcl.DecodeBody(body, ctx, target); d.HasErrors() {
		return fmt.Errorf("%s", d.Error())
	}
	return nil
}
