package microvm

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	weftv1 "github.com/openweft/weft-proto"
	weftpod "github.com/openweft/weft-microvm-init/pkg/pod"
)

// writeManifest dumps a podManifest-shaped JSON to a temp file and
// returns its path.
func writeManifest(t *testing.T, m map[string]any) string {
	t.Helper()
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(t.TempDir(), "pod.json")
	if err := os.WriteFile(p, b, 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestLoadManifest_Valid(t *testing.T) {
	p := writeManifest(t, map[string]any{
		"pod_id": "web",
		"containers": []map[string]any{
			{"id": "c1", "image": "alpine:3.21"},
		},
	})
	m, err := loadManifest(p)
	if err != nil {
		t.Fatal(err)
	}
	if m.PodID != "web" || len(m.Containers) != 1 || m.Containers[0].ID != "c1" {
		t.Errorf("unexpected manifest: %+v", m)
	}
}

func TestLoadManifest_Errors(t *testing.T) {
	t.Run("missing file", func(t *testing.T) {
		if _, err := loadManifest("/no/such/pod.json"); err == nil || !strings.Contains(err.Error(), "read pod manifest") {
			t.Fatalf("got %v", err)
		}
	})
	t.Run("bad json", func(t *testing.T) {
		p := filepath.Join(t.TempDir(), "pod.json")
		_ = os.WriteFile(p, []byte("{not json"), 0o644)
		if _, err := loadManifest(p); err == nil || !strings.Contains(err.Error(), "decode pod manifest") {
			t.Fatalf("got %v", err)
		}
	})
	t.Run("no pod_id", func(t *testing.T) {
		p := writeManifest(t, map[string]any{"containers": []map[string]any{{"id": "c", "image": "x"}}})
		if _, err := loadManifest(p); err == nil || !strings.Contains(err.Error(), "pod_id is required") {
			t.Fatalf("got %v", err)
		}
	})
	t.Run("no containers", func(t *testing.T) {
		p := writeManifest(t, map[string]any{"pod_id": "x"})
		if _, err := loadManifest(p); err == nil || !strings.Contains(err.Error(), "at least one container") {
			t.Fatalf("got %v", err)
		}
	})
	t.Run("container missing id", func(t *testing.T) {
		p := writeManifest(t, map[string]any{
			"pod_id":     "x",
			"containers": []map[string]any{{"image": "alpine"}},
		})
		if _, err := loadManifest(p); err == nil || !strings.Contains(err.Error(), "missing id") {
			t.Fatalf("got %v", err)
		}
	})
	t.Run("container missing image", func(t *testing.T) {
		p := writeManifest(t, map[string]any{
			"pod_id":     "x",
			"containers": []map[string]any{{"id": "c1"}},
		})
		if _, err := loadManifest(p); err == nil || !strings.Contains(err.Error(), "missing image") {
			t.Fatalf("got %v", err)
		}
	})
}

func TestEnvToMap(t *testing.T) {
	got := envToMap([]string{"A=1", "B=two", "MALFORMED", "C="})
	want := map[string]string{"A": "1", "B": "two", "C": ""}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("envToMap = %v, want %v", got, want)
	}
}

// seedContainerConfig writes a derived .weft-microvm/config.json for the given
// image so containerFromImage can read it.
func seedContainerConfig(t *testing.T, image string, p processSpec) string {
	t.Helper()
	rs := refsafe(image)
	rootfs := rootfsPath(rs)
	microvmDir := filepath.Join(rootfs, ".weft-microvm")
	if err := os.MkdirAll(microvmDir, 0o755); err != nil {
		t.Fatal(err)
	}
	out, err := marshalConfig(p)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(microvmDir, "config.json"), out, 0o644); err != nil {
		t.Fatal(err)
	}
	return rootfs
}

func TestContainerFromImage_FromImageConfig(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	var p processSpec
	p.Args = []string{"/bin/server", "--listen"}
	p.Env = []string{"PATH=/bin", "LANG=C"}
	p.Cwd = "/srv"
	p.User.UID = 1000
	p.User.GID = 2000
	rootfs := seedContainerConfig(t, "alpine:3.21", p)

	mc := &manifestCtr{ID: "web", Image: "alpine:3.21", Restart: weftpod.RestartAlways}
	ctr, err := containerFromImage(mc, "rootfs0", rootfs)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(ctr.Command, []string{"/bin/server", "--listen"}) {
		t.Errorf("command from image lost: %v", ctr.Command)
	}
	if ctr.Workdir != "/srv" {
		t.Errorf("workdir = %q", ctr.Workdir)
	}
	if ctr.User != "1000:2000" {
		t.Errorf("user = %q (want 1000:2000)", ctr.User)
	}
	if ctr.RootfsTag != "rootfs0" || ctr.Restart != weftpod.RestartAlways {
		t.Errorf("ctr = %+v", ctr)
	}
	if ctr.Env["PATH"] != "/bin" || ctr.Env["LANG"] != "C" {
		t.Errorf("env = %v", ctr.Env)
	}
}

func TestContainerFromImage_ManifestOverrides(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	var p processSpec
	p.Args = []string{"/image/entry"}
	p.Env = []string{"PATH=/bin", "DEBUG=0"}
	p.Cwd = "/image"
	rootfs := seedContainerConfig(t, "alpine:3.21", p)

	mc := &manifestCtr{
		ID:      "web",
		Image:   "alpine:3.21",
		Command: []string{"/custom", "run"},
		Env:     map[string]string{"DEBUG": "1", "EXTRA": "yes"},
		Workdir: "/over",
		User:    "user:group",
	}
	ctr, err := containerFromImage(mc, "rootfs1", rootfs)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(ctr.Command, []string{"/custom", "run"}) {
		t.Errorf("command override lost: %v", ctr.Command)
	}
	if ctr.Workdir != "/over" || ctr.User != "user:group" {
		t.Errorf("override workdir/user lost: %+v", ctr)
	}
	// Manifest env overrides image env, plus the image's other keys survive.
	if ctr.Env["DEBUG"] != "1" || ctr.Env["EXTRA"] != "yes" || ctr.Env["PATH"] != "/bin" {
		t.Errorf("env merge wrong: %v", ctr.Env)
	}
}

func TestContainerFromImage_MissingConfig(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	rootfs := filepath.Join(t.TempDir(), "rootfs")
	mc := &manifestCtr{ID: "web", Image: "x"}
	if _, err := containerFromImage(mc, "rootfs0", rootfs); err == nil || !strings.Contains(err.Error(), "read") {
		t.Fatalf("expected read error, got %v", err)
	}
}

func TestContainerFromImage_BadConfigJSON(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	rootfs := filepath.Join(t.TempDir(), "rootfs")
	microvmDir := filepath.Join(rootfs, ".weft-microvm")
	_ = os.MkdirAll(microvmDir, 0o755)
	_ = os.WriteFile(filepath.Join(microvmDir, "config.json"), []byte("{bad"), 0o644)
	mc := &manifestCtr{ID: "web", Image: "x"}
	if _, err := containerFromImage(mc, "rootfs0", rootfs); err == nil || !strings.Contains(err.Error(), "decode") {
		t.Fatalf("expected decode error, got %v", err)
	}
}

func TestContainerFromImage_NoCommand(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	// Image config with empty args and manifest sets none → error.
	rootfs := seedContainerConfig(t, "alpine:3.21", processSpec{Cwd: "/"})
	mc := &manifestCtr{ID: "web", Image: "alpine:3.21"}
	if _, err := containerFromImage(mc, "rootfs0", rootfs); err == nil || !strings.Contains(err.Error(), "no command") {
		t.Fatalf("expected no-command error, got %v", err)
	}
}

func TestWritePodJSON(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	spec := weftpod.Spec{
		PodID:      "p1",
		Containers: []weftpod.Container{{ID: "c", RootfsTag: "rootfs0", Command: []string{"/sh"}}},
	}
	dir, err := writePodJSON("p1", spec)
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(dir, "pod.json"))
	if err != nil {
		t.Fatalf("pod.json not written: %v", err)
	}
	var got weftpod.Spec
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	if got.PodID != "p1" || len(got.Containers) != 1 {
		t.Errorf("round-trip mismatch: %+v", got)
	}
}

func TestWritePodJSON_MkdirError(t *testing.T) {
	// Point XDG_DATA_HOME at a regular file so MkdirAll under it fails.
	f := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(f, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("XDG_DATA_HOME", f)
	_, err := writePodJSON("p1", weftpod.Spec{PodID: "p1"})
	if err == nil || !strings.Contains(err.Error(), "mkdir pod config dir") {
		t.Fatalf("expected mkdir error, got %v", err)
	}
}

func TestLocatePodBoot(t *testing.T) {
	t.Run("happy path defaults", func(t *testing.T) {
		t.Setenv("XDG_DATA_HOME", t.TempDir())
		t.Setenv("WEFT_KERNEL", "")
		t.Setenv("WEFT_POD_INITRD", "")
		microvmDir := dataDir()
		_ = os.MkdirAll(microvmDir, 0o755)
		_ = os.WriteFile(filepath.Join(microvmDir, "kernel"), []byte("k"), 0o644)
		_ = os.WriteFile(filepath.Join(microvmDir, "pod-initrd"), []byte("i"), 0o644)
		k, i, err := locatePodBoot()
		if err != nil {
			t.Fatal(err)
		}
		if !strings.HasSuffix(k, "/kernel") || !strings.HasSuffix(i, "/pod-initrd") {
			t.Errorf("k=%q i=%q", k, i)
		}
	})
	t.Run("env overrides", func(t *testing.T) {
		tmp := t.TempDir()
		t.Setenv("XDG_DATA_HOME", t.TempDir())
		k := filepath.Join(tmp, "mykernel")
		i := filepath.Join(tmp, "myinitrd")
		_ = os.WriteFile(k, []byte("k"), 0o644)
		_ = os.WriteFile(i, []byte("i"), 0o644)
		t.Setenv("WEFT_KERNEL", k)
		t.Setenv("WEFT_POD_INITRD", i)
		gk, gi, err := locatePodBoot()
		if err != nil {
			t.Fatal(err)
		}
		if gk != k || gi != i {
			t.Errorf("got k=%q i=%q", gk, gi)
		}
	})
	t.Run("missing kernel", func(t *testing.T) {
		t.Setenv("XDG_DATA_HOME", t.TempDir())
		t.Setenv("WEFT_KERNEL", "")
		t.Setenv("WEFT_POD_INITRD", "")
		if _, _, err := locatePodBoot(); err == nil || !strings.Contains(err.Error(), "kernel not found") {
			t.Fatalf("got %v", err)
		}
	})
	t.Run("missing initrd", func(t *testing.T) {
		t.Setenv("XDG_DATA_HOME", t.TempDir())
		t.Setenv("WEFT_KERNEL", "")
		t.Setenv("WEFT_POD_INITRD", "")
		microvmDir := dataDir()
		_ = os.MkdirAll(microvmDir, 0o755)
		_ = os.WriteFile(filepath.Join(microvmDir, "kernel"), []byte("k"), 0o644)
		if _, _, err := locatePodBoot(); err == nil || !strings.Contains(err.Error(), "initramfs not found") {
			t.Fatalf("got %v", err)
		}
	})
}

func TestRunPod_HappyPath(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	t.Setenv("WEFT_KERNEL", "")
	t.Setenv("WEFT_POD_INITRD", "")
	t.Setenv("WEFT_NO_AUTO_PULL", "1") // images are pre-seeded

	// Seed two pre-pulled container images.
	seedContainerConfig(t, "alpine:3.21", processSpec{Args: []string{"/web"}, Cwd: "/", Env: []string{"PATH=/bin"}})
	seedContainerConfig(t, "redis:7", processSpec{Args: []string{"/redis"}, Cwd: "/", Env: []string{"PATH=/bin"}})
	// Seed pod boot artefacts.
	microvmDir := dataDir()
	_ = os.MkdirAll(microvmDir, 0o755)
	_ = os.WriteFile(filepath.Join(microvmDir, "kernel"), []byte("k"), 0o644)
	_ = os.WriteFile(filepath.Join(microvmDir, "pod-initrd"), []byte("i"), 0o644)

	manifest := writeManifest(t, map[string]any{
		"pod_id": "stack",
		"containers": []map[string]any{
			{"id": "web", "image": "alpine:3.21"},
			{"id": "cache", "image": "redis:7"},
		},
	})

	var gotRegister *weftv1.RegisterMicroVMRequest
	var started bool
	stub := &stubWeft{
		registerFn: func(_ context.Context, in *weftv1.RegisterMicroVMRequest) (*weftv1.RegisterMicroVMResponse, error) {
			gotRegister = in
			return &weftv1.RegisterMicroVMResponse{}, nil
		},
		startFn: func(context.Context, *weftv1.StartVMRequest) (*weftv1.StartVMResponse, error) {
			started = true
			return &weftv1.StartVMResponse{}, nil
		},
	}
	socket := startStubWeft(t, stub)

	if err := Run(Args{Pod: manifest, WeftSocket: socket, Project: "p"}); err != nil {
		t.Fatalf("RunPod: %v", err)
	}
	if !started || gotRegister == nil {
		t.Fatal("register/start not called")
	}
	if gotRegister.Name != "weft-microvm-pod-stack" {
		t.Errorf("name = %q", gotRegister.Name)
	}
	// config share (weftcfg, read-only) prepended, then 2 rootfs shares.
	if len(gotRegister.Shares) != 3 {
		t.Fatalf("share count = %d, want 3: %+v", len(gotRegister.Shares), gotRegister.Shares)
	}
	if gotRegister.Shares[0].Tag != configTag || !gotRegister.Shares[0].ReadOnly {
		t.Errorf("config share wrong: %+v", gotRegister.Shares[0])
	}
	tags := []string{gotRegister.Shares[1].Tag, gotRegister.Shares[2].Tag}
	sort.Strings(tags)
	if !reflect.DeepEqual(tags, []string{"rootfs0", "rootfs1"}) {
		t.Errorf("rootfs tags = %v", tags)
	}
	if !strings.Contains(gotRegister.Cmdline, "virtiofs:"+configTag) {
		t.Errorf("cmdline = %q", gotRegister.Cmdline)
	}
}

func TestRunPod_ContainerNotPulled_NoAutoPull(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	t.Setenv("WEFT_NO_AUTO_PULL", "1")
	manifest := writeManifest(t, map[string]any{
		"pod_id":     "stack",
		"containers": []map[string]any{{"id": "web", "image": "missing:img"}},
	})
	err := Run(Args{Pod: manifest, WeftSocket: "/tmp/unused.sock"})
	if err == nil || !strings.Contains(err.Error(), "not pulled") {
		t.Fatalf("expected not-pulled error, got %v", err)
	}
}

func TestRunPod_AutoPullAttempted(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	t.Setenv("WEFT_NO_AUTO_PULL", "") // auto-pull is the default
	// Image not in cache → RunPod prints and calls Pull, which fails
	// against a localhost port with nothing listening (fast, offline).
	manifest := writeManifest(t, map[string]any{
		"pod_id":     "stack",
		"containers": []map[string]any{{"id": "web", "image": "127.0.0.1:1/no/such:tag"}},
	})
	err := Run(Args{Pod: manifest, WeftSocket: "/tmp/unused.sock"})
	if err == nil || !strings.Contains(err.Error(), "pull") {
		t.Fatalf("expected pull error from auto-pull, got %v", err)
	}
}

func TestRunPod_NoCommand_SpecValidateOrContainerError(t *testing.T) {
	// A container whose image config has no args and no manifest command
	// fails in containerFromImage ("no command").
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	t.Setenv("WEFT_NO_AUTO_PULL", "1")
	seedContainerConfig(t, "alpine:3.21", processSpec{Cwd: "/"})
	manifest := writeManifest(t, map[string]any{
		"pod_id":     "stack",
		"containers": []map[string]any{{"id": "web", "image": "alpine:3.21"}},
	})
	err := Run(Args{Pod: manifest, WeftSocket: "/tmp/unused.sock"})
	if err == nil || !strings.Contains(err.Error(), "no command") {
		t.Fatalf("expected no-command error, got %v", err)
	}
}

func TestRunPod_MissingBoot(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	t.Setenv("WEFT_KERNEL", "")
	t.Setenv("WEFT_POD_INITRD", "")
	t.Setenv("WEFT_NO_AUTO_PULL", "1")
	seedContainerConfig(t, "alpine:3.21", processSpec{Args: []string{"/web"}, Cwd: "/"})
	manifest := writeManifest(t, map[string]any{
		"pod_id":     "stack",
		"containers": []map[string]any{{"id": "web", "image": "alpine:3.21"}},
	})
	// No kernel seeded → locatePodBoot errors.
	err := Run(Args{Pod: manifest, WeftSocket: "/tmp/unused.sock"})
	if err == nil || !strings.Contains(err.Error(), "kernel not found") {
		t.Fatalf("expected kernel-not-found error, got %v", err)
	}
}

func TestRunPod_SpecValidateError_DuplicateID(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	t.Setenv("WEFT_NO_AUTO_PULL", "1")
	// Two containers with the same id pass loadManifest but fail
	// spec.Validate (duplicate id).
	seedContainerConfig(t, "alpine:3.21", processSpec{Args: []string{"/web"}, Cwd: "/"})
	manifest := writeManifest(t, map[string]any{
		"pod_id": "stack",
		"containers": []map[string]any{
			{"id": "dup", "image": "alpine:3.21"},
			{"id": "dup", "image": "alpine:3.21"},
		},
	})
	err := Run(Args{Pod: manifest, WeftSocket: "/tmp/unused.sock"})
	if err == nil || !strings.Contains(err.Error(), "pod spec") {
		t.Fatalf("expected pod spec validate error, got %v", err)
	}
}

func TestRunPod_DialError(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	t.Setenv("WEFT_KERNEL", "")
	t.Setenv("WEFT_POD_INITRD", "")
	t.Setenv("WEFT_NO_AUTO_PULL", "1")
	seedContainerConfig(t, "alpine:3.21", processSpec{Args: []string{"/web"}, Cwd: "/"})
	microvmDir := dataDir()
	_ = os.MkdirAll(microvmDir, 0o755)
	_ = os.WriteFile(filepath.Join(microvmDir, "kernel"), []byte("k"), 0o644)
	_ = os.WriteFile(filepath.Join(microvmDir, "pod-initrd"), []byte("i"), 0o644)
	manifest := writeManifest(t, map[string]any{
		"pod_id":     "stack",
		"containers": []map[string]any{{"id": "web", "image": "alpine:3.21"}},
	})
	err := Run(Args{Pod: manifest, WeftSocket: "/tmp/no-weft-pod.sock"})
	if err == nil {
		t.Fatal("expected dial error")
	}
}

func TestRunPod_StartError(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	t.Setenv("WEFT_KERNEL", "")
	t.Setenv("WEFT_POD_INITRD", "")
	t.Setenv("WEFT_NO_AUTO_PULL", "1")
	seedContainerConfig(t, "alpine:3.21", processSpec{Args: []string{"/web"}, Cwd: "/"})
	microvmDir := dataDir()
	_ = os.MkdirAll(microvmDir, 0o755)
	_ = os.WriteFile(filepath.Join(microvmDir, "kernel"), []byte("k"), 0o644)
	_ = os.WriteFile(filepath.Join(microvmDir, "pod-initrd"), []byte("i"), 0o644)
	manifest := writeManifest(t, map[string]any{
		"pod_id":     "stack",
		"containers": []map[string]any{{"id": "web", "image": "alpine:3.21"}},
	})
	stub := &stubWeft{startFn: func(context.Context, *weftv1.StartVMRequest) (*weftv1.StartVMResponse, error) {
		return nil, errStub("start-fail")
	}}
	socket := startStubWeft(t, stub)
	err := Run(Args{Pod: manifest, WeftSocket: socket})
	if err == nil || !strings.Contains(err.Error(), "StartVM") {
		t.Fatalf("expected StartVM error, got %v", err)
	}
}

func TestRunPod_RegisterError(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	t.Setenv("WEFT_KERNEL", "")
	t.Setenv("WEFT_POD_INITRD", "")
	t.Setenv("WEFT_NO_AUTO_PULL", "1")
	seedContainerConfig(t, "alpine:3.21", processSpec{Args: []string{"/web"}, Cwd: "/"})
	microvmDir := dataDir()
	_ = os.MkdirAll(microvmDir, 0o755)
	_ = os.WriteFile(filepath.Join(microvmDir, "kernel"), []byte("k"), 0o644)
	_ = os.WriteFile(filepath.Join(microvmDir, "pod-initrd"), []byte("i"), 0o644)
	manifest := writeManifest(t, map[string]any{
		"pod_id":     "stack",
		"containers": []map[string]any{{"id": "web", "image": "alpine:3.21"}},
	})
	stub := &stubWeft{registerFn: func(context.Context, *weftv1.RegisterMicroVMRequest) (*weftv1.RegisterMicroVMResponse, error) {
		return nil, errStub("nope")
	}}
	socket := startStubWeft(t, stub)
	err := Run(Args{Pod: manifest, WeftSocket: socket})
	if err == nil || !strings.Contains(err.Error(), "RegisterMicroVM") {
		t.Fatalf("expected RegisterMicroVM error, got %v", err)
	}
}

// errStub is a tiny error type to avoid importing errors in this file
// twice; keeps the failure injection self-contained.
type errStub string

func (e errStub) Error() string { return string(e) }
