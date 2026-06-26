//go:build darwin || linux

package plugin

import (
	"fmt"
	"net"
	"os"
	"syscall"
)

// sendConn passes an open connection's file descriptor across uc to the peer
// process via SCM_RIGHTS, alongside header as the accompanying data. The peer
// rebuilds an equivalent *os.File-backed connection; the descriptor is duplicated
// into the peer, so the sender still owns its own copy and must close it.
func sendConn(uc *net.UnixConn, header []byte, c syscall.Conn) error {
	raw, err := c.SyscallConn()
	if err != nil {
		return err
	}
	var sendErr error
	if cerr := raw.Control(func(fd uintptr) {
		rights := syscall.UnixRights(int(fd))
		_, _, sendErr = uc.WriteMsgUnix(header, rights, nil)
	}); cerr != nil {
		return cerr
	}
	return sendErr
}

// recvConn receives a file descriptor + header sent by sendConn and rebuilds it
// into a net.Conn. buf bounds the header size.
func recvConn(uc *net.UnixConn, maxHeader int) (net.Conn, []byte, error) {
	buf := make([]byte, maxHeader)
	oob := make([]byte, syscall.CmsgSpace(4)) // exactly one fd
	n, oobn, _, _, err := uc.ReadMsgUnix(buf, oob)
	if err != nil {
		return nil, nil, err
	}
	scms, err := syscall.ParseSocketControlMessage(oob[:oobn])
	if err != nil {
		return nil, nil, fmt.Errorf("parsing control message: %w", err)
	}
	if len(scms) == 0 {
		return nil, nil, fmt.Errorf("no file descriptor in handoff")
	}
	fds, err := syscall.ParseUnixRights(&scms[0])
	if err != nil || len(fds) == 0 {
		return nil, nil, fmt.Errorf("parsing fd rights: %w", err)
	}
	f := os.NewFile(uintptr(fds[0]), "wire-client")
	if f == nil {
		return nil, nil, fmt.Errorf("invalid file descriptor")
	}
	conn, err := net.FileConn(f)
	_ = f.Close() // FileConn dups; close our extra reference
	if err != nil {
		return nil, nil, fmt.Errorf("rebuilding connection from fd: %w", err)
	}
	return conn, buf[:n], nil
}
