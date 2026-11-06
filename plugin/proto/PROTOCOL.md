# The plugin protocol

`engine.proto` is the one contract between doze and a module. Every published
module is a binary compiled against some version of this file, and it keeps
running for years after it was built. So the file changes under rules that
keep old modules working with new doze, and new modules working with old doze.

## The rules

1. **Add; never change.**
   - Never renumber, retype, rename or remove a field.
   - Never reuse a field number: mark a retired one `reserved`.
   - Never change an RPC's request or response type.
   - `buf breaking` enforces this in CI against the last release.
2. **A new field is optional on both sides.**
   - An old peer sends its zero value: `0`, `""`, empty. Choose fields so that
     zero means "the old behaviour", e.g. `boot_budget_ms = 0` keeps the host's
     default.
   - An old peer also ignores a field it doesn't know.
   - A failed RPC has only its status message on the wire, so structure for an
     error rides as a status detail, and the message keeps the text it always
     had: a failed DecodeConfig attaches `DecodeDiagnostics`, which an old
     host never unpacks.
3. **A new behaviour is a new capability.**
   - A module advertises what it implements in `Capabilities`, and the host
     calls an optional RPC only if the module advertised it.
   - A host that doesn't know a capability never calls its RPC. A module that
     doesn't implement one never advertises it.
   - This is how the protocol grows without a version bump.
4. **A failure is an error, never a silent default.**
   - An advertised capability whose RPC fails is an error naming the module,
     or, where the Go interface has no error to return, goes to the host's
     `Reporter` with the module's name and the call.
   - A module that can't say its type or capabilities fails its launch.
   - A missing capability is the only "it does nothing" case.
5. **The version number moves only for a change rules 1–3 cannot express.**
   - `ProtocolVersion` (plugin.go) and the package name `dozeplugin.v1` change
     together, and only together.
   - The handshake offers several versions (go-plugin's `VersionedPlugins`,
     `plugin.Protocols`): a host offers every version it supports, a module
     built with a newer SDK serves the older one too, and they agree on the
     newest both know, so a bump never strands published modules. A module
     built before this (it offers only its own version) still handshakes with
     a host that lists that version. Today there is one version, 1.
   - The previous version stays supported for at least two doze minor releases
     or six months, whichever is longer.

## Regenerating

```sh
go install google.golang.org/protobuf/cmd/protoc-gen-go@v1.36.11
go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@v1.6.2
go generate ./plugin/proto
```

The generator versions are pinned in `buf.gen.yaml`. Regenerating an unchanged
`engine.proto` must reproduce the checked-in files byte for byte.
