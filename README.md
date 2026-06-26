# doze-sdk

The contract for building **doze engines**. doze core is a thin host — config /
plan-apply / a supervisor / a proxy with lazy-boot + idle-reap — and every backing
engine (postgres, valkey, s3, …) is an out-of-process plugin that implements this
SDK. Your engine is no different from the built-in ones: there is no privileged
path, so anyone can write one.

```
go get github.com/nerdmenot/doze-sdk
```

## Three packages

| Package | What it is |
|---|---|
| `engine` | The stable **contract**: `engine.Driver` + capability interfaces, and the value types (`Instance`, `Toolchain`, `SpawnPlan`, `Ready`, `Endpoint`, `Pin`, …). |
| `plugin` | The go-plugin/gRPC **protocol** + `dozeplugin.Serve(drv)`, the one call your `main` makes. The host side (launch/adapt) lives here too; you don't touch it. |
| `binaries` | A download / checksum-verify / content-addressed cache library, if your engine fetches a backing binary (Postgres, valkey-server, …) in `Resolve`. |

## Write an engine

A driver is a stateless value implementing `engine.Driver` plus the capabilities
you need. The minimum: identify the engine, decode its config, and say how to run
it. **[`example/httpd.go`](example/httpd.go)** is a complete, runnable engine
(~150 commented lines) that serves a directory of static files — copy it.

```go
type Driver struct{}

func (Driver) Type() string { return "myengine" }                 // the block keyword

func (Driver) DecodeConfig(body hcl.Body, ctx *hcl.EvalContext, baseDir string) (engine.EngineConfig, error) {
	var c Config; return &c, gohclDecode(body, ctx, &c)           // its `myengine { … }` block
}

func (Driver) Plan(_ context.Context, inst engine.Instance, tc engine.Toolchain) (engine.SpawnPlan, error) {
	return engine.SpawnPlan{Specs: []engine.SpawnSpec{{           // how to run it; core supervises
		Name: inst.Name, Bin: tc.Path("myserver"), Args: []string{"--socket", sock},
		Ready: &engine.Ready{Kind: "socket", Target: sock},
	}}}, nil
}
// + Resolve, Provision, Provisioned, BackendSocket, ConnString (see the contract)

func main() {
	gob.Register(&Config{})        // the config crosses the wire as gob
	dozeplugin.Serve(Driver{})
}
```

You return a **`SpawnPlan`** (declarative: bins, args, env, readiness, ordering,
between-step hooks) and core executes + supervises it — so your engine gets the
hardened restart / reap / log handling for free. Composite engines (e.g.
documentdb = Postgres → `CREATE EXTENSION` → FerretDB) are just a multi-spec plan.

### Capabilities (opt-in, discovered by type assertion)

Implement only what you need; core checks for each interface.

| Interface | Adds |
|---|---|
| `Spawner` *(or `LegacySpawner`)* | how to run — a `SpawnPlan` (preferred) |
| `ConfigDecoder` | decode your HCL block into a config value |
| `Versionless` | engine has no `version =` (ships its own binary) |
| `Converger` / `Inventory` / `Pruner` | declarative resources (roles, buckets, queues) for `plan`/`apply`/`destroy` |
| `Templater` | copy-on-write template clone for fast first boot |
| `ProxyFilter` | own wire protocol — TLS termination, startup parse, cancel routing (runs in your process via an fd hand-off) |
| `Lifecycle` / `Hooked` / `HealthChecker` / `Restartable` / `PortBinder` / `Attributer` | supervised long-lived processes (the `process` engine uses these) |
| `BackendProvider` / `SlowBooter` / `ErrorWriter` | misc. hooks |

## How doze runs your engine

doze resolves a config block's type to a driver. For your own engine, two ways:

```sh
# Local / private — point doze straight at your binary. No registry involved.
go build -o /tmp/myengine-plugin .
DOZE_MYENGINE_PLUGIN=/tmp/myengine-plugin doze up

# Distributed — host the binary in a mirror (index.yaml + per-platform archives,
# the doze-modules layout) and point doze at it:
DOZE_MODULES_MIRROR=https://your-host/modules doze up
#   …or commit it to a project:  modules { mirror = "https://your-host/modules" }
```

doze fetches the binary for the host platform, verifies its checksum, caches it
under `~/.doze/modules`, pins it in `doze.lock`, and launches it. The official
engines live in [`doze-modules`](https://github.com/NerdMeNot/doze-modules);
adding one there is a PR. There is no other privileged registry — third-party
engines distribute via the mirror override.

## Versioning & compatibility

This SDK is **pre-1.0** (`v0.x`): the contract may still change between minor
versions — pin a version and read the release notes when you bump.

- **Module path / SemVer.** Import `github.com/nerdmenot/doze-sdk` and pin a tag
  (`go get github.com/nerdmenot/doze-sdk@v0.1.0`). Per Go SemVer, `v0.x` makes no
  compatibility promise; `v1`+ will (with a `/vN` path for any future major).
- **Wire handshake.** Host↔plugin compatibility is gated by
  `plugin.Handshake.ProtocolVersion` (currently `1`). A wire-incompatible protocol
  change bumps it, so a mismatched host and plugin fail fast at launch rather than
  misbehaving. Build your engine against the SDK version the host you target uses.
- **Capabilities are additive.** New capability interfaces are opt-in; not
  implementing one is always valid.

## Notes

- Engine config crosses the boundary as **gob** — `gob.Register(&Config{})` in
  `main`, and keep the config to plain exported types (no interfaces/funcs).
- A driver is **stateless**: one value serves every instance; per-instance data
  arrives in the `engine.Instance` passed to each method.
- doze injects **no** environment into your processes — declare what they need (a
  `process` engine's `env { }` block can reference peers, e.g.
  `DATABASE_URL = postgres.app.url`).
