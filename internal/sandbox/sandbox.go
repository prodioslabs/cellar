package sandbox

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
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

// StatusPhase is the observed lifecycle phase (msb-cloud status).
type StatusPhase string

const (
	PhaseCreated  StatusPhase = "created"
	PhaseStarting StatusPhase = "starting"
	PhaseRunning  StatusPhase = "running"
	PhaseStopping StatusPhase = "stopping"
	PhaseStopped  StatusPhase = "stopped"
	PhaseFailed   StatusPhase = "failed"
)

// Legacy aliases used during the migration of older call sites.
const (
	PhasePending = PhaseCreated
)

// PullPolicy controls OCI image pull behavior.
type PullPolicy string

const (
	PullIfMissing PullPolicy = "if_missing"
	PullAlways    PullPolicy = "always"
	PullNever     PullPolicy = "never"
)

// SecurityProfile is the in-guest security profile.
type SecurityProfile string

const (
	SecurityDefault    SecurityProfile = "default"
	SecurityRestricted SecurityProfile = "restricted"
)

// PolicyAction is allow or deny.
type PolicyAction string

const (
	ActionAllow PolicyAction = "allow"
	ActionDeny  PolicyAction = "deny"
)

// EnvVar is a guest environment variable.
type EnvVar struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

// Resources are cloud resource limits.
type Resources struct {
	VCPUs      uint8  `json:"vcpus"`
	MemoryMiB  uint32 `json:"memory_mib"`
	DiskSizeMiB *uint32 `json:"disk_size_mib,omitempty"`
}

// RuntimeOptions are guest runtime options (msb cloud runtime).
type RuntimeOptions struct {
	Workdir    *string           `json:"workdir,omitempty"`
	Shell      *string           `json:"shell,omitempty"`
	Scripts    map[string]string `json:"scripts,omitempty"`
	Entrypoint []string          `json:"entrypoint,omitempty"`
	Cmd        []string          `json:"cmd,omitempty"`
	User       *string           `json:"user,omitempty"`
	LogLevel   *string           `json:"log_level,omitempty"`
}

// RootfsSource is a tagged OCI/bind/disk-image root filesystem.
// JSON shape: {"type":"oci","reference":"..."} etc.
type RootfsSource struct {
	Type      string `json:"type"` // oci | bind | disk_image
	Reference string `json:"reference,omitempty"`
	Path      string `json:"path,omitempty"`
	Format    string `json:"format,omitempty"`
	Fstype    string `json:"fstype,omitempty"`
}

// MountOptions are virtiofs/bind mount flags.
type MountOptions struct {
	ReadOnly bool `json:"read_only,omitempty"`
	NoExec   bool `json:"no_exec,omitempty"`
	NoSuid   bool `json:"nosuid,omitempty"`
	NoDev    bool `json:"nodev,omitempty"`
}

// VolumeMount is a tagged cloud volume mount.
type VolumeMount struct {
	Type                string        `json:"type"` // bind | named | tmpfs | disk_image
	Host                string        `json:"host,omitempty"`
	Guest               string        `json:"guest"`
	Name                string        `json:"name,omitempty"`
	Format              string        `json:"format,omitempty"`
	Fstype              string        `json:"fstype,omitempty"`
	SizeMiB             *uint32       `json:"size_mib,omitempty"`
	QuotaMiB            *uint32       `json:"quota_mib,omitempty"`
	Options             MountOptions  `json:"options,omitempty"`
	StatVirtualization  string        `json:"stat_virtualization,omitempty"`
	HostPermissions     string        `json:"host_permissions,omitempty"`
}

// PortRange is an inclusive guest port range.
type PortRange struct {
	Start uint16 `json:"start"`
	End   uint16 `json:"end"`
}

// NetworkRule is one msb network policy rule.
// Destination is stored as raw JSON matching Rust serde externally-tagged
// Destination (e.g. "any", {"cidr":"10.0.0.0/8"}, {"domain":"example.com"}).
type NetworkRule struct {
	Direction   string          `json:"direction"` // egress | ingress | any
	Destination json.RawMessage `json:"destination"`
	Protocols   []string        `json:"protocols,omitempty"`
	Ports       []PortRange     `json:"ports,omitempty"`
	Action      PolicyAction    `json:"action"`
}

// NetworkPolicy is msb ordered allow/deny policy.
type NetworkPolicy struct {
	DefaultEgress  PolicyAction  `json:"default_egress"`
	DefaultIngress PolicyAction  `json:"default_ingress"`
	Rules          []NetworkRule `json:"rules,omitempty"`
}

// NetworkSpec is the cloud network block.
type NetworkSpec struct {
	Enabled        bool           `json:"enabled"`
	Policy         *NetworkPolicy `json:"policy,omitempty"`
	MaxConnections *uint          `json:"max_connections,omitempty"`
}

// LifecyclePolicy controls ephemeral / idle / max duration.
type LifecyclePolicy struct {
	Ephemeral        bool   `json:"ephemeral,omitempty"`
	MaxDurationSecs  *uint64 `json:"max_duration_secs,omitempty"`
	IdleTimeoutSecs  *uint64 `json:"idle_timeout_secs,omitempty"`
}

// HandoffInit hands PID 1 to a guest init after agentd setup.
type HandoffInit struct {
	Cmd  string   `json:"cmd"`
	Args []string `json:"args,omitempty"`
	Env  []EnvVar `json:"env,omitempty"`
}

// Rlimit is a POSIX resource limit.
type Rlimit struct {
	Resource string `json:"resource"`
	Soft     uint64 `json:"soft"`
	Hard     uint64 `json:"hard"`
}

// Patch is a rootfs patch applied before VM start (simplified text/file/mkdir/remove/append).
type Patch struct {
	Type    string `json:"type"`
	Path    string `json:"path,omitempty"`
	Content string `json:"content,omitempty"`
	Mode    *uint32 `json:"mode,omitempty"`
	Replace bool   `json:"replace,omitempty"`
	Src     string `json:"src,omitempty"`
	Dst     string `json:"dst,omitempty"`
	Target  string `json:"target,omitempty"`
	Link    string `json:"link,omitempty"`
}

// Spec is the msb-cloud sandbox create body (flattened onto the request).
type Spec struct {
	Name             string            `json:"name"`
	Image            RootfsSource      `json:"image"`
	Resources        Resources         `json:"resources"`
	Runtime          RuntimeOptions    `json:"runtime"`
	Env              []EnvVar          `json:"env,omitempty"`
	Labels           map[string]string `json:"labels,omitempty"`
	Rlimits          []Rlimit          `json:"rlimits,omitempty"`
	Mounts           []VolumeMount     `json:"mounts,omitempty"`
	Patches          []Patch           `json:"patches,omitempty"`
	Network          NetworkSpec       `json:"network"`
	Init             *HandoffInit      `json:"init,omitempty"`
	PullPolicy       PullPolicy        `json:"pull_policy,omitempty"`
	SecurityProfile  SecurityProfile   `json:"security_profile,omitempty"`
	Lifecycle        LifecyclePolicy   `json:"lifecycle"`
	Slug             string            `json:"slug,omitempty"`
}

// Status is observed runtime state.
type Status struct {
	Phase     StatusPhase `json:"phase"`
	Message   string      `json:"message,omitempty"`
	// LocalName is the microsandbox local name (cellar sandbox ID).
	LocalName string    `json:"local_name,omitempty"`
	StartedAt time.Time `json:"started_at,omitempty"`
	StoppedAt time.Time `json:"stopped_at,omitempty"`
	UpdatedAt time.Time `json:"updated_at,omitempty"`
}

// Sandbox is the Raft-replicated sandbox object.
type Sandbox struct {
	ID                   string            `json:"id"`
	Name                 string            `json:"name"`
	Slug                 string            `json:"slug,omitempty"`
	Spec                 Spec              `json:"spec"`
	NodeID               string            `json:"node_id,omitempty"`
	DesiredState         DesiredState      `json:"desired_state"`
	Status               Status            `json:"status"`
	Ephemeral            bool              `json:"ephemeral,omitempty"`
	Labels               map[string]string `json:"labels,omitempty"`
	CreatedAt            time.Time         `json:"created_at"`
	UpdatedAt            time.Time         `json:"updated_at"`
	AssignmentGeneration int64             `json:"assignment_generation,omitempty"`
}

// ErrStaleAssignment is returned when a status update carries an outdated fencing token.
var ErrStaleAssignment = fmt.Errorf("stale assignment generation")

// ErrNameExists is returned when a sandbox name is already taken.
var ErrNameExists = fmt.Errorf("name already exists")

// CheckAssignmentGeneration rejects status from a former owner after reschedule.
func CheckAssignmentGeneration(storedGen, reportedGen int64) error {
	if storedGen > 0 && reportedGen != storedGen {
		return fmt.Errorf("%w: reported %d current %d", ErrStaleAssignment, reportedGen, storedGen)
	}
	return nil
}

// NewID generates a 16-byte hex sandbox ID.
func NewID() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// OCIImage builds an OCI rootfs source.
func OCIImage(reference string) RootfsSource {
	return RootfsSource{Type: "oci", Reference: reference}
}

// ImageReference returns the OCI reference or empty.
func (s Spec) ImageReference() string {
	if s.Image.Type == "" || s.Image.Type == "oci" {
		return s.Image.Reference
	}
	return ""
}

// HasHostMounts reports whether any mount binds a host path (blocks reschedule).
func (s Spec) HasHostMounts() bool {
	for _, m := range s.Mounts {
		switch m.Type {
		case "bind", "disk_image":
			return true
		}
	}
	return false
}

// NamedVolumeNames returns named volume mount names.
func (s Spec) NamedVolumeNames() []string {
	var out []string
	for _, m := range s.Mounts {
		if m.Type == "named" && m.Name != "" {
			out = append(out, m.Name)
		}
	}
	return out
}

// ValidateSpec checks Spec fields.
func ValidateSpec(spec Spec) error {
	name := strings.TrimSpace(spec.Name)
	if name == "" {
		return fmt.Errorf("name is required")
	}
	if len(name) > 128 {
		return fmt.Errorf("name exceeds 128 bytes")
	}
	switch spec.Image.Type {
	case "", "oci":
		if strings.TrimSpace(spec.Image.Reference) == "" {
			return fmt.Errorf("image.reference is required")
		}
	case "bind", "disk_image":
		if strings.TrimSpace(spec.Image.Path) == "" {
			return fmt.Errorf("image.path is required for type %q", spec.Image.Type)
		}
	default:
		return fmt.Errorf("invalid image.type %q", spec.Image.Type)
	}
	if spec.Resources.VCPUs == 0 {
		return fmt.Errorf("resources.vcpus must be >= 1")
	}
	if spec.Resources.MemoryMiB == 0 {
		return fmt.Errorf("resources.memory_mib must be >= 1")
	}
	for i, m := range spec.Mounts {
		if m.Guest == "" {
			return fmt.Errorf("mount[%d]: guest is required", i)
		}
		switch m.Type {
		case "bind", "disk_image":
			if m.Host == "" {
				return fmt.Errorf("mount[%d]: host is required", i)
			}
		case "named":
			if m.Name == "" {
				return fmt.Errorf("mount[%d]: name is required", i)
			}
		case "tmpfs":
		default:
			return fmt.Errorf("mount[%d]: invalid type %q", i, m.Type)
		}
	}
	return nil
}

// NormalizeSpec fills defaults.
func NormalizeSpec(spec Spec) Spec {
	if spec.Image.Type == "" {
		spec.Image.Type = "oci"
	}
	if spec.Resources.VCPUs == 0 {
		spec.Resources.VCPUs = 1
	}
	if spec.Resources.MemoryMiB == 0 {
		spec.Resources.MemoryMiB = 512
	}
	if spec.PullPolicy == "" {
		spec.PullPolicy = PullIfMissing
	}
	if spec.SecurityProfile == "" {
		spec.SecurityProfile = SecurityDefault
	}
	// Network defaults: enabled with deny-all policy when unset.
	if !spec.Network.Enabled && spec.Network.Policy == nil && spec.Network.MaxConnections == nil {
		// Zero value: treat as enabled=false (isolated). Callers that want
		// networking set enabled=true explicitly.
	}
	if spec.Network.Policy != nil {
		if spec.Network.Policy.DefaultEgress == "" {
			spec.Network.Policy.DefaultEgress = ActionDeny
		}
		if spec.Network.Policy.DefaultIngress == "" {
			spec.Network.Policy.DefaultIngress = ActionDeny
		}
	}
	if spec.Slug == "" {
		spec.Slug = spec.Name
	}
	return spec
}

// ApplyLanguagePreset fills Image from a CLI language preset when reference empty.
func ApplyLanguagePreset(spec Spec, preset string) (Spec, error) {
	preset = strings.TrimSpace(preset)
	if preset == "" {
		return spec, nil
	}
	img, err := ResolveImage(preset)
	if err != nil {
		return spec, err
	}
	if spec.Image.Reference != "" && spec.Image.Reference != img {
		return spec, fmt.Errorf("specify image or runtime preset, not both")
	}
	spec.Image = OCIImage(img)
	return spec, nil
}

// Clone returns a deep copy.
func Clone(s *Sandbox) *Sandbox {
	if s == nil {
		return nil
	}
	cp := *s
	cp.Spec = cloneSpec(s.Spec)
	if s.Labels != nil {
		cp.Labels = make(map[string]string, len(s.Labels))
		for k, v := range s.Labels {
			cp.Labels[k] = v
		}
	}
	return &cp
}

func cloneSpec(spec Spec) Spec {
	out := spec
	if spec.Env != nil {
		out.Env = append([]EnvVar(nil), spec.Env...)
	}
	if spec.Labels != nil {
		out.Labels = make(map[string]string, len(spec.Labels))
		for k, v := range spec.Labels {
			out.Labels[k] = v
		}
	}
	if spec.Rlimits != nil {
		out.Rlimits = append([]Rlimit(nil), spec.Rlimits...)
	}
	if spec.Mounts != nil {
		out.Mounts = append([]VolumeMount(nil), spec.Mounts...)
	}
	if spec.Patches != nil {
		out.Patches = append([]Patch(nil), spec.Patches...)
	}
	if spec.Runtime.Scripts != nil {
		out.Runtime.Scripts = make(map[string]string, len(spec.Runtime.Scripts))
		for k, v := range spec.Runtime.Scripts {
			out.Runtime.Scripts[k] = v
		}
	}
	if spec.Runtime.Entrypoint != nil {
		out.Runtime.Entrypoint = append([]string(nil), spec.Runtime.Entrypoint...)
	}
	if spec.Runtime.Cmd != nil {
		out.Runtime.Cmd = append([]string(nil), spec.Runtime.Cmd...)
	}
	if spec.Network.Policy != nil {
		p := *spec.Network.Policy
		if spec.Network.Policy.Rules != nil {
			p.Rules = make([]NetworkRule, len(spec.Network.Policy.Rules))
			for i, r := range spec.Network.Policy.Rules {
				p.Rules[i] = r
				if r.Destination != nil {
					p.Rules[i].Destination = append(json.RawMessage(nil), r.Destination...)
				}
				if r.Protocols != nil {
					p.Rules[i].Protocols = append([]string(nil), r.Protocols...)
				}
				if r.Ports != nil {
					p.Rules[i].Ports = append([]PortRange(nil), r.Ports...)
				}
			}
		}
		out.Network.Policy = &p
	}
	if spec.Init != nil {
		init := *spec.Init
		init.Args = append([]string(nil), spec.Init.Args...)
		init.Env = append([]EnvVar(nil), spec.Init.Env...)
		out.Init = &init
	}
	if spec.Resources.DiskSizeMiB != nil {
		v := *spec.Resources.DiskSizeMiB
		out.Resources.DiskSizeMiB = &v
	}
	return out
}
