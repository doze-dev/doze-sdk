package engine

import (
	"maps"
	"slices"
)

// SortedKeys returns m's keys in sorted order, for deterministic iteration
// over config maps (settings, role parameters, …) whose emission order must be
// stable across runs.
func SortedKeys[V any](m map[string]V) []string {
	return slices.Sorted(maps.Keys(m))
}
