package engine

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
)

// EnsureTemplateDir builds a shared provision template once, atomically, so
// per-instance cold boots can clone it instead of paying the engine's init
// (initdb, mariadb-install-db, …) each time. ready reports whether a directory
// already holds a usable template; init builds one into the directory it is
// given. The build happens in a unique temp dir that is atomically renamed
// into place, so a concurrent boot of another instance can never observe a
// half-built template — whichever boot loses the rename race just uses the
// winner's.
func EnsureTemplateDir(ctx context.Context, templateDir string, ready func(dir string) bool, init func(ctx context.Context, dir string) error) error {
	if ready(templateDir) {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(templateDir), 0o700); err != nil {
		return err
	}
	tmp, err := os.MkdirTemp(filepath.Dir(templateDir), "_tmpl-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp) // no-op once renamed away

	if err := init(ctx, tmp); err != nil {
		return err
	}
	if ready(templateDir) {
		return nil // another boot won the race; our tmp is cleaned up by defer
	}
	if err := os.Rename(tmp, templateDir); err != nil {
		if ready(templateDir) {
			return nil // lost the race between the check and the rename
		}
		return fmt.Errorf("installing template: %w", err)
	}
	return nil
}

// CloneTemplateDir clones templateDir into destDir, copy-on-write where the
// filesystem supports it (APFS/btrfs/XFS), else a plain recursive copy. Any
// existing destDir is replaced.
func CloneTemplateDir(ctx context.Context, templateDir, destDir string) error {
	if err := os.MkdirAll(filepath.Dir(destDir), 0o700); err != nil {
		return err
	}
	_ = os.RemoveAll(destDir) // cp creates destDir fresh

	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		// -c clones (clonefile / CoW) on APFS, falling back to a copy elsewhere.
		cmd = exec.CommandContext(ctx, "cp", "-Rc", templateDir, destDir)
	default:
		cmd = exec.CommandContext(ctx, "cp", "-a", "--reflink=auto", templateDir, destDir)
	}
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("cloning template into %s: %w\n%s", destDir, err, out)
	}
	return nil
}
