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
	NetworkAllowList  string `yaml:"network_allow_list"`
	DomainAllowList   string `yaml:"domain_allow_list"`
	BlockAll          *bool  `yaml:"block_all"`
	EssentialServices bool   `yaml:"essential_services"`
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
	if strings.TrimSpace(doc.Image) == "" && strings.TrimSpace(doc.Runtime) == "" {
		return nil, fmt.Errorf("image or runtime is required")
	}
	if strings.TrimSpace(doc.Image) != "" && strings.TrimSpace(doc.Runtime) != "" {
		return nil, fmt.Errorf("specify image or runtime, not both")
	}

	cidrs := strings.TrimSpace(doc.Network.NetworkAllowList)
	domains := strings.TrimSpace(doc.Network.DomainAllowList)
	hasBlock := doc.Network.BlockAll != nil
	limitCount := 0
	if cidrs != "" {
		limitCount++
	}
	if domains != "" {
		limitCount++
	}
	if hasBlock && *doc.Network.BlockAll {
		limitCount++
	}
	blockAllFalseAlone := hasBlock && !*doc.Network.BlockAll && cidrs == "" && domains == ""
	if limitCount > 1 {
		return nil, fmt.Errorf("network_allow_list, domain_allow_list, and block_all are mutually exclusive")
	}

	var netPol *cellarv1.NetworkPolicy
	if limitCount > 0 || blockAllFalseAlone {
		netPol = &cellarv1.NetworkPolicy{
			NetworkAllowList:  cidrs,
			DomainAllowList:   domains,
			EssentialServices: doc.Network.EssentialServices,
		}
		if hasBlock {
			v := *doc.Network.BlockAll
			netPol.BlockAll = &v
		}
	} else if doc.Network.EssentialServices {
		// essential_services alone implies block_all.
		v := true
		netPol = &cellarv1.NetworkPolicy{BlockAll: &v, EssentialServices: true}
	} else {
		netPol = &cellarv1.NetworkPolicy{Mode: "none"}
	}

	spec := &cellarv1.SandboxSpec{
		Image:      doc.Image,
		Env:        doc.Env,
		WorkingDir: doc.WorkingDir,
		Runtime:    doc.Runtime,
		Resources: &cellarv1.Resources{
			MemoryBytes:  doc.Resources.MemoryBytes,
			CpuNanoCores: int64(doc.Resources.CPUs * 1e9),
		},
		Network: netPol,
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
