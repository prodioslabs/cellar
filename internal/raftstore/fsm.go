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
	"github.com/prodioslabs/cellar/internal/sandbox"
	"github.com/prodioslabs/cellar/internal/store"
	"github.com/prodioslabs/cellar/internal/token"
)

// FSM is the in-memory Raft state machine holding Cluster.RootCA, nodes, and peers.
type FSM struct {
	mu        sync.RWMutex
	cluster   *store.Cluster
	nodes     map[string]*node.Node
	peers     map[string]PeerInfo
	sandboxes map[string]*sandbox.Sandbox
}

// NewFSM creates an empty FSM.
func NewFSM() *FSM {
	return &FSM{
		nodes:     make(map[string]*node.Node),
		peers:     make(map[string]PeerInfo),
		sandboxes: make(map[string]*sandbox.Sandbox),
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
	case opCreateCluster, opInitCluster:
		return f.applyCreateCluster(cmd.Op, cmd.Payload)
	case opUpdateCluster, opSaveCluster, opSaveRootCA:
		return f.applyUpdateCluster(cmd.Op, cmd.Payload)
	case opSaveNode:
		return f.applySaveNode(cmd.Payload)
	case opSavePeer:
		return f.applySavePeer(cmd.Payload)
	case opSaveSandbox:
		return f.applySaveSandbox(cmd.Payload)
	case opDeleteSandbox:
		return f.applyDeleteSandbox(cmd.Payload)
	default:
		return fmt.Errorf("unknown raft op %q", cmd.Op)
	}
}

func (f *FSM) applyCreateCluster(op string, raw json.RawMessage) interface{} {
	if f.cluster != nil {
		return store.ErrAlreadyInitialized
	}

	if op == opCreateCluster {
		var p createClusterPayload
		if err := json.Unmarshal(raw, &p); err != nil {
			return err
		}
		cp := p.Cluster
		cp.RootCA.CAKey = append([]byte(nil), p.Cluster.RootCA.CAKey...)
		cp.RootCA.CACert = append([]byte(nil), p.Cluster.RootCA.CACert...)
		f.cluster = &cp
		return nil
	}

	// Legacy init_cluster
	var p initClusterPayload
	if err := json.Unmarshal(raw, &p); err != nil {
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
	f.cluster = &store.Cluster{
		ClusterID: p.ClusterID,
		RootCA: store.RootCAMaterial{
			CAKey:      append([]byte(nil), p.KeyPEM...),
			CACert:     append([]byte(nil), p.CertPEM...),
			CACertHash: p.CADigestPrefix,
			JoinSecrets: token.Secrets{
				Worker:  p.JoinSecrets.Worker,
				Manager: p.JoinSecrets.Manager,
			},
		},
		CertValidity: validity,
		CreatedAt:    created,
	}
	return nil
}

func (f *FSM) applyUpdateCluster(op string, raw json.RawMessage) interface{} {
	switch op {
	case opUpdateCluster:
		var p updateClusterPayload
		if err := json.Unmarshal(raw, &p); err != nil {
			return err
		}
		if f.cluster == nil {
			return store.ErrNotInitialized
		}
		cp := p.Cluster
		cp.RootCA.CAKey = append([]byte(nil), p.Cluster.RootCA.CAKey...)
		cp.RootCA.CACert = append([]byte(nil), p.Cluster.RootCA.CACert...)
		f.cluster = &cp
		return nil
	case opSaveRootCA:
		var p saveRootCAPayload
		if err := json.Unmarshal(raw, &p); err != nil {
			return err
		}
		if f.cluster == nil {
			return store.ErrNotInitialized
		}
		f.cluster.RootCA.CACert = append([]byte(nil), p.CertPEM...)
		f.cluster.RootCA.CAKey = append([]byte(nil), p.KeyPEM...)
		root, err := ca.LoadRootCA(p.CertPEM, p.KeyPEM)
		if err != nil {
			return err
		}
		f.cluster.RootCA.CACertHash = root.DigestPrefix()
		return nil
	case opSaveCluster:
		var p saveClusterPayload
		if err := json.Unmarshal(raw, &p); err != nil {
			return err
		}
		if f.cluster == nil {
			return store.ErrNotInitialized
		}
		f.cluster.ClusterID = p.ClusterID
		f.cluster.CertValidity = p.CertValidity
		f.cluster.CreatedAt = p.CreatedAt
		f.cluster.RootCA.CACertHash = p.CADigestPrefix
		f.cluster.RootCA.JoinSecrets = token.Secrets{
			Worker:  p.JoinSecrets.Worker,
			Manager: p.JoinSecrets.Manager,
		}
		return nil
	default:
		return fmt.Errorf("unknown update op %q", op)
	}
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

func (f *FSM) applySaveSandbox(raw json.RawMessage) interface{} {
	var p saveSandboxPayload
	if err := json.Unmarshal(raw, &p); err != nil {
		return err
	}
	if p.Sandbox == nil || p.Sandbox.ID == "" {
		return fmt.Errorf("sandbox is required")
	}
	f.sandboxes[p.Sandbox.ID] = sandbox.Clone(p.Sandbox)
	return nil
}

func (f *FSM) applyDeleteSandbox(raw json.RawMessage) interface{} {
	var p deleteSandboxPayload
	if err := json.Unmarshal(raw, &p); err != nil {
		return err
	}
	if p.ID == "" {
		return fmt.Errorf("sandbox id is required")
	}
	if _, ok := f.sandboxes[p.ID]; !ok {
		return store.ErrSandboxNotFound
	}
	delete(f.sandboxes, p.ID)
	return nil
}

// Snapshot returns a point-in-time FSM snapshot.
func (f *FSM) Snapshot() (raft.FSMSnapshot, error) {
	f.mu.RLock()
	defer f.mu.RUnlock()

	snap := &fsmSnapshot{
		nodes:     make(map[string]*node.Node, len(f.nodes)),
		peers:     make(map[string]PeerInfo, len(f.peers)),
		sandboxes: make(map[string]*sandbox.Sandbox, len(f.sandboxes)),
	}
	if f.cluster != nil {
		c := *f.cluster
		c.RootCA.CAKey = append([]byte(nil), f.cluster.RootCA.CAKey...)
		c.RootCA.CACert = append([]byte(nil), f.cluster.RootCA.CACert...)
		snap.cluster = &c
	}
	for id, n := range f.nodes {
		cp := *n
		snap.nodes[id] = &cp
	}
	for id, p := range f.peers {
		snap.peers[id] = p
	}
	for id, sb := range f.sandboxes {
		snap.sandboxes[id] = sandbox.Clone(sb)
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
	sandboxes := make(map[string]*sandbox.Sandbox, len(snap.Sandboxes))
	for id, sb := range snap.Sandboxes {
		if sb == nil {
			continue
		}
		sandboxes[id] = sandbox.Clone(sb)
	}

	var cluster *store.Cluster
	if snap.Cluster != nil {
		c := *snap.Cluster
		c.RootCA.CAKey = append([]byte(nil), snap.Cluster.RootCA.CAKey...)
		c.RootCA.CACert = append([]byte(nil), snap.Cluster.RootCA.CACert...)
		cluster = &c
	} else if len(snap.CertPEM) > 0 && len(snap.KeyPEM) > 0 {
		// Legacy snapshot shape
		root, err := ca.LoadRootCA(snap.CertPEM, snap.KeyPEM)
		if err != nil {
			return err
		}
		var secrets token.Secrets
		var clusterID string
		var validity time.Duration
		var created time.Time
		var hash string
		if snap.LegacyCluster != nil {
			clusterID = snap.LegacyCluster.ClusterID
			validity = snap.LegacyCluster.CertValidity
			created = snap.LegacyCluster.CreatedAt
			hash = snap.LegacyCluster.CADigestPrefix
			secrets = snap.LegacyCluster.JoinSecrets
		}
		if hash == "" {
			hash = root.DigestPrefix()
		}
		cluster = &store.Cluster{
			ClusterID: clusterID,
			RootCA: store.RootCAMaterial{
				CAKey:       append([]byte(nil), snap.KeyPEM...),
				CACert:      append([]byte(nil), snap.CertPEM...),
				CACertHash:  hash,
				JoinSecrets: secrets,
			},
			CertValidity: validity,
			CreatedAt:    created,
		}
	}

	f.mu.Lock()
	defer f.mu.Unlock()
	f.cluster = cluster
	f.nodes = nodes
	f.peers = peers
	f.sandboxes = sandboxes
	return nil
}

type persistedSnapshot struct {
	Cluster   *store.Cluster                `json:"cluster,omitempty"`
	Nodes     map[string]*node.Node         `json:"nodes,omitempty"`
	Peers     map[string]PeerInfo           `json:"peers,omitempty"`
	Sandboxes map[string]*sandbox.Sandbox   `json:"sandboxes,omitempty"`
	// Legacy fields
	CertPEM       []byte              `json:"cert_pem,omitempty"`
	KeyPEM        []byte              `json:"key_pem,omitempty"`
	LegacyCluster *legacyClusterState `json:"legacy_cluster,omitempty"`
}

type legacyClusterState struct {
	ClusterID      string        `json:"cluster_id"`
	CertValidity   time.Duration `json:"cert_validity_ns"`
	JoinSecrets    token.Secrets `json:"join_secrets"`
	CreatedAt      time.Time     `json:"created_at"`
	CADigestPrefix string        `json:"ca_digest_prefix"`
}

type fsmSnapshot struct {
	cluster   *store.Cluster
	nodes     map[string]*node.Node
	peers     map[string]PeerInfo
	sandboxes map[string]*sandbox.Sandbox
}

func (s *fsmSnapshot) Persist(sink raft.SnapshotSink) error {
	err := json.NewEncoder(sink).Encode(persistedSnapshot{
		Cluster:   s.cluster,
		Nodes:     s.nodes,
		Peers:     s.peers,
		Sandboxes: s.sandboxes,
	})
	if err != nil {
		_ = sink.Cancel()
		return err
	}
	return sink.Close()
}

func (s *fsmSnapshot) Release() {}

func (f *FSM) isInitialized() bool {
	f.mu.RLock()
	defer f.mu.RUnlock()
	return f.cluster != nil
}

func (f *FSM) getCluster() (*store.Cluster, error) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	if f.cluster == nil {
		return nil, store.ErrNotInitialized
	}
	cp := *f.cluster
	cp.RootCA.CAKey = append([]byte(nil), f.cluster.RootCA.CAKey...)
	cp.RootCA.CACert = append([]byte(nil), f.cluster.RootCA.CACert...)
	return &cp, nil
}

func (f *FSM) getRootCA() (*ca.RootCA, error) {
	cluster, err := f.getCluster()
	if err != nil {
		return nil, err
	}
	return cluster.LoadRootCA()
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

func (f *FSM) getSandbox(id string) (*sandbox.Sandbox, error) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	sb, ok := f.sandboxes[id]
	if !ok {
		return nil, store.ErrSandboxNotFound
	}
	return sandbox.Clone(sb), nil
}

func (f *FSM) listSandboxes() []*sandbox.Sandbox {
	f.mu.RLock()
	defer f.mu.RUnlock()
	out := make([]*sandbox.Sandbox, 0, len(f.sandboxes))
	for _, sb := range f.sandboxes {
		out = append(out, sandbox.Clone(sb))
	}
	return out
}

func (f *FSM) listSandboxesByNode(nodeID string) []*sandbox.Sandbox {
	f.mu.RLock()
	defer f.mu.RUnlock()
	out := make([]*sandbox.Sandbox, 0)
	for _, sb := range f.sandboxes {
		if sb.NodeID == nodeID {
			out = append(out, sandbox.Clone(sb))
		}
	}
	return out
}
