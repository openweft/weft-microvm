package microvm

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestLocateBootArtefacts_MissingErrorIsHelpful(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_DATA_HOME", tmp)
	t.Setenv("NCL_INIT_ISO", "")
	t.Setenv("NCL_KERNEL", "")
	t.Setenv("NCL_INITRD", "")

	_, err := locateBootArtefacts()
	if err == nil {
		t.Fatalf("expected error when no boot artefacts are anywhere")
	}
	msg := err.Error()
	for _, expected := range []string{"NCL_KERNEL", "NCL_INIT_ISO", "direct-Linux"} {
		if !strings.Contains(msg, expected) {
			t.Errorf("error message should mention %q, got: %s", expected, msg)
		}
	}
}

func TestLocateBootArtefacts_ISOFromEnv(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_DATA_HOME", tmp)
	t.Setenv("NCL_KERNEL", "")
	iso := filepath.Join(tmp, "custom.iso")
	if err := os.WriteFile(iso, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("NCL_INIT_ISO", iso)

	got, err := locateBootArtefacts()
	if err != nil {
		t.Fatal(err)
	}
	if got.bootISO != iso || got.kernel != "" {
		t.Errorf("unexpected resolution: %+v", got)
	}
}

func TestLocateBootArtefacts_ISOFromXDGDataHome(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_DATA_HOME", tmp)
	t.Setenv("NCL_INIT_ISO", "")
	t.Setenv("NCL_KERNEL", "")

	iso := filepath.Join(tmp, "weft-microvm", "ncl-init.iso")
	if err := os.MkdirAll(filepath.Dir(iso), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(iso, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := locateBootArtefacts()
	if err != nil {
		t.Fatal(err)
	}
	if got.bootISO != iso {
		t.Errorf("got %+v, want bootISO=%q", got, iso)
	}
}

func TestLocateBootArtefacts_DirectLinuxFromEnv(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_DATA_HOME", tmp)
	t.Setenv("NCL_INIT_ISO", "")
	kernel := filepath.Join(tmp, "kernel")
	initrd := filepath.Join(tmp, "initrd")
	for _, p := range []string{kernel, initrd} {
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("NCL_KERNEL", kernel)
	t.Setenv("NCL_INITRD", initrd)

	got, err := locateBootArtefacts()
	if err != nil {
		t.Fatal(err)
	}
	if got.kernel != kernel || got.initrd != initrd || got.bootISO != "" {
		t.Errorf("unexpected resolution: %+v", got)
	}
}

func TestLocateBootArtefacts_DirectLinuxPrefersOverISO(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_DATA_HOME", tmp)
	t.Setenv("NCL_INIT_ISO", "")
	t.Setenv("NCL_KERNEL", "")
	t.Setenv("NCL_INITRD", "")

	nclDir := filepath.Join(tmp, "weft-microvm")
	if err := os.MkdirAll(nclDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// Both artefact sets live at the defaults — the kernel one wins.
	for _, p := range []string{filepath.Join(nclDir, "kernel"), filepath.Join(nclDir, "initrd"), filepath.Join(nclDir, "ncl-init.iso")} {
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	got, err := locateBootArtefacts()
	if err != nil {
		t.Fatal(err)
	}
	if got.kernel == "" {
		t.Errorf("expected kernel to win over iso when both are present, got %+v", got)
	}
	if got.bootISO != "" {
		t.Errorf("bootISO should be empty when kernel was selected, got %+v", got)
	}
}

func TestApplyRunOverrides_NoCmd_LeavesConfigAlone(t *testing.T) {
	rootfs := t.TempDir()
	nclDir := filepath.Join(rootfs, ".ncl")
	if err := os.MkdirAll(nclDir, 0o755); err != nil {
		t.Fatal(err)
	}
	before := `{"process":{"args":["/entry"],"env":["X=1"],"cwd":"/srv","user":{"uid":33,"gid":33}}}`
	cfgPath := filepath.Join(nclDir, "config.json")
	if err := os.WriteFile(cfgPath, []byte(before), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := applyRunOverrides(rootfs, Args{Image: "x:1"}); err != nil {
		t.Fatal(err)
	}

	// File should be untouched (byte-for-byte).
	got, _ := os.ReadFile(cfgPath)
	if string(got) != before {
		t.Errorf("config rewritten despite no Cmd override:\n was %s\n now %s", before, got)
	}
}

func TestApplyRunOverrides_WithCmd_RewritesArgsOnly(t *testing.T) {
	rootfs := t.TempDir()
	nclDir := filepath.Join(rootfs, ".ncl")
	_ = os.MkdirAll(nclDir, 0o755)
	cfgPath := filepath.Join(nclDir, "config.json")
	_ = os.WriteFile(cfgPath, []byte(`{
		"process": {
			"args": ["/entry"],
			"env":  ["X=1"],
			"cwd":  "/srv",
			"user": { "uid": 33, "gid": 33 }
		}
	}`), 0o644)

	if err := applyRunOverrides(rootfs, Args{Image: "x:1", Cmd: []string{"sh", "-c", "exit 0"}}); err != nil {
		t.Fatal(err)
	}

	b, _ := os.ReadFile(cfgPath)
	var cf configFile
	if err := json.Unmarshal(b, &cf); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(cf.Process.Args, []string{"sh", "-c", "exit 0"}) {
		t.Errorf("Args not rewritten: %v", cf.Process.Args)
	}
	// Env / Cwd / User must survive.
	if cf.Process.Cwd != "/srv" || cf.Process.User.UID != 33 || cf.Process.Env[0] != "X=1" {
		t.Errorf("non-Cmd fields were clobbered: %+v", cf.Process)
	}
}

func TestApplyRunOverrides_MissingConfig_Errors(t *testing.T) {
	rootfs := t.TempDir()
	if err := applyRunOverrides(rootfs, Args{Image: "x:1", Cmd: []string{"sh"}}); err == nil {
		t.Fatal("expected error when .ncl/config.json absent")
	}
}

func TestApplyRunOverrides_BadJSON_Errors(t *testing.T) {
	rootfs := t.TempDir()
	nclDir := filepath.Join(rootfs, ".ncl")
	if err := os.MkdirAll(nclDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(nclDir, "config.json"), []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	err := applyRunOverrides(rootfs, Args{Image: "x:1", Cmd: []string{"sh"}})
	if err == nil || !strings.Contains(err.Error(), "decode") {
		t.Fatalf("expected decode error, got %v", err)
	}
}

func TestApplyRunOverrides_WriteError(t *testing.T) {
	rootfs := t.TempDir()
	nclDir := filepath.Join(rootfs, ".ncl")
	if err := os.MkdirAll(nclDir, 0o755); err != nil {
		t.Fatal(err)
	}
	cfgPath := filepath.Join(nclDir, "config.json")
	if err := os.WriteFile(cfgPath, []byte(`{"process":{"args":["/x"]}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	// Make the .ncl dir read-only so the rewrite (WriteFile with
	// O_TRUNC of an existing file) fails. chmod 0o500 keeps the file
	// readable for the initial ReadFile but blocks the write.
	if err := os.Chmod(cfgPath, 0o400); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(nclDir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(nclDir, 0o755) })
	err := applyRunOverrides(rootfs, Args{Image: "x:1", Cmd: []string{"sh"}})
	if err == nil || !strings.Contains(err.Error(), "write") {
		t.Fatalf("expected write error, got %v", err)
	}
}
