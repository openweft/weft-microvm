package microvm

import (
	"strings"
	"testing"
)

// TestRun_UnpulledImage_NoAutoPull_HintsAtPull asserts that when
// WEFT_NO_AUTO_PULL=1 (strict offline mode) the error mentions
// `weft-microvm pull` so the operator knows the explicit recovery path.
// Auto-pull is the default and is exercised by
// TestRun_UnpulledImage_AutoPullAttempted below.
func TestRun_UnpulledImage_NoAutoPull_HintsAtPull(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	t.Setenv("WEFT_NO_AUTO_PULL", "1")

	err := Run(Args{Image: "definitely-not-pulled-image:0.0.0", MountTag: "rootfs0"})
	if err == nil {
		t.Fatal("expected error for unpulled image under WEFT_NO_AUTO_PULL=1")
	}
	if !strings.Contains(err.Error(), "weft-microvm pull") {
		t.Errorf("error message should suggest `weft-microvm pull`, got: %s", err)
	}
}

// TestRun_UnpulledImage_AutoPullAttempted asserts that the default
// behaviour is to attempt an auto-pull on cache miss. The pull
// itself will fail (the image doesn't exist), but the error surface
// should mention auto-pull rather than the offline "weft-microvm pull" hint —
// so the user sees they hit a network/registry issue, not a missing
// step.
func TestRun_UnpulledImage_AutoPullAttempted(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	t.Setenv("WEFT_NO_AUTO_PULL", "")

	err := Run(Args{Image: "definitely-not-pulled-image:0.0.0", MountTag: "rootfs0"})
	if err == nil {
		t.Fatal("expected error when the auto-pull target doesn't exist")
	}
	if !strings.Contains(err.Error(), "auto-pull") {
		t.Errorf("error message should mention `auto-pull`, got: %s", err)
	}
}

// TestRun_PodModeDispatch verifies Run routes to the pod-mode branch
// when Args.Pod is set: a non-existent manifest path surfaces the
// pod manifest read error (not the single-container image error).
func TestRun_PodModeDispatch(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())

	err := Run(Args{Pod: "/definitely/not/a/manifest.json"})
	if err == nil {
		t.Fatal("expected error for missing pod manifest")
	}
	if !strings.Contains(err.Error(), "pod manifest") {
		t.Errorf("error should mention `pod manifest` (pod-mode dispatch), got: %s", err)
	}
}

// TestExpandDockerHubShorthand covers the reference-rewrite rules
// the lib applies before handing the ref to the OCI client.
func TestExpandDockerHubShorthand(t *testing.T) {
	cases := []struct{ in, want string }{
		{"alpine", "registry-1.docker.io/library/alpine"},
		{"alpine:3.21", "registry-1.docker.io/library/alpine:3.21"},
		{"library/alpine", "registry-1.docker.io/library/alpine"},
		{"myorg/myimage:v1", "registry-1.docker.io/myorg/myimage:v1"},
		// Already-FQDN refs (host carries a "." or ":port") pass through.
		{"ghcr.io/foo/bar", "ghcr.io/foo/bar"},
		{"quay.io/baz:tag", "quay.io/baz:tag"},
		{"registry.example.com:5000/svc/img", "registry.example.com:5000/svc/img"},
	}
	for _, c := range cases {
		t.Run(c.in, func(t *testing.T) {
			if got := expandDockerHubShorthand(c.in); got != c.want {
				t.Errorf("expandDockerHubShorthand(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}
