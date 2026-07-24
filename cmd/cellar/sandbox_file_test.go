package main

import (
	"strings"
	"testing"
)

func TestParseSandboxCreateFileNone(t *testing.T) {
	const yamlDoc = `
id: demo-alpine
image: alpine
network:
  mode: none
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
  mode: allowlist
  allow_hosts:
    - example.com
  allow_ports:
    - 443
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
	if req.Spec.Network.Mode != "allowlist" {
		t.Fatalf("mode: got %q", req.Spec.Network.Mode)
	}
	if len(req.Spec.Network.Rules) != 1 {
		t.Fatalf("rules: got %d", len(req.Spec.Network.Rules))
	}
	rule := req.Spec.Network.Rules[0]
	if len(rule.Hosts) != 1 || rule.Hosts[0] != "example.com" {
		t.Fatalf("hosts: got %#v", rule.Hosts)
	}
	if len(rule.Ports) != 1 || rule.Ports[0] != 443 {
		t.Fatalf("ports: got %#v", rule.Ports)
	}
	if len(rule.Protocols) != 1 || rule.Protocols[0] != "tcp" {
		t.Fatalf("protocols: got %#v", rule.Protocols)
	}
	if req.Spec.Network.Dns == nil || req.Spec.Network.Dns.Mode != "allowlist" {
		t.Fatalf("dns: got %#v", req.Spec.Network.Dns)
	}
	if len(req.Spec.Network.Dns.Names) != 1 || req.Spec.Network.Dns.Names[0] != "example.com" {
		t.Fatalf("dns names: got %#v", req.Spec.Network.Dns.Names)
	}
}

func TestParseSandboxCreateFileMissingImage(t *testing.T) {
	_, err := parseSandboxCreateFile([]byte("network:\n  mode: none\n"))
	if err == nil || !strings.Contains(err.Error(), "image is required") {
		t.Fatalf("expected image required error, got %v", err)
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
	} {
		req, err := loadSandboxCreateFile(path)
		if err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		if req.Spec.Image == "" {
			t.Fatalf("%s: empty image", path)
		}
	}
}
