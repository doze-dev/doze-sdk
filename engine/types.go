// Package engine defines the driver contract every database engine implements,
// plus the value types the generic runtime and proxy exchange with drivers.
//
// The package contains NO engine-specific code — only the contract. Concrete
// engines live in their own packages (engine/postgres, engine/valkey, …) and
// self-register via Register in an init function.
package engine

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"path/filepath"
	"strings"
)

// Object is one structural item an instance manages: a Postgres role/database/
// schema/extension/grant, an S3 bucket, an SQS queue, an SNS topic/subscription.
// The runtime tracks the set of objects each instance has applied (in the state
// file) so a later apply can prune the ones no longer declared. Kind+Name is the
// identity; Hash is a content fingerprint used to detect changes ("~" in a plan).
type Object struct {
	Kind string `json:"kind"`
	Name string `json:"name"`
	Hash string `json:"hash"`
}

// HashOf returns a short, stable content fingerprint of v (its JSON form), used
// for an Object's Hash so a plan can tell an unchanged object from a changed one.
func HashOf(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])[:12]
}

// VersionSpec is the raw, un-normalized version from config: either a major
// version ("16") or an exact dotted full version ("16.14"). Each driver
// normalizes it to the form its mirror and toolchain expect.
type VersionSpec string

// IsExact reports whether the spec pins an exact version (contains a dot)
// rather than just a major.
func (v VersionSpec) IsExact() bool { return strings.Contains(string(v), ".") }

// String returns the raw spec text.
func (v VersionSpec) String() string { return string(v) }

// Platform identifies the host for toolchain artifact selection.
type Platform struct {
	OS     string // "linux", "darwin"
	Arch   string // "amd64", "arm64"
	Triple string // e.g. "x86_64-unknown-linux-gnu"
}

// Toolchain is a resolved set of executables for one engine version.
type Toolchain struct {
	Engine string            // "postgres"
	Full   string            // resolved full version, e.g. "16.14.0"
	BinDir string            // directory of executables
	Tools  map[string]string // optional logical-name -> absolute-path overrides
}

// Path returns the absolute path to a named executable in the toolchain,
// honoring any explicit override in Tools.
func (t Toolchain) Path(tool string) string {
	if p, ok := t.Tools[tool]; ok && p != "" {
		return p
	}
	return filepath.Join(t.BinDir, tool)
}

// Pin records the exact version a (engine, spec, platform) resolved to and the
// per-triple archive checksums it was verified against.
type Pin struct {
	Resolved string            // full version
	Source   string            // "mirror", "override", …
	Hashes   map[string]string // triple -> "sha256:<hex>"
}

// Locker records and enforces resolved version pins. The binaries lockfile
// implements it; drivers call Record after resolving.
type Locker interface {
	Get(engine string, spec VersionSpec, plat Platform) (Pin, bool)
	Record(engine string, spec VersionSpec, plat Platform, pin Pin)
}

// LockEntry is one (engine, spec) -> pin row of the lock. It lets a whole lock be
// enumerated and round-tripped (e.g. across the plugin boundary) so composite
// engines that pin several component binaries don't lose entries.
type LockEntry struct {
	Engine string
	Spec   VersionSpec
	Pin    Pin
}

// LockLister is the optional enumeration side of a Locker: the binaries lockfile
// implements it so callers can ship the relevant pins to an out-of-process plugin.
type LockLister interface {
	Entries() []LockEntry
}

// Endpoint is doze's client-facing listener(s) for one instance plus the
// backend address the proxy splices to.
type Endpoint struct {
	UnixSocket string // doze-owned client socket path, "" if none
	TCPAddr    string // doze-owned host:port, "" if none
	Backend    string // backend socket path the proxy dials (Driver.BackendSocket)
}

// EngineConfig is the opaque, engine-specific configuration payload decoded
// from a config block. Drivers type-assert it to their own concrete type.
type EngineConfig = any

// Instance is the runtime's view of one declared instance, handed to a driver.
type Instance struct {
	Name      string         // declared instance name (config block label)
	Type      string         // engine type ("postgres")
	Version   VersionSpec    // major or exact full
	DataDir   string         // per-instance data directory
	SocketDir string         // per-instance backend socket directory
	Port      int            // nominal port used for socket naming
	Endpoint  Endpoint       // doze-owned client-facing endpoint(s)
	Spec      EngineConfig   // engine-specific config (decoded by the driver)
	Deps      map[string]Dep // resolved dependencies, keyed by instance name
	// InjectedEnv is the doze-managed environment a supervised process should run
	// with (the same set `doze run` injects: connection strings + AWS creds/region
	// + DOZE_<NAME>_URL). The runtime populates it before Spawn; non-process drivers
	// ignore it.
	InjectedEnv map[string]string
}

// Condition is how "ready" a dependency must be before its dependent boots.
// It is groundwork for the future process engine (process-compose-style
// depends_on); today every dependency is waited for until Healthy.
type Condition string

const (
	// Healthy waits until the dependency accepts connections (the default, and
	// what every current engine needs). Reference-derived dependencies use it.
	Healthy Condition = "healthy"
	// Started requires only that the dependency's process has started. It is a
	// superset of nothing and a subset of Healthy, so the runtime currently
	// satisfies it by waiting for Healthy; a supervised process engine will later
	// use the weaker guarantee to start faster.
	Started Condition = "started"
)

// Dependency is one instance another instance must boot first, plus the
// readiness condition to wait for. The config reference graph produces these
// (every reference is a Healthy dependency); an explicit `depends_on` can add
// more or set conditions.
type Dependency struct {
	Name      string
	Condition Condition
}

// Dep is a resolved dependency the runtime hands to a dependent instance's
// driver: another instance that has been booted and is held running.
type Dep struct {
	Name      string // dependency instance name
	Engine    string // dependency engine type
	SocketDir string // dependency's backend socket directory
	Backend   string // dependency's backend socket path
	URL       string // direct backend connection URL (from BackendProvider)
}
