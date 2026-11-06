package proto

// ProtocolVersion is the doze plugin protocol this wire contract is. It gates
// host↔plugin compatibility twice: at selection time (a module release's
// `protocol` in the signed registry index must equal the host's) and at launch
// (the go-plugin handshake). A wire-incompatible change bumps it, with the
// package name (PROTOCOL.md rule 5).
const ProtocolVersion = 1

// MinHost is the oldest doze a plugin built with this SDK runs under. The
// protocol number says the wire is the same; this says which doze has what
// the SDK leans on inside it (since v0.10.0: doze fetches engine binaries for
// the module). It is stamped into each release in the registry index
// (releases.<v>.doze), so an older doze skips a release it cannot run
// instead of failing after picking it. Raise it with any such reliance.
const MinHost = "0.2.0"
