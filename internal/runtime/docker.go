package runtime

import (
	"context"
	"fmt"
	"io"
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
	"github.com/prodioslabs/cellar/internal/sandboxagent"
)

const (
	labelSandboxID = "cellar.sandbox.id"
	bridgeName     = "cellar-sandboxes"
)

// Driver talks to the host Docker Engine using the runsc runtime.
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

// Close closes the Docker client.
func (d *Driver) Close() error {
	if d.cli == nil {
		return nil
	}
	return d.cli.Close()
}

// EnsureBridge creates the cellar sandbox bridge network if needed.
func (d *Driver) EnsureBridge(ctx context.Context) error {
	_, err := d.cli.NetworkInspect(ctx, bridgeName, network.InspectOptions{})
	if err == nil {
		return nil
	}
	_, err = d.cli.NetworkCreate(ctx, bridgeName, network.CreateOptions{
		Driver: "bridge",
		Labels: map[string]string{"cellar": "true"},
	})
	return err
}

// FindBySandboxID returns a container ID for the sandbox label, if any.
func (d *Driver) FindBySandboxID(ctx context.Context, sandboxID string) (string, error) {
	list, err := d.cli.ContainerList(ctx, container.ListOptions{
		All: true,
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

// CreateOpts configures cellar-agent injection for a sandbox container.
type CreateOpts struct {
	DataDir     string
	AgentBinary string
}

// CreateAndStart creates and starts a runsc container with cellar-agent as PID 1.
func (d *Driver) CreateAndStart(ctx context.Context, sb *sandbox.Sandbox, opts CreateOpts) (containerID string, err error) {
	runtimeName := sb.Spec.Runtime
	if runtimeName == "" {
		runtimeName = sandbox.DefaultRuntime
	}

	if opts.DataDir == "" {
		return "", fmt.Errorf("data dir required for sandbox agent")
	}
	agentBin, err := ResolveAgentBinary(opts.AgentBinary)
	if err != nil {
		return "", err
	}
	token, err := PrepareSandboxDir(opts.DataDir, sb.ID)
	if err != nil {
		return "", err
	}
	hostSandboxDir := SandboxHostDir(opts.DataDir, sb.ID)

	if err := d.pullIfMissing(ctx, sb.Spec.Image); err != nil {
		_ = CleanupSandboxDir(opts.DataDir, sb.ID)
		return "", err
	}

	cfg := &container.Config{
		Image: sb.Spec.Image,
		Env: append(append([]string(nil), sb.Spec.Env...),
			sandboxagent.EnvSandboxID+"="+sb.ID,
			sandboxagent.EnvAgentSock+"="+guestAgentSock,
			sandboxagent.EnvTokenFile+"="+guestRunCellar+"/"+agentTokenName,
		),
		WorkingDir: sb.Spec.WorkingDir,
		Labels:     map[string]string{labelSandboxID: sb.ID},
		// Always cellar-agent; Spec.Command/Args are ignored (use sandbox exec).
		Entrypoint: []string{guestAgentBin},
	}

	host := &container.HostConfig{
		Runtime: runtimeName,
		Resources: container.Resources{
			NanoCPUs: sb.Spec.Resources.CPUNanoCores,
			Memory:   sb.Spec.Resources.MemoryBytes,
		},
		Mounts: []mount.Mount{
			{
				Type:     mount.TypeBind,
				Source:   agentBin,
				Target:   guestAgentBin,
				ReadOnly: true,
			},
			{
				Type:   mount.TypeBind,
				Source: hostSandboxDir,
				Target: guestRunCellar,
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
		if err := d.EnsureBridge(ctx); err != nil {
			_ = CleanupSandboxDir(opts.DataDir, sb.ID)
			return "", fmt.Errorf("ensure bridge: %w", err)
		}
		netCfg = &network.NetworkingConfig{
			EndpointsConfig: map[string]*network.EndpointSettings{
				bridgeName: {},
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

	sock := AgentSockPath(opts.DataDir, sb.ID)
	if err := WaitAgentHealthy(ctx, sock, token, sb.ID, 30*time.Second); err != nil {
		_ = d.cli.ContainerStop(ctx, resp.ID, container.StopOptions{})
		_ = d.cli.ContainerRemove(ctx, resp.ID, container.RemoveOptions{Force: true})
		_ = CleanupSandboxDir(opts.DataDir, sb.ID)
		return "", fmt.Errorf("agent not ready: %w", err)
	}
	return resp.ID, nil
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

// ContainerIP returns the IPv4 address on the cellar bridge, if any.
func (d *Driver) ContainerIP(ctx context.Context, containerID string) (string, error) {
	ins, err := d.cli.ContainerInspect(ctx, containerID)
	if err != nil {
		return "", err
	}
	if ins.NetworkSettings == nil {
		return "", nil
	}
	if n, ok := ins.NetworkSettings.Networks[bridgeName]; ok && n != nil {
		return n.IPAddress, nil
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

// RuntimeAvailable checks whether the named runtime is registered.
func (d *Driver) RuntimeAvailable(ctx context.Context, name string) error {
	info, err := d.cli.Info(ctx)
	if err != nil {
		return err
	}
	if name == "" {
		name = sandbox.DefaultRuntime
	}
	if _, ok := info.Runtimes[name]; !ok {
		var names []string
		for n := range info.Runtimes {
			names = append(names, n)
		}
		return fmt.Errorf("docker runtime %q not registered (have: %s); install runsc", name, strings.Join(names, ", "))
	}
	return nil
}
