package engine

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"syscall"
)

// ClearStaleLock refuses to double-start a running backend and clears a stale
// pid file left by a crash. lockPath is the engine's pid file; its first line
// must be the pid (true of postgres's postmaster.pid and mariadb's .pid file
// alike). what names the instance in the double-start error. A missing file is
// success. Callers remove any orphaned socket files themselves afterwards —
// socket layout is engine-specific.
func ClearStaleLock(what, lockPath string) error {
	raw, err := os.ReadFile(lockPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	lines := strings.SplitN(string(raw), "\n", 2)
	if pid, convErr := strconv.Atoi(strings.TrimSpace(lines[0])); convErr == nil && pid > 0 && ProcessAlive(pid) {
		return fmt.Errorf("%s appears to already be running (pid %d); remove %s if you are sure it is not", what, pid, lockPath)
	}
	if err := os.Remove(lockPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("removing stale lock: %w", err)
	}
	return nil
}

// ProcessAlive reports whether pid is a live process (signal 0 probe) — used
// to distinguish a stale pid file from a genuinely running backend.
func ProcessAlive(pid int) bool {
	p, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return p.Signal(syscall.Signal(0)) == nil
}
