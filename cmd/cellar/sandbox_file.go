package main

import (
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v3"

	cellarv1 "github.com/prodioslabs/cellar/api/gen"
)

// sandboxCreateFile is the YAML document for `cellar sandbox create -f`.
type sandboxCreateFile struct {
	ID         string                 `yaml:"id"`
	Image      string                 `yaml:"image"`
	Command    []string               `yaml:"command"`
	Args       []string               `yaml:"args"`
	Env        []string               `yaml:"env"`
	WorkingDir string                 `yaml:"working_dir"`
	Mounts     []sandboxCreateMount   `yaml:"mounts"`
	Resources  sandboxCreateResources `yaml:"resources"`
	Network    sandboxCreateNetwork   `yaml:"network"`
	Runtime    string                 `yaml:"runtime"`
}

type sandboxCreateMount struct {
	Source   string `yaml:"source"`
	Target   string `yaml:"target"`
	ReadOnly bool   `yaml:"read_only"`
}

type sandboxCreateResources struct {
	MemoryBytes int64   `yaml:"memory_bytes"`
	CPUs        float64 `yaml:"cpus"`
}

type sandboxCreateNetwork struct {
	Mode       string   `yaml:"mode"`
	AllowHosts []string `yaml:"allow_hosts"`
	AllowPorts []uint32 `yaml:"allow_ports"`
}

func loadSandboxCreateFile(path string) (*cellarv1.SandboxCreateRequest, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return parseSandboxCreateFile(data)
}

func parseSandboxCreateFile(data []byte) (*cellarv1.SandboxCreateRequest, error) {
	var doc sandboxCreateFile
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("parse yaml: %w", err)
	}
	if strings.TrimSpace(doc.Image) == "" {
		return nil, fmt.Errorf("image is required")
	}

	mode := doc.Network.Mode
	if mode == "" {
		mode = "none"
	}

	spec := &cellarv1.SandboxSpec{
		Image:      doc.Image,
		Command:    doc.Command,
		Args:       doc.Args,
		Env:        doc.Env,
		WorkingDir: doc.WorkingDir,
		Runtime:    doc.Runtime,
		Resources: &cellarv1.Resources{
			MemoryBytes:  doc.Resources.MemoryBytes,
			CpuNanoCores: int64(doc.Resources.CPUs * 1e9),
		},
		Network: networkPolicyFromAllow(mode, doc.Network.AllowHosts, doc.Network.AllowPorts),
	}
	for _, m := range doc.Mounts {
		if m.Source == "" || m.Target == "" {
			return nil, fmt.Errorf("mount source and target are required")
		}
		spec.Mounts = append(spec.Mounts, &cellarv1.Mount{
			Source:   m.Source,
			Target:   m.Target,
			ReadOnly: m.ReadOnly,
		})
	}

	return &cellarv1.SandboxCreateRequest{
		Spec:      spec,
		SandboxId: doc.ID,
	}, nil
}

// networkPolicyFromAllow builds NetworkPolicy the same way as the flag path.
func networkPolicyFromAllow(mode string, hosts []string, ports []uint32) *cellarv1.NetworkPolicy {
	np := &cellarv1.NetworkPolicy{Mode: mode}
	if mode == "allowlist" || mode == "denylist" {
		np.Rules = []*cellarv1.NetworkRule{{
			Hosts:     hosts,
			Ports:     ports,
			Protocols: []string{"tcp"},
		}}
		np.Dns = &cellarv1.DNSPolicy{Mode: mode, Names: hosts}
	}
	return np
}
