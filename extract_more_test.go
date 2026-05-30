package microvm

import (
	"archive/tar"
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeTar builds an uncompressed tar with the given entries, for
// driving extractTar directly (the no-gzip registry path).
func writeTar(t *testing.T, entries ...struct {
	hdr  tar.Header
	body []byte
}) []byte {
	t.Helper()
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	for _, e := range entries {
		h := e.hdr
		if h.Typeflag == tar.TypeReg {
			h.Size = int64(len(e.body))
		}
		if err := tw.WriteHeader(&h); err != nil {
			t.Fatal(err)
		}
		if h.Typeflag == tar.TypeReg && len(e.body) > 0 {
			if _, err := tw.Write(e.body); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

type tarEntry = struct {
	hdr  tar.Header
	body []byte
}

func TestExtractTar_PlainHardlinkAndDevices(t *testing.T) {
	dest := t.TempDir()
	raw := writeTar(t,
		tarEntry{tar.Header{Name: "bin/", Typeflag: tar.TypeDir, Mode: 0o755}, nil},
		tarEntry{tar.Header{Name: "bin/orig", Typeflag: tar.TypeReg, Mode: 0o644}, []byte("contents")},
		// Hardlink to bin/orig.
		tarEntry{tar.Header{Name: "bin/hardlink", Typeflag: tar.TypeLink, Linkname: "bin/orig"}, nil},
		// Device + fifo entries are skipped (no-op).
		tarEntry{tar.Header{Name: "dev/null", Typeflag: tar.TypeChar, Mode: 0o666}, nil},
		tarEntry{tar.Header{Name: "dev/sda", Typeflag: tar.TypeBlock, Mode: 0o660}, nil},
		tarEntry{tar.Header{Name: "run/fifo", Typeflag: tar.TypeFifo, Mode: 0o644}, nil},
		// A tar type we don't materialise (xattr/GNU header) hits default.
		tarEntry{tar.Header{Name: "pax_global_header", Typeflag: tar.TypeXGlobalHeader}, nil},
	)
	if err := extractTar(bytes.NewReader(raw), dest); err != nil {
		t.Fatalf("extractTar: %v", err)
	}
	// Hardlink shares the original's contents.
	b, err := os.ReadFile(filepath.Join(dest, "bin/hardlink"))
	if err != nil || string(b) != "contents" {
		t.Errorf("hardlink: err=%v body=%q", err, b)
	}
	// Device nodes were skipped.
	if _, err := os.Stat(filepath.Join(dest, "dev/null")); !os.IsNotExist(err) {
		t.Errorf("char device should be skipped, stat err=%v", err)
	}
}

func TestExtractTar_SymlinkOverNonEmptyDir(t *testing.T) {
	dest := t.TempDir()
	// A non-empty directory "d" then a symlink also named "d": os.Remove
	// can't remove the non-empty dir, so os.Symlink fails (path exists).
	raw := writeTar(t,
		tarEntry{tar.Header{Name: "d/", Typeflag: tar.TypeDir, Mode: 0o755}, nil},
		tarEntry{tar.Header{Name: "d/keep", Typeflag: tar.TypeReg, Mode: 0o644}, []byte("k")},
		tarEntry{tar.Header{Name: "d", Typeflag: tar.TypeSymlink, Linkname: "target"}, nil},
	)
	if err := extractTar(bytes.NewReader(raw), dest); err == nil {
		t.Fatal("expected symlink-over-nonempty-dir error")
	}
}

func TestExtractTar_HardlinkMissingTarget(t *testing.T) {
	dest := t.TempDir()
	// Hardlink whose target was never extracted → os.Link fails.
	raw := writeTar(t,
		tarEntry{tar.Header{Name: "link", Typeflag: tar.TypeLink, Linkname: "nonexistent-target"}, nil},
	)
	if err := extractTar(bytes.NewReader(raw), dest); err == nil {
		t.Fatal("expected hardlink-missing-target error")
	}
}

func TestExtractTar_RejectsEscapingEntry(t *testing.T) {
	dest := t.TempDir()
	raw := writeTar(t,
		tarEntry{tar.Header{Name: "../escape", Typeflag: tar.TypeReg, Mode: 0o644}, []byte("x")},
	)
	err := extractTar(bytes.NewReader(raw), dest)
	if err == nil || !strings.Contains(err.Error(), "escapes destination") {
		t.Fatalf("expected escape rejection, got %v", err)
	}
}

func TestExtractTar_HardlinkEscapingLinkname(t *testing.T) {
	dest := t.TempDir()
	raw := writeTar(t,
		tarEntry{tar.Header{Name: "ok", Typeflag: tar.TypeLink, Linkname: "../../etc/passwd"}, nil},
	)
	err := extractTar(bytes.NewReader(raw), dest)
	if err == nil || !strings.Contains(err.Error(), "escapes destination") {
		t.Fatalf("expected hardlink linkname escape rejection, got %v", err)
	}
}

func TestExtractLayer_NotGzip(t *testing.T) {
	dest := t.TempDir()
	if err := extractLayer(bytes.NewReader([]byte("definitely not gzip")), dest); err == nil ||
		!strings.Contains(err.Error(), "gzip open") {
		t.Fatalf("expected gzip open error, got %v", err)
	}
}

func TestExtractTar_RegFileOpenError(t *testing.T) {
	dest := t.TempDir()
	// First a directory "x/", then a regular file also named "x":
	// OpenFile("x") fails because the path is an existing directory.
	raw := writeTar(t,
		tarEntry{tar.Header{Name: "x/", Typeflag: tar.TypeDir, Mode: 0o755}, nil},
		tarEntry{tar.Header{Name: "x", Typeflag: tar.TypeReg, Mode: 0o644}, []byte("clash")},
	)
	if err := extractTar(bytes.NewReader(raw), dest); err == nil {
		t.Fatal("expected error opening a regular file over an existing dir")
	}
}

func TestExtractTar_MkdirParentError(t *testing.T) {
	dest := t.TempDir()
	// "a" is a regular file; then "a/b" needs MkdirAll("a") which fails
	// because "a" already exists as a file.
	raw := writeTar(t,
		tarEntry{tar.Header{Name: "a", Typeflag: tar.TypeReg, Mode: 0o644}, []byte("file")},
		tarEntry{tar.Header{Name: "a/b", Typeflag: tar.TypeReg, Mode: 0o644}, []byte("nested")},
	)
	if err := extractTar(bytes.NewReader(raw), dest); err == nil {
		t.Fatal("expected error creating dir over an existing file")
	}
}

func TestExtractTar_TruncatedStream(t *testing.T) {
	// A header that claims a body longer than what follows triggers
	// tr.Next()'s error path on the *next* iteration.
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	_ = tw.WriteHeader(&tar.Header{Name: "f", Typeflag: tar.TypeReg, Mode: 0o644, Size: 100})
	_, _ = tw.Write([]byte("short")) // fewer than 100 bytes; don't close cleanly
	// Intentionally do NOT call tw.Close() so the archive is truncated.
	raw := buf.Bytes()
	if err := extractTar(bytes.NewReader(raw), t.TempDir()); err == nil {
		t.Fatal("expected error on truncated tar")
	}
}

func TestExtractTar_CorruptHeaderAfterFirstEntry(t *testing.T) {
	// A clean first entry followed by garbage (non-zero, not a valid
	// header) makes the second tr.Next() return a non-EOF error — the
	// "tar:" wrapped failure path.
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	_ = tw.WriteHeader(&tar.Header{Name: "f", Typeflag: tar.TypeReg, Mode: 0o644, Size: 3})
	_, _ = tw.Write([]byte("abc"))
	_ = tw.Flush()
	// Append a full 512-byte block of non-zero garbage: the tar reader
	// tries to parse it as the next header and fails the checksum.
	garbage := bytes.Repeat([]byte{0xAB}, 512)
	raw := append(buf.Bytes(), garbage...)
	err := extractTar(bytes.NewReader(raw), t.TempDir())
	if err == nil || !strings.Contains(err.Error(), "tar:") {
		t.Fatalf("expected wrapped tar error, got %v", err)
	}
}
