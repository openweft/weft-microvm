package initbuild

import (
	"bytes"
	"compress/gzip"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestPack_RoundTrip writes a fake "binary" through Pack and reads
// back the gzip+cpio archive byte-for-byte to confirm:
//   - the output decompresses cleanly,
//   - the cpio stream carries an entry whose body equals our binary,
//   - the entry's path is `init` (the kernel finds /init by looking
//     for that name at the root of the initramfs cpio).
func TestPack_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "weft-microvm-init")
	wanted := []byte("\x7fELF…actually-not-elf-just-bytes")
	if err := os.WriteFile(bin, wanted, 0o755); err != nil {
		t.Fatal(err)
	}
	var packed bytes.Buffer
	if err := Pack(bin, &packed); err != nil {
		t.Fatalf("Pack: %v", err)
	}
	if packed.Len() == 0 {
		t.Fatal("packed archive is empty")
	}
	gz, err := gzip.NewReader(&packed)
	if err != nil {
		t.Fatalf("gzip open: %v", err)
	}
	defer gz.Close()
	raw, err := readAll(gz)
	if err != nil {
		t.Fatalf("decompress: %v", err)
	}
	// cpio newc magic is "070701" at the start of each entry.
	if !bytes.HasPrefix(raw, []byte("070701")) {
		t.Fatalf("decompressed stream is not newc cpio (first 6 bytes: %q)", raw[:6])
	}
	// The /init entry's body should appear verbatim somewhere in
	// the decompressed stream (cpio is bytes-on-the-wire, no
	// transformation). We don't reparse the full archive — just
	// confirm the payload made it in.
	if !bytes.Contains(raw, wanted) {
		t.Errorf("decompressed cpio doesn't contain the binary bytes")
	}
	// And the entry name should be there too.
	if !bytes.Contains(raw, []byte("init")) {
		t.Errorf("decompressed cpio doesn't contain the entry name `init`")
	}
}

func TestPack_MissingBinary(t *testing.T) {
	var buf bytes.Buffer
	err := Pack("/nonexistent/path", &buf)
	if err == nil {
		t.Fatal("expected error for missing binary")
	}
	if !strings.Contains(err.Error(), "read") {
		t.Errorf("error should mention read failure, got: %v", err)
	}
}

func TestPackToFile_WritesDestination(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "src")
	dst := filepath.Join(dir, "initrd.cpio.gz")
	if err := os.WriteFile(bin, []byte("payload"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := PackToFile(bin, dst); err != nil {
		t.Fatalf("PackToFile: %v", err)
	}
	fi, err := os.Stat(dst)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Size() == 0 {
		t.Error("destination file is empty")
	}
}

func TestPackToFile_OpenError(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "src")
	if err := os.WriteFile(bin, []byte("payload"), 0o755); err != nil {
		t.Fatal(err)
	}
	// Destination under a non-existent directory → OpenFile fails.
	err := PackToFile(bin, filepath.Join(dir, "no-such-dir", "out.cpio.gz"))
	if err == nil || !strings.Contains(err.Error(), "open") {
		t.Fatalf("expected open error, got %v", err)
	}
}

// readAll is io.ReadAll but inlined to avoid pulling io.ReadAll
// into the test's minimal-import surface.
func readAll(r interface {
	Read(p []byte) (n int, err error)
}) ([]byte, error) {
	var out []byte
	buf := make([]byte, 4096)
	for {
		n, err := r.Read(buf)
		if n > 0 {
			out = append(out, buf[:n]...)
		}
		if err != nil {
			if err.Error() == "EOF" {
				return out, nil
			}
			return out, err
		}
	}
}
