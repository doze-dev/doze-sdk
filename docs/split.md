# Splitting the SDK (plan M1)

The SDK does two jobs today: it is the kit a module author builds with, and it
holds pieces of doze itself (the lockfile, registry index selection, engine
binary downloads, the plugin host). The second job is why a change to how
doze caches binaries means a new SDK and a rebuild of every module. M1 gives
each job one home.

## Where things are today

Measured on 2026-11-03.

- `engine` (1.1k lines): the driver contract. Used by everyone.
- `plugin` (2.3k): both sides of the gRPC boundary. The host side (adapter,
  manager, host, resolvers) is used only by doze; the plugin side (server,
  `Main`) only by modules.
- `plugin/proto` (generated): the wire contract.
- `binaries` (1k): mirror manifest, downloads, the cache, and the lockfile.
  Used by doze, **and by every module's plugin process**: `server.go` builds a
  `binaries.Manager` on `$DOZE_HOME` and hands it to the driver's `Resolve`.
  The host's own fetcher is dropped at the boundary (`adapter.go`, `_
  engine.Fetcher`).
- `modindex` (0.3k): the signed registry index. doze and `modtool` only.
- `modtool`, `enginetest`, `console`: the author kit.

So each module carries its own copy of how doze downloads and caches engine
binaries: the mirror URL, the cache layout, signature checks. A change to any
of these means every published module must be rebuilt.

## The change

**The host fetches; a module only says what it needs.** `Resolve` keeps its
Go signature, so no driver changes. Its `Fetcher` stops being a local
`binaries.Manager` in the plugin process. It becomes a client for a fetch
service the host serves over go-plugin's broker:

- `ResolveRequest` gains `uint32 fetcher = 4`: the broker id of the host's
  `Fetcher` service (`ResolveMajor`, `Ensure`).
- The plugin server dials that id and passes the client to the driver's
  `Resolve`. Composite engines (ferret: documentdb and ferretdb) work
  unchanged: they call `Ensure` twice.
- A host that sends no `fetcher` is a doze from before the split. The driver
  then gets a Fetcher that answers every call with "this doze is older than
  the module and can't download engine binaries for it: upgrade doze". So a
  driver that downloads nothing (aws, kafka) works with any doze, and one
  that downloads is told why. Nothing is live yet (next-redesign D10/D14), so
  no fallback downloader stays in the SDK.
- The Fetcher is valid for the Resolve it came with: the host stops serving
  it when Resolve returns.

Then the pieces move:

| Piece | From | To |
|---|---|---|
| `binaries` (downloads, cache, mirror, lockfile) | doze-sdk | `doze/internal/binaries`, `doze/internal/lock` |
| `modindex` | doze-sdk | `doze/internal/registry` |
| plugin host side: `adapter.go`, `manager.go`, `host.go`, resolvers, reporter | doze-sdk `plugin` | `doze/internal/pluginhost` |
| `HostPlatform` | `binaries` | `engine` (enginetest needs it) |
| `ProtocolVersion` | `plugin` | `plugin/proto` (modtool needs only this) |

What stays in doze-sdk is the author kit:
- `engine`: the contract.
- `plugin`: plugin side only, meaning `Main`, `Serve`, the server and the
  wire.
- `plugin/proto`, as its own Go module (`doze-sdk/proto`), so the wire
  contract versions alone, with `buf breaking` on it.
- `console`, `enginetest`, `modtool`.

`modtool` needs `modindex` to write release indexes. It keeps a small copy of
the index types, or `modindex` stays in the SDK as the one shared piece
(the index format is a contract between modtool and doze, like the proto).
Recommended: keep `modindex` in the SDK, and move only selection policy
(`Select`) into doze.

## Order

1. SDK: add `HostPlatform` to `engine`, `ProtocolVersion` to the proto
   package, the fetch service and its broker client. Tag v0.10.0. Old hosts
   are refused with the upgrade message.
2. doze: serve the fetch service from the host adapter. Copy `binaries`, the
   lock, selection and the host side into `internal/`. Re-pin to v0.10.0, and
   drop the imports of the moved packages.
3. doze-modules: re-pin, rebuild, publish. No source changes are expected
   (drivers already call `ResolveVia(ctx, lk, fetch, …)`).
4. SDK: delete the moved packages. Split `proto` into its own module. Tag
   v0.11.0.
5. doze-aws and doze-kafka: re-pin when their in-flight branches land. They
   ignore `Locker` and `Fetcher` today, so the fetch change doesn't touch
   them; the deletion in step 4 does only if they import the deleted
   packages, which they don't.

## Checks

- An end-to-end test (`plugin/echo`): a plugin `Resolve` that calls `Ensure`
  goes through the broker to the host's Fetcher, and an old host is refused
  only when a download is attempted.
- By hand: postgres and ferret (composite) built against the new SDK boot
  under doze, and the downloads land in doze's cache. The published postgres
  0.2.4 (older SDK) still boots on the new host.
- doze CI's integration suite boots postgres and ferret (composite) through
  the new path.
- `go list -deps` test in doze-sdk: nothing in the kit imports a download or
  cache package.
- `buf breaking` stays green: field 4 is additive.
