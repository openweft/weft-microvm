package microvm

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	digest "github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

// fakeRegistry serves the slice of the OCI Distribution v2 API that the
// cloud-boot/init oci.Client exercises during a Pull: GET on
// /v2/<repo>/manifests/<digest|tag> and /v2/<repo>/blobs/<digest>.
// Blobs and manifests live in memory keyed by digest (and, for the
// manifest, also by tag).
type fakeRegistry struct {
	mu        sync.Mutex
	blobs     map[string][]byte
	manifests map[string][]byte
	mediaType map[string]string
}

func newFakeRegistry() *fakeRegistry {
	return &fakeRegistry{
		blobs:     map[string][]byte{},
		manifests: map[string][]byte{},
		mediaType: map[string]string{},
	}
}

func (r *fakeRegistry) addBlob(content []byte, mt string) ocispec.Descriptor {
	r.mu.Lock()
	defer r.mu.Unlock()
	dig := fmt.Sprintf("sha256:%x", sha256.Sum256(content))
	r.blobs[dig] = content
	r.mediaType[dig] = mt
	return ocispec.Descriptor{MediaType: mt, Digest: digest.Digest(dig), Size: int64(len(content))}
}

func (r *fakeRegistry) addManifest(content []byte, mt, tag string) ocispec.Descriptor {
	r.mu.Lock()
	defer r.mu.Unlock()
	dig := fmt.Sprintf("sha256:%x", sha256.Sum256(content))
	r.manifests[dig] = content
	r.mediaType[dig] = mt
	if tag != "" {
		r.manifests[tag] = content
		r.mediaType[tag] = mt
	}
	return ocispec.Descriptor{MediaType: mt, Digest: digest.Digest(dig), Size: int64(len(content))}
}

func (r *fakeRegistry) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	if req.URL.Path == "/v2/" || req.URL.Path == "/v2" {
		w.WriteHeader(http.StatusOK)
		return
	}
	parts := strings.Split(strings.TrimPrefix(req.URL.Path, "/v2/"), "/")
	if len(parts) < 3 {
		http.NotFound(w, req)
		return
	}
	last := parts[len(parts)-1]
	kind := parts[len(parts)-2]
	r.mu.Lock()
	defer r.mu.Unlock()
	switch kind {
	case "manifests":
		data, ok := r.manifests[last]
		if !ok {
			http.NotFound(w, req)
			return
		}
		w.Header().Set("Content-Type", r.mediaType[last])
		w.Header().Set("Docker-Content-Digest", fmt.Sprintf("sha256:%x", sha256.Sum256(data)))
		if req.Method == http.MethodHead {
			w.WriteHeader(http.StatusOK)
			return
		}
		_, _ = w.Write(data)
	case "blobs":
		data, ok := r.blobs[last]
		if !ok {
			http.NotFound(w, req)
			return
		}
		mt := r.mediaType[last]
		if mt == "" {
			mt = "application/octet-stream"
		}
		w.Header().Set("Content-Type", mt)
		w.Header().Set("Docker-Content-Digest", last)
		if req.Method == http.MethodHead {
			w.WriteHeader(http.StatusOK)
			return
		}
		_, _ = w.Write(data)
	default:
		http.NotFound(w, req)
	}
}

// layerTarGz returns a gzipped tar carrying the named regular files.
func layerTarGz(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for name, body := range files {
		if err := tw.WriteHeader(&tar.Header{
			Name:     name,
			Typeflag: tar.TypeReg,
			Mode:     0o644,
			Size:     int64(len(body)),
		}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(body)); err != nil {
			t.Fatal(err)
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

// newImageRegistry stands up an in-memory registry serving a single
// image manifest (one gzipped layer + a config) and returns the running
// httptest.Server plus the host:port. The image config carries the
// supplied process bits so the derived .weft-microvm/config.json is observable.
func newImageRegistry(t *testing.T, cfg ocispec.ImageConfig, layerFiles map[string]string) (*httptest.Server, string) {
	t.Helper()
	reg := newFakeRegistry()

	layer := reg.addBlob(layerTarGz(t, layerFiles), ocispec.MediaTypeImageLayerGzip)

	img := ocispec.Image{Config: cfg}
	imgBytes, _ := json.Marshal(img)
	cfgDesc := reg.addBlob(imgBytes, ocispec.MediaTypeImageConfig)

	manifest := ocispec.Manifest{
		Config: cfgDesc,
		Layers: []ocispec.Descriptor{layer},
	}
	manifest.SchemaVersion = 2
	manifest.MediaType = ocispec.MediaTypeImageManifest
	mfBytes, _ := json.Marshal(manifest)
	reg.addManifest(mfBytes, ocispec.MediaTypeImageManifest, "latest")

	srv := httptest.NewServer(reg)
	t.Cleanup(srv.Close)
	host := strings.TrimPrefix(srv.URL, "http://")
	return srv, host
}

func TestPull_HappyPath_FromInMemoryRegistry(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	_, host := newImageRegistry(t,
		ocispec.ImageConfig{
			Entrypoint: []string{"/bin/app"},
			Cmd:        []string{"--flag"},
			Env:        []string{"K=V"},
			WorkingDir: "/work",
		},
		map[string]string{"etc/motd": "hi from layer"},
	)
	ref := host + "/some/repo:latest"

	if err := Pull(ref); err != nil {
		t.Fatalf("Pull: %v", err)
	}

	rs := refsafe(ref)
	root := imageRoot(rs)
	rootfs := rootfsPath(rs)

	// The layer file landed under rootfs.
	if b, err := os.ReadFile(filepath.Join(rootfs, "etc/motd")); err != nil || string(b) != "hi from layer" {
		t.Errorf("layer file: err=%v body=%q", err, b)
	}
	// manifest.json + config.json were persisted.
	for _, f := range []string{"manifest.json", "config.json"} {
		if _, err := os.Stat(filepath.Join(root, f)); err != nil {
			t.Errorf("missing %s: %v", f, err)
		}
	}
	// .weft-microvm/config.json was derived from the image config.
	b, err := os.ReadFile(filepath.Join(rootfs, ".weft-microvm", "config.json"))
	if err != nil {
		t.Fatalf("read derived config: %v", err)
	}
	var cf configFile
	if err := json.Unmarshal(b, &cf); err != nil {
		t.Fatal(err)
	}
	if len(cf.Process.Args) != 2 || cf.Process.Args[0] != "/bin/app" || cf.Process.Cwd != "/work" {
		t.Errorf("derived process spec wrong: %+v", cf.Process)
	}
}

func TestPull_RootfsMkdirError(t *testing.T) {
	// XDG_DATA_HOME is a regular file → os.MkdirAll(rootfs) fails before
	// any network call.
	f := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(f, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("XDG_DATA_HOME", f)
	err := Pull("127.0.0.1:1/some/repo:latest")
	if err == nil || !strings.Contains(err.Error(), "mkdir") {
		t.Fatalf("expected mkdir error, got %v", err)
	}
}

func TestPull_ManifestNotFound(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	reg := newFakeRegistry()
	srv := httptest.NewServer(reg)
	t.Cleanup(srv.Close)
	host := strings.TrimPrefix(srv.URL, "http://")

	err := Pull(host + "/no/such:tag")
	if err == nil || !strings.Contains(err.Error(), "pull manifest") {
		t.Fatalf("expected pull manifest error, got %v", err)
	}
}

func TestPull_BadConfigBlob_DecodeError(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	reg := newFakeRegistry()
	layer := reg.addBlob(layerTarGz(t, map[string]string{"a": "b"}), ocispec.MediaTypeImageLayerGzip)
	// Config blob is not valid JSON → decode image config fails.
	cfgDesc := reg.addBlob([]byte("not-json"), ocispec.MediaTypeImageConfig)
	manifest := ocispec.Manifest{Config: cfgDesc, Layers: []ocispec.Descriptor{layer}}
	manifest.SchemaVersion = 2
	manifest.MediaType = ocispec.MediaTypeImageManifest
	mfBytes, _ := json.Marshal(manifest)
	reg.addManifest(mfBytes, ocispec.MediaTypeImageManifest, "latest")
	srv := httptest.NewServer(reg)
	t.Cleanup(srv.Close)
	host := strings.TrimPrefix(srv.URL, "http://")

	err := Pull(host + "/bad/cfg:latest")
	if err == nil || !strings.Contains(err.Error(), "decode image config") {
		t.Fatalf("expected decode image config error, got %v", err)
	}
}

func TestPull_ConfigBlobMissing(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	reg := newFakeRegistry()
	layer := reg.addBlob(layerTarGz(t, map[string]string{"a": "b"}), ocispec.MediaTypeImageLayerGzip)
	// Manifest points at a config digest with no registered blob → the
	// config PullBlob 404s.
	cfgDesc := ocispec.Descriptor{
		MediaType: ocispec.MediaTypeImageConfig,
		Digest:    digest.FromBytes([]byte("no-config-here")),
		Size:      5,
	}
	manifest := ocispec.Manifest{Config: cfgDesc, Layers: []ocispec.Descriptor{layer}}
	manifest.SchemaVersion = 2
	manifest.MediaType = ocispec.MediaTypeImageManifest
	mfBytes, _ := json.Marshal(manifest)
	reg.addManifest(mfBytes, ocispec.MediaTypeImageManifest, "latest")
	srv := httptest.NewServer(reg)
	t.Cleanup(srv.Close)
	host := strings.TrimPrefix(srv.URL, "http://")

	err := Pull(host + "/no/config:latest")
	if err == nil || !strings.Contains(err.Error(), "pull config blob") {
		t.Fatalf("expected pull config blob error, got %v", err)
	}
}

func TestPull_NumericUserError(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	_, host := newImageRegistry(t,
		ocispec.ImageConfig{Cmd: []string{"/bin/sh"}, User: "alice"},
		map[string]string{"x": "y"},
	)
	// processFromImageConfig errors on a named (non-numeric) User.
	err := Pull(host + "/named/user:latest")
	if err == nil || !strings.Contains(err.Error(), "named users not supported") {
		t.Fatalf("expected named-user error, got %v", err)
	}
}

func TestPullAndExtractLayer_PullError(t *testing.T) {
	// Registry that 404s the blob: the pull goroutine fails, and
	// pullAndExtractLayer surfaces an extract (gzip) error first
	// because the empty body is not a gzip stream. Either way it errors.
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	reg := newFakeRegistry()
	cfgDesc := reg.addBlob([]byte(`{}`), ocispec.MediaTypeImageConfig)
	// Manifest references a layer digest that has no blob registered.
	bogus := ocispec.Descriptor{
		MediaType: ocispec.MediaTypeImageLayerGzip,
		Digest:    digest.FromBytes([]byte("nonexistent")),
		Size:      10,
	}
	manifest := ocispec.Manifest{Config: cfgDesc, Layers: []ocispec.Descriptor{bogus}}
	manifest.SchemaVersion = 2
	manifest.MediaType = ocispec.MediaTypeImageManifest
	mfBytes, _ := json.Marshal(manifest)
	reg.addManifest(mfBytes, ocispec.MediaTypeImageManifest, "latest")
	srv := httptest.NewServer(reg)
	t.Cleanup(srv.Close)
	host := strings.TrimPrefix(srv.URL, "http://")

	err := Pull(host + "/missing/layer:latest")
	if err == nil || !strings.Contains(err.Error(), "layer 0") {
		t.Fatalf("expected layer 0 error, got %v", err)
	}
}
