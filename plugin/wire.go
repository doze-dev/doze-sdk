//go:build darwin || linux

package plugin

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"strings"
	"syscall"

	"github.com/nerdmenot/doze-sdk/engine"
	"github.com/nerdmenot/doze-sdk/plugin/proto"
)

var _ engine.WireProxy = (*pluginDriver)(nil)

// Handoff implements engine.WireProxy. For a plugin that advertised a wire filter
// it dials the plugin's handoff socket, passes the client fd (SCM_RIGHTS), and
// mediates the small side-channel protocol — lazy-booting the backend on demand
// and accounting the spliced phase — while the plugin runs preamble/handshake/
// splice in its own process. A plugin without a wire filter returns
// engine.ErrNoWireProxy so the proxy uses its generic path.
func (d *pluginDriver) Handoff(ctx context.Context, client net.Conn, host engine.WireHost) error {
	if !d.has(capProxyFilter) {
		return engine.ErrNoWireProxy
	}
	addr := d.wireAddr(ctx)
	if addr == "" {
		return engine.ErrNoWireProxy
	}
	sc, ok := client.(syscall.Conn)
	if !ok {
		return engine.ErrNoWireProxy // can't pass this fd; fall back to generic splice
	}

	side, err := net.Dial("unix", addr)
	if err != nil {
		return fmt.Errorf("dialing wire socket: %w", err)
	}
	defer side.Close()
	uc := side.(*net.UnixConn)

	hdr, _ := json.Marshal(wireHeader{
		RequireTLS: host.RequireTLS, LocalUnix: host.LocalUnix,
		TLSCert: host.TLSCert, TLSKey: host.TLSKey,
	})
	if err := sendConn(uc, hdr, sc); err != nil {
		return fmt.Errorf("handing off client fd: %w", err)
	}

	sr := bufio.NewReader(uc)
	for {
		line, err := sr.ReadString('\n')
		if err != nil {
			if err == io.EOF {
				return nil // plugin closed before splicing (e.g. preamble dropped it)
			}
			return err
		}
		switch line = strings.TrimSpace(line); {
		case line == msgHandled:
			return nil // terminal (cancel/reject); no boot, no accounting
		case line == msgBoot:
			ep, berr := host.Boot(ctx)
			if berr != nil {
				fmt.Fprintf(uc, "bootfail %s\n", oneLine(berr.Error()))
				return berr
			}
			fmt.Fprintf(uc, "backend %s\n", ep.Backend)
		case line == msgSplice:
			host.Acquire()
			_, _ = io.Copy(io.Discard, sr) // block until the plugin finishes splicing
			host.Release()
			return nil
		case strings.HasPrefix(line, "error "):
			return fmt.Errorf("wire plugin: %s", strings.TrimPrefix(line, "error "))
		default:
			return fmt.Errorf("wire plugin: unexpected message %q", line)
		}
	}
}

// wireAddr resolves (once) the plugin's fd-handoff socket path. "" means the
// plugin runs no wire filter.
func (d *pluginDriver) wireAddr(ctx context.Context) string {
	d.wireOnce.Do(func() {
		resp, err := d.client.WireAddr(ctx, &proto.Empty{})
		if err == nil {
			d.wireSock = resp.Path
		}
	})
	return d.wireSock
}
