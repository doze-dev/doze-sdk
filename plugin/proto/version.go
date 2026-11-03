package proto

// ProtocolVersion is the doze plugin protocol this wire contract is. It gates
// host↔plugin compatibility twice: at selection time (a module release's
// `protocol` in the signed registry index must equal the host's) and at launch
// (the go-plugin handshake). A wire-incompatible change bumps it, with the
// package name (PROTOCOL.md rule 5).
const ProtocolVersion = 1
