package sandbox_test

import (
	"strings"
	"testing"

	"github.com/prodioslabs/cellar/internal/sandbox"
)

func TestValidateSpec(t *testing.T) {
	err := sandbox.ValidateSpec(sandbox.Spec{})
	if err == nil || !strings.Contains(err.Error(), "name is required") {
		t.Fatalf("expected name required, got %v", err)
	}
	err = sandbox.ValidateSpec(sandbox.Spec{
		Name:      "demo",
		Image:     sandbox.OCIImage("alpine:3.20"),
		Resources: sandbox.Resources{VCPUs: 1, MemoryMiB: 512},
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestNormalizeSpec(t *testing.T) {
	s := sandbox.NormalizeSpec(sandbox.Spec{
		Name:  "demo",
		Image: sandbox.RootfsSource{Reference: "busybox"},
	})
	if s.Image.Type != "oci" {
		t.Fatalf("type=%q", s.Image.Type)
	}
	if s.Resources.VCPUs != 1 || s.Resources.MemoryMiB != 512 {
		t.Fatalf("resources=%+v", s.Resources)
	}
	if s.Slug != "demo" {
		t.Fatalf("slug=%q", s.Slug)
	}
}

func TestHasHostMounts(t *testing.T) {
	s := sandbox.Spec{Mounts: []sandbox.VolumeMount{{Type: "named", Name: "v", Guest: "/data"}}}
	if s.HasHostMounts() {
		t.Fatal("named should not pin host mounts")
	}
	s.Mounts = []sandbox.VolumeMount{{Type: "bind", Host: "/tmp", Guest: "/data"}}
	if !s.HasHostMounts() {
		t.Fatal("bind should pin")
	}
}
