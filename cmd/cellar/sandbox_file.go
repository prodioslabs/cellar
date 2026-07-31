package main

import (
	"fmt"
	"io"
	"net"
	"os"
	"strconv"
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
	Mode               string   `yaml:"mode"`
	AllowHosts         []string `yaml:"allow_hosts"`
	AllowPorts         []uint32 `yaml:"allow_ports"`
	NetworkAllowList   string   `yaml:"network_allow_list"`
	DomainAllowList    string   `yaml:"domain_allow_list"`
	BlockAll           *bool    `yaml:"block_all"`
	EssentialServices  bool     `yaml:"essential_services"`
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

	mode := doc.Network.Mode
	if mode == "" {
		mode = "none"
	}

	sugarCIDRs := strings.TrimSpace(doc.Network.NetworkAllowList)
	sugarDomains := strings.TrimSpace(doc.Network.DomainAllowList)
	sugarBlock := doc.Network.BlockAll != nil
	sugarCount := 0
	if sugarCIDRs != "" {
		sugarCount++
	}
	if sugarDomains != "" {
		sugarCount++
	}
	if sugarBlock && *doc.Network.BlockAll {
		sugarCount++
	}
	blockAllFalseAlone := sugarBlock && !*doc.Network.BlockAll && sugarCIDRs == "" && sugarDomains == ""
	structured := doc.Network.Mode != "" || len(doc.Network.AllowHosts) > 0 || len(doc.Network.AllowPorts) > 0
	if (sugarCount > 0 || blockAllFalseAlone) && structured {
		return nil, fmt.Errorf("cannot combine network_allow_list/domain_allow_list/block_all with mode/allow_hosts/allow_ports")
	}
	if sugarCount > 1 {
		return nil, fmt.Errorf("network_allow_list, domain_allow_list, and block_all are mutually exclusive")
	}

	var netPol *cellarv1.NetworkPolicy
	if sugarCount > 0 || blockAllFalseAlone {
		netPol = &cellarv1.NetworkPolicy{
			NetworkAllowList:  sugarCIDRs,
			DomainAllowList:   sugarDomains,
			EssentialServices: doc.Network.EssentialServices,
		}
		if sugarBlock {
			v := *doc.Network.BlockAll
			netPol.BlockAll = &v
		}
	} else {
		if mode == "allowlist" || mode == "denylist" {
			warnUnmatchableHostRules(os.Stderr, doc.Network.AllowHosts, doc.Network.AllowPorts)
		}
		netPol = networkPolicyFromAllow(mode, doc.Network.AllowHosts, doc.Network.AllowPorts)
		netPol.EssentialServices = doc.Network.EssentialServices
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

// networkPolicyFromAllow builds NetworkPolicy the same way as the flag path.
func networkPolicyFromAllow(mode string, hosts []string, ports []uint32) *cellarv1.NetworkPolicy {
	np := &cellarv1.NetworkPolicy{Mode: mode}
	switch mode {
	case "allowlist", "denylist":
		np.Rules = []*cellarv1.NetworkRule{{
			Hosts:     hosts,
			Ports:     ports,
			Protocols: []string{"tcp"},
		}}
		np.Dns = &cellarv1.DNSPolicy{Mode: mode, Names: hosts}
	case "blockall":
		np.Dns = &cellarv1.DNSPolicy{Mode: "none"}
	}
	return np
}

// warnUnmatchableHostRules reports host rules that can never match, because a
// hostname is only observable on port 80 (Host header) and 443 (TLS SNI).
func warnUnmatchableHostRules(w io.Writer, hosts []string, ports []uint32) {
	if len(ports) == 0 {
		return
	}
	var names []string
	for _, h := range hosts {
		h = strings.TrimSpace(h)
		if h == "" || strings.Contains(h, "/") || net.ParseIP(h) != nil {
			continue
		}
		names = append(names, h)
	}
	if len(names) == 0 {
		return
	}
	var unmatchable []string
	for _, p := range ports {
		if p != 80 && p != 443 {
			unmatchable = append(unmatchable, strconv.FormatUint(uint64(p), 10))
		}
	}
	if len(unmatchable) == 0 {
		return
	}
	fmt.Fprintf(w, "warning: hostname rules (%s) are ignored on port(s) %s; "+
		"only ports 80 and 443 expose a hostname. Use an IP or CIDR instead.\n",
		strings.Join(names, ", "), strings.Join(unmatchable, ", "))
}
