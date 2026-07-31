package runtime

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/api/types/mount"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/client"
	"github.com/docker/docker/pkg/stdcopy"

	"github.com/prodioslabs/cellar/internal/sandbox"
)

const (
	labelSandboxID   = "cellar.sandbox.id"
	labelManaged     = "cellar.managed"
	labelRole        = "cellar.role"
	labelSandboxNet  = "sandbox-net"
	labelSandboxIDKey = "cellar.sandbox_id"
)

// Driver talks to the host Docker Engine.
type Driver struct {
	cli *client.Client
}

// NewDriver connects to the local Docker daemon.
func NewDriver() (*Driver, error) {
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return nil, err
	}
	return &Driver{cli: cli}, nil
}

// NewDriverFromClient wraps an existing Docker client.
func NewDriverFromClient(cli *client.Client) *Driver {
	return &Driver{cli: cli}
}

// Client returns the underlying Docker client.
func (d *Driver) Client() *client.Client { return d.cli }

// Close closes the Docker client.
func (d *Driver) Close() error {
	if d.cli == nil {
		return nil
	}
	return d.cli.Close()
}

// FindBySandboxID returns a container ID for the sandbox label, if any.
func (d *Driver) FindBySandboxID(ctx context.Context, sandboxID string) (string, error) {
	list, err := d.cli.ContainerList(ctx, container.ListOptions{
		All:     true,
		Filters: filters.NewArgs(filters.Arg("label", labelSandboxID+"="+sandboxID)),
	})
	if err != nil {
		return "", err
	}
	if len(list) == 0 {
		return "", nil
	}
	return list[0].ID, nil
}

// SandboxNetworkName is the Docker network name for a sandbox's internal net.
func SandboxNetworkName(sandboxID string) string {
	return "cellar-sb-" + sandboxID
}

// CreateSandboxNetwork creates an Internal bridge with the given /29 subnet.
func (d *Driver) CreateSandboxNetwork(ctx context.Context, sandboxID, subnetCIDR, _gatewayIP string) (networkID string, err error) {
	name := SandboxNetworkName(sandboxID)
	if existing, err := d.cli.NetworkInspect(ctx, name, network.InspectOptions{}); err == nil {
		return existing.ID, nil
	}
	bridgeGW := dockerBridgeGateway(subnetCIDR)
	resp, err := d.cli.NetworkCreate(ctx, name, network.CreateOptions{
		Driver:   "bridge",
		Internal: true,
		IPAM: &network.IPAM{
			Config: []network.IPAMConfig{{
				Subnet:  subnetCIDR,
				Gateway: bridgeGW,
			}},
		},
		Labels: map[string]string{
			labelManaged:      "true",
			labelRole:         labelSandboxNet,
			labelSandboxIDKey: sandboxID,
		},
	})
	if err != nil {
		return "", err
	}
	return resp.ID, nil
}

func dockerBridgeGateway(cidr string) string {
	_, n, err := net.ParseCIDR(cidr)
	if err != nil {
		return subnetBase(cidr) + ".1"
	}
	ip := n.IP.To4()
	if ip == nil {
		return subnetBase(cidr) + ".1"
	}
	out := make(net.IP, 4)
	copy(out, ip)
	out[3]++
	return out.String()
}

func subnetBase(cidr string) string {
	// "172.30.0.0/29" -> "172.30.0"
	host, _, _ := strings.Cut(cidr, "/")
	parts := strings.Split(host, ".")
	if len(parts) != 4 {
		return "172.30.0"
	}
	return parts[0] + "." + parts[1] + "." + parts[2]
}

// RemoveSandboxNetwork removes the per-sandbox internal network. Idempotent.
// Any attached containers (e.g. egress gateway legs) are disconnected first.
func (d *Driver) RemoveSandboxNetwork(ctx context.Context, sandboxID string) error {
	name := SandboxNetworkName(sandboxID)
	if ins, err := d.cli.NetworkInspect(ctx, name, network.InspectOptions{}); err == nil {
		for cid := range ins.Containers {
			_ = d.cli.NetworkDisconnect(ctx, name, cid, true)
		}
	}
	err := d.cli.NetworkRemove(ctx, name)
	if err == nil {
		return nil
	}
	if strings.Contains(err.Error(), "not found") || strings.Contains(err.Error(), "No such network") {
		return nil
	}
	return err
}

// ListManagedSandboxNetworks returns sandboxID -> subnet CIDR for labeled nets.
func (d *Driver) ListManagedSandboxNetworks(ctx context.Context) (map[string]string, error) {
	list, err := d.cli.NetworkList(ctx, network.ListOptions{
		Filters: filters.NewArgs(
			filters.Arg("label", labelManaged+"=true"),
			filters.Arg("label", labelRole+"="+labelSandboxNet),
		),
	})
	if err != nil {
		return nil, err
	}
	out := make(map[string]string)
	for _, n := range list {
		id := n.Labels[labelSandboxIDKey]
		if id == "" {
			continue
		}
		ins, err := d.cli.NetworkInspect(ctx, n.ID, network.InspectOptions{})
		if err != nil {
			continue
		}
		if len(ins.IPAM.Config) > 0 && ins.IPAM.Config[0].Subnet != "" {
			out[id] = ins.IPAM.Config[0].Subnet
		}
	}
	return out, nil
}

// SandboxNetworkSubnet returns the IPv4 subnet CIDR for a sandbox network, if any.
func (d *Driver) SandboxNetworkSubnet(ctx context.Context, sandboxID string) (string, error) {
	name := SandboxNetworkName(sandboxID)
	ins, err := d.cli.NetworkInspect(ctx, name, network.InspectOptions{})
	if err != nil {
		return "", err
	}
	if len(ins.IPAM.Config) == 0 || ins.IPAM.Config[0].Subnet == "" {
		return "", fmt.Errorf("sandbox network %s has no subnet", name)
	}
	return ins.IPAM.Config[0].Subnet, nil
}

// FindSandboxNetworkID returns the Docker network ID for a sandbox, if any.
func (d *Driver) FindSandboxNetworkID(ctx context.Context, sandboxID string) (string, error) {
	name := SandboxNetworkName(sandboxID)
	ins, err := d.cli.NetworkInspect(ctx, name, network.InspectOptions{})
	if err != nil {
		return "", err
	}
	return ins.ID, nil
}

// CreateOpts configures cellar-agent injection and optional egress topology.
type CreateOpts struct {
	DataDir     string
	AgentBinary string
	// Egress fields (empty when NetworkMode is none).
	NetworkName string // cellar-sb-<id>
	DNSServer   string // gateway .2
	SandboxIP   string // conventional .3
}

const defaultPidsLimit = int64(4096)

// CreateAndStart creates and starts a container with cellar-agent as PID 1.
func (d *Driver) CreateAndStart(ctx context.Context, sb *sandbox.Sandbox, opts CreateOpts) (containerID string, err error) {
	if opts.DataDir == "" {
		return "", fmt.Errorf("data dir required for sandbox")
	}
	agentBin, err := ResolveAgentBinary(opts.AgentBinary)
	if err != nil {
		return "", err
	}
	if err := PrepareSandboxDir(opts.DataDir, sb.ID); err != nil {
		return "", err
	}

	if err := d.pullIfMissing(ctx, sb.Spec.Image); err != nil {
		_ = CleanupSandboxDir(opts.DataDir, sb.ID)
		return "", err
	}

	pidsLimit := defaultPidsLimit
	cfg := &container.Config{
		Image: sb.Spec.Image,
		User:  "0:0",
		Env: append(append([]string(nil), sb.Spec.Env...),
			"CELLAR_SANDBOX_ID="+sb.ID,
		),
		WorkingDir: sb.Spec.WorkingDir,
		Labels: map[string]string{
			labelSandboxID: sb.ID,
			labelManaged:   "true",
		},
		Entrypoint: []string{guestAgentBin},
	}

	host := &container.HostConfig{
		Resources: container.Resources{
			NanoCPUs:  sb.Spec.Resources.CPUNanoCores,
			Memory:    sb.Spec.Resources.MemoryBytes,
			PidsLimit: &pidsLimit,
		},
		CapDrop:     []string{"ALL"},
		SecurityOpt: []string{"no-new-privileges:true"},
		Mounts: []mount.Mount{
			{
				Type:     mount.TypeBind,
				Source:   agentBin,
				Target:   guestAgentBin,
				ReadOnly: true,
			},
		},
	}
	for _, m := range sb.Spec.Mounts {
		host.Mounts = append(host.Mounts, mount.Mount{
			Type:     mount.TypeBind,
			Source:   m.Source,
			Target:   m.Target,
			ReadOnly: m.ReadOnly,
		})
	}

	var netCfg *network.NetworkingConfig
	switch sb.Spec.Network.Mode {
	case sandbox.NetworkNone, "":
		host.NetworkMode = "none"
	default:
		if opts.NetworkName == "" || opts.DNSServer == "" || opts.SandboxIP == "" {
			_ = CleanupSandboxDir(opts.DataDir, sb.ID)
			return "", fmt.Errorf("egress network options required for networked sandbox")
		}
		// Mounting over /etc/resolv.conf bypasses Docker's embedded 127.0.0.11
		// stub. HostConfig.DNS alone still leaves that stub in place on
		// user-defined networks; the bind mount is the only engine-version-
		// independent way to force all DNS through the egress gateway.
		resolvPath, err := WriteEgressResolvConf(opts.DataDir, sb.ID, opts.DNSServer)
		if err != nil {
			_ = CleanupSandboxDir(opts.DataDir, sb.ID)
			return "", err
		}
		host.Mounts = append(host.Mounts, mount.Mount{
			Type:     mount.TypeBind,
			Source:   resolvPath,
			Target:   guestResolvConf,
			ReadOnly: true,
		})
		host.DNS = []string{opts.DNSServer}
		ep := &network.EndpointSettings{
			IPAMConfig: &network.EndpointIPAMConfig{IPv4Address: opts.SandboxIP},
		}
		netCfg = &network.NetworkingConfig{
			EndpointsConfig: map[string]*network.EndpointSettings{
				opts.NetworkName: ep,
			},
		}
	}

	resp, err := d.cli.ContainerCreate(ctx, cfg, host, netCfg, nil, "cellar-sb-"+sb.ID)
	if err != nil {
		_ = CleanupSandboxDir(opts.DataDir, sb.ID)
		return "", err
	}
	if err := d.cli.ContainerStart(ctx, resp.ID, container.StartOptions{}); err != nil {
		_ = d.cli.ContainerRemove(ctx, resp.ID, container.RemoveOptions{Force: true})
		_ = CleanupSandboxDir(opts.DataDir, sb.ID)
		return "", err
	}

	if err := d.waitContainerRunning(ctx, resp.ID, 30*time.Second); err != nil {
		diagnostics := d.agentStartupDiagnostics(ctx, resp.ID)
		_ = d.cli.ContainerStop(ctx, resp.ID, container.StopOptions{})
		_ = d.cli.ContainerRemove(ctx, resp.ID, container.RemoveOptions{Force: true})
		_ = CleanupSandboxDir(opts.DataDir, sb.ID)
		if diagnostics != "" {
			return "", fmt.Errorf("sandbox not ready: %w (%s)", err, diagnostics)
		}
		return "", fmt.Errorf("sandbox not ready: %w", err)
	}
	return resp.ID, nil
}

func (d *Driver) waitContainerRunning(ctx context.Context, containerID string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		phase, exitCode, err := d.InspectPhase(ctx, containerID)
		if err == nil && phase == sandbox.PhaseRunning {
			return nil
		}
		if err == nil && (phase == sandbox.PhaseStopped || phase == sandbox.PhaseFailed) {
			return fmt.Errorf("container exited (phase=%s exit=%d)", phase, exitCode)
		}
		if time.Now().After(deadline) {
			if err != nil {
				return fmt.Errorf("timeout waiting for running: %w", err)
			}
			return fmt.Errorf("timeout waiting for container to become running (phase=%s)", phase)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
}

func (d *Driver) agentStartupDiagnostics(ctx context.Context, containerID string) string {
	diagnosticCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
	defer cancel()

	var parts []string
	if ins, err := d.cli.ContainerInspect(diagnosticCtx, containerID); err == nil && ins.State != nil {
		if ins.State.Running {
			parts = append(parts, "container still running")
		} else {
			parts = append(parts, fmt.Sprintf("container exited with code %d", ins.State.ExitCode))
		}
	}

	logs, err := d.Logs(diagnosticCtx, containerID, false, "20")
	if err == nil {
		defer logs.Close()
		var stdout, stderr bytes.Buffer
		if err := DemuxLogs(logs, &stdout, &stderr); err == nil {
			text := strings.TrimSpace(strings.Join([]string{stderr.String(), stdout.String()}, "\n"))
			if text != "" {
				const maxLogBytes = 4096
				if len(text) > maxLogBytes {
					text = text[len(text)-maxLogBytes:]
				}
				parts = append(parts, "logs: "+strings.Join(strings.Fields(text), " "))
			}
		}
	}
	return strings.Join(parts, "; ")
}

func (d *Driver) pullIfMissing(ctx context.Context, ref string) error {
	_, _, err := d.cli.ImageInspectWithRaw(ctx, ref)
	if err == nil {
		return nil
	}
	rc, err := d.cli.ImagePull(ctx, ref, image.PullOptions{})
	if err != nil {
		return fmt.Errorf("pull %s: %w", ref, err)
	}
	defer rc.Close()
	_, _ = io.Copy(io.Discard, rc)
	return nil
}

// Stop stops a container.
func (d *Driver) Stop(ctx context.Context, containerID string, timeoutSec int) error {
	if containerID == "" {
		return nil
	}
	to := timeoutSec
	return d.cli.ContainerStop(ctx, containerID, container.StopOptions{Timeout: &to})
}

// Remove force-removes a container.
func (d *Driver) Remove(ctx context.Context, containerID string) error {
	if containerID == "" {
		return nil
	}
	return d.cli.ContainerRemove(ctx, containerID, container.RemoveOptions{Force: true})
}

// InspectPhase maps Docker state to a sandbox phase.
func (d *Driver) InspectPhase(ctx context.Context, containerID string) (sandbox.Phase, int32, error) {
	ins, err := d.cli.ContainerInspect(ctx, containerID)
	if err != nil {
		return sandbox.PhaseFailed, 0, err
	}
	if ins.State == nil {
		return sandbox.PhaseFailed, 0, fmt.Errorf("missing state")
	}
	switch {
	case ins.State.Running:
		return sandbox.PhaseRunning, 0, nil
	case ins.State.ExitCode != 0:
		return sandbox.PhaseFailed, int32(ins.State.ExitCode), nil
	default:
		return sandbox.PhaseStopped, int32(ins.State.ExitCode), nil
	}
}

// ContainerIP returns the IPv4 address on the sandbox's internal network, if any.
func (d *Driver) ContainerIP(ctx context.Context, containerID string) (string, error) {
	ins, err := d.cli.ContainerInspect(ctx, containerID)
	if err != nil {
		return "", err
	}
	if ins.NetworkSettings == nil {
		return "", nil
	}
	for name, n := range ins.NetworkSettings.Networks {
		if n != nil && n.IPAddress != "" && strings.HasPrefix(name, "cellar-sb-") {
			return n.IPAddress, nil
		}
	}
	for _, n := range ins.NetworkSettings.Networks {
		if n != nil && n.IPAddress != "" {
			return n.IPAddress, nil
		}
	}
	return "", nil
}

// Logs streams container logs. Caller must close the returned reader.
func (d *Driver) Logs(ctx context.Context, containerID string, follow bool, tail string) (io.ReadCloser, error) {
	opts := container.LogsOptions{
		ShowStdout: true,
		ShowStderr: true,
		Follow:     follow,
		Timestamps: false,
	}
	if tail != "" && tail != "all" {
		opts.Tail = tail
	}
	return d.cli.ContainerLogs(ctx, containerID, opts)
}

// DemuxLogs copies Docker multiplexed logs to stdout/stderr writers.
func DemuxLogs(r io.Reader, stdout, stderr io.Writer) error {
	_, err := stdcopy.StdCopy(stdout, stderr, r)
	return err
}

// ExecInspectExit returns the exit code for an exec ID after attach completes.
func (d *Driver) ExecInspectExit(ctx context.Context, execID string) (int, error) {
	ins, err := d.cli.ContainerExecInspect(ctx, execID)
	if err != nil {
		return -1, err
	}
	return ins.ExitCode, nil
}

// Wait waits for container exit.
func (d *Driver) Wait(ctx context.Context, containerID string) (int64, error) {
	ch, errCh := d.cli.ContainerWait(ctx, containerID, container.WaitConditionNotRunning)
	select {
	case <-ctx.Done():
		return -1, ctx.Err()
	case err := <-errCh:
		return -1, err
	case res := <-ch:
		return res.StatusCode, nil
	}
}

// Ping checks Docker connectivity.
func (d *Driver) Ping(ctx context.Context) error {
	_, err := d.cli.Ping(ctx)
	return err
}

// EgressState is persisted per sandbox for restart recovery.
type EgressState struct {
	GatewayID  string `json:"gateway_id"`
	SubnetCIDR string `json:"subnet_cidr"`
	NetworkID  string `json:"network_id"`
	GatewayIP  string `json:"gateway_ip"`
	SandboxIP  string `json:"sandbox_ip"`
}

// WriteEgressState persists egress assignment under the sandbox host dir.
func WriteEgressState(dataDir, sandboxID string, st EgressState) error {
	path := filepath.Join(SandboxHostDir(dataDir, sandboxID), "egress.json")
	data, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

// ReadEgressState loads egress assignment, if present.
func ReadEgressState(dataDir, sandboxID string) (EgressState, bool, error) {
	path := filepath.Join(SandboxHostDir(dataDir, sandboxID), "egress.json")
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return EgressState{}, false, nil
		}
		return EgressState{}, false, err
	}
	var st EgressState
	if err := json.Unmarshal(data, &st); err != nil {
		return EgressState{}, false, err
	}
	return st, true, nil
}
