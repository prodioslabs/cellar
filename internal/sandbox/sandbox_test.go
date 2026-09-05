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

func TestApplyLanguagePreset(t *testing.T) {
	s, err := sandbox.ApplyLanguagePreset(sandbox.Spec{Name: "x"}, "node-26")
	if err != nil {
		t.Fatal(err)
	}
	if s.Image.Reference != "node:26-alpine" {
		t.Fatalf("image=%q", s.Image.Reference)
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
