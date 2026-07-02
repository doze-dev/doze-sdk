package engine

import (
	"fmt"
	"strconv"
	"strings"
)

// Major returns the spec's major segment ("16.14" -> "16", "18" -> "18").
func (v VersionSpec) Major() string {
	if i := strings.IndexByte(string(v), '.'); i > 0 {
		return string(v)[:i]
	}
	return string(v)
}

// RequireVersion is the uniform decode-time check for version-gated config:
// it returns an error when spec's major is numeric and below min. An empty or
// non-numeric spec passes — versionless engines and unusual specs are gated
// elsewhere. what names the argument for the error, e.g. `"io_method"`.
func RequireVersion(spec VersionSpec, min int, what string) error {
	major, err := strconv.Atoi(spec.Major())
	if err != nil {
		return nil
	}
	if major < min {
		return fmt.Errorf("%s requires engine version >= %d (declared %s)", what, min, spec)
	}
	return nil
}

// RequireVersionBelow is RequireVersion's counterpart for removed arguments:
// it returns an error when spec's major is numeric and at or above max.
func RequireVersionBelow(spec VersionSpec, max int, what string) error {
	major, err := strconv.Atoi(spec.Major())
	if err != nil {
		return nil
	}
	if major >= max {
		return fmt.Errorf("%s was removed in engine version %d (declared %s)", what, max, spec)
	}
	return nil
}
