package raftstore

import (
	"encoding/json"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/hashicorp/raft"

	"github.com/prodioslabs/cellar/internal/ca"
	"github.com/prodioslabs/cellar/internal/node"
	"github.com/prodioslabs/cellar/internal/store"
)

// FSM is the in-memory Raft state machine holding RootCA, cluster config, nodes, and peers.
type FSM struct {
	mu      sync.RWMutex
	root    *ca.RootCA
	cluster *store.ClusterState
	nodes   map[string]*node.Node
	peers   map[string]PeerInfo // keyed by node ID
}

// NewFSM creates an empty FSM.
func NewFSM() *FSM {
	return &FSM{
		nodes: make(map[string]*node.Node),
		peers: make(map[string]PeerInfo),
	}
}

// Apply applies a committed log entry.
func (f *FSM) Apply(l *raft.Log) interface{} {
	cmd, err := decodeCommand(l.Data)
	if err != nil {
		return err
	}

	f.mu.Lock()
	defer f.mu.Unlock()

	switch cmd.Op {
	case opInitCluster:
		return f.applyInitCluster(cmd.Payload)
	case opSaveRootCA:
		return f.applySaveRootCA(cmd.Payload)
	case opSaveCluster:
		return f.applySaveCluster(cmd.Payload)
	case opSaveNode:
		return f.applySaveNode(cmd.Payload)
	case opSavePeer:
		return f.applySavePeer(cmd.Payload)
	default:
		return fmt.Errorf("unknown raft op %q", cmd.Op)
	}
}

func (f *FSM) applyInitCluster(raw json.RawMessage) interface{} {
	if f.cluster != nil {
		return store.ErrAlreadyInitialized
	}
	var p initClusterPayload
	if err := json.Unmarshal(raw, &p); err != nil {
		return err
	}
	root, err := ca.LoadRootCA(p.CertPEM, p.KeyPEM)
	if err != nil {
		return err
	}
	validity := p.CertValidity
	if validity <= 0 {
		validity = ca.DefaultNodeValidity
	}
	created := p.CreatedAt
	if created.IsZero() {
		created = time.Now().UTC()
	}
	f.root = root
	f.cluster = &store.ClusterState{
		ClusterID:      p.ClusterID,
		CertValidity:   validity,
		JoinSecrets:    p.JoinSecrets,
		CreatedAt:      created,
		CADigestPrefix: p.CADigestPrefix,
	}
	return nil
}

func (f *FSM) applySaveRootCA(raw json.RawMessage) interface{} {
	var p saveRootCAPayload
	if err := json.Unmarshal(raw, &p); err != nil {
		return err
	}
	root, err := ca.LoadRootCA(p.CertPEM, p.KeyPEM)
	if err != nil {
		return err
	}
	f.root = root
	if f.cluster != nil {
		f.cluster.CADigestPrefix = root.DigestPrefix()
	}
	return nil
}

func (f *FSM) applySaveCluster(raw json.RawMessage) interface{} {
	var p saveClusterPayload
	if err := json.Unmarshal(raw, &p); err != nil {
		return err
	}
	f.cluster = &store.ClusterState{
		ClusterID:      p.ClusterID,
		CertValidity:   p.CertValidity,
		JoinSecrets:    p.JoinSecrets,
		CreatedAt:      p.CreatedAt,
		CADigestPrefix: p.CADigestPrefix,
	}
	return nil
}

func (f *FSM) applySaveNode(raw json.RawMessage) interface{} {
	var p saveNodePayload
	if err := json.Unmarshal(raw, &p); err != nil {
		return err
	}
	if p.Node == nil || p.Node.ID == "" {
		return fmt.Errorf("node is required")
	}
	cp := *p.Node
	f.nodes[p.Node.ID] = &cp
	return nil
}

func (f *FSM) applySavePeer(raw json.RawMessage) interface{} {
	var p savePeerPayload
	if err := json.Unmarshal(raw, &p); err != nil {
		return err
	}
	if p.Peer.NodeID == "" {
		return fmt.Errorf("peer node_id is required")
	}
	f.peers[p.Peer.NodeID] = p.Peer
	return nil
}

// Snapshot returns a point-in-time FSM snapshot.
func (f *FSM) Snapshot() (raft.FSMSnapshot, error) {
	f.mu.RLock()
	defer f.mu.RUnlock()

	snap := &fsmSnapshot{
		nodes: make(map[string]*node.Node, len(f.nodes)),
		peers: make(map[string]PeerInfo, len(f.peers)),
	}
	if f.root != nil {
		snap.certPEM = append([]byte(nil), f.root.CertPEM...)
		snap.keyPEM = append([]byte(nil), f.root.KeyPEM...)
	}
	if f.cluster != nil {
		c := *f.cluster
		snap.cluster = &c
	}
	for id, n := range f.nodes {
		cp := *n
		snap.nodes[id] = &cp
	}
	for id, p := range f.peers {
		snap.peers[id] = p
	}
	return snap, nil
}

// Restore replaces FSM state from a snapshot.
func (f *FSM) Restore(rc io.ReadCloser) error {
	defer rc.Close()

	var snap persistedSnapshot
	if err := json.NewDecoder(rc).Decode(&snap); err != nil {
		return fmt.Errorf("decode snapshot: %w", err)
	}

	var root *ca.RootCA
	if len(snap.CertPEM) > 0 && len(snap.KeyPEM) > 0 {
		var err error
		root, err = ca.LoadRootCA(snap.CertPEM, snap.KeyPEM)
		if err != nil {
			return err
		}
	}

	nodes := make(map[string]*node.Node, len(snap.Nodes))
	for id, n := range snap.Nodes {
		if n == nil {
			continue
		}
		cp := *n
		nodes[id] = &cp
	}
	peers := make(map[string]PeerInfo, len(snap.Peers))
	for id, p := range snap.Peers {
		peers[id] = p
	}

	f.mu.Lock()
	defer f.mu.Unlock()
	f.root = root
	f.cluster = snap.Cluster
	f.nodes = nodes
	f.peers = peers
	return nil
}

type persistedSnapshot struct {
	CertPEM []byte                `json:"cert_pem,omitempty"`
	KeyPEM  []byte                `json:"key_pem,omitempty"`
	Cluster *store.ClusterState   `json:"cluster,omitempty"`
	Nodes   map[string]*node.Node `json:"nodes,omitempty"`
	Peers   map[string]PeerInfo   `json:"peers,omitempty"`
}

type fsmSnapshot struct {
	certPEM []byte
	keyPEM  []byte
	cluster *store.ClusterState
	nodes   map[string]*node.Node
	peers   map[string]PeerInfo
}

func (s *fsmSnapshot) Persist(sink raft.SnapshotSink) error {
	err := func() error {
		enc := json.NewEncoder(sink)
		return enc.Encode(persistedSnapshot{
			CertPEM: s.certPEM,
			KeyPEM:  s.keyPEM,
			Cluster: s.cluster,
			Nodes:   s.nodes,
			Peers:   s.peers,
		})
	}()
	if err != nil {
		_ = sink.Cancel()
		return err
	}
	return sink.Close()
}

func (s *fsmSnapshot) Release() {}

// --- read helpers (used by RaftStore) ---

func (f *FSM) getRootCA() (*ca.RootCA, error) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	if f.root == nil {
		return nil, store.ErrNotInitialized
	}
	return f.root, nil
}

func (f *FSM) getCluster() (*store.ClusterState, error) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	if f.cluster == nil {
		return nil, store.ErrNotInitialized
	}
	cp := *f.cluster
	return &cp, nil
}

func (f *FSM) isInitialized() bool {
	f.mu.RLock()
	defer f.mu.RUnlock()
	return f.cluster != nil && f.root != nil
}

func (f *FSM) getNode(id string) (*node.Node, error) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	n, ok := f.nodes[id]
	if !ok {
		return nil, store.ErrNodeNotFound
	}
	cp := *n
	return &cp, nil
}

func (f *FSM) listNodes() []*node.Node {
	f.mu.RLock()
	defer f.mu.RUnlock()
	out := make([]*node.Node, 0, len(f.nodes))
	for _, n := range f.nodes {
		cp := *n
		out = append(out, &cp)
	}
	return out
}

func (f *FSM) getPeer(id string) (PeerInfo, bool) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	p, ok := f.peers[id]
	return p, ok
}

func (f *FSM) peerByRaftAddr(addr string) (PeerInfo, bool) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	for _, p := range f.peers {
		if p.RaftAddr == addr {
			return p, true
		}
	}
	return PeerInfo{}, false
}

func (f *FSM) listPeers() []PeerInfo {
	f.mu.RLock()
	defer f.mu.RUnlock()
	out := make([]PeerInfo, 0, len(f.peers))
	for _, p := range f.peers {
		out = append(out, p)
	}
	return out
}
