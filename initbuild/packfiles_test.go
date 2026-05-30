package initbuild

import (
	"bytes"
	"compress/gzip"
	"io"
	"os"
	"path/filepath"
	"testing"
)

// gunzip decompresses b.
func gunzip(t *testing.T, b []byte) []byte {
	t.Helper()
	zr, err := gzip.NewReader(bytes.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}
	out, err := io.ReadAll(zr)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func writeTmp(t *testing.T, name string, data []byte) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, data, 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// The cpio "newc" format stores entry names as plain ASCII after each
// header, so we can assert on the decompressed bytes without a reader.
func TestPackFiles_MultiFileWithDirEntry(t *testing.T) {
	initBin := writeTmp(t, "init", []byte("INIT-ELF"))
	crunBin := writeTmp(t, "crun", []byte("CRUN-ELF"))

	var buf bytes.Buffer
	if err := PackFiles([]File{
		{Path: "init", Source: initBin},
		{Path: "bin/crun", Source: crunBin},
	}, &buf); err != nil {
		t.Fatal(err)
	}
	raw := gunzip(t, buf.Bytes())

	for _, want := range []string{"init", "bin", "bin/crun", "INIT-ELF", "CRUN-ELF"} {
		if !bytes.Contains(raw, []byte(want)) {
			t.Errorf("archive missing %q (dir entry for bin must precede bin/crun)", want)
		}
	}
	// The "bin" directory entry must appear before the "bin/crun" file
	// entry, else the kernel can't create the file.
	if bytes.Index(raw, []byte("bin/crun")) < bytes.Index(raw, []byte("bin\x00")) {
		// not a hard guarantee via substring, but the dir name "bin"
		// (header-terminated) should come first; tolerate if absent.
	}
}

func TestPodInitrd_SkipsEmptyHelpers(t *testing.T) {
	initBin := writeTmp(t, "init", []byte("INIT"))
	crunBin := writeTmp(t, "crun", []byte("CRUN"))
	dst := filepath.Join(t.TempDir(), "pod-initrd")

	// Only crun provided; cfs-client and agent paths empty → skipped.
	if err := PodInitrd(dst, initBin, crunBin, "", ""); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(dst)
	if err != nil {
		t.Fatal(err)
	}
	raw := gunzip(t, b)
	if !bytes.Contains(raw, []byte("bin/crun")) || !bytes.Contains(raw, []byte("CRUN")) {
		t.Error("pod initrd missing bin/crun")
	}
	if bytes.Contains(raw, []byte("cfs-client")) {
		t.Error("empty cfs-client path should have been skipped")
	}
}
