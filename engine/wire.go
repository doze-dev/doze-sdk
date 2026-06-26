package engine

import (
	"context"
	"errors"
	"net"
)

// ErrNoWireProxy is returned by WireProxy.Handoff when the driver does not
// actually run an out-of-process wire filter (e.g. a plugin that did not
// advertise one). The proxy treats it as "not a wire proxy" and falls back to its
// generic accept→boot→splice path, so a single always-present Handoff method can
// stand in for an optional capability without a type-assertion explosion.
var ErrNoWireProxy = errors.New("engine: driver has no wire proxy")

// WireProxy is implemented by a driver that runs its own protocol filter
// (PG-style TLS/startup/cancel) out-of-process — a plugin. Instead of the proxy
// calling ProxyFilter.Preamble/Handshake and splicing in-process, it hands the
// whole client connection to the driver via Handoff; the driver (a plugin)
// performs the preamble, dials the backend, and splices in its own process so no
// per-byte traffic crosses the host boundary. The host keeps lazy-boot and
// connection accounting by supplying them as callbacks on WireHost.
type WireProxy interface {
	// Handoff takes ownership of client for its full lifetime. It boots the
	// backend lazily via host.Boot only when the connection actually needs one
	// (a cancel request is served without booting), brackets the spliced phase
	// with host.Acquire/host.Release for idle accounting, and returns when the
	// connection is done. It must close client before returning. Returning
	// ErrNoWireProxy means "I am not a wire proxy" and asks the caller to fall
	// back to its generic path (client untouched).
	Handoff(ctx context.Context, client net.Conn, host WireHost) error
}

// WireHost gives a WireProxy the host-owned operations it can't do itself: lazily
// booting the backend and accounting a live connection for idle-reaping.
type WireHost struct {
	Boot       func(ctx context.Context) (Endpoint, error) // lazy-boot, returns the backend
	Acquire    func()                                      // a connection began splicing
	Release    func()                                      // a spliced connection ended
	RequireTLS bool                                        // reject plaintext TCP clients
	LocalUnix  bool                                        // client is on a unix socket (TLS-exempt)
	TLSCert    []byte                                      // server cert DER, if host TLS is configured
	TLSKey     []byte                                      // server key (PKCS#8 DER), paired with TLSCert
}
