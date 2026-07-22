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
	"google.golang.org/grpc/credentials/insecure"

	cellarv1 "github.com/prodioslabs/cellar/api/gen"
	"github.com/prodioslabs/cellar/internal/ca"
	"github.com/prodioslabs/cellar/internal/grpcapi"
	"github.com/prodioslabs/cellar/internal/identity"
	"github.com/prodioslabs/cellar/internal/node"
	"github.com/prodioslabs/cellar/internal/raftstore"
	"github.com/prodioslabs/cellar/internal/renew"
	"github.com/prodioslabs/cellar/internal/store"
	"github.com/prodioslabs/cellar/internal/token"
)

const (
	DefaultSocket     = "/var/run/cellar/cellar.sock"
	DefaultListenAddr = ":17946"
	DefaultRaftAddr   = "127.0.0.1:17947"
)

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

	localLis   net.Listener
	localGRPC  *grpc.Server
	remoteLis  net.Listener
	remoteGRPC *grpc.Server

	cancel context.CancelFunc
	runCtx context.Context
	wg     sync.WaitGroup
}

func New(cfg Config) *Daemon {
	if cfg.DataDir == "" {
		cfg.DataDir = "./cellar-data"
	}
	if cfg.SocketPath == "" {
		cfg.SocketPath = DefaultSocket
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
		switch state.Role {
		case node.RoleManager:
			if err := d.resumeManager(ctx, state); err != nil {
				log.Printf("resume manager: %v", err)
			}
		case node.RoleWorker:
			if err := d.resumeWorker(ctx, state); err != nil {
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
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.remoteGRPC != nil {
		d.remoteGRPC.GracefulStop()
	}
	if d.remoteLis != nil {
		_ = d.remoteLis.Close()
	}
	if d.localGRPC != nil {
		d.localGRPC.GracefulStop()
	}
	if d.localLis != nil {
		_ = d.localLis.Close()
	}
	if d.raft != nil {
		_ = d.raft.Close()
	}
	if d.cancel != nil {
		d.cancel()
	}
	d.wg.Wait()
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

func (d *Daemon) startRemoteGRPC(listenAddr string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.remoteGRPC != nil {
		return nil
	}
	tlsCfg, err := d.idStore.ServerTLSConfig()
	if err != nil {
		return err
	}
	// Advertise cellar-manager as the TLS server name for clients.
	tlsCfg.Certificates[0].Leaf = nil
	lis, err := net.Listen("tcp", listenAddr)
	if err != nil {
		return fmt.Errorf("listen remote: %w", err)
	}
	s := grpc.NewServer(grpc.Creds(credentials.NewTLS(tlsCfg)))
	if d.caServer != nil {
		grpcapi.RegisterRemote(s, d.caServer)
	}
	d.remoteLis = lis
	d.remoteGRPC = s
	d.wg.Add(1)
	go func() {
		defer d.wg.Done()
		if err := s.Serve(lis); err != nil {
			log.Printf("remote grpc serve: %v", err)
		}
	}()
	log.Printf("remote gRPC listening on %s", listenAddr)
	return nil
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
	d.raft = rs
	d.caServer = grpcapi.NewCAServer(rs, rs)
	if err := rs.WaitForLeader(30 * time.Second); err != nil {
		log.Printf("wait leader: %v", err)
	}
	if err := rs.WaitInitialized(30 * time.Second); err == nil {
		_ = d.caServer.UpdateRootCA(ctx)
	}
	d.wg.Add(1)
	go func() {
		defer d.wg.Done()
		d.watchLeadership(ctx)
	}()
	d.wg.Add(1)
	go func() {
		defer d.wg.Done()
		_ = d.renewLoop(ctx, advertise)
	}()
	return d.startRemoteGRPC(listen)
}

func (d *Daemon) resumeWorker(ctx context.Context, state identity.DaemonState) error {
	advertise := state.AdvertiseAddr
	d.wg.Add(1)
	go func() {
		defer d.wg.Done()
		_ = d.renewLoop(ctx, advertise)
	}()
	return nil
}

func (d *Daemon) watchLeadership(ctx context.Context) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	wasLeader := false
	for {
		select {
		case <-ctx.Done():
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
			}
			if !leader && wasLeader {
				d.caServer.Stop()
				log.Printf("lost raft leadership; CA signer stopped")
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
	d.caServer = grpcapi.NewCAServer(rs, rs)

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

	d.wg.Add(1)
	go func() {
		defer d.wg.Done()
		d.watchLeadership(d.runCtx)
	}()
	d.wg.Add(1)
	go func() {
		defer d.wg.Done()
		_ = d.renewLoop(d.runCtx, advertise)
	}()

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
	s := grpc.NewServer(grpc.Creds(credentials.NewTLS(tlsCfg)))
	if d.caServer != nil {
		grpcapi.RegisterRemote(s, d.caServer)
	}
	d.remoteLis = lis
	d.remoteGRPC = s
	d.wg.Add(1)
	go func() {
		defer d.wg.Done()
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

	state := identity.DaemonState{
		NodeID:        issued.NodeId,
		Role:          role,
		AdvertiseAddr: advertise,
		ListenAddr:    listen,
		RaftAddr:      raftAddr,
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
		d.caServer = grpcapi.NewCAServer(rs, rs)
		_ = d.caServer.UpdateRootCA(ctx)
		d.wg.Add(1)
		go func() {
			defer d.wg.Done()
			d.watchLeadership(d.runCtx)
		}()
		d.wg.Add(1)
		go func() {
			defer d.wg.Done()
			_ = d.renewLoop(d.runCtx, req.RemoteAddr)
		}()
		if err := d.startRemoteGRPCLocked(listen); err != nil {
			return nil, err
		}
	} else {
		if err := d.idStore.Save(mat, state); err != nil {
			return nil, err
		}
		d.wg.Add(1)
		go func() {
			defer d.wg.Done()
			_ = d.renewLoop(d.runCtx, req.RemoteAddr)
		}()
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

func (d *Daemon) renewLoop(ctx context.Context, managerAddr string) error {
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
		addr := managerAddr
		if addr == "" {
			addr = d.idStore.State().AdvertiseAddr
		}
		issued, err := grpcapi.IssueRenew(ctx, addr, mat.Certificate, mat.PrivateKey, mat.CACert, mat.NodeID, csrPEM)
		if err != nil {
			log.Printf("renew: %v", err)
			continue
		}
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
func (c *controlServer) JoinToken(ctx context.Context, req *cellarv1.JoinTokenRequest) (*cellarv1.JoinTokenResponse, error) {
	return c.d.JoinToken(ctx, req)
}
func (c *controlServer) Status(ctx context.Context, req *cellarv1.StatusRequest) (*cellarv1.StatusResponse, error) {
	return c.d.Status(ctx, req)
}

func defaultAdvertise(listen string) string {
	host := listen
	if strings.HasPrefix(host, ":") {
		host = "127.0.0.1" + host
	}
	return host
}

// DialLocal connects to the local control socket.
func DialLocal(socketPath string) (*grpc.ClientConn, error) {
	if socketPath == "" {
		socketPath = DefaultSocket
	}
	abs, err := filepath.Abs(socketPath)
	if err != nil {
		return nil, fmt.Errorf("resolve socket path: %w", err)
	}
	// Resolve to an absolute path so unix:///… has an empty authority. Relative
	// paths like ./foo.sock become unix://./foo.sock, which gRPC rejects.
	return grpc.NewClient(
		"unix://"+abs,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "unix", abs)
		}),
	)
}
