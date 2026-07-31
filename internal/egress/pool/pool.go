package pool

import (
	"context"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/api/types/mount"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/client"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	cellarv1 "github.com/prodioslabs/cellar/api/gen"
	"github.com/prodioslabs/cellar/internal/sandbox"
)

const (
	DefaultImage    = "cellar/egress-gateway"
	DefaultMaxLegs  = 100
	labelManaged    = "cellar.managed"
	labelRole       = "cellar.role"
	roleGateway     = "egress-gateway"
	EgressNetName   = "cellar-egress"
	controlSockName = "control.sock"
	guestControlDir = "/run/cellar/egress"
)

// Config configures the gateway container pool.
type Config struct {
	DataDir           string
	Image             string
	MaxLegs           int
	PrivateExceptions []string
}

// Instance is one egress-gateway container.
type Instance struct {
	ID       string // short gateway id (directory / label)
	CID      string // docker container id
	SockPath string
	Legs     int
}

// Pool manages shared egress-gateway containers on a node.
type Pool struct {
	mu       sync.Mutex
	cli      *client.Client
	cfg      Config
	gateways []*Instance
	// sandbox -> gateway id
	assign map[string]string
}

// New creates a pool (does not start gateways yet).
func New(cli *client.Client, cfg Config) *Pool {
	if cfg.Image == "" {
		cfg.Image = DefaultImage
	}
	if cfg.MaxLegs <= 0 {
		cfg.MaxLegs = DefaultMaxLegs
	}
	return &Pool{
		cli:    cli,
		cfg:    cfg,
		assign: make(map[string]string),
	}
}

// EnsureReady creates the egress bridge and at least one gateway.
func (p *Pool) EnsureReady(ctx context.Context) error {
	if err := p.ensureEgressNetwork(ctx); err != nil {
		return err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if err := p.adoptExistingLocked(ctx); err != nil {
		log.Printf("egress pool adopt: %v", err)
	}
	if len(p.gateways) == 0 {
		inst, err := p.spawnLocked(ctx)
		if err != nil {
			return err
		}
		p.gateways = append(p.gateways, inst)
	}
	return nil
}

func (p *Pool) ensureEgressNetwork(ctx context.Context) error {
	_, err := p.cli.NetworkInspect(ctx, EgressNetName, network.InspectOptions{})
	if err == nil {
		return nil
	}
	_, err = p.cli.NetworkCreate(ctx, EgressNetName, network.CreateOptions{
		Driver: "bridge",
		Labels: map[string]string{
			labelManaged: "true",
			labelRole:    "egress",
		},
	})
	return err
}

func (p *Pool) adoptExistingLocked(ctx context.Context) error {
	list, err := p.cli.ContainerList(ctx, container.ListOptions{
		All: true,
		Filters: filters.NewArgs(
			filters.Arg("label", labelManaged+"=true"),
			filters.Arg("label", labelRole+"="+roleGateway),
		),
	})
	if err != nil {
		return err
	}
	p.gateways = nil
	for _, c := range list {
		id := c.Labels["cellar.gateway_id"]
		if id == "" {
			continue
		}
		sock := filepath.Join(p.cfg.DataDir, "egress", id, controlSockName)
		if c.State != "running" {
			if err := p.cli.ContainerStart(ctx, c.ID, container.StartOptions{}); err != nil {
				log.Printf("egress pool: start adopted %s: %v", id, err)
				continue
			}
		}
		p.gateways = append(p.gateways, &Instance{
			ID:       id,
			CID:      c.ID,
			SockPath: sock,
		})
	}
	return nil
}

func (p *Pool) spawnLocked(ctx context.Context) (*Instance, error) {
	id := fmt.Sprintf("gw-%d", time.Now().UnixNano())
	hostDir := filepath.Join(p.cfg.DataDir, "egress", id)
	// 0700 so world-writable control.sock (created as root in the container)
	// is only reachable by the cellard user that owns this directory.
	if err := os.MkdirAll(hostDir, 0o700); err != nil {
		return nil, err
	}
	if err := os.Chmod(hostDir, 0o700); err != nil {
		return nil, err
	}
	sock := filepath.Join(hostDir, controlSockName)
	_ = os.Remove(sock)

	if err := p.pullIfMissing(ctx, p.cfg.Image); err != nil {
		return nil, err
	}

	cfg := &container.Config{
		Image: p.cfg.Image,
		Labels: map[string]string{
			labelManaged:        "true",
			labelRole:           roleGateway,
			"cellar.gateway_id": id,
		},
		Entrypoint: []string{"/usr/local/bin/cellar-egress-gateway", "-control-sock", guestControlDir + "/" + controlSockName},
	}
	host := &container.HostConfig{
		CapAdd:        []string{"NET_ADMIN"},
		RestartPolicy: container.RestartPolicy{Name: "always"},
		Mounts: []mount.Mount{{
			Type:   mount.TypeBind,
			Source: hostDir,
			Target: guestControlDir,
		}},
	}
	netCfg := &network.NetworkingConfig{
		EndpointsConfig: map[string]*network.EndpointSettings{
			EgressNetName: {},
		},
	}
	resp, err := p.cli.ContainerCreate(ctx, cfg, host, netCfg, nil, "cellar-egress-"+id)
	if err != nil {
		return nil, fmt.Errorf("create egress gateway: %w", err)
	}
	if err := p.cli.ContainerStart(ctx, resp.ID, container.StartOptions{}); err != nil {
		_ = p.cli.ContainerRemove(ctx, resp.ID, container.RemoveOptions{Force: true})
		return nil, fmt.Errorf("start egress gateway: %w", err)
	}
	inst := &Instance{ID: id, CID: resp.ID, SockPath: sock}
	if err := waitSock(sock, 30*time.Second); err != nil {
		_ = p.cli.ContainerRemove(ctx, resp.ID, container.RemoveOptions{Force: true})
		return nil, fmt.Errorf("egress gateway control sock: %w", err)
	}
	return inst, nil
}

func (p *Pool) pullIfMissing(ctx context.Context, ref string) error {
	_, _, err := p.cli.ImageInspectWithRaw(ctx, ref)
	if err == nil {
		return nil
	}
	rc, err := p.cli.ImagePull(ctx, ref, image.PullOptions{})
	if err != nil {
		log.Printf("egress pool: image pull %s: %v (continuing if local)", ref, err)
		_, _, err2 := p.cli.ImageInspectWithRaw(ctx, ref)
		return err2
	}
	defer rc.Close()
	_, _ = io.Copy(io.Discard, rc)
	return nil
}

func waitSock(path string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	var lastErr error
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("unix", path, 200*time.Millisecond)
		if err == nil {
			_ = conn.Close()
			return nil
		}
		lastErr = err
		time.Sleep(100 * time.Millisecond)
	}
	if lastErr != nil {
		return fmt.Errorf("timeout waiting for %s: %w", path, lastErr)
	}
	return fmt.Errorf("timeout waiting for %s", path)
}

// Assign picks the least-loaded gateway under MaxLegs, spawning if needed.
func (p *Pool) Assign(ctx context.Context, sandboxID string) (*Instance, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if gwID, ok := p.assign[sandboxID]; ok {
		for _, g := range p.gateways {
			if g.ID == gwID {
				return g, nil
			}
		}
	}
	var best *Instance
	for _, g := range p.gateways {
		if g.Legs >= p.cfg.MaxLegs {
			continue
		}
		if best == nil || g.Legs < best.Legs {
			best = g
		}
	}
	if best == nil {
		inst, err := p.spawnLocked(ctx)
		if err != nil {
			return nil, err
		}
		p.gateways = append(p.gateways, inst)
		best = inst
	}
	best.Legs++
	p.assign[sandboxID] = best.ID
	return best, nil
}

// Release decrements leg count for a sandbox's gateway.
func (p *Pool) Release(sandboxID string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	gwID, ok := p.assign[sandboxID]
	if !ok {
		return
	}
	delete(p.assign, sandboxID)
	for _, g := range p.gateways {
		if g.ID == gwID && g.Legs > 0 {
			g.Legs--
		}
	}
}

// GatewayFor returns the instance assigned to a sandbox.
func (p *Pool) GatewayFor(sandboxID string) (*Instance, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	id, ok := p.assign[sandboxID]
	if !ok {
		return nil, false
	}
	for _, g := range p.gateways {
		if g.ID == id {
			return g, true
		}
	}
	return nil, false
}

// SetAssignment records that sandboxID is on gw without incrementing legs
// (used when adopting live state after restart).
func (p *Pool) SetAssignment(sandboxID, gwID string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.assign[sandboxID] = gwID
	for _, g := range p.gateways {
		if g.ID == gwID {
			g.Legs++
			return
		}
	}
}

// ConnectSandbox attaches the gateway to an internal network at gatewayIP.
func (p *Pool) ConnectSandbox(ctx context.Context, gw *Instance, networkID, gatewayIP string) error {
	return p.cli.NetworkConnect(ctx, networkID, gw.CID, &network.EndpointSettings{
		IPAMConfig: &network.EndpointIPAMConfig{IPv4Address: gatewayIP},
	})
}

// DisconnectSandbox detaches the gateway from a network.
func (p *Pool) DisconnectSandbox(ctx context.Context, gw *Instance, networkID string) error {
	return p.cli.NetworkDisconnect(ctx, networkID, gw.CID, true)
}

// ControlClient dials the gateway's Unix control socket.
func ControlClient(sockPath string) (cellarv1.EgressGatewayControlClient, *grpc.ClientConn, error) {
	conn, err := grpc.NewClient("unix://"+sockPath, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, nil, err
	}
	return cellarv1.NewEgressGatewayControlClient(conn), conn, nil
}

// RegisterSandbox pushes session state to the gateway.
func (p *Pool) RegisterSandbox(ctx context.Context, gw *Instance, sandboxID, networkID, subnetCIDR, gatewayIP string, policy sandbox.NetworkPolicy) error {
	cli, conn, err := ControlClient(gw.SockPath)
	if err != nil {
		return err
	}
	defer conn.Close()
	_, err = cli.RegisterSandbox(ctx, &cellarv1.RegisterSandboxRequest{
		SandboxId:         sandboxID,
		NetworkId:         networkID,
		SubnetCidr:        subnetCIDR,
		GatewayIp:         gatewayIP,
		Policy:            sandbox.NetworkPolicyToProto(policy),
		PrivateExceptions: append([]string(nil), p.cfg.PrivateExceptions...),
	})
	return err
}

// DeregisterSandbox tells the gateway to drop a sandbox.
func (p *Pool) DeregisterSandbox(ctx context.Context, gw *Instance, sandboxID string) error {
	cli, conn, err := ControlClient(gw.SockPath)
	if err != nil {
		return err
	}
	defer conn.Close()
	_, err = cli.DeregisterSandbox(ctx, &cellarv1.DeregisterSandboxRequest{SandboxId: sandboxID})
	return err
}

// UpdatePolicy replaces policy on the gateway.
func (p *Pool) UpdatePolicy(ctx context.Context, gw *Instance, sandboxID string, policy sandbox.NetworkPolicy) error {
	cli, conn, err := ControlClient(gw.SockPath)
	if err != nil {
		return err
	}
	defer conn.Close()
	_, err = cli.UpdatePolicy(ctx, &cellarv1.UpdatePolicyRequest{
		SandboxId: sandboxID,
		Policy:    sandbox.NetworkPolicyToProto(policy),
	})
	return err
}

// DockerClient returns the underlying client (for Driver sharing).
func (p *Pool) DockerClient() *client.Client { return p.cli }
