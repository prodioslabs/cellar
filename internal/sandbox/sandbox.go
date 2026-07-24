package sandbox

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net"
	"strings"
	"time"
)

// DesiredState is the Raft-replicated intent for a sandbox.
type DesiredState string

const (
	DesiredRunning DesiredState = "running"
	DesiredStopped DesiredState = "stopped"
	DesiredRemoved DesiredState = "removed"
)

// Phase is the observed lifecycle phase.
type Phase string

const (
	PhasePending  Phase = "pending"
	PhaseStarting Phase = "starting"
	PhaseRunning  Phase = "running"
	PhaseStopped  Phase = "stopped"
	PhaseFailed   Phase = "failed"
)

// NetworkMode controls external connectivity.
type NetworkMode string

const (
	NetworkNone      NetworkMode = "none"
	NetworkAllowlist NetworkMode = "allowlist"
	NetworkDenylist  NetworkMode = "denylist"
)

// DNSMode controls DNS resolution policy inside the egress path.
type DNSMode string

const (
	DNSNone      DNSMode = "none"
	DNSAllowlist DNSMode = "allowlist"
	DNSDenylist  DNSMode = "denylist"
)

// Mount is a host bind mount.
type Mount struct {
	Source   string `json:"source"`
	Target   string `json:"target"`
	ReadOnly bool   `json:"read_only"`
}

// Resources are Docker-mapped resource limits.
type Resources struct {
	CPUNanoCores int64 `json:"cpu_nano_cores,omitempty"` // 1e9 = 1 CPU
	MemoryBytes  int64 `json:"memory_bytes,omitempty"`
}

// NetworkRule matches destinations for egress policy.
type NetworkRule struct {
	Hosts     []string `json:"hosts,omitempty"` // hostname, suffix (.example.com), or CIDR/IP
	Ports     []uint32 `json:"ports,omitempty"` // empty = any
	Protocols []string `json:"protocols,omitempty"` // v1: "tcp"
}

// DNSPolicy filters name resolution.
type DNSPolicy struct {
	Mode  DNSMode  `json:"mode"`
	Names []string `json:"names,omitempty"`
}

// NetworkPolicy is enforced by the userspace egress proxy.
type NetworkPolicy struct {
	Mode  NetworkMode  `json:"mode"`
	DNS   DNSPolicy    `json:"dns"`
	Rules []NetworkRule `json:"rules,omitempty"`
}

// Spec is the desired sandbox configuration.
type Spec struct {
	Image      string        `json:"image"`
	Command    []string      `json:"command,omitempty"`
	Args       []string      `json:"args,omitempty"`
	Env        []string      `json:"env,omitempty"`
	WorkingDir string        `json:"working_dir,omitempty"`
	Mounts     []Mount       `json:"mounts,omitempty"`
	Resources  Resources     `json:"resources"`
	Network    NetworkPolicy `json:"network"`
	Runtime    string        `json:"runtime,omitempty"` // language preset (node-26, …); XOR with custom Image
}

// Status is observed runtime state.
type Status struct {
	Phase         Phase     `json:"phase"`
	ContainerID   string    `json:"container_id,omitempty"`
	ExitCode      int32     `json:"exit_code,omitempty"`
	Message       string    `json:"message,omitempty"`
	StartedAt     time.Time `json:"started_at,omitempty"`
	FinishedAt    time.Time `json:"finished_at,omitempty"`
	UpdatedAt     time.Time `json:"updated_at,omitempty"`
}

// Sandbox is the Raft-replicated sandbox object.
type Sandbox struct {
	ID           string       `json:"id"`
	Spec         Spec         `json:"spec"`
	NodeID       string       `json:"node_id,omitempty"`
	DesiredState DesiredState `json:"desired_state"`
	Status       Status       `json:"status"`
	CreatedAt    time.Time    `json:"created_at"`
	UpdatedAt    time.Time    `json:"updated_at"`
}

// NewID generates a 16-byte hex sandbox ID.
func NewID() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// DefaultOCIRuntime is the OCI runtime name Docker must have registered (gVisor).
const DefaultOCIRuntime = "runsc"

// DefaultRuntime is kept as an alias for callers that still refer to the OCI runtime.
const DefaultRuntime = DefaultOCIRuntime

// ValidateSpec checks Spec fields. Exactly one of Image or Runtime must select a
// container image (Runtime may already be resolved to Image by NormalizeSpec).
func ValidateSpec(spec Spec) error {
	image := strings.TrimSpace(spec.Image)
	runtime := strings.TrimSpace(spec.Runtime)
	if runtime != "" {
		expected, err := ResolveImage(runtime)
		if err != nil {
			return err
		}
		if image != "" && image != expected {
			return fmt.Errorf("specify image or runtime, not both (runtime %q → %q)", runtime, expected)
		}
	} else if image == "" {
		return fmt.Errorf("image or runtime is required")
	}
	switch spec.Network.Mode {
	case "", NetworkNone, NetworkAllowlist, NetworkDenylist:
	default:
		return fmt.Errorf("invalid network mode %q", spec.Network.Mode)
	}
	if spec.Network.Mode == "" {
		// ok; Normalize fills none
	}
	switch spec.Network.DNS.Mode {
	case "", DNSNone, DNSAllowlist, DNSDenylist:
	default:
		return fmt.Errorf("invalid dns mode %q", spec.Network.DNS.Mode)
	}
	for i, m := range spec.Mounts {
		if m.Source == "" || m.Target == "" {
			return fmt.Errorf("mount[%d]: source and target are required", i)
		}
	}
	for i, r := range spec.Network.Rules {
		if len(r.Hosts) == 0 {
			return fmt.Errorf("network rule[%d]: hosts required", i)
		}
		for _, h := range r.Hosts {
			if err := validateHostOrCIDR(h); err != nil {
				return fmt.Errorf("network rule[%d]: %w", i, err)
			}
		}
	}
	return nil
}

func validateHostOrCIDR(h string) error {
	h = strings.TrimSpace(h)
	if h == "" {
		return fmt.Errorf("empty host")
	}
	if strings.Contains(h, "/") {
		if _, _, err := net.ParseCIDR(h); err != nil {
			return fmt.Errorf("invalid CIDR %q", h)
		}
		return nil
	}
	if ip := net.ParseIP(h); ip != nil {
		return nil
	}
	// hostname / suffix
	if strings.HasPrefix(h, ".") {
		if len(h) < 2 {
			return fmt.Errorf("invalid hostname suffix %q", h)
		}
		return nil
	}
	if strings.ContainsAny(h, " \t") {
		return fmt.Errorf("invalid hostname %q", h)
	}
	return nil
}

// NormalizeSpec fills defaults and resolves language runtimes to images.
func NormalizeSpec(spec Spec) Spec {
	// Legacy: Runtime previously meant the OCI runtime (always runsc).
	if spec.Runtime == DefaultOCIRuntime || spec.Runtime == "runc" {
		spec.Runtime = ""
	}
	if img, err := ResolveImage(spec.Runtime); err == nil && strings.TrimSpace(spec.Image) == "" {
		spec.Image = img
	}
	if spec.Network.Mode == "" {
		spec.Network.Mode = NetworkNone
	}
	if spec.Network.DNS.Mode == "" {
		if spec.Network.Mode == NetworkNone {
			spec.Network.DNS.Mode = DNSNone
		} else {
			spec.Network.DNS.Mode = spec.Network.Mode.asDNS()
		}
	}
	return spec
}

func (m NetworkMode) asDNS() DNSMode {
	switch m {
	case NetworkAllowlist:
		return DNSAllowlist
	case NetworkDenylist:
		return DNSDenylist
	default:
		return DNSNone
	}
}

// Clone returns a deep copy.
func Clone(s *Sandbox) *Sandbox {
	if s == nil {
		return nil
	}
	cp := *s
	cp.Spec = cloneSpec(s.Spec)
	return &cp
}

func cloneSpec(spec Spec) Spec {
	out := spec
	if spec.Command != nil {
		out.Command = append([]string(nil), spec.Command...)
	}
	if spec.Args != nil {
		out.Args = append([]string(nil), spec.Args...)
	}
	if spec.Env != nil {
		out.Env = append([]string(nil), spec.Env...)
	}
	if spec.Mounts != nil {
		out.Mounts = append([]Mount(nil), spec.Mounts...)
	}
	if spec.Network.Rules != nil {
		out.Network.Rules = make([]NetworkRule, len(spec.Network.Rules))
		for i, r := range spec.Network.Rules {
			out.Network.Rules[i] = NetworkRule{
				Hosts:     append([]string(nil), r.Hosts...),
				Ports:     append([]uint32(nil), r.Ports...),
				Protocols: append([]string(nil), r.Protocols...),
			}
		}
	}
	if spec.Network.DNS.Names != nil {
		out.Network.DNS.Names = append([]string(nil), spec.Network.DNS.Names...)
	}
	return out
}
