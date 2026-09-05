package daemon

import (
	"context"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"log"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"

	cellarv1 "github.com/prodioslabs/cellar/api/gen"
	"github.com/prodioslabs/cellar/internal/ca"
	"github.com/prodioslabs/cellar/internal/grpcapi"
	"github.com/prodioslabs/cellar/internal/identity"
	"github.com/prodioslabs/cellar/internal/node"
	"github.com/prodioslabs/cellar/internal/paths"
	"github.com/prodioslabs/cellar/internal/raftstore"
	"github.com/prodioslabs/cellar/internal/renew"
	"github.com/prodioslabs/cellar/internal/runtime"
	"github.com/prodioslabs/cellar/internal/sandbox"
	"github.com/prodioslabs/cellar/internal/store"
	"github.com/prodioslabs/cellar/internal/token"
)

const (
	DefaultListenAddr = ":17946"
	DefaultRaftAddr   = ":17947"

	gracefulStopTimeout = 5 * time.Second
	wgWaitTimeout       = 5 * time.Second
	teardownTimeout     = 20 * time.Second
)

// Re-export platform defaults for existing call sites.
var (
	DefaultSocket  = paths.DefaultSocket
	DefaultDataDir = paths.DefaultDataDir
)

// DefaultSocketPath returns the platform default control socket path.
func DefaultSocketPath() string { return paths.DefaultSocketPath() }

// DefaultDataDirPath returns the platform default data directory.
func DefaultDataDirPath() string { return paths.DefaultDataDirPath() }

// Config configures the always-on cellard process.
type Config struct {
	DataDir    string
	SocketPath string
	ListenAddr string // default remote listen; may be overridden by init/join
	RaftAddr   string
}

// Daemon is the long-running cellar node process.
type Daemon struct {
	cfg Config

	mu       sync.Mutex
	idStore  *identity.Store
	raft     *raftstore.Store
	caServer *grpcapi.CAServer

	sandboxServer *grpcapi.SandboxServer
	sandboxAPI    *grpcapi.SandboxAPIServer
	runtimeSrv    *grpcapi.RuntimeServer
	driver        *runtime.Driver
	agent         *runtime.Agent
	// runtimeErr is set when this node cannot start a microsandbox runtime agent.
	// SandboxCreate surfaces it when no other node has a live runtime.
	runtimeErr error
	// lastAssigned is the sandbox list from the latest runtime heartbeat.
	// The agent's 3s reconcile loop reads it via fetchAssignments when this
	// node is not the Raft leader. Heartbeat refreshes it every 5s from the
	// leader's ListSandboxesByNode reply (the pull path after SaveSandbox).
	lastAssigned []*sandbox.Sandbox

	localLis   net.Listener
	localGRPC  *grpc.Server
	remoteLis  net.Listener
	remoteGRPC *grpc.Server

	cancel        context.CancelFunc
	runCtx        context.Context
	clusterCancel context.CancelFunc
	clusterCtx    context.Context
	wg            sync.WaitGroup // local control serve
	clusterWG     sync.WaitGroup // renew, leadership, heartbeat, agent, remote gRPC
}

// newCAServer builds a CA server that kicks vacate after RaftLeave deletes a node.
func (d *Daemon) newCAServer(rs *raftstore.Store) *grpcapi.CAServer {
	ca := grpcapi.NewCAServer(rs, rs, d)
	ca.SetAfterNodeDelete(func(ctx context.Context) {
		d.runEviction(ctx)
	})
	return ca
}

func New(cfg Config) *Daemon {
	if cfg.DataDir == "" {
		cfg.DataDir = DefaultDataDir
	}
	// Docker bind mounts require absolute host paths; resolve once so identity,
	// raft, and sandbox dirs all share the same absolute data-dir.
	if abs, err := filepath.Abs(cfg.DataDir); err == nil {
		cfg.DataDir = abs
	}
	if cfg.SocketPath == "" {
		cfg.SocketPath = DefaultSocket
	}
	if abs, err := filepath.Abs(cfg.SocketPath); err == nil {
		cfg.SocketPath = abs
	}
	if cfg.ListenAddr == "" {
		cfg.ListenAddr = DefaultListenAddr
	}
	if cfg.RaftAddr == "" {
		cfg.RaftAddr = DefaultRaftAddr
	}
	return &Daemon{
		cfg:     cfg,
		idStore: identity.NewStore(cfg.DataDir),
	}
}

// Run starts the unix Control socket and resumes prior cluster membership if any.
func (d *Daemon) Run(ctx context.Context) error {
	ctx, d.cancel = context.WithCancel(ctx)
	d.runCtx = ctx
	if err := d.idStore.Load(); err != nil {
		return fmt.Errorf("load identity: %w", err)
	}

	if err := d.startLocalControl(); err != nil {
		return err
	}

	if d.idStore.HasIdentity() {
		state := d.idStore.State()
		clusterCtx := d.ensureClusterCtx()
		switch state.Role {
		case node.RoleManager:
			if err := d.resumeManager(clusterCtx, state); err != nil {
				log.Printf("resume manager: %v", err)
			}
		case node.RoleWorker:
			if err := d.resumeWorker(clusterCtx, state); err != nil {
				log.Printf("resume worker: %v", err)
			}
		}
	}

	log.Printf("cellard listening on unix://%s (data-dir=%s)", d.cfg.SocketPath, d.cfg.DataDir)

	<-ctx.Done()
	d.shutdown()
	return ctx.Err()
}

func (d *Daemon) shutdown() {
	// Snapshot under the lock, then release it before GracefulStop. Holding
	// d.mu across GracefulStop deadlocks with admitted RPCs (Status, etc.)
	// that also need the mutex.
	d.mu.Lock()
	remoteGRPC := d.remoteGRPC
	remoteLis := d.remoteLis
	localGRPC := d.localGRPC
	localLis := d.localLis
	raft := d.raft
	driver := d.driver
	agent := d.agent
	clusterCancel := d.clusterCancel
	cancel := d.cancel
	d.remoteGRPC = nil
	d.remoteLis = nil
	d.localGRPC = nil
	d.localLis = nil
	d.raft = nil
	d.driver = nil
	d.agent = nil
	d.caServer = nil
	d.sandboxServer = nil
	d.sandboxAPI = nil
	d.runtimeSrv = nil
	d.clusterCancel = nil
	d.clusterCtx = nil
	d.cancel = nil
	d.mu.Unlock()

	gracefulStop(remoteGRPC, gracefulStopTimeout)
	if remoteLis != nil {
		_ = remoteLis.Close()
	}
	gracefulStop(localGRPC, gracefulStopTimeout)
	if localLis != nil {
		_ = localLis.Close()
	}

	// Cancel cluster then run context so agent / renew / heartbeat loops exit,
	// then wait briefly so TeardownLocal does not race a concurrent reconcile.
	if clusterCancel != nil {
		clusterCancel()
	}
	if cancel != nil {
		cancel()
	}
	waitWG(&d.clusterWG, wgWaitTimeout)
	waitWG(&d.wg, wgWaitTimeout)

	if agent != nil {
		tctx, tcancel := context.WithTimeout(context.Background(), teardownTimeout)
		agent.TeardownLocal(tctx)
		tcancel()
	}

	if raft != nil {
		_ = raft.Close()
	}
	if driver != nil {
		_ = driver.Close()
	}
}

// ensureClusterCtx returns a cancellable context for cluster membership work.
// Creates a fresh child of runCtx when none is active (e.g. after Leave).
func (d *Daemon) ensureClusterCtx() context.Context {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.ensureClusterCtxLocked()
}

func (d *Daemon) ensureClusterCtxLocked() context.Context {
	if d.clusterCtx != nil {
		select {
		case <-d.clusterCtx.Done():
		default:
			return d.clusterCtx
		}
	}
	parent := d.runCtx
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithCancel(parent)
	d.clusterCtx = ctx
	d.clusterCancel = cancel
	return ctx
}

// gracefulStop waits for GracefulStop up to timeout, then Force Stop.
func gracefulStop(s *grpc.Server, timeout time.Duration) {
	if s == nil {
		return
	}
	done := make(chan struct{})
	go func() {
		s.GracefulStop()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(timeout):
		s.Stop()
		<-done
	}
}

func waitWG(wg *sync.WaitGroup, timeout time.Duration) {
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(timeout):
		log.Printf("cellard: shutdown waiting for goroutines timed out after %s", timeout)
	}
}

func (d *Daemon) startLocalControl() error {
	dir := filepath.Dir(d.cfg.SocketPath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	_ = os.Remove(d.cfg.SocketPath)
	lis, err := net.Listen("unix", d.cfg.SocketPath)
	if err != nil {
		return fmt.Errorf("listen unix: %w", err)
	}
	if err := os.Chmod(d.cfg.SocketPath, 0o660); err != nil {
		_ = lis.Close()
		return err
	}
	s := grpc.NewServer()
	cellarv1.RegisterControlServer(s, &controlServer{d: d})
	d.localLis = lis
	d.localGRPC = s
	d.wg.Add(1)
	go func() {
		defer d.wg.Done()
		if err := s.Serve(lis); err != nil {
			log.Printf("local control serve: %v", err)
		}
	}()
	return nil
}

func (d *Daemon) Leave(ctx context.Context, req *cellarv1.LeaveRequest) (*cellarv1.LeaveResponse, error) {
	d.mu.Lock()
	mat := d.idStore.Material()
	if mat == nil {
		d.mu.Unlock()
		return nil, fmt.Errorf("node is not part of a cluster")
	}
	role := mat.Role
	nodeID := mat.NodeID
	certPEM := mat.Certificate
	keyPEM := mat.PrivateKey
	caPEM := mat.CACert
	raft := d.raft
	state := d.idStore.State()
	force := req.GetForce()
	d.mu.Unlock()

	if role == node.RoleManager && !force {
		return nil, fmt.Errorf("leaving as a manager requires --force")
	}

	// Ask the leader to drop Raft membership (managers) and the node record.
	addrs := []string{}
	if raft != nil {
		if a := raft.LeaderGRPC(); a != "" {
			addrs = append(addrs, a)
		}
	}
	addrs = grpcapi.MergeManagerAddrs("", addrs, state.ManagerAddrs, []string{state.ManagerAddr, state.AdvertiseAddr})
	if role == node.RoleManager && force && raft != nil && raft.NumVoters() <= 1 {
		// Sole voter: skip remote leave; local wipe abandons the cluster.
	} else if len(addrs) > 0 {
		var leaveErr error
		for _, addr := range addrs {
			leaveErr = grpcapi.RaftLeave(ctx, addr, certPEM, keyPEM, caPEM, nodeID)
			if leaveErr == nil {
				break
			}
			if !isRetryableManagerErr(leaveErr) {
				break
			}
		}
		if leaveErr != nil && !force {
			return nil, fmt.Errorf("leave cluster: %w", leaveErr)
		}
		if leaveErr != nil {
			log.Printf("cluster leave unregister: %v (continuing local wipe)", leaveErr)
		}
	}

	d.stopClusterLocal()

	if err := d.idStore.Clear(); err != nil {
		return nil, err
	}
	raftDir := filepath.Join(d.cfg.DataDir, "raft")
	if err := os.RemoveAll(raftDir); err != nil {
		return nil, fmt.Errorf("remove raft dir: %w", err)
	}
	log.Printf("left cluster; local identity cleared")
	return &cellarv1.LeaveResponse{}, nil
}

// stopClusterLocal tears down remote gRPC, runtime, and raft without touching
// the local Control unix socket.
func (d *Daemon) stopClusterLocal() {
	d.mu.Lock()
	remoteGRPC := d.remoteGRPC
	remoteLis := d.remoteLis
	raft := d.raft
	driver := d.driver
	agent := d.agent
	clusterCancel := d.clusterCancel
	d.remoteGRPC = nil
	d.remoteLis = nil
	d.raft = nil
	d.driver = nil
	d.agent = nil
	d.caServer = nil
	d.sandboxServer = nil
	d.sandboxAPI = nil
	d.runtimeSrv = nil
	d.runtimeErr = nil
	d.lastAssigned = nil
	d.clusterCancel = nil
	d.clusterCtx = nil
	d.mu.Unlock()

	gracefulStop(remoteGRPC, gracefulStopTimeout)
	if remoteLis != nil {
		_ = remoteLis.Close()
	}
	if clusterCancel != nil {
		clusterCancel()
	}
	waitWG(&d.clusterWG, wgWaitTimeout)

	if agent != nil {
		tctx, tcancel := context.WithTimeout(context.Background(), teardownTimeout)
		agent.TeardownLocal(tctx)
		tcancel()
	}
	if raft != nil {
		_ = raft.Close()
	}
	if driver != nil {
		_ = driver.Close()
	}
}

func (d *Daemon) resumeManager(ctx context.Context, state identity.DaemonState) error {
	listen := state.ListenAddr
	if listen == "" {
		listen = d.cfg.ListenAddr
	}
	raftAddr := state.RaftAddr
	if raftAddr == "" {
		raftAddr = d.cfg.RaftAddr
	}
	raftAddr = defaultRaftAddr(raftAddr)
	advertise := state.AdvertiseAddr
	if advertise == "" {
		advertise = defaultAdvertise(listen)
	}

	rs, err := raftstore.Open(raftstore.Config{
		DataDir:       d.cfg.DataDir,
		NodeID:        state.NodeID,
		RaftAddr:      raftAddr,
		GRPCAdvertise: advertise,
		Bootstrap:     false,
	})
	if err != nil {
		return err
	}
	if err := rs.WaitForLeader(30 * time.Second); err != nil {
		log.Printf("wait leader: %v", err)
	}
	if err := rs.WaitInitialized(30 * time.Second); err != nil {
		log.Printf("wait initialized: %v", err)
	}

	d.mu.Lock()
	defer d.mu.Unlock()
	d.raft = rs
	d.caServer = d.newCAServer(rs)
	d.sandboxServer = grpcapi.NewSandboxServer(rs, rs, d)
	d.sandboxAPI = grpcapi.NewSandboxAPIServer(rs, rs, d.sandboxServer, d)
	_ = d.caServer.UpdateRootCA(ctx)
	d.clusterWG.Add(1)
	go func() {
		defer d.clusterWG.Done()
		d.watchLeadership(ctx)
	}()
	d.clusterWG.Add(1)
	go func() {
		defer d.clusterWG.Done()
		_ = d.renewLoop(ctx)
	}()
	if err := d.startRuntimeLocked(ctx); err != nil {
		log.Printf("runtime: %v", err)
	}
	return d.startRemoteGRPCLocked(listen)
}

func (d *Daemon) resumeWorker(ctx context.Context, state identity.DaemonState) error {
	listen := state.ListenAddr
	if listen == "" {
		listen = d.cfg.ListenAddr
	}
	d.clusterWG.Add(1)
	go func() {
		defer d.clusterWG.Done()
		_ = d.renewLoop(ctx)
	}()
	d.mu.Lock()
	defer d.mu.Unlock()
	if err := d.startRuntimeLocked(ctx); err != nil {
		log.Printf("runtime: %v", err)
	}
	return d.startRemoteGRPCLocked(listen)
}

func (d *Daemon) watchLeadership(ctx context.Context) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	wasLeader := false
	var evictCancel context.CancelFunc
	for {
		select {
		case <-ctx.Done():
			if evictCancel != nil {
				evictCancel()
				evictCancel = nil
			}
			return
		case <-ticker.C:
			if d.raft == nil || d.caServer == nil {
				continue
			}
			leader := d.raft.IsLeader()
			if leader && !wasLeader {
				if err := d.caServer.UpdateRootCA(ctx); err != nil {
					log.Printf("UpdateRootCA: %v", err)
				} else {
					log.Printf("became raft leader; CA signer ready")
				}
				evictCtx, cancel := context.WithCancel(ctx)
				evictCancel = cancel
				d.clusterWG.Add(1)
				go func() {
					defer d.clusterWG.Done()
					d.evictionLoop(evictCtx)
				}()
			}
			if !leader && wasLeader {
				d.caServer.Stop()
				log.Printf("lost raft leadership; CA signer stopped")
				if evictCancel != nil {
					evictCancel()
					evictCancel = nil
				}
			}
			wasLeader = leader
		}
	}
}

func (d *Daemon) Init(ctx context.Context, req *cellarv1.InitRequest) (*cellarv1.InitResponse, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.idStore.HasIdentity() {
		return nil, fmt.Errorf("node already initialized; leave cluster first")
	}

	listen := req.ListenAddr
	if listen == "" {
		listen = d.cfg.ListenAddr
	}
	raftAddr := req.RaftAddr
	if raftAddr == "" {
		raftAddr = d.cfg.RaftAddr
	}
	raftAddr = defaultRaftAddr(raftAddr)
	advertise := req.AdvertiseAddr
	if advertise == "" {
		advertise = defaultAdvertise(listen)
	}
	validity := time.Duration(req.CertValidityNs)
	if validity <= 0 {
		validity = ca.DefaultNodeValidity
	}

	root, err := ca.GenerateRootCA("cellar", ca.DefaultCAValidity)
	if err != nil {
		return nil, err
	}
	secrets, err := token.GenerateSecrets()
	if err != nil {
		return nil, err
	}
	clusterID, err := node.NewID()
	if err != nil {
		return nil, err
	}

	nodeID, certPEM, keyPEM, nb, na, err := grpcapi.SelfIssue(root, node.RoleManager, validity)
	if err != nil {
		return nil, err
	}

	mat := &identity.Material{
		NodeID:      nodeID,
		Role:        node.RoleManager,
		ClusterID:   clusterID,
		Certificate: certPEM,
		PrivateKey:  keyPEM,
		CACert:      root.CertPEM,
		NotBefore:   nb,
		NotAfter:    na,
	}
	state := identity.DaemonState{
		NodeID:        nodeID,
		Role:          node.RoleManager,
		ClusterID:     clusterID,
		AdvertiseAddr: advertise,
		ListenAddr:    listen,
		RaftAddr:      raftAddr,
		Initialized:   true,
	}
	if err := d.idStore.Save(mat, state); err != nil {
		return nil, err
	}

	rs, err := raftstore.Open(raftstore.Config{
		DataDir:       d.cfg.DataDir,
		NodeID:        nodeID,
		RaftAddr:      raftAddr,
		GRPCAdvertise: advertise,
		Bootstrap:     true,
	})
	if err != nil {
		return nil, err
	}
	d.raft = rs
	d.caServer = d.newCAServer(rs)
	d.sandboxServer = grpcapi.NewSandboxServer(rs, rs, d)
	d.sandboxAPI = grpcapi.NewSandboxAPIServer(rs, rs, d.sandboxServer, d)

	if err := rs.CreateCluster(ctx, store.ClusterConfig{
		ClusterID:    clusterID,
		CertValidity: validity,
		RootCA:       root,
		JoinSecrets:  secrets,
	}); err != nil {
		_ = rs.Close()
		d.raft = nil
		return nil, err
	}

	_ = rs.SaveNode(ctx, &node.Node{
		ID:             nodeID,
		Role:           node.RoleManager,
		Membership:     node.MembershipAccepted,
		IssuedAt:       nb.UTC(),
		ExpiresAt:      na.UTC(),
		CertificatePEM: string(certPEM),
	})

	if err := d.caServer.UpdateRootCA(ctx); err != nil {
		return nil, err
	}

	clusterCtx := d.ensureClusterCtxLocked()
	d.clusterWG.Add(1)
	go func() {
		defer d.clusterWG.Done()
		d.watchLeadership(clusterCtx)
	}()
	d.clusterWG.Add(1)
	go func() {
		defer d.clusterWG.Done()
		_ = d.renewLoop(clusterCtx)
	}()

	if err := d.startRuntimeLocked(clusterCtx); err != nil {
		log.Printf("runtime: %v", err)
	}

	// startRemoteGRPC needs lock; release and re-acquire carefully — we're holding d.mu
	if err := d.startRemoteGRPCLocked(listen); err != nil {
		return nil, err
	}

	pair, err := token.FormatPair(root.DigestPrefix(), secrets)
	if err != nil {
		return nil, err
	}
	return &cellarv1.InitResponse{
		ClusterId:     clusterID,
		NodeId:        nodeID,
		WorkerToken:   pair.Worker,
		ManagerToken:  pair.Manager,
		AdvertiseAddr: advertise,
	}, nil
}

func (d *Daemon) startRemoteGRPCLocked(listenAddr string) error {
	if d.remoteGRPC != nil {
		return nil
	}
	tlsCfg, err := d.idStore.ServerTLSConfig()
	if err != nil {
		return err
	}
	lis, err := net.Listen("tcp", listenAddr)
	if err != nil {
		return fmt.Errorf("listen remote: %w", err)
	}
	opts := []grpc.ServerOption{grpc.Creds(credentials.NewTLS(tlsCfg))}
	if d.raft != nil {
		touch := func(id string) {
			grpcapi.TouchAPIKeyBestEffort(d.raft, d.raft, id)
		}
		opts = append(opts,
			grpc.UnaryInterceptor(grpcapi.APIKeyUnaryInterceptor(d.raft, touch)),
			grpc.StreamInterceptor(grpcapi.APIKeyStreamInterceptor(d.raft, touch)),
		)
	}
	s := grpc.NewServer(opts...)
	if d.caServer != nil {
		grpcapi.RegisterRemote(s, d.caServer, d.sandboxServer)
	}
	if d.sandboxAPI != nil {
		grpcapi.RegisterSandboxAPI(s, d.sandboxAPI)
	}
	if d.runtimeSrv != nil {
		grpcapi.RegisterRuntime(s, d.runtimeSrv)
	}
	d.remoteLis = lis
	d.remoteGRPC = s
	d.clusterWG.Add(1)
	go func() {
		defer d.clusterWG.Done()
		if err := s.Serve(lis); err != nil {
			log.Printf("remote grpc serve: %v", err)
		}
	}()
	log.Printf("remote gRPC listening on %s", listenAddr)
	return nil
}

func (d *Daemon) Join(ctx context.Context, req *cellarv1.JoinRequest) (*cellarv1.JoinResponse, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.idStore.HasIdentity() {
		return nil, fmt.Errorf("node already joined a cluster")
	}
	if req.Token == "" || req.RemoteAddr == "" {
		return nil, fmt.Errorf("token and remote_addr are required")
	}

	listen := req.ListenAddr
	if listen == "" {
		listen = d.cfg.ListenAddr
	}
	raftAddr := req.RaftAddr
	if raftAddr == "" {
		raftAddr = d.cfg.RaftAddr
	}
	raftAddr = defaultRaftAddr(raftAddr)
	advertise := req.AdvertiseAddr
	if advertise == "" {
		advertise = defaultAdvertise(listen)
	}

	caPEM, err := grpcapi.DownloadRootCA(ctx, req.RemoteAddr, req.Token)
	if err != nil {
		return nil, err
	}
	keyPEM, csrPEM, _, err := ca.GenerateKeyAndCSR("")
	if err != nil {
		return nil, err
	}
	issued, err := grpcapi.IssueWithToken(ctx, req.RemoteAddr, caPEM, req.Token, csrPEM)
	if err != nil {
		return nil, err
	}

	role := node.Role(issued.Role)
	mat := &identity.Material{
		NodeID:      issued.NodeId,
		Role:        role,
		Certificate: issued.Certificate,
		PrivateKey:  keyPEM,
		CACert:      caPEM,
		NotAfter:    time.Unix(0, issued.ExpiresAtUnixNano).UTC(),
	}
	// Fill NotBefore from cert
	if block, _ := pem.Decode(issued.Certificate); block != nil {
		if cert, err := x509.ParseCertificate(block.Bytes); err == nil {
			mat.NotBefore = cert.NotBefore
			mat.NotAfter = cert.NotAfter
		}
	}

	prefer, addrs := seedManagerAddrs(req.RemoteAddr, issued.GetLeaderGrpc(), issued.GetManagerAddrs())
	state := identity.DaemonState{
		NodeID:        issued.NodeId,
		Role:          role,
		AdvertiseAddr: advertise,
		ListenAddr:    listen,
		RaftAddr:      raftAddr,
		ManagerAddr:   prefer,
		ManagerAddrs:  addrs,
		Initialized:   true,
	}

	if role == node.RoleManager {
		// Open raft first without bootstrap, then ask leader to add us.
		rs, err := raftstore.Open(raftstore.Config{
			DataDir:       d.cfg.DataDir,
			NodeID:        issued.NodeId,
			RaftAddr:      raftAddr,
			GRPCAdvertise: advertise,
			Bootstrap:     false,
		})
		if err != nil {
			return nil, err
		}
		if err := grpcapi.RaftJoin(ctx, req.RemoteAddr, issued.Certificate, keyPEM, caPEM, issued.NodeId, rs.RaftAddr(), advertise); err != nil {
			_ = rs.Close()
			return nil, fmt.Errorf("raft join: %w", err)
		}
		if err := rs.WaitForLeader(30 * time.Second); err != nil {
			_ = rs.Close()
			return nil, err
		}
		if err := rs.WaitInitialized(30 * time.Second); err != nil {
			_ = rs.Close()
			return nil, err
		}
		cluster, err := rs.GetCluster(ctx)
		if err != nil {
			_ = rs.Close()
			return nil, err
		}
		mat.ClusterID = cluster.ClusterID
		state.ClusterID = cluster.ClusterID

		if err := d.idStore.Save(mat, state); err != nil {
			_ = rs.Close()
			return nil, err
		}
		d.raft = rs
		d.caServer = d.newCAServer(rs)
		d.sandboxServer = grpcapi.NewSandboxServer(rs, rs, d)
		d.sandboxAPI = grpcapi.NewSandboxAPIServer(rs, rs, d.sandboxServer, d)
		_ = d.caServer.UpdateRootCA(ctx)
		clusterCtx := d.ensureClusterCtxLocked()
		d.clusterWG.Add(1)
		go func() {
			defer d.clusterWG.Done()
			d.watchLeadership(clusterCtx)
		}()
		d.clusterWG.Add(1)
		go func() {
			defer d.clusterWG.Done()
			_ = d.renewLoop(clusterCtx)
		}()
		if err := d.startRuntimeLocked(clusterCtx); err != nil {
			log.Printf("runtime: %v", err)
		}
		if err := d.startRemoteGRPCLocked(listen); err != nil {
			return nil, err
		}
	} else {
		if err := d.idStore.Save(mat, state); err != nil {
			return nil, err
		}
		clusterCtx := d.ensureClusterCtxLocked()
		d.clusterWG.Add(1)
		go func() {
			defer d.clusterWG.Done()
			_ = d.renewLoop(clusterCtx)
		}()
		if err := d.startRuntimeLocked(clusterCtx); err != nil {
			log.Printf("runtime: %v", err)
		}
		if err := d.startRemoteGRPCLocked(listen); err != nil {
			return nil, err
		}
	}

	return &cellarv1.JoinResponse{
		NodeId:    issued.NodeId,
		Role:      string(role),
		ClusterId: mat.ClusterID,
	}, nil
}

func (d *Daemon) JoinToken(ctx context.Context, req *cellarv1.JoinTokenRequest) (*cellarv1.JoinTokenResponse, error) {
	d.mu.Lock()
	raft := d.raft
	state := d.idStore.State()
	d.mu.Unlock()

	if raft == nil {
		return nil, fmt.Errorf("this node is not a manager")
	}
	cluster, err := raft.GetCluster(ctx)
	if err != nil {
		return nil, err
	}
	role := node.Role(req.Role)
	if role != node.RoleWorker && role != node.RoleManager {
		return nil, fmt.Errorf("role must be worker or manager")
	}
	tok, err := token.Format(cluster.RootCA.CACertHash, cluster.RootCA.JoinSecrets, role)
	if err != nil {
		return nil, err
	}
	advertise := raft.LeaderGRPC()
	if advertise == "" {
		advertise = state.AdvertiseAddr
	}
	if advertise == "" {
		advertise = raft.GRPCAdvertise()
	}
	cmd := fmt.Sprintf("cellar join --token %s %s", tok, advertise)
	return &cellarv1.JoinTokenResponse{
		Token:         tok,
		AdvertiseAddr: advertise,
		JoinCommand:   cmd,
	}, nil
}

func (d *Daemon) Status(ctx context.Context, _ *cellarv1.StatusRequest) (*cellarv1.StatusResponse, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	state := d.idStore.State()
	mat := d.idStore.Material()
	resp := &cellarv1.StatusResponse{
		Initialized:   state.Initialized || mat != nil,
		AdvertiseAddr: state.AdvertiseAddr,
		ListenAddr:    state.ListenAddr,
		RaftAddr:      state.RaftAddr,
		ClusterId:     state.ClusterID,
	}
	if mat != nil {
		resp.NodeId = mat.NodeID
		resp.Role = string(mat.Role)
	}
	if d.raft != nil {
		resp.IsLeader = d.raft.IsLeader()
		if resp.ClusterId == "" {
			if c, err := d.raft.GetCluster(ctx); err == nil {
				resp.ClusterId = c.ClusterID
			}
		}
	}
	return resp, nil
}

func (d *Daemon) renewLoop(ctx context.Context) error {
	for {
		mat := d.idStore.Material()
		if mat == nil {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(time.Minute):
				continue
			}
		}
		wait := renew.NextCheck(mat.NotBefore, mat.NotAfter, time.Now(), renew.DefaultThreshold)
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
		mat = d.idStore.Material()
		if mat == nil {
			continue
		}
		if !renew.Needed(mat.NotBefore, mat.NotAfter, time.Now(), renew.DefaultThreshold) {
			continue
		}
		keyPEM, csrPEM, _, err := ca.GenerateKeyAndCSR(mat.NodeID)
		if err != nil {
			continue
		}
		var issued *cellarv1.IssueNodeCertificateResponse
		err = d.forEachManager(func(addr string) error {
			var rerr error
			issued, rerr = grpcapi.IssueRenew(ctx, addr, mat.Certificate, mat.PrivateKey, mat.CACert, mat.NodeID, csrPEM)
			return rerr
		})
		if err != nil {
			log.Printf("renew: %v", err)
			continue
		}
		d.applyManagerEndpoints(issued.GetLeaderGrpc(), issued.GetManagerAddrs())
		newMat := &identity.Material{
			NodeID:      issued.NodeId,
			Role:        node.Role(issued.Role),
			ClusterID:   mat.ClusterID,
			Certificate: issued.Certificate,
			PrivateKey:  keyPEM,
			CACert:      mat.CACert,
			NotAfter:    time.Unix(0, issued.ExpiresAtUnixNano).UTC(),
		}
		if block, _ := pem.Decode(issued.Certificate); block != nil {
			if cert, err := x509.ParseCertificate(block.Bytes); err == nil {
				newMat.NotBefore = cert.NotBefore
				newMat.NotAfter = cert.NotAfter
			}
		}
		state := d.idStore.State()
		_ = d.idStore.Save(newMat, state)
		log.Printf("renewed node certificate for %s", newMat.NodeID)
	}
}

type controlServer struct {
	cellarv1.UnimplementedControlServer
	d *Daemon
}

func (c *controlServer) Init(ctx context.Context, req *cellarv1.InitRequest) (*cellarv1.InitResponse, error) {
	return c.d.Init(ctx, req)
}
func (c *controlServer) Join(ctx context.Context, req *cellarv1.JoinRequest) (*cellarv1.JoinResponse, error) {
	return c.d.Join(ctx, req)
}
func (c *controlServer) Leave(ctx context.Context, req *cellarv1.LeaveRequest) (*cellarv1.LeaveResponse, error) {
	return c.d.Leave(ctx, req)
}
func (c *controlServer) JoinToken(ctx context.Context, req *cellarv1.JoinTokenRequest) (*cellarv1.JoinTokenResponse, error) {
	return c.d.JoinToken(ctx, req)
}
func (c *controlServer) Status(ctx context.Context, req *cellarv1.StatusRequest) (*cellarv1.StatusResponse, error) {
	return c.d.Status(ctx, req)
}
func (c *controlServer) SandboxCreate(ctx context.Context, req *cellarv1.SandboxCreateRequest) (*cellarv1.SandboxCreateResponse, error) {
	return c.d.SandboxCreate(ctx, req)
}
func (c *controlServer) SandboxStart(ctx context.Context, req *cellarv1.SandboxStartRequest) (*cellarv1.SandboxStartResponse, error) {
	return c.d.SandboxStart(ctx, req)
}
func (c *controlServer) SandboxStop(ctx context.Context, req *cellarv1.SandboxStopRequest) (*cellarv1.SandboxStopResponse, error) {
	return c.d.SandboxStop(ctx, req)
}
func (c *controlServer) SandboxRemove(ctx context.Context, req *cellarv1.SandboxRemoveRequest) (*cellarv1.SandboxRemoveResponse, error) {
	return c.d.SandboxRemove(ctx, req)
}
func (c *controlServer) SandboxGet(ctx context.Context, req *cellarv1.SandboxGetRequest) (*cellarv1.SandboxGetResponse, error) {
	return c.d.SandboxGet(ctx, req)
}
func (c *controlServer) SandboxGetByName(ctx context.Context, req *cellarv1.SandboxGetByNameRequest) (*cellarv1.SandboxGetResponse, error) {
	return c.d.SandboxGetByName(ctx, req)
}
func (c *controlServer) SandboxList(ctx context.Context, req *cellarv1.SandboxListRequest) (*cellarv1.SandboxListResponse, error) {
	return c.d.SandboxList(ctx, req)
}
func (c *controlServer) SandboxLogs(req *cellarv1.SandboxLogsRequest, stream cellarv1.Control_SandboxLogsServer) error {
	return c.d.SandboxLogs(req, stream)
}
func (c *controlServer) APIKeyCreate(ctx context.Context, req *cellarv1.APIKeyCreateRequest) (*cellarv1.APIKeyCreateResponse, error) {
	return c.d.APIKeyCreate(ctx, req)
}
func (c *controlServer) APIKeyList(ctx context.Context, req *cellarv1.APIKeyListRequest) (*cellarv1.APIKeyListResponse, error) {
	return c.d.APIKeyList(ctx, req)
}
func (c *controlServer) APIKeyDelete(ctx context.Context, req *cellarv1.APIKeyDeleteRequest) (*cellarv1.APIKeyDeleteResponse, error) {
	return c.d.APIKeyDelete(ctx, req)
}
func (c *controlServer) NodeList(ctx context.Context, req *cellarv1.NodeListRequest) (*cellarv1.NodeListResponse, error) {
	return c.d.NodeList(ctx, req)
}
func (c *controlServer) NodeInspect(ctx context.Context, req *cellarv1.NodeInspectRequest) (*cellarv1.NodeInspectResponse, error) {
	return c.d.NodeInspect(ctx, req)
}
func (c *controlServer) NodePromote(ctx context.Context, req *cellarv1.NodePromoteRequest) (*cellarv1.NodePromoteResponse, error) {
	return c.d.NodePromote(ctx, req)
}
func (c *controlServer) NodeDemote(ctx context.Context, req *cellarv1.NodeDemoteRequest) (*cellarv1.NodeDemoteResponse, error) {
	return c.d.NodeDemote(ctx, req)
}
func (c *controlServer) NodeRemove(ctx context.Context, req *cellarv1.NodeRemoveRequest) (*cellarv1.NodeRemoveResponse, error) {
	return c.d.NodeRemove(ctx, req)
}
func (c *controlServer) NodeUpdate(ctx context.Context, req *cellarv1.NodeUpdateRequest) (*cellarv1.NodeUpdateResponse, error) {
	return c.d.NodeUpdate(ctx, req)
}

func defaultAdvertise(listen string) string {
	if strings.HasPrefix(listen, ":") {
		return privateIPv4() + listen
	}
	return listen
}

func defaultRaftAddr(addr string) string {
	if strings.HasPrefix(addr, ":") {
		return privateIPv4() + addr
	}
	return addr
}

// privateIPv4 returns the first non-loopback RFC1918 IPv4 address, or
// 127.0.0.1 if none is found.
func privateIPv4() string {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return "127.0.0.1"
	}
	for _, addr := range addrs {
		ipNet, ok := addr.(*net.IPNet)
		if !ok || ipNet.IP.IsLoopback() {
			continue
		}
		ip := ipNet.IP.To4()
		if ip == nil || !ip.IsPrivate() {
			continue
		}
		return ip.String()
	}
	return "127.0.0.1"
}

// DialLocal connects to the local control socket.
func DialLocal(socketPath string) (*grpc.ClientConn, error) {
	return paths.DialLocal(socketPath)
}
