package engine

import (
	"context"
	"strings"
)

// ResolveVia is the standard mirror-backed Resolve flow shared by engine
// drivers: honor the lockfile pin, else treat an exact spec as the artifact
// version, else resolve the major through the mirror's majors map; then Ensure
// the toolchain and record the pin so the lockfile freezes the exact artifact.
//
// exactFull reports whether spec names a FULL artifact version for this engine
// and returns it (normalized where the engine's archive versions differ from
// user-facing ones, e.g. postgres "16.14" -> "16.14.0"). ExactDots covers the
// common cases. A driver's Resolve typically reduces to an env-override check
// plus one ResolveVia call.
func ResolveVia(ctx context.Context, lk Locker, fetch Fetcher, plat Platform, eng string, spec VersionSpec, exactFull func(VersionSpec) (string, bool)) (Toolchain, error) {
	full, expectedSHA := "", ""
	if pin, ok := lk.Get(eng, spec, plat); ok && pin.Resolved != "" {
		full = pin.Resolved
		expectedSHA = pin.Hashes[plat.Triple]
	} else if v, ok := exactFull(spec); ok {
		full = v
	} else {
		v, err := fetch.ResolveMajor(eng, spec.String())
		if err != nil {
			return Toolchain{}, err
		}
		full = v
	}
	binDir, digest, err := fetch.Ensure(ctx, eng, full, plat, expectedSHA)
	if err != nil {
		return Toolchain{}, err
	}
	hashes := map[string]string{}
	if digest != "" {
		hashes[plat.Triple] = digest
	}
	lk.Record(eng, spec, plat, Pin{Resolved: full, Source: "mirror", Hashes: hashes})
	return Toolchain{Engine: eng, Full: full, BinDir: binDir}, nil
}

// ExactDots returns an exactFull predicate for ResolveVia: a spec names a full
// artifact version when it carries at least n dots. Engines with three-part
// fulls and one- or two-part majors (valkey "8.1.8", temporal "1.1.0", mariadb
// "11.4.5") use ExactDots(2); an engine whose fulls are two-part (a bare
// "16.14") would use ExactDots(1). VersionSpec.IsExact ("any dot = exact") is
// wrong for engines with dotted majors — this is the replacement.
func ExactDots(n int) func(VersionSpec) (string, bool) {
	return func(v VersionSpec) (string, bool) {
		return v.String(), strings.Count(v.String(), ".") >= n
	}
}
