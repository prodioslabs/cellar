package runtime

import (
	"context"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	msb "github.com/superradcompany/microsandbox/sdk/go"

	"github.com/prodioslabs/cellar/internal/sandbox"
)

// Driver talks to the local microsandbox runtime via the official Go SDK.
type Driver struct {
	mu      sync.Mutex
	handles map[string]*msb.Sandbox // cellar sandbox ID -> handle
}

// NewDriver constructs a Driver. Call EnsureInstalled before first use.
func NewDriver() *Driver {
	return &Driver{handles: make(map[string]*msb.Sandbox)}
}

// EnsureInstalled downloads msb + libkrunfw if needed.
func EnsureInstalled(ctx context.Context) error {
	return msb.EnsureInstalled(ctx)
}

// Close releases all retained sandbox handles without destroying VMs.
func (d *Driver) Close() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	for id, h := range d.handles {
		_ = h.Close()
		delete(d.handles, id)
	}
	return nil
}

// LocalName returns the microsandbox name for a cellar sandbox ID.
func LocalName(sandboxID string) string {
	return sandboxID
}

// FindBySandboxID returns whether a local sandbox handle is tracked.
func (d *Driver) FindBySandboxID(_ context.Context, sandboxID string) (string, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if _, ok := d.handles[sandboxID]; ok {
		return sandboxID, nil
	}
	return "", nil
}

// CreateOrConnect creates or reconnects a sandbox and starts it when start is true.
func (d *Driver) CreateOrConnect(ctx context.Context, sb *sandbox.Sandbox, start bool) error {
	if sb == nil {
		return fmt.Errorf("sandbox is required")
	}
	opts, err := specToOptions(sb.Spec)
	if err != nil {
		return err
	}
	name := LocalName(sb.ID)
	h, err := msb.ConnectOrCreateSandbox(ctx, name, opts...)
	if err != nil {
		return fmt.Errorf("msb create/connect %s: %w", name, err)
	}
	d.mu.Lock()
	if old, ok := d.handles[sb.ID]; ok && old != h {
		_ = old.Close()
	}
	d.handles[sb.ID] = h
	d.mu.Unlock()

	if start {
		if err := d.ensureRunning(ctx, sb.ID); err != nil {
			return err
		}
	}
	return nil
}

func (d *Driver) ensureRunning(ctx context.Context, sandboxID string) error {
	h := d.handle(sandboxID)
	if h == nil {
		return fmt.Errorf("sandbox %s not tracked", sandboxID)
	}
	if _, err := h.Ping(ctx); err == nil {
		return nil
	}
	started, err := msb.StartSandbox(ctx, LocalName(sandboxID))
	if err != nil {
		restarted, err2 := h.Restart(ctx)
		if err2 != nil {
			return fmt.Errorf("start sandbox: %v (restart: %w)", err, err2)
		}
		if restarted != nil {
			d.mu.Lock()
			if old := d.handles[sandboxID]; old != nil && old != restarted {
				_ = old.Close()
			}
			d.handles[sandboxID] = restarted
			d.mu.Unlock()
		}
		return nil
	}
	if started != nil {
		d.mu.Lock()
		if old := d.handles[sandboxID]; old != nil && old != started {
			_ = old.Close()
		}
		d.handles[sandboxID] = started
		d.mu.Unlock()
	}
	return nil
}

// Start ensures the sandbox VM is running.
func (d *Driver) Start(ctx context.Context, sandboxID string) error {
	return d.ensureRunning(ctx, sandboxID)
}

// Stop stops the sandbox VM but keeps disk state.
func (d *Driver) Stop(ctx context.Context, sandboxID string, _ int) error {
	h := d.handle(sandboxID)
	if h == nil {
		// Try start-less stop: connect then stop.
		re, err := msb.ConnectOrCreateSandbox(ctx, LocalName(sandboxID))
		if err != nil {
			return nil // already gone
		}
		defer re.Close()
		return re.Stop(ctx)
	}
	return h.Stop(ctx)
}

// Remove destroys the sandbox VM and releases the handle.
func (d *Driver) Remove(ctx context.Context, sandboxID string) error {
	h := d.handle(sandboxID)
	if h != nil {
		_ = h.Stop(ctx)
		_ = h.Close()
	}
	err := msb.RemoveSandbox(ctx, LocalName(sandboxID))
	d.mu.Lock()
	delete(d.handles, sandboxID)
	d.mu.Unlock()
	return err
}

// InspectPhase returns the observed lifecycle phase.
func (d *Driver) InspectPhase(ctx context.Context, sandboxID string) (sandbox.StatusPhase, error) {
	h := d.handle(sandboxID)
	if h == nil {
		re, err := msb.ConnectOrCreateSandbox(ctx, LocalName(sandboxID))
		if err != nil {
			return sandbox.PhaseStopped, nil
		}
		d.mu.Lock()
		d.handles[sandboxID] = re
		d.mu.Unlock()
		h = re
	}
	if _, err := h.Ping(ctx); err != nil {
		return sandbox.PhaseStopped, nil
	}
	return sandbox.PhaseRunning, nil
}

// AgentSocketPath returns the host unix socket for agentd relay.
func (d *Driver) AgentSocketPath(sandboxID string) (string, error) {
	return msb.AgentSocketPath(LocalName(sandboxID))
}

// LogEntry is one streamed log line.
type LogEntry struct {
	ID        string
	Source    string
	Timestamp time.Time
	Text      string
}

// LogFollowOptions configures Driver.FollowLogs.
type LogFollowOptions struct {
	Follow     bool
	Sources    []string
	FromCursor string
}

// FollowLogs streams sandbox logs until ctx is cancelled (or EOF when Follow is false).
func (d *Driver) FollowLogs(ctx context.Context, sandboxID string, opts LogFollowOptions, emit func(LogEntry) error) error {
	h := d.handle(sandboxID)
	if h == nil {
		return fmt.Errorf("sandbox %s not running locally", sandboxID)
	}
	stream, err := h.LogStream(ctx, msb.LogStreamOptions{
		Follow:     opts.Follow,
		Sources:    mapLogSources(opts.Sources),
		FromCursor: opts.FromCursor,
	})
	if err != nil {
		return err
	}
	defer stream.Close()
	for {
		entry, err := stream.Recv(ctx)
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		if err := emit(LogEntry{
			ID:        entry.Cursor,
			Source:    string(entry.Source),
			Timestamp: entry.Timestamp,
			Text:      string(entry.Data),
		}); err != nil {
			return err
		}
	}
}

// EnsureVolume creates a named volume on this node if missing.
func (d *Driver) EnsureVolume(ctx context.Context, name string, capacityGiB *uint32) error {
	opts := []msb.VolumeOption{}
	if capacityGiB != nil {
		opts = append(opts, msb.WithVolumeQuota(*capacityGiB*1024))
	}
	_, err := msb.CreateVolume(ctx, name, opts...)
	if err != nil {
		// Idempotent: already exists is OK.
		if strings.Contains(strings.ToLower(err.Error()), "already") {
			return nil
		}
		return err
	}
	return nil
}

// DeleteVolume removes a named volume on this node.
func (d *Driver) DeleteVolume(ctx context.Context, name string) error {
	return msb.RemoveVolume(ctx, name)
}

// VolumeFS returns filesystem ops for a named volume.
func (d *Driver) VolumeFS(ctx context.Context, name string) (*msb.VolumeFs, error) {
	list, err := msb.ListVolumes(ctx)
	if err != nil {
		return nil, err
	}
	for _, h := range list {
		if h != nil && h.Name() == name {
			return h.FS(), nil
		}
	}
	v, err := msb.CreateVolume(ctx, name)
	if err != nil {
		return nil, err
	}
	return v.FS(), nil
}

func (d *Driver) handle(id string) *msb.Sandbox {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.handles[id]
}

func mapLogSources(sources []string) []msb.LogSource {
	if len(sources) == 0 {
		return nil
	}
	out := make([]msb.LogSource, 0, len(sources))
	for _, s := range sources {
		switch strings.ToLower(strings.TrimSpace(s)) {
		case "stdout":
			out = append(out, msb.LogSourceStdout)
		case "stderr":
			out = append(out, msb.LogSourceStderr)
		case "system":
			out = append(out, msb.LogSourceSystem)
		case "output":
			out = append(out, msb.LogSourceOutput)
		}
	}
	return out
}

func specToOptions(spec sandbox.Spec) ([]msb.SandboxOption, error) {
	var opts []msb.SandboxOption
	ref := spec.ImageReference()
	if ref == "" {
		return nil, fmt.Errorf("oci image reference required")
	}
	opts = append(opts, msb.WithImage(ref))
	if spec.Resources.VCPUs > 0 {
		opts = append(opts, msb.WithCPUs(spec.Resources.VCPUs))
	}
	if spec.Resources.MemoryMiB > 0 {
		opts = append(opts, msb.WithMemory(spec.Resources.MemoryMiB))
	}
	if spec.Resources.DiskSizeMiB != nil {
		opts = append(opts, msb.WithRootDisk(msb.RootDisk.Managed(*spec.Resources.DiskSizeMiB)))
	}
	if spec.Runtime.Workdir != nil && *spec.Runtime.Workdir != "" {
		opts = append(opts, msb.WithWorkdir(*spec.Runtime.Workdir))
	}
	if len(spec.Runtime.Cmd) > 0 {
		opts = append(opts, msb.WithCmd(spec.Runtime.Cmd...))
	}
	if len(spec.Runtime.Entrypoint) > 0 {
		opts = append(opts, msb.WithEntrypoint(spec.Runtime.Entrypoint...))
	}
	if len(spec.Env) > 0 {
		env := make(map[string]string, len(spec.Env))
		for _, e := range spec.Env {
			env[e.Key] = e.Value
		}
		opts = append(opts, msb.WithEnv(env))
	}
	if len(spec.Labels) > 0 {
		opts = append(opts, msb.WithLabels(spec.Labels))
	}
	if spec.Lifecycle.Ephemeral {
		opts = append(opts, msb.WithEphemeral(true))
	}
	if net := networkFromSpec(spec.Network); net != nil {
		opts = append(opts, msb.WithNetwork(net))
	} else if !spec.Network.Enabled {
		opts = append(opts, msb.WithNetwork(msb.NetworkPolicy.None()))
	}
	if len(spec.Mounts) > 0 {
		mounts := make(map[string]msb.MountConfig, len(spec.Mounts))
		for _, m := range spec.Mounts {
			mo := msb.MountOptions{Readonly: m.Options.ReadOnly, Noexec: m.Options.NoExec, Nosuid: m.Options.NoSuid, Nodev: m.Options.NoDev}
			switch m.Type {
			case "named":
				mounts[m.Guest] = msb.Mount.Named(m.Name, mo)
			case "bind":
				mounts[m.Guest] = msb.Mount.Bind(m.Host, mo)
			case "tmpfs":
				to := msb.TmpfsOptions{Noexec: m.Options.NoExec, Nosuid: m.Options.NoSuid, Nodev: m.Options.NoDev, Readonly: m.Options.ReadOnly}
				if m.SizeMiB != nil {
					to.SizeMiB = *m.SizeMiB
				}
				mounts[m.Guest] = msb.Mount.Tmpfs(to)
			}
		}
		opts = append(opts, msb.WithMounts(mounts))
	}
	switch spec.PullPolicy {
	case sandbox.PullAlways:
		opts = append(opts, msb.WithPullPolicy(msb.PullPolicyAlways))
	case sandbox.PullNever:
		opts = append(opts, msb.WithPullPolicy(msb.PullPolicyNever))
	}
	return opts, nil
}

func networkFromSpec(ns sandbox.NetworkSpec) *msb.NetworkConfig {
	if !ns.Enabled {
		return nil
	}
	if ns.Policy == nil {
		return msb.NetworkPolicy.AllowAll()
	}
	cfg := &msb.NetworkConfig{
		DefaultEgress:  mapAction(ns.Policy.DefaultEgress),
		DefaultIngress: mapAction(ns.Policy.DefaultIngress),
	}
	for _, r := range ns.Policy.Rules {
		cfg.Rules = append(cfg.Rules, msb.PolicyRule{
			Action:      mapAction(r.Action),
			Direction:   mapDirection(r.Direction),
			Destination: destinationString(r.Destination),
			Protocols:   mapProtocols(r.Protocols),
			Ports:       mapPorts(r.Ports),
		})
	}
	if ns.MaxConnections != nil {
		v := uint(*ns.MaxConnections)
		cfg.MaxConnections = &v
	}
	return cfg
}

func mapAction(a sandbox.PolicyAction) msb.PolicyAction {
	if a == sandbox.ActionAllow {
		return msb.PolicyActionAllow
	}
	return msb.PolicyActionDeny
}

func mapDirection(d string) msb.PolicyDirection {
	switch strings.ToLower(d) {
	case "ingress":
		return msb.PolicyDirectionIngress
	case "any":
		return msb.PolicyDirectionAny
	default:
		return msb.PolicyDirectionEgress
	}
}

func mapProtocols(ps []string) []msb.PolicyProtocol {
	out := make([]msb.PolicyProtocol, 0, len(ps))
	for _, p := range ps {
		out = append(out, msb.PolicyProtocol(strings.ToLower(p)))
	}
	return out
}

func mapPorts(ports []sandbox.PortRange) []string {
	out := make([]string, 0, len(ports))
	for _, p := range ports {
		if p.Start == p.End {
			out = append(out, fmt.Sprintf("%d", p.Start))
		} else {
			out = append(out, fmt.Sprintf("%d-%d", p.Start, p.End))
		}
	}
	return out
}

func destinationString(raw []byte) string {
	s := strings.TrimSpace(string(raw))
	if s == `"any"` || s == "any" {
		return "*"
	}
	s = strings.Trim(s, `"`)
	if strings.HasPrefix(s, "{") {
		if i := strings.Index(s, ":"); i > 0 {
			val := strings.Trim(s[i+1:], ` "}`)
			key := strings.Trim(s[1:i], `"`)
			switch key {
			case "group":
				return val
			case "domain_suffix":
				if !strings.HasPrefix(val, ".") {
					return "." + val
				}
				return val
			default:
				return val
			}
		}
	}
	return s
}
