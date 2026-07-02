package engine

import (
	"bufio"
	"context"
	"crypto/tls"
	"io"
	"net"
	"time"

	"github.com/hashicorp/hcl/v2"
	"github.com/zclconf/go-cty/cty"
)

// SlowBooter is implemented by engines whose cold boot legitimately takes longer
// than the proxy's default client-boot budget — e.g. documentdb builds a Postgres
// cluster and runs CREATE EXTENSION … CASCADE on first boot. The proxy waits up to
// BootBudget (instead of its default) before giving up on a client-triggered boot.
// Once provisioned, later boots are quick and finish well within either bound.
type SlowBooter interface {
	BootBudget() time.Duration
}

// Driver is the minimal contract every database engine implements. The generic
// runtime depends only on these methods; richer behavior (convergence,
// protocol-aware proxying, copy-on-write templates) is discovered via the
// optional capability interfaces below using type assertions.
type Driver interface {
	// Type is the config block keyword and registry key, e.g. "postgres".
	Type() string

	// Resolve locates (or downloads) the toolchain for spec on plat, using fetch
	// to read the mirror and download archives, and recording the resolved pin
	// in lk. spec is normalized per engine.
	Resolve(ctx context.Context, spec VersionSpec, plat Platform, lk Locker, fetch Fetcher) (Toolchain, error)

	// Provision makes inst.DataDir ready to boot, running the engine's init step
	// if needed. It is idempotent.
	Provision(ctx context.Context, inst Instance, tc Toolchain) error

	// Provisioned reports whether dataDir already holds an initialized store.
	Provisioned(dataDir string) bool

	// BackendSocket returns the absolute path the proxy dials to reach a running
	// backend, given its socket directory and nominal port.
	BackendSocket(socketDir string, port int) string

	// ConnString builds the connection URL doze surfaces for an instance —
	// reported by `doze output` and resolvable as its <type>.<name>.url attribute —
	// pointed at the doze-owned endpoint. envVar is the conventional variable name
	// (DATABASE_URL, REDIS_URL, MONGODB_URI). doze does not inject it anywhere;
	// users reference it explicitly.
	ConnString(inst Instance, ep Endpoint) (envVar, url string)

	// A driver must also describe how to run: either Spawner (a declarative
	// SpawnPlan core executes and supervises — preferred, and what plugins use) or
	// LegacySpawner (the in-tree Spawn + WaitReady path). The runtime asserts for
	// these; a driver implementing neither cannot boot.
}

// RemoteDecoder is implemented by a plugin-backed driver: it decodes its own HCL
// block out-of-process. The config evaluator uses it instead of ConfigDecoder for
// plugin engines, handing over the source file, the block address, the flattened
// eval-context variables, and the instance's declared engine version; the result
// is an opaque spec only the plugin understands.
type RemoteDecoder interface {
	DecodeRemote(file []byte, blockType, blockLabel string, vars map[string]cty.Value, baseDir string, version VersionSpec) (any, error)
}

// LegacySpawner is the pre-SpawnPlan run path: the driver starts the backend
// itself and core supervises the returned Process. Preferred replacement is
// Spawner (see spawn.go). The runtime uses Spawner when present, else this.
type LegacySpawner interface {
	// Spawn starts the server bound to the instance's backend socket and returns
	// a running handle. It does not block on readiness.
	Spawn(ctx context.Context, inst Instance, tc Toolchain) (Process, error)
	// WaitReady blocks until the backend accepts connections, the process dies,
	// or ctx expires.
	WaitReady(ctx context.Context, inst Instance, tc Toolchain, p Process) error
}

// Fetcher resolves and downloads engine toolchains from the mirror. The
// binaries package implements it; the runtime passes one to Driver.Resolve.
// Defined here (not imported from binaries) to keep the dependency one-way.
type Fetcher interface {
	// ResolveMajor returns the full version the mirror maps a major to.
	ResolveMajor(engineType, major string) (full string, err error)
	// Ensure makes the toolchain for (engineType, full) present and returns its
	// bin dir and verified "sha256:<hex>" digest. expectedSHA, when non-empty
	// (from the lockfile), must match.
	Ensure(ctx context.Context, engineType, full string, plat Platform, expectedSHA string) (binDir, digest string, err error)
}

// Process is a running backend process. The generic supervisor implements it.
type Process interface {
	PID() int
	Alive() bool
	Logs() []string
	Stop(ctx context.Context) error
	Wait() error
}

// Converger is implemented by engines that converge to a declared structural
// spec (roles, databases, schemas, grants, extensions). The runtime calls it
// only on a freshly provisioned instance (and on explicit `doze apply`). Engines
// without structure (Valkey, Kvrocks) do not implement it.
type Converger interface {
	Converge(ctx context.Context, inst Instance, tc Toolchain, ep Endpoint) error
}

// Inventory is implemented by engines whose instances manage discrete structural
// objects, so `doze plan`/`apply`/`destroy` can track and diff them. Objects
// returns the objects the instance currently declares (derived from its config,
// no live query). Engines that implement Converger should usually implement this
// too; engines without structure (Valkey, Kvrocks) implement neither.
type Inventory interface {
	Objects(inst Instance) []Object
}

// Pruner is implemented by engines that can delete previously-applied objects no
// longer declared. The runtime calls Prune during apply (for objects removed from
// config) and destroy (for every applied object), passing the objects to drop.
type Pruner interface {
	Prune(ctx context.Context, inst Instance, tc Toolchain, ep Endpoint, removed []Object) error
}

// Lifecycle is implemented by engines whose instances are supervised, long-lived
// processes rather than lazy, idle-reaped backends — the model a future
// `process` engine (process-compose-style) will use. Supervised returns true to
// exempt an instance from the idle reaper and keep it running. Optional: engines
// without it are lazy (boot on connect, reap when idle), the default for every
// current engine.
type Lifecycle interface {
	Supervised(inst Instance) bool
}

// Hooked is implemented by supervised engines that run lifecycle commands around
// an instance's start and stop. The runtime calls PreStart after dependencies are
// up but before Spawn (e.g. run migrations), PostStart after the instance becomes
// ready, and PreStop before signalling the process. A non-nil error from PreStart
// aborts (and taints) the boot. Optional.
type Hooked interface {
	PreStart(ctx context.Context, inst Instance) error
	PostStart(ctx context.Context, inst Instance) error
	PreStop(ctx context.Context, inst Instance) error
}

// HealthChecker is implemented by supervised engines that expose an ongoing
// liveness probe, run periodically after readiness so the dashboard can show a
// health badge. It is distinct from WaitReady (the one-shot readiness gate during
// boot); the runtime does not auto-restart on a failed CheckHealth in v1. Optional.
type HealthChecker interface {
	CheckHealth(ctx context.Context, inst Instance) error
}

// RestartPolicy is one of the supported supervisor restart behaviors.
type RestartPolicy string

const (
	// RestartNo never restarts — the instance stays reaped after it exits.
	RestartNo RestartPolicy = "no"
	// RestartOnFailure restarts only when the process exits non-zero.
	RestartOnFailure RestartPolicy = "on_failure"
	// RestartAlways restarts on any exit (clean or not).
	RestartAlways RestartPolicy = "always"
)

// RestartSpec is what to do when a supervised process exits unexpectedly.
type RestartSpec struct {
	Policy     RestartPolicy
	Backoff    time.Duration // base delay; the runtime grows it exponentially, capped
	MaxRetries int           // 0 means no restart attempts
}

// Restartable is implemented by supervised engines that want the runtime to
// re-boot them after an unexpected exit, per RestartPolicy. Engines without it (or
// with RestartNo) stay reaped — today's behavior for crashed backends. Optional.
type Restartable interface {
	RestartPolicy(inst Instance) RestartSpec
}

// PortBinder is implemented by supervised engines that bind their own listening
// port instead of sitting behind a doze proxy. AdvertisedAddr returns the
// "host:port" the app listens on (derived from its decoded Spec); the runtime uses
// it as the instance's endpoint address and the daemon opens no proxy listener for
// it. ok is false when the port is not yet known. Optional.
type PortBinder interface {
	AdvertisedAddr(inst Instance) (addr string, ok bool)
}

// Attributer is implemented by engines that expose attributes beyond the generic
// baseline (name, engine, host, port, address, socket, url) under their
// <type>.<name> reference. The runtime/config merge these over the baseline when
// building the evaluation context, so config can reference e.g. postgres.x.owner
// or sqs.jobs.queues. Optional — engines without it expose only the baseline.
type Attributer interface {
	Attributes(inst Instance, ep Endpoint) map[string]cty.Value
}

// BackendProvider is implemented by engines that can serve as another instance's
// backend. BackendURL returns a URL a local process uses to connect directly to
// this instance's backend over its unix socket (not via the doze proxy).
type BackendProvider interface {
	BackendURL(inst Instance) string
}

// Versionless is implemented by engines that ship inside the doze binary and
// therefore have no selectable version (the local-AWS services). config does not
// require a `version` for their instances.
type Versionless interface {
	Versionless()
}

// ConfigDecoder is implemented by drivers that decode their own config block
// body into an EngineConfig. config calls it for each block whose keyword
// matches a registered driver. baseDir is the config file's directory, for
// resolving relative paths (e.g. extension source bundles). version is the
// instance's declared engine version (empty for versionless engines) so the
// driver can reject version-gated arguments at decode time — see RequireVersion.
type ConfigDecoder interface {
	DecodeConfig(body hcl.Body, ctx *hcl.EvalContext, baseDir string, version VersionSpec) (EngineConfig, error)
}

// Describer is implemented by drivers that publish their own catalog metadata —
// the config schema, an example, port, version labels, and a tagline — so the
// module registry's meta.yaml is generated from the driver (the single source of
// truth) rather than hand-authored and left to drift. The build tool (`dzm meta`)
// calls Describe and writes the result; a driver test asserts every HCL argument
// appears in the description. Optional.
type Describer interface {
	Describe() Description
}

// Description is the published catalog metadata for one engine module. Fields map
// onto the registry's meta.yaml shape.
type Description struct {
	Title        string      // human name, e.g. "PostgreSQL"
	Tagline      string      // one-line summary
	Category     string      // e.g. "database", "cache", "queue", "workflow"
	Description  string      // a paragraph
	Port         int         // the conventional/default client port
	Versions     []string    // selectable version labels, e.g. ["16","17","18"]
	Example      string        // a complete HCL block example
	ExampleLabel string        // the instance label used in Example
	Config       []ConfigArg   // one per top-level HCL argument the block accepts
	Blocks       []ConfigBlock // one per nested block type (role, bucket, queue, …)
	Homepage     string
	Source       string // the module source address, e.g. "doze/postgres"
}

// ConfigBlock documents one nested block type an engine block accepts, with its
// own argument table (rendered as a sub-section in the registry docs).
type ConfigBlock struct {
	Name  string      // the block keyword, e.g. "role"
	Label string      // what the block label means ("name", "role"), "" if unlabeled
	Desc  string      // one-line description
	Args  []ConfigArg // the block's arguments
}

// ConfigArg documents one HCL argument (or nested block) an engine block accepts.
type ConfigArg struct {
	Name     string // the HCL attribute/block name
	Type     string // "string" | "number" | "bool" | "map(string)" | "block" | …
	Default  string // rendered default, "" if none
	Desc     string // one-line description
	Required bool
	// Since/Until bound the engine MAJORS the argument applies to ("18" = added
	// in engine 18; both empty = all versions). Rendered as version badges in the
	// registry docs; the driver enforces the same bound in DecodeConfig (usually
	// via RequireVersion) so docs and validation share one source.
	Since string
	Until string
}

// Templater is implemented by engines that support copy-on-write data-dir
// templates: provision once into a shared template, then clone per instance for
// instant cold boots and disposable databases. The runtime owns templateDir
// (keyed by engine + resolved version); the driver owns how it is built and
// cloned. Optional — engines without it are provisioned directly.
type Templater interface {
	// EnsureTemplate provisions templateDir if it does not already exist.
	EnsureTemplate(ctx context.Context, tc Toolchain, templateDir string) error
	// CloneTemplate materializes destDir as a (copy-on-write where possible)
	// clone of templateDir.
	CloneTemplate(ctx context.Context, templateDir, destDir string) error
}

// ProxyFilter is implemented by engines whose wire protocol needs handling on
// the splice path: reading a startup preamble, terminating TLS, and routing
// out-of-band control messages (e.g. the Postgres CancelRequest dance). Engines
// without it get the pure accept -> boot -> count -> splice path.
type ProxyFilter interface {
	// Preamble processes the initial client bytes before routing. It may upgrade
	// the connection to TLS (per opts) and buffers any startup bytes to replay
	// to the backend. If the connection is a terminal out-of-band control
	// request (e.g. a cancel), the filter handles it fully using reg and returns
	// Handled=true, after which the proxy neither boots nor splices.
	Preamble(ctx context.Context, client net.Conn, reg CancelRegistry, opts ProxyOpts) (PreambleResult, error)

	// Handshake observes the backend->client startup exchange on the spliced
	// pair, optionally rewriting it (e.g. swapping the backend cancel key for a
	// synthetic one registered in reg). It returns whether the stream is ready
	// to splice and a cleanup func the proxy defers (e.g. to unregister the key).
	Handshake(client net.Conn, backend *bufio.Reader, backendSocket string, reg CancelRegistry) (ready bool, cleanup func(), err error)
}

// ErrorWriter is implemented by engines that can encode a protocol-level error
// message, so the proxy can report a boot/dial failure cleanly instead of just
// dropping the connection. Optional.
type ErrorWriter interface {
	WriteError(w io.Writer, code, message string)
}

// ProxyOpts carries the proxy's TLS policy to a ProxyFilter.
type ProxyOpts struct {
	TLS        *tls.Config
	RequireTLS bool
	LocalUnix  bool // client connected over a unix socket (TLS-exempt)
}

// PreambleResult is what a ProxyFilter.Preamble returns to the proxy.
type PreambleResult struct {
	Client  net.Conn // possibly TLS-upgraded; the proxy splices on this
	Replay  []byte   // bytes to write to the backend before splicing
	Handled bool     // terminal (e.g. cancel handled) -> do not boot/splice
}

// CancelTarget identifies a backend for out-of-band cancellation.
type CancelTarget struct {
	BackendSocket string
	Key           []byte // opaque engine-specific real key (PG: pid+secret)
}

// CancelRegistry maps generated synthetic keys to real backend targets. The
// proxy owns one and passes it to ProxyFilter methods; only the filter uses it.
type CancelRegistry interface {
	// Register stores target under a freshly generated synthetic key (returned).
	Register(target CancelTarget) (synthetic []byte)
	Unregister(synthetic []byte)
	Lookup(synthetic []byte) (CancelTarget, bool)
}
