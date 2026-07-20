package store

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/prodioslabs/cellar/internal/ca"
	"github.com/prodioslabs/cellar/internal/node"
)

// FileStore persists CA material and cluster state as PEM + JSON under a data directory.
type FileStore struct {
	dir string
	mu  sync.RWMutex
}

// NewFileStore creates a FileStore rooted at dir.
func NewFileStore(dir string) (*FileStore, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("create data dir: %w", err)
	}
	for _, sub := range []string{"ca", "nodes"} {
		if err := os.MkdirAll(filepath.Join(dir, sub), 0o700); err != nil {
			return nil, fmt.Errorf("create %s dir: %w", sub, err)
		}
	}
	return &FileStore{dir: dir}, nil
}

func (s *FileStore) caCertPath() string { return filepath.Join(s.dir, "ca", "ca.crt") }
func (s *FileStore) caKeyPath() string  { return filepath.Join(s.dir, "ca", "ca.key") }
func (s *FileStore) clusterPath() string {
	return filepath.Join(s.dir, "cluster.json")
}
func (s *FileStore) nodePath(id string) string {
	return filepath.Join(s.dir, "nodes", id+".json")
}

// InitCluster bootstraps the cluster; fails if already initialized.
func (s *FileStore) InitCluster(ctx context.Context, cfg ClusterConfig) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if ok, err := s.isInitializedLocked(); err != nil {
		return err
	} else if ok {
		return ErrAlreadyInitialized
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

	if err := s.saveRootCALocked(cfg.RootCA); err != nil {
		return err
	}

	state := &ClusterState{
		ClusterID:      cfg.ClusterID,
		CertValidity:   validity,
		JoinSecret:     cfg.JoinSecret,
		CreatedAt:      time.Now().UTC(),
		CADigestPrefix: cfg.RootCA.DigestPrefix(),
	}
	return s.saveClusterLocked(state)
}

// IsInitialized reports whether cluster.json and CA material exist.
func (s *FileStore) IsInitialized(ctx context.Context) (bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.isInitializedLocked()
}

func (s *FileStore) isInitializedLocked() (bool, error) {
	_, err := os.Stat(s.clusterPath())
	if err == nil {
		return true, nil
	}
	if os.IsNotExist(err) {
		return false, nil
	}
	return false, err
}

// GetRootCA loads the root CA from disk.
func (s *FileStore) GetRootCA(ctx context.Context) (*ca.RootCA, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.getRootCALocked()
}

func (s *FileStore) getRootCALocked() (*ca.RootCA, error) {
	certPEM, err := os.ReadFile(s.caCertPath())
	if err != nil {
		if os.IsNotExist(err) {
			return nil, ErrNotInitialized
		}
		return nil, err
	}
	keyPEM, err := os.ReadFile(s.caKeyPath())
	if err != nil {
		if os.IsNotExist(err) {
			return nil, ErrNotInitialized
		}
		return nil, err
	}
	return ca.LoadRootCA(certPEM, keyPEM)
}

// SaveRootCA writes the root CA PEM files.
func (s *FileStore) SaveRootCA(ctx context.Context, root *ca.RootCA) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.saveRootCALocked(root)
}

func (s *FileStore) saveRootCALocked(root *ca.RootCA) error {
	if err := atomicWrite(s.caCertPath(), root.CertPEM, 0o644); err != nil {
		return fmt.Errorf("write ca.crt: %w", err)
	}
	if err := atomicWrite(s.caKeyPath(), root.KeyPEM, 0o600); err != nil {
		return fmt.Errorf("write ca.key: %w", err)
	}
	return nil
}

// GetCluster loads cluster.json.
func (s *FileStore) GetCluster(ctx context.Context) (*ClusterState, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.getClusterLocked()
}

func (s *FileStore) getClusterLocked() (*ClusterState, error) {
	data, err := os.ReadFile(s.clusterPath())
	if err != nil {
		if os.IsNotExist(err) {
			return nil, ErrNotInitialized
		}
		return nil, err
	}
	var state ClusterState
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, fmt.Errorf("decode cluster.json: %w", err)
	}
	return &state, nil
}

// SaveCluster writes cluster.json.
func (s *FileStore) SaveCluster(ctx context.Context, state *ClusterState) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.saveClusterLocked(state)
}

func (s *FileStore) saveClusterLocked(state *ClusterState) error {
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	return atomicWrite(s.clusterPath(), data, 0o600)
}

// GetNode loads a node record.
func (s *FileStore) GetNode(ctx context.Context, nodeID string) (*node.Node, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	data, err := os.ReadFile(s.nodePath(nodeID))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, ErrNodeNotFound
		}
		return nil, err
	}
	var n node.Node
	if err := json.Unmarshal(data, &n); err != nil {
		return nil, fmt.Errorf("decode node: %w", err)
	}
	return &n, nil
}

// SaveNode persists a node record.
func (s *FileStore) SaveNode(ctx context.Context, n *node.Node) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	data, err := json.MarshalIndent(n, "", "  ")
	if err != nil {
		return err
	}
	return atomicWrite(s.nodePath(n.ID), data, 0o600)
}

// ListNodes returns all node records.
func (s *FileStore) ListNodes(ctx context.Context) ([]*node.Node, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	entries, err := os.ReadDir(filepath.Join(s.dir, "nodes"))
	if err != nil {
		return nil, err
	}
	var nodes []*node.Node
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".json" {
			continue
		}
		data, err := os.ReadFile(filepath.Join(s.dir, "nodes", e.Name()))
		if err != nil {
			return nil, err
		}
		var n node.Node
		if err := json.Unmarshal(data, &n); err != nil {
			return nil, err
		}
		nodes = append(nodes, &n)
	}
	return nodes, nil
}

func atomicWrite(path string, data []byte, mode os.FileMode) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer func() {
		_ = os.Remove(tmpName)
	}()

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Chmod(mode); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}
