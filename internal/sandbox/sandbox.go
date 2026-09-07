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
	VCPUs       uint8   `json:"vcpus"`
	MemoryMiB   uint32  `json:"memory_mib"`
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

// MountOptions are virtiofs/bind mount flags (cloud MountOptions twin).
type MountOptions struct {
	Readonly    bool    `json:"readonly,omitempty"`
	Noexec      bool    `json:"noexec,omitempty"`
	Nosuid      bool    `json:"nosuid,omitempty"`
	Nodev       bool    `json:"nodev,omitempty"`
	OverrideUID *uint32 `json:"override_uid,omitempty"`
	OverrideGID *uint32 `json:"override_gid,omitempty"`
}

// VolumeMount is a tagged cloud volume mount.
type VolumeMount struct {
	Type               string       `json:"type"` // bind | named | tmpfs | disk_image
	Host               string       `json:"host,omitempty"`
	Guest              string       `json:"guest"`
	Name               string       `json:"name,omitempty"`
	Format             string       `json:"format,omitempty"`
	Fstype             string       `json:"fstype,omitempty"`
	SizeMiB            *uint32      `json:"size_mib,omitempty"`
	QuotaMiB           *uint32      `json:"quota_mib,omitempty"`
	Options            MountOptions `json:"options,omitempty"`
	StatVirtualization string       `json:"stat_virtualization,omitempty"`
	HostPermissions    string       `json:"host_permissions,omitempty"`
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
	Secrets        *SecretsConfig `json:"secrets,omitempty"`
	MaxConnections *uint          `json:"max_connections,omitempty"`
}

// SecretsConfig is the cloud twin of microsandbox CloudSecretsConfig.
// Real values stay on the host; the guest only sees placeholders.
type SecretsConfig struct {
	Entries     []SecretEntry          `json:"entries"`
	OnViolation *SecretViolationAction `json:"on_violation,omitempty"`
}

// SecretEntry is one create-time secret for network-proxy substitution.
type SecretEntry struct {
	EnvVar string `json:"env_var"`
	Value  string `json:"value"`
	// Source is an optional host-side reference. Create currently requires
	// an inline Value; Source-only entries are rejected.
	Source       json.RawMessage     `json:"source,omitempty"`
	Placeholder  string              `json:"placeholder"`
	AllowedHosts []SecretHostPattern `json:"allowed_hosts"`
	Injection    SecretInjection     `json:"injection"`
	// RequireTLSIdentity defaults to true when omitted (nil).
	RequireTLSIdentity *bool                  `json:"require_tls_identity,omitempty"`
	OnViolation        *SecretViolationAction `json:"on_violation,omitempty"`
}

// SecretInjection selects where the network proxy may substitute a secret.
// Defaults match microsandbox: headers/basic_auth true, query/body false.
type SecretInjection struct {
	Headers     *bool `json:"headers,omitempty"`
	BasicAuth   *bool `json:"basic_auth,omitempty"`
	QueryParams *bool `json:"query_params,omitempty"`
	Body        *bool `json:"body,omitempty"`
}

// HeadersEffective returns whether header injection is enabled (default true).
func (i SecretInjection) HeadersEffective() bool {
	if i.Headers == nil {
		return true
	}
	return *i.Headers
}

// BasicAuthEffective returns whether basic-auth injection is enabled (default true).
func (i SecretInjection) BasicAuthEffective() bool {
	if i.BasicAuth == nil {
		return true
	}
	return *i.BasicAuth
}

// QueryParamsEffective returns whether query-param injection is enabled (default false).
func (i SecretInjection) QueryParamsEffective() bool {
	if i.QueryParams == nil {
		return false
	}
	return *i.QueryParams
}

// BodyEffective returns whether body injection is enabled (default false).
func (i SecretInjection) BodyEffective() bool {
	if i.Body == nil {
		return false
	}
	return *i.Body
}

// SecretHostPattern is a tagged host allow-list entry.
// JSON: {"type":"exact","value":"api.example.com"}, {"type":"wildcard","value":"*.example.com"}, {"type":"any"}.
type SecretHostPattern struct {
	Type  string `json:"type"` // exact | wildcard | any
	Value string `json:"value,omitempty"`
}

// SecretViolationAction is a tagged violation action.
// JSON: {"type":"block"}, {"type":"block_and_log"}, {"type":"block_and_terminate"}.
type SecretViolationAction struct {
	Type string `json:"type"`
}

// LifecyclePolicy controls ephemeral / idle / max duration.
type LifecyclePolicy struct {
	Ephemeral       bool    `json:"ephemeral,omitempty"`
	MaxDurationSecs *uint64 `json:"max_duration_secs,omitempty"`
	IdleTimeoutSecs *uint64 `json:"idle_timeout_secs,omitempty"`
}

// EnvPair is one init env entry as a cloud (String,String) tuple: ["KEY","VAL"].
type EnvPair struct {
	Key   string
	Value string
}

// UnmarshalJSON accepts cloud tuples [["K","V"]] and legacy {key,value} objects.
func (p *EnvPair) UnmarshalJSON(b []byte) error {
	b = bytesTrimSpace(b)
	if len(b) == 0 || string(b) == "null" {
		return nil
	}
	if b[0] == '[' {
		var pair [2]string
		if err := json.Unmarshal(b, &pair); err != nil {
			return err
		}
		p.Key, p.Value = pair[0], pair[1]
		return nil
	}
	var obj EnvVar
	if err := json.Unmarshal(b, &obj); err != nil {
		return err
	}
	p.Key, p.Value = obj.Key, obj.Value
	return nil
}

// MarshalJSON emits the cloud tuple shape.
func (p EnvPair) MarshalJSON() ([]byte, error) {
	return json.Marshal([2]string{p.Key, p.Value})
}

func bytesTrimSpace(b []byte) []byte {
	return []byte(strings.TrimSpace(string(b)))
}

// HandoffInit hands PID 1 to a guest init after agentd setup.
type HandoffInit struct {
	Cmd  string    `json:"cmd"`
	Args []string  `json:"args,omitempty"`
	Env  []EnvPair `json:"env,omitempty"`
}

// ValidRlimitResources are CloudRlimitResource snake_case names.
var ValidRlimitResources = map[string]struct{}{
	"cpu": {}, "fsize": {}, "data": {}, "stack": {}, "core": {}, "rss": {},
	"nproc": {}, "nofile": {}, "memlock": {}, "as": {}, "locks": {},
	"sigpending": {}, "msgqueue": {}, "nice": {}, "rtprio": {}, "rttime": {},
}

// Rlimit is a POSIX resource limit (CloudRlimit twin).
type Rlimit struct {
	Resource string `json:"resource"`
	Soft     uint64 `json:"soft"`
	Hard     uint64 `json:"hard"`
}

// PatchContent is text (JSON string) or raw bytes (JSON array of ints, serde Vec<u8>).
type PatchContent struct {
	Text  string
	Bytes []byte
	isRaw bool
}

// UnmarshalJSON accepts a JSON string or byte array.
func (c *PatchContent) UnmarshalJSON(b []byte) error {
	b = bytesTrimSpace(b)
	if len(b) == 0 || string(b) == "null" {
		return nil
	}
	if b[0] == '"' {
		c.isRaw = false
		return json.Unmarshal(b, &c.Text)
	}
	c.isRaw = true
	c.Text = ""
	return json.Unmarshal(b, &c.Bytes)
}

// MarshalJSON emits a JSON string or byte array.
func (c PatchContent) MarshalJSON() ([]byte, error) {
	if c.isRaw || len(c.Bytes) > 0 && c.Text == "" {
		if c.Bytes == nil {
			c.Bytes = []byte{}
		}
		return json.Marshal(c.Bytes)
	}
	return json.Marshal(c.Text)
}

// IsZero reports whether content is empty (for omitempty-style checks).
func (c PatchContent) IsZero() bool {
	return c.Text == "" && len(c.Bytes) == 0 && !c.isRaw
}

// Patch is a rootfs patch applied before VM start (CloudPatch twin).
type Patch struct {
	Type    string        `json:"type"`
	Path    string        `json:"path,omitempty"`
	Content *PatchContent `json:"content,omitempty"`
	Mode    *uint32       `json:"mode,omitempty"`
	Replace bool          `json:"replace,omitempty"`
	Src     string        `json:"src,omitempty"`
	Dst     string        `json:"dst,omitempty"`
	Target  string        `json:"target,omitempty"`
	Link    string        `json:"link,omitempty"`
}

// Spec is the microsandbox CloudSandboxSpec create body (no Cellar extensions).
type Spec struct {
	Name            string            `json:"name"`
	Image           RootfsSource      `json:"image"`
	Resources       Resources         `json:"resources"`
	Runtime         RuntimeOptions    `json:"runtime"`
	Env             []EnvVar          `json:"env,omitempty"`
	Labels          map[string]string `json:"labels,omitempty"`
	Rlimits         []Rlimit          `json:"rlimits,omitempty"`
	Mounts          []VolumeMount     `json:"mounts,omitempty"`
	Patches         []Patch           `json:"patches,omitempty"`
	Network         NetworkSpec       `json:"network"`
	Init            *HandoffInit      `json:"init,omitempty"`
	PullPolicy      PullPolicy        `json:"pull_policy,omitempty"`
	SecurityProfile SecurityProfile   `json:"security_profile,omitempty"`
	Lifecycle       LifecyclePolicy   `json:"lifecycle"`
}

// Status is observed runtime state.
type Status struct {
	Phase   StatusPhase `json:"phase"`
	Message string      `json:"message,omitempty"`
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

// HasHostMounts reports whether any mount or rootfs binds a host path (blocks reschedule).
func (s Spec) HasHostMounts() bool {
	switch s.Image.Type {
	case "bind", "disk_image":
		return true
	}
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
		if spec.Image.Type == "disk_image" && strings.TrimSpace(spec.Image.Format) == "" {
			return fmt.Errorf("image.format is required for disk_image")
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
		case "bind":
			if m.Host == "" {
				return fmt.Errorf("mount[%d]: host is required", i)
			}
		case "disk_image":
			if m.Host == "" {
				return fmt.Errorf("mount[%d]: host is required", i)
			}
			if m.Format == "" {
				return fmt.Errorf("mount[%d]: format is required for disk_image", i)
			}
		case "named":
			if m.Name == "" {
				return fmt.Errorf("mount[%d]: name is required", i)
			}
		case "tmpfs":
		default:
			return fmt.Errorf("mount[%d]: invalid type %q", i, m.Type)
		}
		if (m.Options.OverrideUID == nil) != (m.Options.OverrideGID == nil) {
			return fmt.Errorf("mount[%d]: override_uid and override_gid must be set together", i)
		}
	}
	for i, p := range spec.Patches {
		switch p.Type {
		case "text", "file", "append":
			if p.Path == "" {
				return fmt.Errorf("patch[%d]: path is required", i)
			}
		case "mkdir", "remove":
			if p.Path == "" {
				return fmt.Errorf("patch[%d]: path is required", i)
			}
		case "copy_file", "copy_dir":
			if p.Src == "" || p.Dst == "" {
				return fmt.Errorf("patch[%d]: src and dst are required", i)
			}
		case "symlink":
			if p.Target == "" || p.Link == "" {
				return fmt.Errorf("patch[%d]: target and link are required", i)
			}
		default:
			return fmt.Errorf("patch[%d]: invalid type %q", i, p.Type)
		}
	}
	for i, r := range spec.Rlimits {
		if _, ok := ValidRlimitResources[r.Resource]; !ok {
			return fmt.Errorf("rlimits[%d]: invalid resource %q", i, r.Resource)
		}
		if r.Soft > r.Hard {
			return fmt.Errorf("rlimits[%d]: soft (%d) must not exceed hard (%d)", i, r.Soft, r.Hard)
		}
	}
	if err := validateSecrets(spec.Network); err != nil {
		return err
	}
	return nil
}

func validateSecrets(ns NetworkSpec) error {
	if ns.Secrets == nil || len(ns.Secrets.Entries) == 0 {
		return nil
	}
	if !ns.Enabled {
		return fmt.Errorf("network.secrets require network.enabled")
	}
	if err := validateViolationAction("network.secrets.on_violation", ns.Secrets.OnViolation); err != nil {
		return err
	}
	for i, e := range ns.Secrets.Entries {
		prefix := fmt.Sprintf("network.secrets.entries[%d]", i)
		if strings.TrimSpace(e.EnvVar) == "" {
			return fmt.Errorf("%s: env_var is required", prefix)
		}
		if strings.ContainsAny(e.EnvVar, "=\x00") {
			return fmt.Errorf("%s: env_var cannot contain '=' or NUL", prefix)
		}
		if e.Value == "" {
			if len(e.Source) > 0 {
				return fmt.Errorf("%s: source-only secrets are not supported; provide value", prefix)
			}
			return fmt.Errorf("%s: value is required", prefix)
		}
		if e.Placeholder != "" {
			if strings.ContainsAny(e.Placeholder, "\x00\r\n") {
				return fmt.Errorf("%s: placeholder cannot contain NUL, CR, or LF", prefix)
			}
			if len(e.Placeholder) > 1024 {
				return fmt.Errorf("%s: placeholder exceeds 1024 bytes", prefix)
			}
		}
		if len(e.AllowedHosts) == 0 {
			return fmt.Errorf("%s: allowed_hosts is required", prefix)
		}
		for j, h := range e.AllowedHosts {
			switch h.Type {
			case "exact", "wildcard":
				if strings.TrimSpace(h.Value) == "" {
					return fmt.Errorf("%s.allowed_hosts[%d]: value is required for type %q", prefix, j, h.Type)
				}
			case "any":
			default:
				return fmt.Errorf("%s.allowed_hosts[%d]: invalid type %q", prefix, j, h.Type)
			}
		}
		if err := validateViolationAction(prefix+".on_violation", e.OnViolation); err != nil {
			return err
		}
	}
	return nil
}

func validateViolationAction(path string, a *SecretViolationAction) error {
	if a == nil {
		return nil
	}
	switch a.Type {
	case "block", "block_and_log", "block_and_terminate":
		return nil
	case "":
		return fmt.Errorf("%s: type is required", path)
	default:
		return fmt.Errorf("%s: unsupported type %q", path, a.Type)
	}
}

// RequireTLSIdentityEffective returns whether TLS identity is required (default true).
func (e SecretEntry) RequireTLSIdentityEffective() bool {
	if e.RequireTLSIdentity == nil {
		return true
	}
	return *e.RequireTLSIdentity
}

// SpecToJSONRedacted marshals Spec with secret values cleared for customer APIs.
func SpecToJSONRedacted(spec Spec) ([]byte, error) {
	cp := cloneSpec(spec)
	if cp.Network.Secrets != nil {
		for i := range cp.Network.Secrets.Entries {
			cp.Network.Secrets.Entries[i].Value = ""
		}
	}
	return json.Marshal(cp)
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
	return spec
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
		out.Mounts = make([]VolumeMount, len(spec.Mounts))
		for i, m := range spec.Mounts {
			out.Mounts[i] = m
			if m.Options.OverrideUID != nil {
				v := *m.Options.OverrideUID
				out.Mounts[i].Options.OverrideUID = &v
			}
			if m.Options.OverrideGID != nil {
				v := *m.Options.OverrideGID
				out.Mounts[i].Options.OverrideGID = &v
			}
			if m.SizeMiB != nil {
				v := *m.SizeMiB
				out.Mounts[i].SizeMiB = &v
			}
			if m.QuotaMiB != nil {
				v := *m.QuotaMiB
				out.Mounts[i].QuotaMiB = &v
			}
		}
	}
	if spec.Patches != nil {
		out.Patches = make([]Patch, len(spec.Patches))
		for i, p := range spec.Patches {
			out.Patches[i] = p
			if p.Content != nil {
				cp := *p.Content
				if p.Content.Bytes != nil {
					cp.Bytes = append([]byte(nil), p.Content.Bytes...)
				}
				out.Patches[i].Content = &cp
			}
			if p.Mode != nil {
				v := *p.Mode
				out.Patches[i].Mode = &v
			}
		}
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
	if spec.Network.Secrets != nil {
		s := *spec.Network.Secrets
		if spec.Network.Secrets.OnViolation != nil {
			v := *spec.Network.Secrets.OnViolation
			s.OnViolation = &v
		}
		if spec.Network.Secrets.Entries != nil {
			s.Entries = make([]SecretEntry, len(spec.Network.Secrets.Entries))
			for i, e := range spec.Network.Secrets.Entries {
				s.Entries[i] = e
				if e.Source != nil {
					s.Entries[i].Source = append(json.RawMessage(nil), e.Source...)
				}
				if e.AllowedHosts != nil {
					s.Entries[i].AllowedHosts = append([]SecretHostPattern(nil), e.AllowedHosts...)
				}
				if e.RequireTLSIdentity != nil {
					v := *e.RequireTLSIdentity
					s.Entries[i].RequireTLSIdentity = &v
				}
				if e.OnViolation != nil {
					v := *e.OnViolation
					s.Entries[i].OnViolation = &v
				}
				inj := e.Injection
				if e.Injection.Headers != nil {
					v := *e.Injection.Headers
					inj.Headers = &v
				}
				if e.Injection.BasicAuth != nil {
					v := *e.Injection.BasicAuth
					inj.BasicAuth = &v
				}
				if e.Injection.QueryParams != nil {
					v := *e.Injection.QueryParams
					inj.QueryParams = &v
				}
				if e.Injection.Body != nil {
					v := *e.Injection.Body
					inj.Body = &v
				}
				s.Entries[i].Injection = inj
			}
		}
		out.Network.Secrets = &s
	}
	if spec.Init != nil {
		init := *spec.Init
		init.Args = append([]string(nil), spec.Init.Args...)
		init.Env = append([]EnvPair(nil), spec.Init.Env...)
		out.Init = &init
	}
	if spec.Resources.DiskSizeMiB != nil {
		v := *spec.Resources.DiskSizeMiB
		out.Resources.DiskSizeMiB = &v
	}
	if spec.Lifecycle.MaxDurationSecs != nil {
		v := *spec.Lifecycle.MaxDurationSecs
		out.Lifecycle.MaxDurationSecs = &v
	}
	if spec.Lifecycle.IdleTimeoutSecs != nil {
		v := *spec.Lifecycle.IdleTimeoutSecs
		out.Lifecycle.IdleTimeoutSecs = &v
	}
	if spec.Runtime.Workdir != nil {
		v := *spec.Runtime.Workdir
		out.Runtime.Workdir = &v
	}
	if spec.Runtime.Shell != nil {
		v := *spec.Runtime.Shell
		out.Runtime.Shell = &v
	}
	if spec.Runtime.User != nil {
		v := *spec.Runtime.User
		out.Runtime.User = &v
	}
	if spec.Runtime.LogLevel != nil {
		v := *spec.Runtime.LogLevel
		out.Runtime.LogLevel = &v
	}
	if spec.Network.MaxConnections != nil {
		v := *spec.Network.MaxConnections
		out.Network.MaxConnections = &v
	}
	return out
}
