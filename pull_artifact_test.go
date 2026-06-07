package microvm

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	digest "github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

// newKernelArtifactRegistry stands up an in-memory registry serving a
// kernel artifact under a multi-arch OCI index : the index lists one
// per-arch manifest matching runtime.GOARCH whose single layer carries
// the kernel media type. Returns the running httptest server plus the
// host:port portion suitable for use as an oras-go reference host.
func newKernelArtifactRegistry(t *testing.T, kernelBody []byte) (*httptest.Server, string) {
	t.Helper()
	reg := newFakeRegistry()

	kernel := reg.addBlob(kernelBody, kernelLayerMediaType)
	// A minimal config blob ; oras-go requires Manifest.Config to point
	// at a fetchable descriptor even when the artifact carries no real
	// runtime config (custom artifactType).
	cfg := reg.addBlob([]byte("{}"), "application/vnd.openweft.microvm.kernel.config")

	perArch := ocispec.Manifest{Config: cfg, Layers: []ocispec.Descriptor{kernel}}
	perArch.SchemaVersion = 2
	perArch.MediaType = ocispec.MediaTypeImageManifest
	perArchBytes, _ := json.Marshal(perArch)
	perArchDesc := reg.addManifest(perArchBytes, ocispec.MediaTypeImageManifest, "")
	perArchDesc.Platform = &ocispec.Platform{OS: "linux", Architecture: runtime.GOARCH}

	idx := ocispec.Index{Manifests: []ocispec.Descriptor{perArchDesc}}
	idx.SchemaVersion = 2
	idx.MediaType = ocispec.MediaTypeImageIndex
	idxBytes, _ := json.Marshal(idx)
	reg.addManifest(idxBytes, ocispec.MediaTypeImageIndex, "latest")

	srv := httptest.NewServer(reg)
	t.Cleanup(srv.Close)
	host := strings.TrimPrefix(srv.URL, "http://")
	return srv, host
}

// newPodInitrdArtifactRegistry mirrors newKernelArtifactRegistry but
// publishes the pod-initrd media type instead. Same multi-arch index
// shape; the layer media type is what distinguishes the two artifact
// kinds at fetch time.
func newPodInitrdArtifactRegistry(t *testing.T, initrdBody []byte) (*httptest.Server, string) {
	t.Helper()
	reg := newFakeRegistry()

	initrd := reg.addBlob(initrdBody, podInitrdLayerMediaType)
	cfg := reg.addBlob([]byte("{}"), "application/vnd.openweft.microvm.pod-initrd.config")

	perArch := ocispec.Manifest{Config: cfg, Layers: []ocispec.Descriptor{initrd}}
	perArch.SchemaVersion = 2
	perArch.MediaType = ocispec.MediaTypeImageManifest
	perArchBytes, _ := json.Marshal(perArch)
	perArchDesc := reg.addManifest(perArchBytes, ocispec.MediaTypeImageManifest, "")
	perArchDesc.Platform = &ocispec.Platform{OS: "linux", Architecture: runtime.GOARCH}

	idx := ocispec.Index{Manifests: []ocispec.Descriptor{perArchDesc}}
	idx.SchemaVersion = 2
	idx.MediaType = ocispec.MediaTypeImageIndex
	idxBytes, _ := json.Marshal(idx)
	reg.addManifest(idxBytes, ocispec.MediaTypeImageIndex, "latest")

	srv := httptest.NewServer(reg)
	t.Cleanup(srv.Close)
	host := strings.TrimPrefix(srv.URL, "http://")
	return srv, host
}

// TestPullKernel_MultiArchIndex verifies that PullKernel descends a
// multi-arch OCI index (the published shape : one manifest per arch
// pointing at a single-platform kernel layer). Pre-fix, PullKernel
// json-decoded the index as a Manifest, found no layers matching the
// kernel media type, and bailed — never descending. The fix routes
// through resolvePlatform so the arch-matching descent runs.
func TestPullKernel_MultiArchIndex(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	want := []byte("fake-kernel-bytes")
	_, host := newKernelArtifactRegistry(t, want)

	if err := PullKernel(host + "/weft/kernel:latest"); err != nil {
		t.Fatalf("PullKernel: %v", err)
	}
	got, err := os.ReadFile(KernelPath())
	if err != nil {
		t.Fatalf("read kernel: %v", err)
	}
	if string(got) != string(want) {
		t.Errorf("kernel bytes mismatch: got %q want %q", got, want)
	}
}

// TestPullPodInitrd_MultiArchIndex confirms the equivalent path for the
// pod-initrd artifact. Pre-fix this case worked because PullPodInitrd
// had its own inline index descent ; the refactor moves it to the
// shared resolvePlatform helper and this test guards the behaviour.
func TestPullPodInitrd_MultiArchIndex(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	want := []byte("fake-initrd-cpio-gz")
	_, host := newPodInitrdArtifactRegistry(t, want)

	if err := PullPodInitrd(host + "/weft/pod-initrd:latest"); err != nil {
		t.Fatalf("PullPodInitrd: %v", err)
	}
	got, err := os.ReadFile(PodInitrdPath())
	if err != nil {
		t.Fatalf("read pod-initrd: %v", err)
	}
	if string(got) != string(want) {
		t.Errorf("pod-initrd bytes mismatch: got %q want %q", got, want)
	}
}

// TestPullKernel_MirrorRouting confirms that WEFT_MICROVM_REGISTRY_MIRROR
// routes the kernel fetch through the configured mirror host, the same
// way Pull does. Pre-fix, PullKernel ignored the env var and always hit
// the canonical upstream — a cross-cluster regression hidden because
// no test exercised the kernel path with a mirror configured.
//
// The test stands up TWO registries : the canonical "upstream" carrying
// no data, and a "mirror" carrying the artifact. With the env var set
// to the mirror's host, PullKernel must succeed reading the mirror —
// proving the request never went to the canonical host.
func TestPullKernel_MirrorRouting(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	// Mirror has the artifact ; canonical does not.
	_, mirrorHost := newKernelArtifactRegistry(t, []byte("from-mirror"))
	canonicalReg := newFakeRegistry()
	canonicalSrv := httptest.NewServer(canonicalReg)
	t.Cleanup(canonicalSrv.Close)
	canonicalHost := strings.TrimPrefix(canonicalSrv.URL, "http://")

	t.Setenv("WEFT_MICROVM_REGISTRY_MIRROR", mirrorHost)
	if err := PullKernel(canonicalHost + "/weft/kernel:latest"); err != nil {
		t.Fatalf("PullKernel via mirror: %v", err)
	}
	got, err := os.ReadFile(KernelPath())
	if err != nil {
		t.Fatalf("read kernel: %v", err)
	}
	if string(got) != "from-mirror" {
		t.Errorf("expected mirror payload, got %q", got)
	}
}

// TestPullPodInitrd_MirrorRouting is the pod-initrd analogue. Same
// asymmetry pre-fix : the kernel + initrd pullers did NOT honour
// WEFT_MICROVM_REGISTRY_MIRROR even though the layer puller did.
func TestPullPodInitrd_MirrorRouting(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	_, mirrorHost := newPodInitrdArtifactRegistry(t, []byte("from-mirror-initrd"))
	canonicalReg := newFakeRegistry()
	canonicalSrv := httptest.NewServer(canonicalReg)
	t.Cleanup(canonicalSrv.Close)
	canonicalHost := strings.TrimPrefix(canonicalSrv.URL, "http://")

	t.Setenv("WEFT_MICROVM_REGISTRY_MIRROR", mirrorHost)
	if err := PullPodInitrd(canonicalHost + "/weft/pod-initrd:latest"); err != nil {
		t.Fatalf("PullPodInitrd via mirror: %v", err)
	}
	got, err := os.ReadFile(PodInitrdPath())
	if err != nil {
		t.Fatalf("read pod-initrd: %v", err)
	}
	if string(got) != "from-mirror-initrd" {
		t.Errorf("expected mirror payload, got %q", got)
	}
}

// TestPullKernel_MissingLayer surfaces a manifest with no layer of the
// expected media type — guards the no-layer-found error path stays
// reachable after the refactor to resolvePlatform.
func TestPullKernel_MissingLayer(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	reg := newFakeRegistry()
	// A manifest with only the "wrong" media type for a kernel.
	wrong := reg.addBlob([]byte("not-the-kernel"), "application/vnd.openweft.other")
	cfg := reg.addBlob([]byte("{}"), "application/vnd.openweft.microvm.kernel.config")
	perArch := ocispec.Manifest{Config: cfg, Layers: []ocispec.Descriptor{wrong}}
	perArch.SchemaVersion = 2
	perArch.MediaType = ocispec.MediaTypeImageManifest
	perArchBytes, _ := json.Marshal(perArch)
	perArchDesc := reg.addManifest(perArchBytes, ocispec.MediaTypeImageManifest, "")
	perArchDesc.Platform = &ocispec.Platform{OS: "linux", Architecture: runtime.GOARCH}
	idx := ocispec.Index{Manifests: []ocispec.Descriptor{perArchDesc}}
	idx.SchemaVersion = 2
	idx.MediaType = ocispec.MediaTypeImageIndex
	idxBytes, _ := json.Marshal(idx)
	reg.addManifest(idxBytes, ocispec.MediaTypeImageIndex, "latest")

	srv := httptest.NewServer(reg)
	t.Cleanup(srv.Close)
	host := strings.TrimPrefix(srv.URL, "http://")

	err := PullKernel(host + "/no/kernel:latest")
	if err == nil || !strings.Contains(err.Error(), "no kernel layer") {
		t.Fatalf("expected no-kernel-layer error, got %v", err)
	}
}

// TestResolvePlatform_EmptyOSMatches confirms the new tolerance rule :
// per-arch index entries that omit OS (oras-go publishes some artifacts
// this way when --artifact-platform only specifies arch) still match a
// linux/<arch> query. Pre-fix, the strict OS equality rejected them.
// The compiled-in shared helper avoids re-decoding the index in
// PullKernel + PullPodInitrd ; we exercise it directly via Pull's
// resolvePlatform call site rather than via reflection.
func TestResolvePlatform_EmptyOSMatches(t *testing.T) {
	// Build an in-memory registry with an index whose only manifest has
	// Architecture set but OS empty. PullKernel must still find it.
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	reg := newFakeRegistry()
	kernel := reg.addBlob([]byte("k"), kernelLayerMediaType)
	cfg := reg.addBlob([]byte("{}"), "application/vnd.openweft.microvm.kernel.config")
	perArch := ocispec.Manifest{Config: cfg, Layers: []ocispec.Descriptor{kernel}}
	perArch.SchemaVersion = 2
	perArch.MediaType = ocispec.MediaTypeImageManifest
	perArchBytes, _ := json.Marshal(perArch)
	dig := digest.FromBytes(perArchBytes)
	reg.manifests[string(dig)] = perArchBytes
	reg.mediaType[string(dig)] = ocispec.MediaTypeImageManifest

	idx := ocispec.Index{
		Manifests: []ocispec.Descriptor{{
			MediaType: ocispec.MediaTypeImageManifest,
			Digest:    dig,
			Size:      int64(len(perArchBytes)),
			Platform:  &ocispec.Platform{Architecture: runtime.GOARCH}, // OS deliberately empty
		}},
	}
	idx.SchemaVersion = 2
	idx.MediaType = ocispec.MediaTypeImageIndex
	idxBytes, _ := json.Marshal(idx)
	reg.addManifest(idxBytes, ocispec.MediaTypeImageIndex, "latest")

	srv := httptest.NewServer(reg)
	t.Cleanup(srv.Close)
	host := strings.TrimPrefix(srv.URL, "http://")

	if err := PullKernel(host + "/empty/os:latest"); err != nil {
		t.Fatalf("PullKernel: %v", err)
	}
	got, err := os.ReadFile(KernelPath())
	if err != nil || string(got) != "k" {
		t.Fatalf("kernel body wrong: err=%v body=%q", err, got)
	}
	// Cleanup hint: KernelPath() is under the XDG temp dir so t.TempDir's
	// auto-cleanup handles it ; we don't need an explicit Remove here.
	_ = filepath.Join // keep filepath imported in lean cases
}
