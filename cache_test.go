package microvm

import (
	"strings"
	"testing"
)

func TestRefsafe(t *testing.T) {
	cases := []struct{ in, want string }{
		{"alpine:3.21", "alpine_3.21"},
		{"ghcr.io/owner/repo:v1.2.3", "ghcr.io_owner_repo_v1.2.3"},
		{"registry.example.com:5000/repo:tag", "registry.example.com_5000_repo_tag"},
		{"plainname", "plainname"},
	}
	for _, c := range cases {
		t.Run(c.in, func(t *testing.T) {
			got := refsafe(c.in)
			if got != c.want {
				t.Errorf("got %q, want %q", got, c.want)
			}
			if strings.ContainsAny(got, "/:") {
				t.Errorf("refsafe should not contain / or :, got %q", got)
			}
		})
	}
}

func TestDataHome_XDGAndFallbacks(t *testing.T) {
	t.Run("XDG_DATA_HOME wins", func(t *testing.T) {
		t.Setenv("XDG_DATA_HOME", "/custom/data")
		if got := dataHome(); got != "/custom/data" {
			t.Errorf("got %q", got)
		}
	})
	t.Run("home fallback", func(t *testing.T) {
		t.Setenv("XDG_DATA_HOME", "")
		// HOME set → ~/.local/share.
		t.Setenv("HOME", "/home/tester")
		got := dataHome()
		if !strings.HasSuffix(got, "/.local/share") {
			t.Errorf("expected ~/.local/share suffix, got %q", got)
		}
	})
	t.Run("/tmp fallback when no home", func(t *testing.T) {
		t.Setenv("XDG_DATA_HOME", "")
		// Clearing HOME makes os.UserHomeDir error on unix → /tmp.
		t.Setenv("HOME", "")
		if got := dataHome(); got != "/tmp" {
			t.Errorf("expected /tmp fallback, got %q", got)
		}
	})
}

func TestImageRoot_RootfsPath_Stable(t *testing.T) {
	r1 := imageRoot("alpine_3.21")
	r2 := imageRoot("alpine_3.21")
	if r1 != r2 {
		t.Errorf("non-deterministic: %q vs %q", r1, r2)
	}
	rfs := rootfsPath("alpine_3.21")
	if !strings.HasPrefix(rfs, r1) || !strings.HasSuffix(rfs, "/rootfs") {
		t.Errorf("rootfsPath shape unexpected: %q (under %q)", rfs, r1)
	}
}
