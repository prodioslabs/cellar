package sandbox

import (
	"time"

	cellarv1 "github.com/prodioslabs/cellar/api/gen"
)

// SpecFromProto converts a proto spec.
func SpecFromProto(p *cellarv1.SandboxSpec) Spec {
	if p == nil {
		return Spec{}
	}
	spec := Spec{
		Image:      p.Image,
		Command:    append([]string(nil), p.Command...),
		Args:       append([]string(nil), p.Args...),
		Env:        append([]string(nil), p.Env...),
		WorkingDir: p.WorkingDir,
		Runtime:    p.Runtime,
	}
	if p.Resources != nil {
		spec.Resources = Resources{
			CPUNanoCores: p.Resources.CpuNanoCores,
			MemoryBytes:  p.Resources.MemoryBytes,
		}
	}
	for _, m := range p.Mounts {
		if m == nil {
			continue
		}
		spec.Mounts = append(spec.Mounts, Mount{
			Source:   m.Source,
			Target:   m.Target,
			ReadOnly: m.ReadOnly,
		})
	}
	spec.Network = NetworkPolicyFromProto(p.Network)
	return NormalizeSpec(spec)
}

// NetworkPolicyFromProto converts a proto network policy (canonical fields only).
// Sugar fields are ignored here; use ResolveNetworkPolicyFromProto at API boundaries.
func NetworkPolicyFromProto(p *cellarv1.NetworkPolicy) NetworkPolicy {
	var out NetworkPolicy
	if p == nil {
		return out
	}
	out.Mode = NetworkMode(p.Mode)
	out.EssentialServices = p.EssentialServices
	if p.Dns != nil {
		out.DNS = DNSPolicy{
			Mode:  DNSMode(p.Dns.Mode),
			Names: append([]string(nil), p.Dns.Names...),
		}
	}
	for _, r := range p.Rules {
		if r == nil {
			continue
		}
		out.Rules = append(out.Rules, NetworkRule{
			Hosts:     append([]string(nil), r.Hosts...),
			Ports:     append([]uint32(nil), r.Ports...),
			Protocols: append([]string(nil), r.Protocols...),
		})
	}
	return out
}

// ResolveNetworkPolicyFromProto translates Daytona-style sugar, normalizes, and
// validates. Used by Create and UpdateNetwork.
func ResolveNetworkPolicyFromProto(p *cellarv1.NetworkPolicy) (NetworkPolicy, error) {
	if p == nil {
		return NormalizeNetworkPolicy(NetworkPolicy{}), nil
	}
	base := NetworkPolicyFromProto(p)
	// Clear structured fields from the sugar-check view when only sugar is set:
	// NetworkPolicyFromProto already copied mode/rules; ResolveNetworkPolicy
	// detects conflicts with hasStructuredNetwork.
	var blockAll *bool
	if p.BlockAll != nil {
		v := p.GetBlockAll()
		blockAll = &v
	}
	return ResolveNetworkPolicy(base, p.NetworkAllowList, p.DomainAllowList, blockAll, p.EssentialServices)
}

// NetworkPolicyToProto converts a network policy.
func NetworkPolicyToProto(np NetworkPolicy) *cellarv1.NetworkPolicy {
	out := &cellarv1.NetworkPolicy{
		Mode:              string(np.Mode),
		EssentialServices: np.EssentialServices,
		Dns: &cellarv1.DNSPolicy{
			Mode:  string(np.DNS.Mode),
			Names: append([]string(nil), np.DNS.Names...),
		},
	}
	for _, r := range np.Rules {
		out.Rules = append(out.Rules, &cellarv1.NetworkRule{
			Hosts:     append([]string(nil), r.Hosts...),
			Ports:     append([]uint32(nil), r.Ports...),
			Protocols: append([]string(nil), r.Protocols...),
		})
	}
	return out
}

// SpecToProto converts a Go spec.
func SpecToProto(spec Spec) *cellarv1.SandboxSpec {
	out := &cellarv1.SandboxSpec{
		Image:      spec.Image,
		Command:    append([]string(nil), spec.Command...),
		Args:       append([]string(nil), spec.Args...),
		Env:        append([]string(nil), spec.Env...),
		WorkingDir: spec.WorkingDir,
		Runtime:    spec.Runtime,
		Resources: &cellarv1.Resources{
			CpuNanoCores: spec.Resources.CPUNanoCores,
			MemoryBytes:  spec.Resources.MemoryBytes,
		},
		Network: NetworkPolicyToProto(spec.Network),
	}
	for _, m := range spec.Mounts {
		out.Mounts = append(out.Mounts, &cellarv1.Mount{
			Source:   m.Source,
			Target:   m.Target,
			ReadOnly: m.ReadOnly,
		})
	}
	return out
}

// StatusFromProto converts proto status.
func StatusFromProto(p *cellarv1.SandboxStatus) Status {
	if p == nil {
		return Status{}
	}
	st := Status{
		Phase:       Phase(p.Phase),
		ContainerID: p.ContainerId,
		ExitCode:    p.ExitCode,
		Message:     p.Message,
	}
	if p.StartedAtUnixNano > 0 {
		st.StartedAt = time.Unix(0, p.StartedAtUnixNano).UTC()
	}
	if p.FinishedAtUnixNano > 0 {
		st.FinishedAt = time.Unix(0, p.FinishedAtUnixNano).UTC()
	}
	if p.UpdatedAtUnixNano > 0 {
		st.UpdatedAt = time.Unix(0, p.UpdatedAtUnixNano).UTC()
	}
	return st
}

// StatusToProto converts Go status.
func StatusToProto(st Status) *cellarv1.SandboxStatus {
	out := &cellarv1.SandboxStatus{
		Phase:       string(st.Phase),
		ContainerId: st.ContainerID,
		ExitCode:    st.ExitCode,
		Message:     st.Message,
	}
	if !st.StartedAt.IsZero() {
		out.StartedAtUnixNano = st.StartedAt.UnixNano()
	}
	if !st.FinishedAt.IsZero() {
		out.FinishedAtUnixNano = st.FinishedAt.UnixNano()
	}
	if !st.UpdatedAt.IsZero() {
		out.UpdatedAtUnixNano = st.UpdatedAt.UnixNano()
	}
	return out
}

// ToProto converts a Sandbox.
func ToProto(s *Sandbox) *cellarv1.Sandbox {
	if s == nil {
		return nil
	}
	out := &cellarv1.Sandbox{
		Id:           s.ID,
		Spec:         SpecToProto(s.Spec),
		NodeId:       s.NodeID,
		DesiredState: string(s.DesiredState),
		Status:       StatusToProto(s.Status),
	}
	if !s.CreatedAt.IsZero() {
		out.CreatedAtUnixNano = s.CreatedAt.UnixNano()
	}
	if !s.UpdatedAt.IsZero() {
		out.UpdatedAtUnixNano = s.UpdatedAt.UnixNano()
	}
	return out
}

// FromProto converts a proto Sandbox.
func FromProto(p *cellarv1.Sandbox) *Sandbox {
	if p == nil {
		return nil
	}
	s := &Sandbox{
		ID:           p.Id,
		Spec:         SpecFromProto(p.Spec),
		NodeID:       p.NodeId,
		DesiredState: DesiredState(p.DesiredState),
		Status:       StatusFromProto(p.Status),
	}
	if p.CreatedAtUnixNano > 0 {
		s.CreatedAt = time.Unix(0, p.CreatedAtUnixNano).UTC()
	}
	if p.UpdatedAtUnixNano > 0 {
		s.UpdatedAt = time.Unix(0, p.UpdatedAtUnixNano).UTC()
	}
	return s
}
