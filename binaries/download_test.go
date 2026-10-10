package binaries

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func tgz(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for name, body := range files {
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o755, Size: int64(len(body)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		tw.Write([]byte(body))
	}
	tw.Close()
	gz.Close()
	return buf.Bytes()
}

func zipOf(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, body := range files {
		hdr := &zip.FileHeader{Name: name, Method: zip.Deflate}
		hdr.SetMode(0o755)
		w, err := zw.CreateHeader(hdr)
		if err != nil {
			t.Fatal(err)
		}
		w.Write([]byte(body))
	}
	zw.Close()
	return buf.Bytes()
}

func serve(t *testing.T, bodies map[string][]byte) (*httptest.Server, *int) {
	t.Helper()
	hits := new(int)
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*hits++
		b, ok := bodies[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Write(b)
	}))
	t.Cleanup(s.Close)
	return s, hits
}

func read(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestEnsureDownloadTarGzWithOneTopLevelDirectory(t *testing.T) {
	srv, hits := serve(t, map[string][]byte{"/mongo.tgz": tgz(t, map[string]string{"mongo-8.0/bin/mongod": "binary", "mongo-8.0/LICENSE": "x"})})
	m := NewManager(t.TempDir())
	dir, digest, err := m.EnsureDownload(context.Background(), srv.URL+"/mongo.tgz", "")
	if err != nil {
		t.Fatal(err)
	}
	// The run directory is the archive's own root, so `bin/mongod` resolves.
	if got := read(t, filepath.Join(dir, "bin", "mongod")); got != "binary" {
		t.Fatalf("bin/mongod = %q", got)
	}
	if !strings.HasPrefix(digest, "sha256:") {
		t.Fatalf("digest = %q", digest)
	}
	// A second call is a cache hit with the same digest, and no request.
	before := *hits
	dir2, digest2, err := m.EnsureDownload(context.Background(), srv.URL+"/mongo.tgz", digest)
	if err != nil || dir2 != dir || digest2 != digest || *hits != before {
		t.Fatalf("second call = %q, %q, %v, hits %d→%d", dir2, digest2, err, before, *hits)
	}
}

func TestEnsureDownloadZipAndBareBinary(t *testing.T) {
	srv, _ := serve(t, map[string][]byte{
		"/tool.zip": zipOf(t, map[string]string{"tool": "z", "README": "r"}),
		"/mailpit":  []byte("#!/bin/sh\necho hi\n"),
	})
	m := NewManager(t.TempDir())
	dir, _, err := m.EnsureDownload(context.Background(), srv.URL+"/tool.zip", "")
	if err != nil {
		t.Fatal(err)
	}
	if got := read(t, filepath.Join(dir, "tool")); got != "z" {
		t.Fatalf("zip: tool = %q", got)
	}
	if fi, err := os.Stat(filepath.Join(dir, "tool")); err != nil || fi.Mode()&0o100 == 0 {
		t.Fatalf("zip: the executable bit was lost: %v %v", fi, err)
	}

	dir, _, err = m.EnsureDownload(context.Background(), srv.URL+"/mailpit", "")
	if err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(filepath.Join(dir, "mailpit"))
	if err != nil || fi.Mode()&0o100 == 0 {
		t.Fatalf("a bare binary should be executable, named after the URL: %v %v", fi, err)
	}
}

// lifecycle.md measure: serve a different archive at the same URL and the wake
// fails with the hash mismatch — from the cache, and from the network.
func TestAChangedDownloadIsRefused(t *testing.T) {
	bodies := map[string][]byte{"/tool.tgz": tgz(t, map[string]string{"bin/tool": "v1"})}
	srv, _ := serve(t, bodies)
	url := srv.URL + "/tool.tgz"

	m := NewManager(t.TempDir())
	_, pinned, err := m.EnsureDownload(context.Background(), url, "")
	if err != nil {
		t.Fatal(err)
	}

	// A new machine, an empty cache, and the upstream archive replaced: the
	// lock's pin refuses it before anything is unpacked.
	bodies["/tool.tgz"] = tgz(t, map[string]string{"bin/tool": "v2 — tampered"})
	fresh := NewManager(t.TempDir())
	_, _, err = fresh.EnsureDownload(context.Background(), url, pinned)
	if err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
		t.Fatalf("a changed download on a cold cache = %v, want a checksum mismatch", err)
	}
	if entries, _ := os.ReadDir(filepath.Join(fresh.Home, "downloads")); len(entries) != 0 {
		t.Fatalf("the refused archive was left in the cache: %v", entries)
	}

	// A warm cache that holds something other than the pin is refused as well.
	_, _, err = m.EnsureDownload(context.Background(), url, "sha256:0000")
	if err == nil || !strings.Contains(err.Error(), "pins") {
		t.Fatalf("a cache that disagrees with the lock = %v", err)
	}
}

func TestADownloadCannotEscapeItsDirectory(t *testing.T) {
	srv, _ := serve(t, map[string][]byte{"/evil.tgz": tgz(t, map[string]string{"../../escaped": "x"})})
	m := NewManager(t.TempDir())
	_, _, err := m.EnsureDownload(context.Background(), srv.URL+"/evil.tgz", "")
	if err == nil || !strings.Contains(err.Error(), "unsafe path") {
		t.Fatalf("a path-escaping archive = %v", err)
	}
}

func TestTheLockPinsADownloadOnce(t *testing.T) {
	path := filepath.Join(t.TempDir(), "doze.lock")
	l, err := LoadLock(path)
	if err != nil {
		t.Fatal(err)
	}
	l.RecordDownload("https://example.com/a.tgz", "sha256:aaa")
	l.RecordDownload("https://example.com/a.tgz", "sha256:bbb") // never re-pinned
	if err := l.Save(); err != nil {
		t.Fatal(err)
	}
	raw := read(t, path)
	if !strings.Contains(raw, "downloads:") || !strings.Contains(raw, "sha256:aaa") || strings.Contains(raw, "bbb") {
		t.Fatalf("doze.lock:\n%s", raw)
	}
	l2, err := LoadLock(path)
	if err != nil {
		t.Fatal(err)
	}
	if got, ok := l2.GetDownload("https://example.com/a.tgz"); !ok || got != "sha256:aaa" {
		t.Fatalf("reloaded pin = %q, %v", got, ok)
	}
}
