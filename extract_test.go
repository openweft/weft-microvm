package microvm

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// makeTarGz builds a tiny in-memory tar.gz with the given entries.
// `entries` is a list of (header, body) — body may be nil for non-
// regular types.
func makeTarGz(t *testing.T, entries ...struct {
	hdr  tar.Header
	body []byte
}) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, e := range entries {
		h := e.hdr
		if h.Typeflag == tar.TypeReg {
			h.Size = int64(len(e.body))
		}
		if err := tw.WriteHeader(&h); err != nil {
			t.Fatalf("writeHeader %q: %v", h.Name, err)
		}
		if h.Typeflag == tar.TypeReg && len(e.body) > 0 {
			if _, err := tw.Write(e.body); err != nil {
				t.Fatalf("write body for %q: %v", h.Name, err)
			}
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestExtractLayer_BasicFilesDirsSymlinks(t *testing.T) {
	dest := t.TempDir()
	pkg := makeTarGz(t,
		struct {
			hdr  tar.Header
			body []byte
		}{
			tar.Header{Name: "etc/", Typeflag: tar.TypeDir, Mode: 0o755}, nil,
		},
		struct {
			hdr  tar.Header
			body []byte
		}{
			tar.Header{Name: "etc/hostname", Typeflag: tar.TypeReg, Mode: 0o644}, []byte("weft-microvm-host\n"),
		},
		struct {
			hdr  tar.Header
			body []byte
		}{
			tar.Header{Name: "bin/sh", Typeflag: tar.TypeReg, Mode: 0o755}, []byte("#!/bin/sh\n"),
		},
		struct {
			hdr  tar.Header
			body []byte
		}{
			tar.Header{Name: "bin/ash", Typeflag: tar.TypeSymlink, Linkname: "sh"}, nil,
		},
	)
	if err := extractLayer(bytes.NewReader(pkg), dest); err != nil {
		t.Fatalf("extractLayer: %v", err)
	}
	if b, err := os.ReadFile(filepath.Join(dest, "etc/hostname")); err != nil || string(b) != "weft-microvm-host\n" {
		t.Errorf("etc/hostname: err=%v body=%q", err, b)
	}
	if fi, err := os.Lstat(filepath.Join(dest, "bin/ash")); err != nil || fi.Mode()&os.ModeSymlink == 0 {
		t.Errorf("bin/ash symlink missing: err=%v mode=%v", err, fi.Mode())
	}
}

func TestExtractLayer_PerFileWhiteout(t *testing.T) {
	dest := t.TempDir()
	// Layer A: writes /etc/secret
	a := makeTarGz(t, struct {
		hdr  tar.Header
		body []byte
	}{
		tar.Header{Name: "etc/secret", Typeflag: tar.TypeReg, Mode: 0o600}, []byte("hush"),
	})
	if err := extractLayer(bytes.NewReader(a), dest); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dest, "etc/secret")); err != nil {
		t.Fatal("setup: layer A's file missing")
	}

	// Layer B: whiteout marker for /etc/secret
	b := makeTarGz(t, struct {
		hdr  tar.Header
		body []byte
	}{
		tar.Header{Name: "etc/.wh.secret", Typeflag: tar.TypeReg, Mode: 0o644}, nil,
	})
	if err := extractLayer(bytes.NewReader(b), dest); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dest, "etc/secret")); !os.IsNotExist(err) {
		t.Errorf("etc/secret should be gone after whiteout; stat err=%v", err)
	}
	if _, err := os.Stat(filepath.Join(dest, "etc/.wh.secret")); !os.IsNotExist(err) {
		t.Errorf("whiteout marker should NOT be materialised; stat err=%v", err)
	}
}

func TestExtractLayer_OpaqueWhiteout(t *testing.T) {
	dest := t.TempDir()
	// Layer A: writes /opt/{a,b,c}
	a := makeTarGz(t,
		struct {
			hdr  tar.Header
			body []byte
		}{tar.Header{Name: "opt/", Typeflag: tar.TypeDir, Mode: 0o755}, nil},
		struct {
			hdr  tar.Header
			body []byte
		}{tar.Header{Name: "opt/a", Typeflag: tar.TypeReg, Mode: 0o644}, []byte("a")},
		struct {
			hdr  tar.Header
			body []byte
		}{tar.Header{Name: "opt/b", Typeflag: tar.TypeReg, Mode: 0o644}, []byte("b")},
		struct {
			hdr  tar.Header
			body []byte
		}{tar.Header{Name: "opt/c", Typeflag: tar.TypeReg, Mode: 0o644}, []byte("c")},
	)
	if err := extractLayer(bytes.NewReader(a), dest); err != nil {
		t.Fatal(err)
	}

	// Layer B: opaque-dir whiteout under /opt + a new entry /opt/d
	b := makeTarGz(t,
		struct {
			hdr  tar.Header
			body []byte
		}{tar.Header{Name: "opt/.wh..wh..opq", Typeflag: tar.TypeReg, Mode: 0o644}, nil},
		struct {
			hdr  tar.Header
			body []byte
		}{tar.Header{Name: "opt/d", Typeflag: tar.TypeReg, Mode: 0o644}, []byte("d")},
	)
	if err := extractLayer(bytes.NewReader(b), dest); err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{"opt/a", "opt/b", "opt/c"} {
		if _, err := os.Stat(filepath.Join(dest, n)); !os.IsNotExist(err) {
			t.Errorf("%s should be wiped by opaque whiteout; err=%v", n, err)
		}
	}
	if b, err := os.ReadFile(filepath.Join(dest, "opt/d")); err != nil || string(b) != "d" {
		t.Errorf("opt/d should remain after the whiteout: err=%v body=%q", err, b)
	}
}

func TestSafeJoin_RejectsEscape(t *testing.T) {
	if _, err := safeJoin("/tmp/dest", "../../etc/passwd"); err == nil {
		t.Errorf("safeJoin should reject ../../etc/passwd")
	}
	if _, err := safeJoin("/tmp/dest", "/etc/passwd"); err != nil {
		// Absolute paths get the leading slash stripped, so they
		// become relative inside dest — that's fine.
		t.Errorf("safeJoin should accept absolute (rewritten to relative): %v", err)
	}
}

// silence unused-import linter complaint if errors package is dropped later.
var _ = errors.New
