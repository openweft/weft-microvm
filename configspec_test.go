package microvm

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

func TestProcessFromImageConfig_Entrypoint_Plus_Cmd(t *testing.T) {
	c := ocispec.ImageConfig{
		Entrypoint: []string{"/usr/bin/myapp"},
		Cmd:        []string{"--server", "--port=8080"},
		Env:        []string{"FOO=bar", "PATH=/usr/bin"},
		WorkingDir: "/data",
		User:       "1000:1000",
	}
	p, err := processFromImageConfig(c)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(p.Args, []string{"/usr/bin/myapp", "--server", "--port=8080"}) {
		t.Errorf("Args = %v", p.Args)
	}
	if p.Cwd != "/data" || p.User.UID != 1000 || p.User.GID != 1000 {
		t.Errorf("cwd=%q uid=%d gid=%d", p.Cwd, p.User.UID, p.User.GID)
	}
	if !reflect.DeepEqual(p.Env, []string{"FOO=bar", "PATH=/usr/bin"}) {
		t.Errorf("Env = %v", p.Env)
	}
}

func TestProcessFromImageConfig_OnlyCmd(t *testing.T) {
	c := ocispec.ImageConfig{
		Cmd: []string{"/bin/sh"},
	}
	p, _ := processFromImageConfig(c)
	if !reflect.DeepEqual(p.Args, []string{"/bin/sh"}) {
		t.Errorf("Args = %v", p.Args)
	}
	if p.Cwd != "/" {
		t.Errorf("default cwd lost: %q", p.Cwd)
	}
	if len(p.Env) == 0 {
		t.Errorf("default Env empty")
	}
}

func TestProcessFromImageConfig_Empty_Fallback(t *testing.T) {
	p, err := processFromImageConfig(ocispec.ImageConfig{})
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Args) != 1 || p.Args[0] != "/bin/sh" {
		t.Errorf("empty image config should fall back to /bin/sh; got %v", p.Args)
	}
}

func TestParseImageUser(t *testing.T) {
	cases := []struct {
		in       string
		uid, gid uint32
		err      bool
	}{
		{"1000", 1000, 0, false},
		{"1000:1000", 1000, 1000, false},
		{"0:0", 0, 0, false},
		{"33:33", 33, 33, false},
		// Named forms: deferred to ncl-init via /etc/passwd lookup,
		// returning an explicit error today.
		{"root", 0, 0, true},
		{"user:group", 0, 0, true},
		{"1000:group", 0, 0, true},
	}
	for _, c := range cases {
		t.Run(c.in, func(t *testing.T) {
			uid, gid, err := parseImageUser(c.in)
			if (err != nil) != c.err {
				t.Fatalf("err=%v wantErr=%v", err, c.err)
			}
			if err != nil {
				if !strings.Contains(err.Error(), "named") {
					t.Errorf("error %q should mention `named`", err)
				}
				return
			}
			if uid != c.uid || gid != c.gid {
				t.Errorf("uid=%d gid=%d (want %d %d)", uid, gid, c.uid, c.gid)
			}
		})
	}
}

func TestApplyUserOverrides_CmdReplaces(t *testing.T) {
	base, _ := processFromImageConfig(ocispec.ImageConfig{
		Entrypoint: []string{"/entry"},
		Cmd:        []string{"--default"},
	})
	if !reflect.DeepEqual(base.Args, []string{"/entry", "--default"}) {
		t.Fatalf("setup: %v", base.Args)
	}
	out := applyUserOverrides(base, Args{Cmd: []string{"sh", "-c", "echo hi"}})
	if !reflect.DeepEqual(out.Args, []string{"sh", "-c", "echo hi"}) {
		t.Errorf("override args lost: %v", out.Args)
	}
}

func TestApplyUserOverrides_NoOverride_PreservesBase(t *testing.T) {
	base, _ := processFromImageConfig(ocispec.ImageConfig{
		Entrypoint: []string{"/entry"},
		Cmd:        []string{"--default"},
	})
	out := applyUserOverrides(base, Args{})
	if !reflect.DeepEqual(out.Args, base.Args) {
		t.Errorf("base mutated: was %v, now %v", base.Args, out.Args)
	}
}

func TestMarshalConfig_Shape(t *testing.T) {
	p, _ := processFromImageConfig(ocispec.ImageConfig{
		Cmd: []string{"/bin/true"},
	})
	b, err := marshalConfig(p)
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Process processSpec `json:"process"`
	}
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("round-trip decode: %v", err)
	}
	if got.Process.Args[0] != "/bin/true" {
		t.Errorf("round-trip mismatch: %+v", got.Process)
	}
}
