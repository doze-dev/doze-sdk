package binaries

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"gopkg.in/yaml.v3"

	"github.com/nerdmenot/doze-sdk/engine"
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
	// Modules maps plugin module name -> version spec ("default") -> pin. This is
	// the second pin layer: the plugin binary doze fetches from doze-modules, above
	// the service binary the plugin itself resolves.
	Modules map[string]map[string]*lockPin `yaml:"modules,omitempty"`

	path  string
	dirty bool
}

type lockPin struct {
	Resolved string            `yaml:"resolved"`         // full version, e.g. "16.14.0"
	Source   string            `yaml:"source"`           // "mirror", "override"
	Hashes   map[string]string `yaml:"hashes,omitempty"` // triple -> "sha256:<hex>"
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

// GetModule returns the pin for plugin module (name, spec), if present.
func (l *Lock) GetModule(name, spec string) (engine.Pin, bool) {
	if l == nil {
		return engine.Pin{}, false
	}
	p, ok := l.Modules[name][spec]
	if !ok {
		return engine.Pin{}, false
	}
	return engine.Pin{Resolved: p.Resolved, Source: p.Source, Hashes: copyMap(p.Hashes)}, true
}

// RecordModule pins a plugin module (name, spec) to a resolved version and merges
// the platform's archive hash — the doze-modules layer of the lock.
func (l *Lock) RecordModule(name, spec string, pin engine.Pin) {
	if l == nil {
		return
	}
	if l.Modules == nil {
		l.Modules = map[string]map[string]*lockPin{}
	}
	if l.Modules[name] == nil {
		l.Modules[name] = map[string]*lockPin{}
	}
	p := l.Modules[name][spec]
	if p == nil || p.Resolved != pin.Resolved {
		p = &lockPin{Resolved: pin.Resolved, Source: pin.Source, Hashes: map[string]string{}}
		l.Modules[name][spec] = p
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
	for k, v := range m {
		out[k] = v
	}
	return out
}
