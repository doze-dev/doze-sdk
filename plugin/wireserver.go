//go:build darwin || linux

package plugin

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"github.com/nerdmenot/doze-sdk/engine"
)

// maxWireHeader bounds the JSON control header that accompanies a handed-off fd
// (it carries the TLS material, so it must comfortably hold a cert + key in DER).
const maxWireHeader = 1 << 16

// wireHeader is the per-connection context core sends to the plugin alongside the
// client fd. It mirrors the host-side bits of engine.ProxyOpts that the plugin
// can't infer from the bare socket.
type wireHeader struct {
	RequireTLS bool   `json:"require_tls"`
	LocalUnix  bool   `json:"local_unix"`
	TLSCert    []byte `json:"tls_cert,omitempty"` // leaf cert DER
	TLSKey     []byte `json:"tls_key,omitempty"`  // PKCS#8 key DER
}

// The side-channel is line-oriented text after the fd handoff. Plugin→core:
// "boot" (need a backend), "handled" (terminal, e.g. cancel — no boot/splice),
// "splice" (about to splice; core should start accounting), "error <msg>".
// Core→plugin: "backend <socket>" or "bootfail <msg>".
const (
	msgBoot    = "boot"
	msgHandled = "handled"
	msgSplice  = "splice"
)

// wireServer accepts client-fd handoffs from core and runs the driver's
// ProxyFilter (preamble, backend dial, handshake, splice) in the plugin process.
// One server handles every connection for its engine, so its cancel registry —
// the synthetic→backend map a Handshake populates and a cancel Preamble reads —
// is shared and consistent across connections.
type wireServer struct {
	ln   net.Listener
	path string
	pf   engine.ProxyFilter
	reg  *localCancelRegistry
}

func startWireServer(pf engine.ProxyFilter) (*wireServer, error) {
	path := filepath.Join(os.TempDir(), "doze-wire-"+strconv.Itoa(os.Getpid())+".sock")
	_ = os.Remove(path)
	ln, err := net.Listen("unix", path)
	if err != nil {
		return nil, err
	}
	w := &wireServer{ln: ln, path: path, pf: pf, reg: newLocalCancelRegistry()}
	go w.serve()
	return w, nil
}

func (w *wireServer) addr() string { return w.path }

func (w *wireServer) serve() {
	for {
		c, err := w.ln.Accept()
		if err != nil {
			return
		}
		uc, ok := c.(*net.UnixConn)
		if !ok {
			_ = c.Close()
			continue
		}
		go w.handle(uc)
	}
}

func (w *wireServer) handle(side *net.UnixConn) {
	defer side.Close()
	client, hb, err := recvConn(side, maxWireHeader)
	if err != nil {
		return
	}
	defer client.Close()

	var hdr wireHeader
	_ = json.Unmarshal(hb, &hdr)
	opts := engine.ProxyOpts{RequireTLS: hdr.RequireTLS, LocalUnix: hdr.LocalUnix}
	if len(hdr.TLSCert) > 0 {
		opts.TLS = buildServerTLS(hdr.TLSCert, hdr.TLSKey)
	}

	res, err := w.pf.Preamble(context.Background(), client, w.reg, opts)
	if err != nil {
		fmt.Fprintf(side, "error %s\n", oneLine(err.Error()))
		return
	}
	if res.Handled {
		fmt.Fprintln(side, msgHandled)
		return
	}
	client = res.Client

	// Ask core to lazy-boot the backend and tell us its socket.
	sr := bufio.NewReader(side)
	fmt.Fprintln(side, msgBoot)
	line, err := sr.ReadString('\n')
	if err != nil {
		return
	}
	socket, ok := strings.CutPrefix(strings.TrimSpace(line), "backend ")
	if !ok {
		return // bootfail or unexpected
	}

	backend, err := net.Dial("unix", socket)
	if err != nil {
		return
	}
	defer backend.Close()
	if len(res.Replay) > 0 {
		if _, err := backend.Write(res.Replay); err != nil {
			return
		}
	}

	backendR := bufio.NewReader(backend)
	ready, cleanup, herr := w.pf.Handshake(client, backendR, socket, w.reg)
	if cleanup != nil {
		defer cleanup()
	}
	if herr != nil || !ready {
		return
	}

	fmt.Fprintln(side, msgSplice) // core: begin connection accounting
	spliceConns(client, backend, backendR)
}

// spliceConns copies bytes both ways until either side closes (mirrors the
// proxy's in-core splice; backend→client reads from backendR to keep handshake
// read-ahead).
func spliceConns(client, backend net.Conn, backendR io.Reader) {
	var once sync.Once
	closeBoth := func() {
		once.Do(func() { _ = client.Close(); _ = backend.Close() })
	}
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); _, _ = io.Copy(backend, client); closeBoth() }()
	go func() { defer wg.Done(); _, _ = io.Copy(client, backendR); closeBoth() }()
	wg.Wait()
}

func buildServerTLS(certDER, keyDER []byte) *tls.Config {
	key, err := x509.ParsePKCS8PrivateKey(keyDER)
	if err != nil {
		return nil
	}
	return &tls.Config{Certificates: []tls.Certificate{{
		Certificate: [][]byte{certDER},
		PrivateKey:  key,
	}}}
}

func oneLine(s string) string { return strings.ReplaceAll(s, "\n", " ") }

// localCancelRegistry is the plugin-process implementation of
// engine.CancelRegistry (identical to the proxy's): one instance per wireServer,
// shared across that engine's connections so cancel routing works out-of-process.
type localCancelRegistry struct {
	mu sync.Mutex
	m  map[string]engine.CancelTarget
}

func newLocalCancelRegistry() *localCancelRegistry {
	return &localCancelRegistry{m: map[string]engine.CancelTarget{}}
}

func (r *localCancelRegistry) Register(t engine.CancelTarget) []byte {
	r.mu.Lock()
	defer r.mu.Unlock()
	for {
		key := make([]byte, 8)
		_, _ = rand.Read(key)
		if key[0]|key[1]|key[2]|key[3] == 0 {
			continue
		}
		if _, exists := r.m[string(key)]; !exists {
			r.m[string(key)] = t
			return key
		}
	}
}

func (r *localCancelRegistry) Unregister(key []byte) {
	r.mu.Lock()
	delete(r.m, string(key))
	r.mu.Unlock()
}

func (r *localCancelRegistry) Lookup(key []byte) (engine.CancelTarget, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	t, ok := r.m[string(key)]
	return t, ok
}
