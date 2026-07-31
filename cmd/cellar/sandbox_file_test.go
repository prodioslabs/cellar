package main

import (
	"strings"
	"testing"
)

func TestParseSandboxCreateFileNone(t *testing.T) {
	const yamlDoc = `
id: demo-alpine
image: alpine
`
	req, err := parseSandboxCreateFile([]byte(yamlDoc))
	if err != nil {
		t.Fatal(err)
	}
	if req.SandboxId != "demo-alpine" {
		t.Fatalf("id: got %q", req.SandboxId)
	}
	if req.Spec.Image != "alpine" {
		t.Fatalf("image: got %q", req.Spec.Image)
	}
	if req.Spec.Network.Mode != "none" {
		t.Fatalf("network mode: got %q", req.Spec.Network.Mode)
	}
	if len(req.Spec.Network.Rules) != 0 {
		t.Fatalf("expected no rules, got %#v", req.Spec.Network.Rules)
	}
}

func TestParseSandboxCreateFileAllowlist(t *testing.T) {
	const yamlDoc = `
id: demo-curl
image: curlimages/curl
resources:
  memory_bytes: 268435456
  cpus: 0.5
network:
  domain_allow_list: example.com
`
	req, err := parseSandboxCreateFile([]byte(yamlDoc))
	if err != nil {
		t.Fatal(err)
	}
	if req.Spec.Resources.MemoryBytes != 268435456 {
		t.Fatalf("memory: got %d", req.Spec.Resources.MemoryBytes)
	}
	if req.Spec.Resources.CpuNanoCores != 500000000 {
		t.Fatalf("cpu nano: got %d", req.Spec.Resources.CpuNanoCores)
	}
	if req.Spec.Network.GetDomainAllowList() != "example.com" {
		t.Fatalf("domain_allow_list: got %q", req.Spec.Network.GetDomainAllowList())
	}
	if req.Spec.Network.Mode != "" {
		t.Fatalf("mode should be empty (limits), got %q", req.Spec.Network.Mode)
	}
}

func TestParseSandboxCreateFileMissingImage(t *testing.T) {
	_, err := parseSandboxCreateFile([]byte("network:\n  block_all: true\n"))
	if err == nil || !strings.Contains(err.Error(), "image or runtime is required") {
		t.Fatalf("expected image or runtime required error, got %v", err)
	}
}

func TestParseSandboxCreateFileRuntime(t *testing.T) {
	const yamlDoc = `
id: demo-node
runtime: node-26
`
	req, err := parseSandboxCreateFile([]byte(yamlDoc))
	if err != nil {
		t.Fatal(err)
	}
	if req.Spec.Runtime != "node-26" {
		t.Fatalf("runtime: got %q", req.Spec.Runtime)
	}
	if req.Spec.Image != "" {
		t.Fatalf("image should be empty pre-resolve, got %q", req.Spec.Image)
	}
}

func TestParseSandboxCreateFileBothImageAndRuntime(t *testing.T) {
	_, err := parseSandboxCreateFile([]byte("image: alpine\nruntime: node-26\n"))
	if err == nil || !strings.Contains(err.Error(), "not both") {
		t.Fatalf("expected not both error, got %v", err)
	}
}

func TestParseSandboxCreateFileMounts(t *testing.T) {
	const yamlDoc = `
image: alpine
mounts:
  - source: /tmp/host
    target: /tmp/guest
    read_only: true
`
	req, err := parseSandboxCreateFile([]byte(yamlDoc))
	if err != nil {
		t.Fatal(err)
	}
	if len(req.Spec.Mounts) != 1 {
		t.Fatalf("mounts: got %d", len(req.Spec.Mounts))
	}
	m := req.Spec.Mounts[0]
	if m.Source != "/tmp/host" || m.Target != "/tmp/guest" || !m.ReadOnly {
		t.Fatalf("mount: got %#v", m)
	}
}

func TestLoadSandboxCreateFileExamples(t *testing.T) {
	for _, path := range []string{
		"../../examples/sandbox.yaml",
		"../../examples/sandbox-allowlist.yaml",
		"../../examples/sandbox-domain-allowlist.yaml",
		"../../examples/sandbox-block-all.yaml",
		"../../examples/sandbox-allow-all.yaml",
		"../../examples/sandbox-essential-services.yaml",
		"../../examples/sandbox-runtime.yaml",
	} {
		req, err := loadSandboxCreateFile(path)
		if err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		if req.Spec.Image == "" && req.Spec.Runtime == "" {
			t.Fatalf("%s: empty image and runtime", path)
		}
	}
}

func TestParseSandboxCreateFileDomainAllowList(t *testing.T) {
	const yamlDoc = `
id: demo-domains
image: curlimages/curl
network:
  domain_allow_list: example.com,*.openai.com
  essential_services: true
`
	req, err := parseSandboxCreateFile([]byte(yamlDoc))
	if err != nil {
		t.Fatal(err)
	}
	if req.Spec.Network.GetDomainAllowList() != "example.com,*.openai.com" {
		t.Fatalf("domain_allow_list: got %q", req.Spec.Network.GetDomainAllowList())
	}
	if !req.Spec.Network.EssentialServices {
		t.Fatal("expected essential_services")
	}
	if req.Spec.Network.Mode != "" {
		t.Fatalf("mode should be empty (limits), got %q", req.Spec.Network.Mode)
	}
}

func TestParseSandboxCreateFileBlockAll(t *testing.T) {
	const yamlDoc = `
image: alpine
network:
  block_all: true
`
	req, err := parseSandboxCreateFile([]byte(yamlDoc))
	if err != nil {
		t.Fatal(err)
	}
	if req.Spec.Network.BlockAll == nil || !*req.Spec.Network.BlockAll {
		t.Fatal("expected block_all true")
	}
}

func TestParseSandboxCreateFileAllowAll(t *testing.T) {
	const yamlDoc = `
image: alpine
network:
  allow_all: true
`
	req, err := parseSandboxCreateFile([]byte(yamlDoc))
	if err != nil {
		t.Fatal(err)
	}
	if req.Spec.Network.AllowAll == nil || !*req.Spec.Network.AllowAll {
		t.Fatal("expected allow_all true")
	}
	if req.Spec.Network.Mode != "" {
		t.Fatalf("mode should be empty (limits), got %q", req.Spec.Network.Mode)
	}
}

func TestParseSandboxCreateFileEssentialServicesAlone(t *testing.T) {
	const yamlDoc = `
image: alpine
network:
  essential_services: true
`
	req, err := parseSandboxCreateFile([]byte(yamlDoc))
	if err != nil {
		t.Fatal(err)
	}
	if !req.Spec.Network.EssentialServices {
		t.Fatal("expected essential_services")
	}
	if req.Spec.Network.BlockAll == nil || !*req.Spec.Network.BlockAll {
		t.Fatal("expected block_all implied by essential_services alone")
	}
	if req.Spec.Network.Mode != "" {
		t.Fatalf("mode should be empty (limits), got %q", req.Spec.Network.Mode)
	}
}

func TestParseSandboxCreateFileLimitConflict(t *testing.T) {
	_, err := parseSandboxCreateFile([]byte(`
image: alpine
network:
  domain_allow_list: example.com
  block_all: true
`))
	if err == nil || !strings.Contains(err.Error(), "mutually exclusive") {
		t.Fatalf("expected mutually exclusive, got %v", err)
	}
}
