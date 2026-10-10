package binaries

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// DownloadPin pins one downloaded URL to the checksum of what it served.
type DownloadPin struct {
	SHA256 string `yaml:"sha256"` // "sha256:<hex>"
}

// GetDownload returns the digest the lock pins a URL to.
func (l *Lock) GetDownload(url string) (string, bool) {
	if l == nil {
		return "", false
	}
	p, ok := l.Downloads[url]
	if !ok || p == nil || p.SHA256 == "" {
		return "", false
	}
	return p.SHA256, true
}

// RecordDownload pins a URL to a digest the first time it is fetched. A URL
// that is already pinned is never re-pinned here: a different digest is a
// mismatch for the caller to refuse, not a new truth.
func (l *Lock) RecordDownload(url, digest string) {
	if l == nil || digest == "" {
		return
	}
	if _, ok := l.GetDownload(url); ok {
		return
	}
	if l.Downloads == nil {
		l.Downloads = map[string]*DownloadPin{}
	}
	l.Downloads[url] = &DownloadPin{SHA256: digest}
	l.dirty = true
}

// EnsureDownload makes the archive or binary at a URL present in the cache and
// returns the directory a command should run in, and the archive's digest
// ("sha256:<hex>").
//
// It is for anything a stack wants to run that is not in the doze registry: a
// tar.gz, a zip, or a single executable. The cache is keyed by URL. The bytes
// are streamed to disk and hashed on the way, so nothing is held in memory,
// and nothing is unpacked unless the digest matches expectedSHA (the lock's
// pin; empty on first use, when the caller pins whatever arrived).
//
// A cache hit is checked against expectedSHA too: the archive is gone by then,
// so it is the digest recorded when it was unpacked that is compared. A lock
// that pins something else is an error, never satisfied by what is on disk.
//
// The returned directory is the archive's root: if the archive holds a single
// top-level directory (mongodb-macos-arm64-8.0.4/…) that directory, otherwise
// the extraction root. A bare executable is placed in a directory of its own.
func (m *Manager) EnsureDownload(ctx context.Context, rawURL, expectedSHA string) (dir, digest string, err error) {
	key := sha256.Sum256([]byte(rawURL))
	contentDir := filepath.Join(m.Home, "downloads", hex.EncodeToString(key[:8]))

	if have := readShaMeta(contentDir); have != "" {
		if expectedSHA != "" && have != expectedSHA {
			return "", "", fmt.Errorf("the download %s is cached as %s but doze.lock pins %s — it changed at the source, or the lock is wrong; remove %s to fetch it again",
				rawURL, have, expectedSHA, contentDir)
		}
		return archiveRoot(contentDir), have, nil
	}

	m.logf("downloading %s…", rawURL)
	if err := os.MkdirAll(filepath.Dir(contentDir), 0o755); err != nil {
		return "", "", err
	}
	tmp := contentDir + ".tmp"
	_ = os.RemoveAll(tmp)
	if err := os.MkdirAll(tmp, 0o755); err != nil {
		return "", "", err
	}
	defer os.RemoveAll(tmp)

	file := filepath.Join(tmp, ".download")
	digest, err = m.streamTo(ctx, rawURL, file)
	if err != nil {
		return "", "", err
	}
	if expectedSHA != "" && digest != expectedSHA {
		return "", "", fmt.Errorf("checksum mismatch for %s: doze.lock pins %s, the server sent %s — nothing was unpacked", rawURL, expectedSHA, digest)
	}

	body := filepath.Join(tmp, "root")
	if err := os.MkdirAll(body, 0o755); err != nil {
		return "", "", err
	}
	if err := unpack(file, body, rawURL); err != nil {
		return "", "", fmt.Errorf("unpacking %s: %w", rawURL, err)
	}
	_ = os.Remove(file)
	// The cache entry is the directory holding root/ and the digest record.
	writeShaMeta(tmp, digest)
	_ = os.RemoveAll(contentDir)
	if err := os.Rename(tmp, contentDir); err != nil {
		return "", "", err
	}
	m.logf("cached %s", rawURL)
	return archiveRoot(contentDir), digest, nil
}

// archiveRoot is where a command runs: the single top-level directory of the
// archive if there is exactly one, else the extraction root.
func archiveRoot(contentDir string) string {
	body := filepath.Join(contentDir, "root")
	entries, err := os.ReadDir(body)
	if err == nil && len(entries) == 1 && entries[0].IsDir() {
		return filepath.Join(body, entries[0].Name())
	}
	return body
}

// streamTo copies a URL to a file while hashing it, and returns the digest.
func (m *Manager) streamTo(ctx context.Context, rawURL, dest string) (string, error) {
	var src io.ReadCloser
	if p, ok := strings.CutPrefix(rawURL, "file://"); ok {
		f, err := os.Open(p)
		if err != nil {
			return "", err
		}
		src = f
	} else {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
		if err != nil {
			return "", err
		}
		resp, err := m.HTTP.Do(req)
		if err != nil {
			return "", fmt.Errorf("downloading %s: %w", rawURL, err)
		}
		if resp.StatusCode != http.StatusOK {
			resp.Body.Close()
			return "", fmt.Errorf("GET %s: %s", rawURL, resp.Status)
		}
		src = resp.Body
	}
	defer src.Close()
	out, err := os.Create(dest)
	if err != nil {
		return "", err
	}
	h := sha256.New()
	if _, err := io.Copy(io.MultiWriter(out, h), src); err != nil {
		out.Close()
		return "", fmt.Errorf("downloading %s: %w", rawURL, err)
	}
	if err := out.Close(); err != nil {
		return "", err
	}
	return "sha256:" + hex.EncodeToString(h.Sum(nil)), nil
}

// unpack extracts file into dest by what it is: gzip'd tar, zip, or — anything
// else — a single executable, named after the URL's last path segment.
func unpack(file, dest, rawURL string) error {
	f, err := os.Open(file)
	if err != nil {
		return err
	}
	defer f.Close()
	var magic [4]byte
	n, _ := io.ReadFull(f, magic[:])
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return err
	}
	switch {
	case n >= 2 && magic[0] == 0x1f && magic[1] == 0x8b:
		return untar(f, dest)
	case n >= 4 && magic[0] == 'P' && magic[1] == 'K' && magic[2] == 3 && magic[3] == 4:
		st, err := f.Stat()
		if err != nil {
			return err
		}
		return unzip(f, st.Size(), dest)
	}
	name := "download"
	if u, err := url.Parse(rawURL); err == nil && path.Base(u.Path) != "" && path.Base(u.Path) != "." && path.Base(u.Path) != "/" {
		name = path.Base(u.Path)
	}
	out, err := os.OpenFile(filepath.Join(dest, name), os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, f); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

// inside reports whether target is dest or below it.
func inside(dest, target string) bool {
	dest = filepath.Clean(dest)
	return target == dest || strings.HasPrefix(target, dest+string(os.PathSeparator))
}

func untar(r io.Reader, dest string) error {
	gz, err := gzip.NewReader(r)
	if err != nil {
		return err
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		target := filepath.Join(dest, hdr.Name)
		if !inside(dest, target) {
			return fmt.Errorf("unsafe path in archive: %q", hdr.Name)
		}
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := writeFile(target, tr, os.FileMode(hdr.Mode)&0o777); err != nil {
				return err
			}
		case tar.TypeSymlink:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			_ = os.Symlink(hdr.Linkname, target)
		}
	}
}

func unzip(r io.ReaderAt, size int64, dest string) error {
	zr, err := zip.NewReader(r, size)
	if err != nil {
		return err
	}
	for _, zf := range zr.File {
		target := filepath.Join(dest, zf.Name)
		if !inside(dest, target) {
			return fmt.Errorf("unsafe path in archive: %q", zf.Name)
		}
		if zf.FileInfo().IsDir() {
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
			continue
		}
		rc, err := zf.Open()
		if err != nil {
			return err
		}
		err = writeFile(target, rc, zf.Mode()&0o777)
		rc.Close()
		if err != nil {
			return err
		}
	}
	return nil
}

func writeFile(target string, r io.Reader, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	if mode == 0 {
		mode = 0o644
	}
	f, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	if _, err := io.Copy(f, r); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}
