package identity

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/prodioslabs/cellar/internal/node"
)

const (
	certFile     = "node.crt"
	keyFile      = "node.key"
	caFile       = "ca.crt"
	metaFile     = "identity.json"
	stateFile    = "daemon.json"
)

// Material is a node's local TLS identity (never includes CA private key).
type Material struct {
	NodeID      string
	Role        node.Role
	ClusterID   string
	Certificate []byte
	PrivateKey  []byte
	CACert      []byte
	NotBefore   time.Time
	NotAfter    time.Time
}

// DaemonState persists role/cluster metadata across restarts.
type DaemonState struct {
	NodeID         string    `json:"node_id"`
	Role           node.Role `json:"role"`
	ClusterID      string    `json:"cluster_id"`
	AdvertiseAddr  string    `json:"advertise_addr"`
	ListenAddr     string    `json:"listen_addr"`
	RaftAddr       string    `json:"raft_addr"`
	Initialized    bool      `json:"initialized"`
}

// Store loads and persists node identity under a data directory.
type Store struct {
	mu      sync.RWMutex
	dataDir string
	mat     *Material
	state   DaemonState
}

func NewStore(dataDir string) *Store {
	return &Store{dataDir: dataDir}
}

func (s *Store) DataDir() string { return s.dataDir }

func (s *Store) Load() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := os.MkdirAll(s.dataDir, 0o700); err != nil {
		return err
	}

	statePath := filepath.Join(s.dataDir, stateFile)
	if b, err := os.ReadFile(statePath); err == nil {
		_ = json.Unmarshal(b, &s.state)
	}

	certPEM, err := os.ReadFile(filepath.Join(s.dataDir, certFile))
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	keyPEM, err := os.ReadFile(filepath.Join(s.dataDir, keyFile))
	if err != nil {
		return err
	}
	caPEM, err := os.ReadFile(filepath.Join(s.dataDir, caFile))
	if err != nil {
		return err
	}

	block, _ := pem.Decode(certPEM)
	if block == nil {
		return fmt.Errorf("invalid node certificate PEM")
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return err
	}

	role := s.state.Role
	if role == "" {
		role, err = node.RoleFromCertificate(cert)
		if err != nil {
			return err
		}
	}
	nodeID := s.state.NodeID
	if nodeID == "" {
		nodeID = cert.Subject.CommonName
	}

	s.mat = &Material{
		NodeID:      nodeID,
		Role:        role,
		ClusterID:   s.state.ClusterID,
		Certificate: certPEM,
		PrivateKey:  keyPEM,
		CACert:      caPEM,
		NotBefore:   cert.NotBefore,
		NotAfter:    cert.NotAfter,
	}
	return nil
}

func (s *Store) Save(mat *Material, state DaemonState) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := os.MkdirAll(s.dataDir, 0o700); err != nil {
		return err
	}
	if err := atomicWrite(filepath.Join(s.dataDir, certFile), mat.Certificate, 0o644); err != nil {
		return err
	}
	if err := atomicWrite(filepath.Join(s.dataDir, keyFile), mat.PrivateKey, 0o600); err != nil {
		return err
	}
	if err := atomicWrite(filepath.Join(s.dataDir, caFile), mat.CACert, 0o644); err != nil {
		return err
	}
	meta, err := json.MarshalIndent(struct {
		NodeID    string    `json:"node_id"`
		Role      node.Role `json:"role"`
		ExpiresAt time.Time `json:"expires_at"`
	}{mat.NodeID, mat.Role, mat.NotAfter}, "", "  ")
	if err != nil {
		return err
	}
	if err := atomicWrite(filepath.Join(s.dataDir, metaFile), meta, 0o600); err != nil {
		return err
	}
	state.NodeID = mat.NodeID
	state.Role = mat.Role
	state.ClusterID = mat.ClusterID
	state.Initialized = true
	sb, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	if err := atomicWrite(filepath.Join(s.dataDir, stateFile), sb, 0o600); err != nil {
		return err
	}
	s.mat = mat
	s.state = state
	return nil
}

func (s *Store) SaveState(state DaemonState) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := os.MkdirAll(s.dataDir, 0o700); err != nil {
		return err
	}
	sb, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	if err := atomicWrite(filepath.Join(s.dataDir, stateFile), sb, 0o600); err != nil {
		return err
	}
	s.state = state
	return nil
}

func (s *Store) Material() *Material {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.mat
}

func (s *Store) State() DaemonState {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.state
}

func (s *Store) HasIdentity() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.mat != nil
}

// ServerTLSConfig builds a TLS config for the remote gRPC listener.
// ClientAuth is VerifyClientCertIfGiven so bootstrap RPCs work without a client cert.
func (s *Store) ServerTLSConfig() (*tls.Config, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.mat == nil {
		return nil, fmt.Errorf("identity not loaded")
	}
	cert, err := tls.X509KeyPair(s.mat.Certificate, s.mat.PrivateKey)
	if err != nil {
		return nil, err
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(s.mat.CACert) {
		return nil, fmt.Errorf("failed to parse CA certificate")
	}
	return &tls.Config{
		Certificates: []tls.Certificate{cert},
		ClientCAs:    pool,
		ClientAuth:   tls.VerifyClientCertIfGiven,
		MinVersion:   tls.VersionTLS12,
	}, nil
}

// ClientTLSConfig builds mTLS client config for dialing managers.
func (s *Store) ClientTLSConfig(serverName string) (*tls.Config, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.mat == nil {
		return nil, fmt.Errorf("identity not loaded")
	}
	cert, err := tls.X509KeyPair(s.mat.Certificate, s.mat.PrivateKey)
	if err != nil {
		return nil, err
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(s.mat.CACert) {
		return nil, fmt.Errorf("failed to parse CA certificate")
	}
	return &tls.Config{
		Certificates: []tls.Certificate{cert},
		RootCAs:      pool,
		ServerName:   serverName,
		MinVersion:   tls.VersionTLS12,
	}, nil
}

func atomicWrite(path string, data []byte, mode os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()

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
