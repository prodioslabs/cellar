package sandbox_test

import (
	"strings"
	"testing"

	"github.com/prodioslabs/cellar/internal/sandbox"
)

func TestValidateSpec(t *testing.T) {
	err := sandbox.ValidateSpec(sandbox.Spec{})
	if err == nil || !strings.Contains(err.Error(), "image or runtime is required") {
		t.Fatalf("expected image or runtime required, got %v", err)
	}
	err = sandbox.ValidateSpec(sandbox.Spec{
		Image: "alpine",
		Network: sandbox.NetworkPolicy{
			Mode: sandbox.NetworkAllowlist,
			Rules: []sandbox.NetworkRule{{Hosts: []string{"example.com"}, Ports: []uint32{443}}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestValidateSpecRuntime(t *testing.T) {
	err := sandbox.ValidateSpec(sandbox.Spec{Runtime: "node-26"})
	if err != nil {
		t.Fatal(err)
	}
	err = sandbox.ValidateSpec(sandbox.Spec{Image: "alpine", Runtime: "node-26"})
	if err == nil || !strings.Contains(err.Error(), "not both") {
		t.Fatalf("expected not both error, got %v", err)
	}
	err = sandbox.ValidateSpec(sandbox.Spec{Runtime: "node-99"})
	if err == nil || !strings.Contains(err.Error(), "unknown runtime") {
		t.Fatalf("expected unknown runtime, got %v", err)
	}
	// Resolved pair (normalize already filled image) is OK.
	err = sandbox.ValidateSpec(sandbox.Spec{Image: "node:26-alpine", Runtime: "node-26"})
	if err != nil {
		t.Fatal(err)
	}
}

func TestNormalizeSpec(t *testing.T) {
	s := sandbox.NormalizeSpec(sandbox.Spec{Image: "busybox"})
	if s.Runtime != "" {
		t.Fatalf("runtime=%q", s.Runtime)
	}
	if s.Network.Mode != sandbox.NetworkNone {
		t.Fatalf("mode=%q", s.Network.Mode)
	}
}

func TestNormalizeSpecRuntime(t *testing.T) {
	s := sandbox.NormalizeSpec(sandbox.Spec{Runtime: "python-3.13"})
	if s.Image != "astral/uv:python3.13-alpine" {
		t.Fatalf("image=%q", s.Image)
	}
	if s.Runtime != "python-3.13" {
		t.Fatalf("runtime=%q", s.Runtime)
	}
}

func TestNormalizeSpecLegacyOCIRuntime(t *testing.T) {
	for _, legacy := range []string{"runsc", "runc"} {
		s := sandbox.NormalizeSpec(sandbox.Spec{Image: "alpine", Runtime: legacy})
		if s.Runtime != "" {
			t.Fatalf("expected legacy %q cleared, got %q", legacy, s.Runtime)
		}
		if s.Image != "alpine" {
			t.Fatalf("image=%q", s.Image)
		}
	}
}

func TestResolveImage(t *testing.T) {
	cases := map[string]string{
		"node-26":     "node:26-alpine",
		"bun-1.3":     "oven/bun:1.3-alpine",
		"python-3.13": "astral/uv:python3.13-alpine",
		"go-1.26":     "golang:1.26-alpine",
	}
	for id, want := range cases {
		got, err := sandbox.ResolveImage(id)
		if err != nil {
			t.Fatalf("%s: %v", id, err)
		}
		if got != want {
			t.Fatalf("%s: got %q want %q", id, got, want)
		}
	}
}
