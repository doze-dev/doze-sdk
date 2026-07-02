package binaries

import (
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"sort"

	"gopkg.in/yaml.v3"

	"github.com/doze-dev/doze-sdk/engine"
)

// LockFileName is the conventional name of the binaries lockfile, kept next to
// doze.hcl and committed to source control. It is YAML (JSON, a YAML subset,
// still parses, so lockfiles written by older doze keep loading).
const LockFileName = "doze.lock"

// Lock pins each (engine, version spec) to an exact resolved version and the
// per-triple archive checksums it was verified against. It is doze's go.sum:
// commit it and every machine gets byte-identical binaries. Lock implements
// engine.Locker.
type Lock struct {
	// Engines maps engine type -> version spec ("16" or "16.14") -> pin.
	Engines map[string]map[string]*lockPin `yaml:"engines"`
	// Modules maps a module source ("doze/postgres") to its pin — one pin per
	// source. This is the second pin layer: the plugin binary doze fetches from
	// the registry, above the service binary the plugin itself resolves.
	Modules map[string]*ModulePin `yaml:"modules,omitempty"`
	// Keys pins each registry namespace to its publisher's base64 ed25519 public
	// key (trust-on-first-use): once recorded, a changed key is rejected until the
	// pin is cleared, so a compromised registry can't silently swap signing keys.
	Keys map[string]string `yaml:"keys,omitempty"`

	path  string
	dirty bool
}

type lockPin struct {
	Resolved string            `yaml:"resolved"`         // full version, e.g. "16.14.0"
	Source   string            `yaml:"source"`           // "mirror", "override"
	Hashes   map[string]string `yaml:"hashes,omitempty"` // triple -> "sha256:<hex>"
}

// ModulePin pins one registry module source to an exact release. Version is the
// MODULE release ("0.2.0"), a different axis from the engine versions in the
// Engines layer. Protocol and Engines are copied from the verified index at pin
// time so protocol/engine-support gating works offline.
type ModulePin struct {
	Version  string            `yaml:"version"`
	Protocol int               `yaml:"protocol,omitempty"`
	Engines  []string          `yaml:"engines,omitempty"` // supported engine majors; empty = no gate
	Hashes   map[string]string `yaml:"hashes,omitempty"`  // triple -> "sha256:<hex>", every published triple
}

// LoadLock reads the lockfile at path. A missing file yields an empty lock.
func LoadLock(path string) (*Lock, error) {
	l := &Lock{Engines: map[string]map[string]*lockPin{}, path: path}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return l, nil
		}
		return nil, err
	}
	if err := yaml.Unmarshal(data, l); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", path, err)
	}
	if l.Engines == nil {
		l.Engines = map[string]map[string]*lockPin{}
	}
	// Drop pre-redesign module entries (their nested channel maps parse to a
	// versionless ModulePin). They re-pin on the next resolve.
	for source, p := range l.Modules {
		if p == nil || p.Version == "" {
			delete(l.Modules, source)
			l.dirty = true
		}
	}
	l.path = path
	return l, nil
}

// Path returns the lockfile path.
func (l *Lock) Path() string { return l.path }

// Get returns the pin for (engine, spec), if present. (engine.Locker)
func (l *Lock) Get(eng string, spec engine.VersionSpec, _ engine.Platform) (engine.Pin, bool) {
	if l == nil {
		return engine.Pin{}, false
	}
	p, ok := l.Engines[eng][string(spec)]
	if !ok {
		return engine.Pin{}, false
	}
	return engine.Pin{Resolved: p.Resolved, Source: p.Source, Hashes: copyMap(p.Hashes)}, true
}

// Record pins (engine, spec) to a resolved version and merges the platform's
// artifact hash. (engine.Locker)
func (l *Lock) Record(eng string, spec engine.VersionSpec, _ engine.Platform, pin engine.Pin) {
	if l == nil {
		return
	}
	key := string(spec)
	if l.Engines[eng] == nil {
		l.Engines[eng] = map[string]*lockPin{}
	}
	p := l.Engines[eng][key]
	if p == nil || p.Resolved != pin.Resolved {
		p = &lockPin{Resolved: pin.Resolved, Source: pin.Source, Hashes: map[string]string{}}
		l.Engines[eng][key] = p
		l.dirty = true
	}
	if p.Source == "" && pin.Source != "" {
		p.Source = pin.Source
		l.dirty = true
	}
	for triple, sha := range pin.Hashes {
		if p.Hashes == nil {
			p.Hashes = map[string]string{}
		}
		if p.Hashes[triple] != sha {
			p.Hashes[triple] = sha
			l.dirty = true
		}
	}
}

// Entries returns every (engine, spec) pin in the lock, so the whole set can be
// shipped to an out-of-process plugin that resolves several component binaries.
// (engine.LockLister)
func (l *Lock) Entries() []engine.LockEntry {
	if l == nil {
		return nil
	}
	var out []engine.LockEntry
	for eng, specs := range l.Engines {
		for spec, p := range specs {
			if p == nil {
				continue
			}
			out = append(out, engine.LockEntry{
				Engine: eng,
				Spec:   engine.VersionSpec(spec),
				Pin:    engine.Pin{Resolved: p.Resolved, Source: p.Source, Hashes: copyMap(p.Hashes)},
			})
		}
	}
	return out
}

// GetModule returns the pin for a module source ("doze/postgres"), if present.
func (l *Lock) GetModule(source string) (ModulePin, bool) {
	if l == nil {
		return ModulePin{}, false
	}
	p := l.Modules[source]
	// A pin without a version is a pre-redesign entry that survived parsing
	// (its nested fields are simply unknown keys to ModulePin) — treat as absent.
	if p == nil || p.Version == "" {
		return ModulePin{}, false
	}
	return ModulePin{Version: p.Version, Protocol: p.Protocol, Engines: append([]string(nil), p.Engines...), Hashes: copyMap(p.Hashes)}, true
}

// RecordModule pins a module source to a release — the doze-modules layer of the
// lock. It replaces any existing pin for the source (that is how `doze modules
// upgrade` moves a pin).
func (l *Lock) RecordModule(source string, pin ModulePin) {
	if l == nil || pin.Version == "" {
		return
	}
	if l.Modules == nil {
		l.Modules = map[string]*ModulePin{}
	}
	p := l.Modules[source]
	if p != nil && p.Version == pin.Version && p.Protocol == pin.Protocol &&
		slices.Equal(p.Engines, pin.Engines) && maps.Equal(p.Hashes, pin.Hashes) {
		return
	}
	l.Modules[source] = &ModulePin{
		Version:  pin.Version,
		Protocol: pin.Protocol,
		Engines:  append([]string(nil), pin.Engines...),
		Hashes:   copyMap(pin.Hashes),
	}
	l.dirty = true
}

// DropModule removes a module source's pin (used before re-resolving on upgrade
// failure paths and by tests).
func (l *Lock) DropModule(source string) {
	if l == nil {
		return
	}
	if _, ok := l.Modules[source]; ok {
		delete(l.Modules, source)
		l.dirty = true
	}
}

// GetKey returns the pinned publisher key for a registry namespace, if any.
func (l *Lock) GetKey(namespace string) (string, bool) {
	if l == nil {
		return "", false
	}
	k, ok := l.Keys[namespace]
	return k, ok
}

// RecordKey pins a namespace's publisher key (trust-on-first-use). It is a no-op
// if the same key is already pinned; the caller rejects a *different* key.
func (l *Lock) RecordKey(namespace, key string) {
	if l == nil || key == "" {
		return
	}
	if l.Keys == nil {
		l.Keys = map[string]string{}
	}
	if l.Keys[namespace] != key {
		l.Keys[namespace] = key
		l.dirty = true
	}
}

// Resolved returns the full versions pinned for an engine (across all specs).
func (l *Lock) Resolved(eng string) []string {
	var out []string
	for _, p := range l.Engines[eng] {
		if p != nil && p.Resolved != "" {
			out = append(out, p.Resolved)
		}
	}
	return out
}

// Specs returns the pinned version specs for an engine, sorted.
func (l *Lock) Specs(eng string) []string {
	var out []string
	for k := range l.Engines[eng] {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// Save writes the lockfile if it has unsaved changes.
func (l *Lock) Save() error {
	if l == nil || !l.dirty || l.path == "" {
		return nil
	}
	data, err := yaml.Marshal(l)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(l.path), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(l.path, data, 0o644); err != nil {
		return err
	}
	l.dirty = false
	return nil
}

func copyMap(m map[string]string) map[string]string {
	if m == nil {
		return nil
	}
	out := make(map[string]string, len(m))
	maps.Copy(out, m)
	return out
}
