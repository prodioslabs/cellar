package sandbox_test

import (
	"testing"

	"github.com/prodioslabs/cellar/internal/sandbox"
)

func TestValidateSpec(t *testing.T) {
	err := sandbox.ValidateSpec(sandbox.Spec{})
	if err == nil {
		t.Fatal("expected error for empty image")
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

func TestNormalizeSpec(t *testing.T) {
	s := sandbox.NormalizeSpec(sandbox.Spec{Image: "busybox"})
	if s.Runtime != sandbox.DefaultRuntime {
		t.Fatalf("runtime=%q", s.Runtime)
	}
	if s.Network.Mode != sandbox.NetworkNone {
		t.Fatalf("mode=%q", s.Network.Mode)
	}
}
