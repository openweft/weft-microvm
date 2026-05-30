package microvm

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	vzdv1 "github.com/openweft/weft-proto"
	"google.golang.org/grpc"
)

// stubVzd is a minimal in-process WeftAgent implementing only the two
// RPCs the microvm orchestration calls (RegisterMicroVM, StartVM) plus
// the embedded unimplemented base for the rest. Per-RPC behaviour is
// injected via the Fn hooks.
type stubVzd struct {
	vzdv1.UnimplementedWeftAgentServer
	registerFn func(context.Context, *vzdv1.RegisterMicroVMRequest) (*vzdv1.RegisterMicroVMResponse, error)
	startFn    func(context.Context, *vzdv1.StartVMRequest) (*vzdv1.StartVMResponse, error)
}

func (s *stubVzd) RegisterMicroVM(ctx context.Context, in *vzdv1.RegisterMicroVMRequest) (*vzdv1.RegisterMicroVMResponse, error) {
	if s.registerFn != nil {
		return s.registerFn(ctx, in)
	}
	return &vzdv1.RegisterMicroVMResponse{}, nil
}

func (s *stubVzd) StartVM(ctx context.Context, in *vzdv1.StartVMRequest) (*vzdv1.StartVMResponse, error) {
	if s.startFn != nil {
		return s.startFn(ctx, in)
	}
	return &vzdv1.StartVMResponse{}, nil
}

// startStubVzd stands up a grpc.Server on a short unix socket and
// returns the socket path. Cleanup is registered via t.Cleanup.
func startStubVzd(t *testing.T, stub *stubVzd) string {
	t.Helper()
	socket := filepath.Join("/tmp", fmt.Sprintf("mv-test-%s.sock", time.Now().Format("150405.000000000")))
	srv := grpc.NewServer()
	vzdv1.RegisterWeftAgentServer(srv, stub)
	lis, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatalf("listen unix %s: %v", socket, err)
	}
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.GracefulStop)
	time.Sleep(5 * time.Millisecond)
	return socket
}

// seedPulledImage materialises a minimal cached rootfs (with the
// derived .ncl/config.json) so runMicroVM/RunPod skip the auto-pull
// path. XDG_DATA_HOME must already be set to a temp dir.
func seedPulledImage(t *testing.T, image string, args []string) string {
	t.Helper()
	rs := refsafe(image)
	rootfs := rootfsPath(rs)
	nclDir := filepath.Join(rootfs, ".ncl")
	if err := os.MkdirAll(nclDir, 0o755); err != nil {
		t.Fatal(err)
	}
	var p processSpec
	p.Args = args
	p.Env = []string{"PATH=/bin"}
	p.Cwd = "/"
	out, err := marshalConfig(p)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(nclDir, "config.json"), out, 0o644); err != nil {
		t.Fatal(err)
	}
	return rootfs
}

// seedKernel writes a fake kernel artefact at the default direct-Linux
// path so locateBootArtefacts resolves without env vars.
func seedKernel(t *testing.T) {
	t.Helper()
	nclDir := dataDir()
	if err := os.MkdirAll(nclDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(nclDir, "kernel"), []byte("vmlinux"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestRunMicroVM_HappyPath(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	t.Setenv("NCL_KERNEL", "")
	t.Setenv("NCL_INITRD", "")
	t.Setenv("NCL_INIT_ISO", "")
	seedPulledImage(t, "alpine:3.21", []string{"/bin/sh"})
	seedKernel(t)

	var gotRegister *vzdv1.RegisterMicroVMRequest
	var gotStart *vzdv1.StartVMRequest
	stub := &stubVzd{
		registerFn: func(_ context.Context, in *vzdv1.RegisterMicroVMRequest) (*vzdv1.RegisterMicroVMResponse, error) {
			gotRegister = in
			return &vzdv1.RegisterMicroVMResponse{}, nil
		},
		startFn: func(_ context.Context, in *vzdv1.StartVMRequest) (*vzdv1.StartVMResponse, error) {
			gotStart = in
			return &vzdv1.StartVMResponse{}, nil
		},
	}
	socket := startStubVzd(t, stub)

	err := Run(Args{Image: "alpine:3.21", VzdSocket: socket, Project: "team-net"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if gotRegister == nil || gotStart == nil {
		t.Fatal("RegisterMicroVM/StartVM not called")
	}
	if gotRegister.Name != "ncl-alpine_3.21" {
		t.Errorf("vm name = %q", gotRegister.Name)
	}
	if gotRegister.Project != "team-net" {
		t.Errorf("project not threaded: %q", gotRegister.Project)
	}
	if gotRegister.Kernel == "" || gotRegister.BootIso != "" {
		t.Errorf("expected direct-Linux boot (kernel set, no iso): %+v", gotRegister)
	}
	if len(gotRegister.Shares) != 1 || gotRegister.Shares[0].Tag != "rootfs0" || !gotRegister.Shares[0].Clone {
		t.Errorf("share wrong: %+v", gotRegister.Shares)
	}
	if !strings.Contains(gotRegister.Cmdline, "virtiofs:rootfs0") {
		t.Errorf("cmdline = %q", gotRegister.Cmdline)
	}
	if gotStart.Name != "ncl-alpine_3.21" {
		t.Errorf("start name = %q", gotStart.Name)
	}
}

func TestRunMicroVM_CustomMountTag(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	t.Setenv("NCL_KERNEL", "")
	t.Setenv("NCL_INIT_ISO", "")
	seedPulledImage(t, "alpine:3.21", []string{"/bin/sh"})
	// Only an ISO present → UKI boot mode (BootIso set, kernel empty).
	nclDir := dataDir()
	_ = os.MkdirAll(nclDir, 0o755)
	if err := os.WriteFile(filepath.Join(nclDir, "ncl-init.iso"), []byte("iso"), 0o644); err != nil {
		t.Fatal(err)
	}

	var gotRegister *vzdv1.RegisterMicroVMRequest
	stub := &stubVzd{registerFn: func(_ context.Context, in *vzdv1.RegisterMicroVMRequest) (*vzdv1.RegisterMicroVMResponse, error) {
		gotRegister = in
		return &vzdv1.RegisterMicroVMResponse{}, nil
	}}
	socket := startStubVzd(t, stub)

	if err := Run(Args{Image: "alpine:3.21", VzdSocket: socket, MountTag: "rootfsX"}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if gotRegister.BootIso == "" || gotRegister.Kernel != "" {
		t.Errorf("expected UKI boot mode: %+v", gotRegister)
	}
	if gotRegister.Shares[0].Tag != "rootfsX" {
		t.Errorf("custom mount tag lost: %+v", gotRegister.Shares)
	}
	if !strings.Contains(gotRegister.Cmdline, "virtiofs:rootfsX") {
		t.Errorf("cmdline = %q", gotRegister.Cmdline)
	}
}

func TestRunMicroVM_RegisterError(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	t.Setenv("NCL_KERNEL", "")
	t.Setenv("NCL_INIT_ISO", "")
	seedPulledImage(t, "alpine:3.21", []string{"/bin/sh"})
	seedKernel(t)
	stub := &stubVzd{registerFn: func(context.Context, *vzdv1.RegisterMicroVMRequest) (*vzdv1.RegisterMicroVMResponse, error) {
		return nil, errors.New("boom-register")
	}}
	socket := startStubVzd(t, stub)

	err := Run(Args{Image: "alpine:3.21", VzdSocket: socket})
	if err == nil || !strings.Contains(err.Error(), "RegisterMicroVM") {
		t.Fatalf("expected RegisterMicroVM error, got %v", err)
	}
}

func TestRunMicroVM_StartError(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	t.Setenv("NCL_KERNEL", "")
	t.Setenv("NCL_INIT_ISO", "")
	seedPulledImage(t, "alpine:3.21", []string{"/bin/sh"})
	seedKernel(t)
	stub := &stubVzd{startFn: func(context.Context, *vzdv1.StartVMRequest) (*vzdv1.StartVMResponse, error) {
		return nil, errors.New("boom-start")
	}}
	socket := startStubVzd(t, stub)

	err := Run(Args{Image: "alpine:3.21", VzdSocket: socket})
	if err == nil || !strings.Contains(err.Error(), "StartVM") {
		t.Fatalf("expected StartVM error, got %v", err)
	}
}

func TestRunMicroVM_DialError(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	t.Setenv("NCL_KERNEL", "")
	t.Setenv("NCL_INIT_ISO", "")
	seedPulledImage(t, "alpine:3.21", []string{"/bin/sh"})
	seedKernel(t)

	// Point at a socket nothing listens on → dial (WithBlock) times out.
	err := Run(Args{Image: "alpine:3.21", VzdSocket: "/tmp/definitely-no-vzd-here.sock"})
	if err == nil {
		t.Fatal("expected dial error")
	}
}

func TestRunMicroVM_NoBootArtefacts(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	t.Setenv("NCL_KERNEL", "")
	t.Setenv("NCL_INITRD", "")
	t.Setenv("NCL_INIT_ISO", "")
	seedPulledImage(t, "alpine:3.21", []string{"/bin/sh"})
	// No kernel / iso seeded → locateBootArtefacts errors before dial.
	err := Run(Args{Image: "alpine:3.21", VzdSocket: "/tmp/unused.sock"})
	if err == nil || !strings.Contains(err.Error(), "no ncl boot artefacts") {
		t.Fatalf("expected boot-artefact error, got %v", err)
	}
}

func TestRunMicroVM_ApplyOverridesError(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	// Seed a rootfs dir but NO .ncl/config.json so applyRunOverrides
	// (triggered by a Cmd override) fails.
	rs := refsafe("alpine:3.21")
	if err := os.MkdirAll(rootfsPath(rs), 0o755); err != nil {
		t.Fatal(err)
	}
	err := Run(Args{Image: "alpine:3.21", Cmd: []string{"sh"}, VzdSocket: "/tmp/unused.sock"})
	if err == nil || !strings.Contains(err.Error(), "config.json") {
		t.Fatalf("expected applyRunOverrides error, got %v", err)
	}
}

func TestBootArtefacts_Describe(t *testing.T) {
	cases := []struct {
		b    bootArtefacts
		want string
	}{
		{bootArtefacts{bootISO: "/x.iso"}, "iso=/x.iso"},
		{bootArtefacts{kernel: "/k", initrd: "/i"}, "kernel=/k initrd=/i"},
		{bootArtefacts{kernel: "/k"}, "kernel=/k"},
	}
	for _, c := range cases {
		if got := c.b.describe(); got != c.want {
			t.Errorf("describe(%+v) = %q, want %q", c.b, got, c.want)
		}
	}
}

func TestDialVzd_BadSocket(t *testing.T) {
	_, _, err := dialVzd("/tmp/no-such-vzd.sock")
	if err == nil {
		t.Fatal("expected dial error for missing socket")
	}
}
