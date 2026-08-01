package egress

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/client"
	"github.com/docker/go-connections/nat"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"

	cellarv1 "github.com/prodioslabs/cellar/api/gen"
	"github.com/prodioslabs/cellar/internal/sandbox"
)

const (
	DefaultImage     = "cellar/egress-gateway"
	DefaultMaxLegs   = 100
	labelManaged     = "cellar.managed"
	labelRole        = "cellar.role"
	roleGateway      = "egress-gateway"
	EgressNetName    = "cellar-egress"
	controlPort      = "17948"
	controlPortProto = "17948/tcp"
	tokenFileName    = "control.token"
	envControlToken  = "CELLAR_EGRESS_CONTROL_TOKEN"
)

// PoolConfig configures the gateway container pool.
type PoolConfig struct {
	DataDir           string
	Image             string
	MaxLegs           int
	PrivateExceptions []string
}

// Instance is one egress-gateway container.
type Instance struct {
	ID          string // short gateway id (directory / label)
	CID         string // docker container id
	ControlAddr string // host dial address, e.g. 127.0.0.1:32768
	Token       string
	Legs        int
}

// Pool manages shared egress-gateway containers on a node.
type Pool struct {
	mu       sync.Mutex
	cli      *client.Client
	cfg      PoolConfig
	gateways []*Instance
	// sandbox -> gateway id
	assign map[string]string
}

// NewPool creates a pool (does not start gateways yet).
func NewPool(cli *client.Client, cfg PoolConfig) *Pool {
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
// Any leftover managed gateway containers are removed first so a new image
// tag is always used (no adopt across restarts).
func (p *Pool) EnsureReady(ctx context.Context) error {
	if err := p.ensureEgressNetwork(ctx); err != nil {
		return err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if err := p.removeExistingGatewaysLocked(ctx); err != nil {
		log.Printf("egress pool: remove existing gateways: %v", err)
	}
	inst, err := p.spawnLocked(ctx)
	if err != nil {
		return err
	}
	p.gateways = []*Instance{inst}
	return nil
}

// Close removes all managed egress-gateway containers and clears pool state.
func (p *Pool) Close(ctx context.Context) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, g := range p.gateways {
		if g == nil {
			continue
		}
		p.removeObsoleteGateway(ctx, g.CID, g.ID)
	}
	p.gateways = nil
	p.assign = make(map[string]string)
	if err := p.removeExistingGatewaysLocked(ctx); err != nil {
		log.Printf("egress pool close: sweep leftovers: %v", err)
	}
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

func (p *Pool) removeExistingGatewaysLocked(ctx context.Context) error {
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
	for _, c := range list {
		id := c.Labels["cellar.gateway_id"]
		p.removeObsoleteGateway(ctx, c.ID, id)
	}
	p.gateways = nil
	return nil
}

func (p *Pool) removeObsoleteGateway(ctx context.Context, containerID, gwID string) {
	if containerID == "" {
		return
	}
	_ = p.cli.ContainerRemove(ctx, containerID, container.RemoveOptions{Force: true})
	if gwID != "" && p.cfg.DataDir != "" {
		_ = os.RemoveAll(filepath.Join(p.cfg.DataDir, "egress", gwID))
	}
}

func (p *Pool) spawnLocked(ctx context.Context) (*Instance, error) {
	id := fmt.Sprintf("gw-%d", time.Now().UnixNano())
	hostDir := filepath.Join(p.cfg.DataDir, "egress", id)
	if err := os.MkdirAll(hostDir, 0o700); err != nil {
		return nil, err
	}
	if err := os.Chmod(hostDir, 0o700); err != nil {
		return nil, err
	}
	token, err := mintToken()
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(filepath.Join(hostDir, tokenFileName), []byte(token), 0o600); err != nil {
		return nil, err
	}

	if err := p.pullIfMissing(ctx, p.cfg.Image); err != nil {
		return nil, err
	}

	exposed, err := nat.NewPort("tcp", controlPort)
	if err != nil {
		return nil, err
	}
	cfg := &container.Config{
		Image: p.cfg.Image,
		Labels: map[string]string{
			labelManaged:        "true",
			labelRole:           roleGateway,
			"cellar.gateway_id": id,
		},
		Env:          []string{envControlToken + "=" + token},
		ExposedPorts: nat.PortSet{exposed: struct{}{}},
		Entrypoint: []string{
			"/usr/local/bin/cellar-egress-gateway",
			"-control-addr", "0.0.0.0:" + controlPort,
		},
	}
	host := &container.HostConfig{
		CapAdd:        []string{"NET_ADMIN"},
		RestartPolicy: container.RestartPolicy{Name: "always"},
		PortBindings: nat.PortMap{
			exposed: []nat.PortBinding{{
				HostIP:   "127.0.0.1",
				HostPort: "0",
			}},
		},
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
	addr, err := publishedControlAddr(ctx, p.cli, resp.ID)
	if err != nil {
		_ = p.cli.ContainerRemove(ctx, resp.ID, container.RemoveOptions{Force: true})
		return nil, fmt.Errorf("egress gateway published port: %w", err)
	}
	if err := waitTCP(addr, 30*time.Second); err != nil {
		_ = p.cli.ContainerRemove(ctx, resp.ID, container.RemoveOptions{Force: true})
		return nil, fmt.Errorf("egress gateway control: %w", err)
	}
	return &Instance{ID: id, CID: resp.ID, ControlAddr: addr, Token: token}, nil
}

func mintToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func publishedControlAddr(ctx context.Context, cli *client.Client, containerID string) (string, error) {
	ins, err := cli.ContainerInspect(ctx, containerID)
	if err != nil {
		return "", err
	}
	if ins.NetworkSettings == nil {
		return "", fmt.Errorf("no network settings")
	}
	bindings := ins.NetworkSettings.Ports[nat.Port(controlPortProto)]
	if len(bindings) == 0 || bindings[0].HostPort == "" {
		return "", fmt.Errorf("control port %s not published", controlPort)
	}
	hostIP := bindings[0].HostIP
	if hostIP == "" || hostIP == "0.0.0.0" {
		hostIP = "127.0.0.1"
	}
	return net.JoinHostPort(hostIP, bindings[0].HostPort), nil
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

func waitTCP(addr string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	var lastErr error
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", addr, 200*time.Millisecond)
		if err == nil {
			_ = conn.Close()
			return nil
		}
		lastErr = err
		time.Sleep(100 * time.Millisecond)
	}
	if lastErr != nil {
		return fmt.Errorf("timeout waiting for %s: %w", addr, lastErr)
	}
	return fmt.Errorf("timeout waiting for %s", addr)
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

// SetAssignment records that sandboxID is on gwID and increments that
// gateway's leg count when the instance is known to the pool.
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
// Idempotent: if already connected at that IP, it succeeds. Stale holders of
// the address (e.g. an obsolete gateway left after a restart) are disconnected first.
func (p *Pool) ConnectSandbox(ctx context.Context, gw *Instance, networkID, gatewayIP string) error {
	if err := p.clearGatewayAddress(ctx, networkID, gw.CID, gatewayIP); err != nil {
		log.Printf("egress pool: clear gateway address on %s: %v", networkID, err)
	}
	err := p.cli.NetworkConnect(ctx, networkID, gw.CID, &network.EndpointSettings{
		IPAMConfig: &network.EndpointIPAMConfig{IPv4Address: gatewayIP},
	})
	if err == nil {
		return nil
	}
	// Already connected with the same endpoint is fine.
	if strings.Contains(err.Error(), "already exists") || strings.Contains(err.Error(), "already connected") {
		return nil
	}
	if strings.Contains(err.Error(), "Address already in use") {
		_ = p.clearGatewayAddress(ctx, networkID, gw.CID, gatewayIP)
		return p.cli.NetworkConnect(ctx, networkID, gw.CID, &network.EndpointSettings{
			IPAMConfig: &network.EndpointIPAMConfig{IPv4Address: gatewayIP},
		})
	}
	return err
}

func (p *Pool) clearGatewayAddress(ctx context.Context, networkID, gatewayCID, gatewayIP string) error {
	ins, err := p.cli.NetworkInspect(ctx, networkID, network.InspectOptions{})
	if err != nil {
		return err
	}
	for cid, ep := range ins.Containers {
		sameContainer := cid == gatewayCID || strings.HasPrefix(gatewayCID, cid) || strings.HasPrefix(cid, gatewayCID)
		ip := endpointIPv4(ep.IPv4Address)
		holdsTarget := gatewayIP != "" && ip == gatewayIP
		if sameContainer || holdsTarget {
			_ = p.cli.NetworkDisconnect(ctx, networkID, cid, true)
		}
	}
	return nil
}

func endpointIPv4(cidrOrIP string) string {
	if cidrOrIP == "" {
		return ""
	}
	ip, _, err := net.ParseCIDR(cidrOrIP)
	if err == nil && ip != nil {
		return ip.String()
	}
	if ip := net.ParseIP(cidrOrIP); ip != nil {
		return ip.String()
	}
	return ""
}

// DisconnectSandbox detaches the gateway from a network.
func (p *Pool) DisconnectSandbox(ctx context.Context, gw *Instance, networkID string) error {
	return p.cli.NetworkDisconnect(ctx, networkID, gw.CID, true)
}

// ControlClient dials the gateway's published loopback control port with bearer auth.
func ControlClient(addr, token string) (cellarv1.EgressGatewayControlClient, *grpc.ClientConn, error) {
	conn, err := grpc.NewClient(
		addr,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithUnaryInterceptor(bearerUnary(token)),
		grpc.WithStreamInterceptor(bearerStream(token)),
	)
	if err != nil {
		return nil, nil, err
	}
	return cellarv1.NewEgressGatewayControlClient(conn), conn, nil
}

func bearerUnary(token string) grpc.UnaryClientInterceptor {
	return func(ctx context.Context, method string, req, reply any, cc *grpc.ClientConn, invoker grpc.UnaryInvoker, opts ...grpc.CallOption) error {
		return invoker(metadata.AppendToOutgoingContext(ctx, "authorization", "Bearer "+token), method, req, reply, cc, opts...)
	}
}

func bearerStream(token string) grpc.StreamClientInterceptor {
	return func(ctx context.Context, desc *grpc.StreamDesc, cc *grpc.ClientConn, method string, streamer grpc.Streamer, opts ...grpc.CallOption) (grpc.ClientStream, error) {
		return streamer(metadata.AppendToOutgoingContext(ctx, "authorization", "Bearer "+token), desc, cc, method, opts...)
	}
}

// RegisterSandbox pushes session state to the gateway.
func (p *Pool) RegisterSandbox(ctx context.Context, gw *Instance, sandboxID, networkID, subnetCIDR, gatewayIP string, policy sandbox.NetworkPolicy) error {
	cli, conn, err := ControlClient(gw.ControlAddr, gw.Token)
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
	cli, conn, err := ControlClient(gw.ControlAddr, gw.Token)
	if err != nil {
		return err
	}
	defer conn.Close()
	_, err = cli.DeregisterSandbox(ctx, &cellarv1.DeregisterSandboxRequest{SandboxId: sandboxID})
	return err
}

// UpdatePolicy replaces policy on the gateway.
func (p *Pool) UpdatePolicy(ctx context.Context, gw *Instance, sandboxID string, policy sandbox.NetworkPolicy) error {
	cli, conn, err := ControlClient(gw.ControlAddr, gw.Token)
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

// Image returns the egress-gateway Docker image used by this pool.
func (p *Pool) Image() string { return p.cfg.Image }
