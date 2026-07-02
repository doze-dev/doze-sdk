package plugin

import (
	"github.com/zclconf/go-cty/cty"
	ctyjson "github.com/zclconf/go-cty/cty/json"
)

// RawSpec holds a plugin's opaque, already-serialized (gob) config. Core stores it
// as an instance's Spec after a remote decode and ships the bytes verbatim on every
// later call; only the owning plugin decodes them back to its typed struct.
type RawSpec struct{ Bytes []byte }

// Raw exposes the opaque spec bytes so the host can fingerprint an instance's
// config (for drift detection) without decoding it or importing this package's
// concrete type — core asserts inst.Spec to interface{ Raw() []byte }.
func (r *RawSpec) Raw() []byte { return r.Bytes }

// varsToJSON serializes an eval context's variables for the wire: each top-level
// value (var.*, local.*, a resource's attribute object, each.*/count.*) is encoded
// with ctyjson so the plugin can reconstruct the EvalContext to evaluate references
// inside its block.
func varsToJSON(vars map[string]cty.Value) (map[string][]byte, error) {
	out := make(map[string][]byte, len(vars))
	for k, v := range vars {
		b, err := ctyjson.Marshal(v, v.Type())
		if err != nil {
			return nil, err
		}
		out[k] = b
	}
	return out, nil
}

// varsFromJSON reverses varsToJSON, recovering the cty values via their implied
// JSON type (sufficient for the strings/numbers/objects config references use).
func varsFromJSON(in map[string][]byte) (map[string]cty.Value, error) {
	out := make(map[string]cty.Value, len(in))
	for k, b := range in {
		t, err := ctyjson.ImpliedType(b)
		if err != nil {
			return nil, err
		}
		v, err := ctyjson.Unmarshal(b, t)
		if err != nil {
			return nil, err
		}
		out[k] = v
	}
	return out, nil
}
