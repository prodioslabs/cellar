package raftstore

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"time"

	"github.com/hashicorp/raft"
	raftboltdb "github.com/hashicorp/raft-boltdb/v2"

	"github.com/prodioslabs/cellar/internal/ca"
	"github.com/prodioslabs/cellar/internal/node"
	"github.com/prodioslabs/cellar/internal/store"
)

const (
	applyTimeout     = 10 * time.Second
	raftRetainSnap   = 2
	defaultRaftAddr  = "127.0.0.1:7947"
	maxPool          = 3
	transportTimeout = 10 * time.Second
)

// Config configures a Raft-backed store for a manager node.
type Config struct {
	DataDir       string
	NodeID        string
	RaftAddr      string // host:port for Raft TCP transport
	GRPCAdvertise string // e.g. 10.0.0.1:7946 for joiners / redirects
	Bootstrap     bool
}

// Store implements store.Store using HashiCorp Raft.
type Store struct {
	cfg       Config
	fsm       *FSM
	raft      *raft.Raft
	transport *raft.NetworkTransport
	bolt      *raftboltdb.BoltStore
}

// Open starts Raft for this manager. When Bootstrap is set and there is no
// existing state, it forms a single-voter cluster.
func Open(cfg Config) (*Store, error) {
	if cfg.DataDir == "" {
		return nil, fmt.Errorf("data dir is required")
	}
	if cfg.NodeID == "" {
		return nil, fmt.Errorf("node id is required")
	}
	if cfg.RaftAddr == "" {
		cfg.RaftAddr = defaultRaftAddr
	}

	raftDir := filepath.Join(cfg.DataDir, "raft")
	if err := os.MkdirAll(raftDir, 0o700); err != nil {
		return nil, fmt.Errorf("create raft dir: %w", err)
	}
	snapDir := filepath.Join(raftDir, "snapshots")
	if err := os.MkdirAll(snapDir, 0o700); err != nil {
		return nil, fmt.Errorf("create snapshot dir: %w", err)
	}

	fsm := NewFSM()

	boltDB, err := raftboltdb.New(raftboltdb.Options{
		Path: filepath.Join(raftDir, "raft.db"),
	})
	if err != nil {
		return nil, fmt.Errorf("open raft boltdb: %w", err)
	}

	snapshots, err := raft.NewFileSnapshotStore(raftDir, raftRetainSnap, os.Stderr)
	if err != nil {
		_ = boltDB.Close()
		return nil, fmt.Errorf("snapshot store: %w", err)
	}

	addr, err := net.ResolveTCPAddr("tcp", cfg.RaftAddr)
	if err != nil {
		_ = boltDB.Close()
		return nil, fmt.Errorf("resolve raft addr: %w", err)
	}
	transport, err := raft.NewTCPTransport(cfg.RaftAddr, addr, maxPool, transportTimeout, os.Stderr)
	if err != nil {
		_ = boltDB.Close()
		return nil, fmt.Errorf("raft transport: %w", err)
	}

	raftCfg := raft.DefaultConfig()
	raftCfg.LocalID = raft.ServerID(cfg.NodeID)

	hasState, err := raft.HasExistingState(boltDB, boltDB, snapshots)
	if err != nil {
		transport.Close()
		_ = boltDB.Close()
		return nil, fmt.Errorf("check raft state: %w", err)
	}

	if cfg.Bootstrap && !hasState {
		configuration := raft.Configuration{
			Servers: []raft.Server{{
				ID:      raft.ServerID(cfg.NodeID),
				Address: transport.LocalAddr(),
			}},
		}
		if err := raft.BootstrapCluster(raftCfg, boltDB, boltDB, snapshots, transport, configuration); err != nil {
			transport.Close()
			_ = boltDB.Close()
			return nil, fmt.Errorf("bootstrap cluster: %w", err)
		}
	}

	r, err := raft.NewRaft(raftCfg, fsm, boltDB, boltDB, snapshots, transport)
	if err != nil {
		transport.Close()
		_ = boltDB.Close()
		return nil, fmt.Errorf("start raft: %w", err)
	}

	s := &Store{
		cfg:       cfg,
		fsm:       fsm,
		raft:      r,
		transport: transport,
		bolt:      boltDB,
	}

	if cfg.Bootstrap {
		if err := s.waitUntilLeader(15 * time.Second); err != nil {
			_ = s.Close()
			return nil, err
		}
		if err := s.SavePeer(context.Background(), PeerInfo{
			NodeID:   cfg.NodeID,
			RaftAddr: string(transport.LocalAddr()),
			GRPCAddr: cfg.GRPCAdvertise,
		}); err != nil {
			_ = s.Close()
			return nil, fmt.Errorf("register bootstrap peer: %w", err)
		}
	}

	return s, nil
}

// Close shuts down Raft and underlying stores.
func (s *Store) Close() error {
	if s.raft != nil {
		fut := s.raft.Shutdown()
		if err := fut.Error(); err != nil {
			return err
		}
	}
	if s.transport != nil {
		_ = s.transport.Close()
	}
	if s.bolt != nil {
		return s.bolt.Close()
	}
	return nil
}

func (s *Store) waitUntilLeader(timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if s.raft.State() == raft.Leader {
			return nil
		}
		time.Sleep(50 * time.Millisecond)
	}
	return fmt.Errorf("timed out waiting to become raft leader")
}

// WaitForLeader blocks until a cluster leader is known.
func (s *Store) WaitForLeader(timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		_, id := s.raft.LeaderWithID()
		if id != "" {
			return nil
		}
		time.Sleep(50 * time.Millisecond)
	}
	return fmt.Errorf("timed out waiting for raft leader")
}

// WaitInitialized blocks until cluster state has been applied to the FSM.
func (s *Store) WaitInitialized(timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if s.fsm.isInitialized() {
			return nil
		}
		time.Sleep(50 * time.Millisecond)
	}
	return fmt.Errorf("timed out waiting for cluster initialization")
}

// IsLeader reports whether this node is the current Raft leader.
func (s *Store) IsLeader() bool {
	return s.raft.State() == raft.Leader
}

// LeaderGRPC returns the advertised gRPC address of the current leader, if known.
func (s *Store) LeaderGRPC() string {
	addr, id := s.raft.LeaderWithID()
	if id == "" {
		return ""
	}
	if p, ok := s.fsm.getPeer(string(id)); ok && p.GRPCAddr != "" {
		return p.GRPCAddr
	}
	if p, ok := s.fsm.peerByRaftAddr(string(addr)); ok && p.GRPCAddr != "" {
		return p.GRPCAddr
	}
	if s.IsLeader() {
		return s.cfg.GRPCAdvertise
	}
	return ""
}

// NodeID returns this manager's Raft server ID.
func (s *Store) NodeID() string { return s.cfg.NodeID }

// RaftAddr returns this manager's Raft bind address.
func (s *Store) RaftAddr() string { return string(s.transport.LocalAddr()) }

// GRPCAdvertise returns this manager's advertised gRPC address.
func (s *Store) GRPCAdvertise() string { return s.cfg.GRPCAdvertise }

// AddVoter adds a manager as a Raft voter and records its peer info.
func (s *Store) AddVoter(ctx context.Context, peer PeerInfo) error {
	if !s.IsLeader() {
		return store.ErrNotLeader
	}
	if peer.NodeID == "" || peer.RaftAddr == "" {
		return fmt.Errorf("node_id and raft_addr are required")
	}
	fut := s.raft.AddVoter(raft.ServerID(peer.NodeID), raft.ServerAddress(peer.RaftAddr), 0, applyTimeout)
	if err := fut.Error(); err != nil {
		return fmt.Errorf("add voter: %w", err)
	}
	return s.SavePeer(ctx, peer)
}

// RemoveServer removes a manager from the Raft configuration.
func (s *Store) RemoveServer(nodeID string) error {
	if !s.IsLeader() {
		return store.ErrNotLeader
	}
	fut := s.raft.RemoveServer(raft.ServerID(nodeID), 0, applyTimeout)
	if err := fut.Error(); err != nil {
		return fmt.Errorf("remove server: %w", err)
	}
	return nil
}

// SavePeer persists manager address metadata via Raft.
func (s *Store) SavePeer(ctx context.Context, peer PeerInfo) error {
	_ = ctx
	if !s.IsLeader() {
		return store.ErrNotLeader
	}
	data, err := encodeCommand(opSavePeer, savePeerPayload{Peer: peer})
	if err != nil {
		return err
	}
	return s.apply(data)
}

func (s *Store) apply(data []byte) error {
	fut := s.raft.Apply(data, applyTimeout)
	if err := fut.Error(); err != nil {
		return err
	}
	if resp := fut.Response(); resp != nil {
		if err, ok := resp.(error); ok && err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) requireLeader() error {
	if !s.IsLeader() {
		return store.ErrNotLeader
	}
	return nil
}

// CreateCluster bootstraps Cluster.RootCA into the Raft log.
func (s *Store) CreateCluster(ctx context.Context, cfg store.ClusterConfig) error {
	_ = ctx
	if err := s.requireLeader(); err != nil {
		return err
	}
	if cfg.RootCA == nil {
		return fmt.Errorf("root CA is required")
	}
	if cfg.ClusterID == "" {
		return fmt.Errorf("cluster ID is required")
	}
	validity := cfg.CertValidity
	if validity <= 0 {
		validity = ca.DefaultNodeValidity
	}
	cluster := store.Cluster{
		ClusterID: cfg.ClusterID,
		RootCA: store.RootCAMaterial{
			CAKey:       append([]byte(nil), cfg.RootCA.KeyPEM...),
			CACert:      append([]byte(nil), cfg.RootCA.CertPEM...),
			CACertHash:  cfg.RootCA.DigestPrefix(),
			JoinSecrets: cfg.JoinSecrets,
		},
		CertValidity: validity,
		CreatedAt:    time.Now().UTC(),
	}
	data, err := encodeCommand(opCreateCluster, createClusterPayload{Cluster: cluster})
	if err != nil {
		return err
	}
	return s.apply(data)
}

// IsInitialized reports whether cluster state exists in the FSM.
func (s *Store) IsInitialized(ctx context.Context) (bool, error) {
	_ = ctx
	return s.fsm.isInitialized(), nil
}

// GetRootCA returns the replicated RootCA from Cluster.RootCA.
func (s *Store) GetRootCA(ctx context.Context) (*ca.RootCA, error) {
	_ = ctx
	return s.fsm.getRootCA()
}

// GetCluster returns the replicated cluster object (includes CAKey).
func (s *Store) GetCluster(ctx context.Context) (*store.Cluster, error) {
	_ = ctx
	return s.fsm.getCluster()
}

// UpdateCluster replicates cluster configuration updates (e.g. token rotation).
func (s *Store) UpdateCluster(ctx context.Context, cluster *store.Cluster) error {
	_ = ctx
	if err := s.requireLeader(); err != nil {
		return err
	}
	if cluster == nil {
		return fmt.Errorf("cluster is required")
	}
	data, err := encodeCommand(opUpdateCluster, updateClusterPayload{Cluster: *cluster})
	if err != nil {
		return err
	}
	return s.apply(data)
}

// GetNode returns a node record from the FSM.
func (s *Store) GetNode(ctx context.Context, nodeID string) (*node.Node, error) {
	_ = ctx
	return s.fsm.getNode(nodeID)
}

// SaveNode replicates a node record.
func (s *Store) SaveNode(ctx context.Context, n *node.Node) error {
	_ = ctx
	if err := s.requireLeader(); err != nil {
		return err
	}
	if n == nil {
		return fmt.Errorf("node is required")
	}
	data, err := encodeCommand(opSaveNode, saveNodePayload{Node: n})
	if err != nil {
		return err
	}
	return s.apply(data)
}

// ListNodes returns all node records from the FSM.
func (s *Store) ListNodes(ctx context.Context) ([]*node.Node, error) {
	_ = ctx
	return s.fsm.listNodes(), nil
}

// ListPeers returns known manager peer metadata.
func (s *Store) ListPeers() []PeerInfo {
	return s.fsm.listPeers()
}

// Ensure Store implements store.Store.
var _ store.Store = (*Store)(nil)
