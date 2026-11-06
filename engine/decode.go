package engine

import (
	"sort"

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
		return InSourceOrder(d)
	}
	return nil
}

// InSourceOrder sorts diagnostics by where they point: gohcl reports
// unexpected arguments in map order, so two mistakes would otherwise swap
// places from one run to the next.
func InSourceOrder(d hcl.Diagnostics) hcl.Diagnostics {
	sort.SliceStable(d, func(i, j int) bool {
		a, b := d[i].Subject, d[j].Subject
		if a == nil || b == nil {
			return a != nil
		}
		if a.Filename != b.Filename {
			return a.Filename < b.Filename
		}
		return a.Start.Byte < b.Start.Byte
	})
	return d
}
