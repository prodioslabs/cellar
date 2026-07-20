package agent

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/prodioslabs/cellar/internal/ca"
	"github.com/prodioslabs/cellar/internal/node"
	"github.com/prodioslabs/cellar/internal/renew"
)

// Agent is a cluster node client that joins via join token and renews certificates.
type Agent struct {
	ManagerURL string
	DataDir    string
	HTTPClient *http.Client

	mu       sync.RWMutex
	nodeID   string
	role     node.Role
	certPEM  []byte
	keyPEM   []byte
	caPEM    []byte
	notBefore time.Time
	notAfter  time.Time
}

// Identity holds the node's issued identity material on disk.
type Identity struct {
	NodeID      string    `json:"node_id"`
	Role        node.Role `json:"role"`
	Certificate string    `json:"certificate"`
	PrivateKey  string    `json:"-"`
	CACert      string    `json:"ca_certificate"`
	ExpiresAt   time.Time `json:"expires_at"`
}

// New creates an Agent. dataDir stores node.crt, node.key, ca.crt, and identity.json.
func New(managerURL, dataDir string) *Agent {
	return &Agent{
		ManagerURL: managerURL,
		DataDir:    dataDir,
		HTTPClient: &http.Client{Timeout: 30 * time.Second},
	}
}

// Join obtains a node certificate using a join token and persists local credentials.
func (a *Agent) Join(ctx context.Context, joinToken string) (*Identity, error) {
	if err := os.MkdirAll(a.DataDir, 0o700); err != nil {
		return nil, fmt.Errorf("create data dir: %w", err)
	}

	caPEM, err := a.fetchCACertificate(ctx)
	if err != nil {
		return nil, err
	}

	keyPEM, csrPEM, _, err := ca.GenerateKeyAndCSR("")
	if err != nil {
		return nil, err
	}

	resp, err := a.issue(ctx, issueBody{CSR: string(csrPEM), Token: joinToken})
	if err != nil {
		return nil, err
	}

	id := &Identity{
		NodeID:      resp.NodeID,
		Role:        resp.Role,
		Certificate: resp.Certificate,
		PrivateKey:  string(keyPEM),
		CACert:      string(caPEM),
		ExpiresAt:   resp.ExpiresAt,
	}
	if err := a.persist(id); err != nil {
		return nil, err
	}
	if err := a.loadFromDisk(); err != nil {
		return nil, err
	}
	return id, nil
}

// Load loads previously persisted credentials from DataDir.
func (a *Agent) Load() error {
	return a.loadFromDisk()
}

// Renew issues a new certificate for the existing node ID.
func (a *Agent) Renew(ctx context.Context) (*Identity, error) {
	a.mu.RLock()
	nodeID := a.nodeID
	a.mu.RUnlock()
	if nodeID == "" {
		if err := a.loadFromDisk(); err != nil {
			return nil, err
		}
		a.mu.RLock()
		nodeID = a.nodeID
		a.mu.RUnlock()
	}
	if nodeID == "" {
		return nil, fmt.Errorf("agent has no node identity; call Join first")
	}

	keyPEM, csrPEM, _, err := ca.GenerateKeyAndCSR(nodeID)
	if err != nil {
		return nil, err
	}

	resp, err := a.issue(ctx, issueBody{CSR: string(csrPEM), NodeID: nodeID})
	if err != nil {
		return nil, err
	}

	caPEM := a.caPEM
	if len(caPEM) == 0 {
		caPEM, err = a.fetchCACertificate(ctx)
		if err != nil {
			return nil, err
		}
	}

	id := &Identity{
		NodeID:      resp.NodeID,
		Role:        resp.Role,
		Certificate: resp.Certificate,
		PrivateKey:  string(keyPEM),
		CACert:      string(caPEM),
		ExpiresAt:   resp.ExpiresAt,
	}
	if err := a.persist(id); err != nil {
		return nil, err
	}
	if err := a.loadFromDisk(); err != nil {
		return nil, err
	}
	return id, nil
}

// NeedsRenewal reports whether the local certificate is within the renewal window.
func (a *Agent) NeedsRenewal() bool {
	a.mu.RLock()
	defer a.mu.RUnlock()
	if a.notAfter.IsZero() {
		return true
	}
	return renew.Needed(a.notBefore, a.notAfter, time.Now(), renew.DefaultThreshold)
}

// RunRenewLoop periodically renews the node certificate until ctx is cancelled.
func (a *Agent) RunRenewLoop(ctx context.Context) error {
	for {
		a.mu.RLock()
		nb, na := a.notBefore, a.notAfter
		a.mu.RUnlock()

		wait := renew.NextCheck(nb, na, time.Now(), renew.DefaultThreshold)
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}

		if a.NeedsRenewal() {
			if _, err := a.Renew(ctx); err != nil {
				// Soft-fail and retry on next tick.
				continue
			}
		}
	}
}

// TLSConfig returns a tls.Config using the node's certificate and trusted CA.
func (a *Agent) TLSConfig() (*tls.Config, error) {
	a.mu.RLock()
	defer a.mu.RUnlock()

	if len(a.certPEM) == 0 || len(a.keyPEM) == 0 || len(a.caPEM) == 0 {
		return nil, fmt.Errorf("identity not loaded")
	}
	cert, err := tls.X509KeyPair(a.certPEM, a.keyPEM)
	if err != nil {
		return nil, err
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(a.caPEM) {
		return nil, fmt.Errorf("failed to parse CA certificate")
	}
	return &tls.Config{
		Certificates: []tls.Certificate{cert},
		RootCAs:      pool,
		ClientCAs:    pool,
		ClientAuth:   tls.RequireAndVerifyClientCert,
		MinVersion:   tls.VersionTLS12,
	}, nil
}

// NodeID returns the loaded node ID.
func (a *Agent) NodeID() string {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.nodeID
}

// Role returns the loaded node role.
func (a *Agent) Role() node.Role {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.role
}

// CanAccessControlPlane reports whether this node's role may call control-plane APIs.
// Both managers and workers use the agent for join/renew/mTLS; only managers return true.
func (a *Agent) CanAccessControlPlane() bool {
	return a.Role().CanAccessControlPlane()
}

type issueBody struct {
	CSR    string `json:"csr"`
	Token  string `json:"token,omitempty"`
	NodeID string `json:"node_id,omitempty"`
}

type issueResponse struct {
	NodeID      string    `json:"node_id"`
	Role        node.Role `json:"role"`
	Certificate string    `json:"certificate"`
	ExpiresAt   time.Time `json:"expires_at"`
}

type certResponse struct {
	Certificate string `json:"certificate"`
}

type errorBody struct {
	Error string `json:"error"`
}

func (a *Agent) fetchCACertificate(ctx context.Context) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, a.ManagerURL+"/api/v1/ca/certificate", nil)
	if err != nil {
		return nil, err
	}
	resp, err := a.HTTPClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, apiError(resp.StatusCode, body)
	}
	var cr certResponse
	if err := json.Unmarshal(body, &cr); err != nil {
		return nil, err
	}
	return []byte(cr.Certificate), nil
}

func (a *Agent) issue(ctx context.Context, body issueBody) (*issueResponse, error) {
	payload, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, a.ManagerURL+"/api/v1/ca/issue", bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := a.HTTPClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, apiError(resp.StatusCode, raw)
	}
	var out issueResponse
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func apiError(status int, body []byte) error {
	var eb errorBody
	if err := json.Unmarshal(body, &eb); err == nil && eb.Error != "" {
		return fmt.Errorf("API %d: %s", status, eb.Error)
	}
	return fmt.Errorf("API %d: %s", status, string(body))
}

func (a *Agent) persist(id *Identity) error {
	certPath := filepath.Join(a.DataDir, "node.crt")
	keyPath := filepath.Join(a.DataDir, "node.key")
	caPath := filepath.Join(a.DataDir, "ca.crt")
	metaPath := filepath.Join(a.DataDir, "identity.json")

	if err := atomicWrite(certPath, []byte(id.Certificate), 0o644); err != nil {
		return err
	}
	if err := atomicWrite(keyPath, []byte(id.PrivateKey), 0o600); err != nil {
		return err
	}
	if err := atomicWrite(caPath, []byte(id.CACert), 0o644); err != nil {
		return err
	}
	meta, err := json.MarshalIndent(struct {
		NodeID    string    `json:"node_id"`
		Role      node.Role `json:"role"`
		ExpiresAt time.Time `json:"expires_at"`
	}{id.NodeID, id.Role, id.ExpiresAt}, "", "  ")
	if err != nil {
		return err
	}
	return atomicWrite(metaPath, meta, 0o600)
}

func (a *Agent) loadFromDisk() error {
	certPEM, err := os.ReadFile(filepath.Join(a.DataDir, "node.crt"))
	if err != nil {
		return err
	}
	keyPEM, err := os.ReadFile(filepath.Join(a.DataDir, "node.key"))
	if err != nil {
		return err
	}
	caPEM, err := os.ReadFile(filepath.Join(a.DataDir, "ca.crt"))
	if err != nil {
		return err
	}
	metaRaw, err := os.ReadFile(filepath.Join(a.DataDir, "identity.json"))
	if err != nil {
		return err
	}
	var meta struct {
		NodeID    string    `json:"node_id"`
		Role      node.Role `json:"role"`
		ExpiresAt time.Time `json:"expires_at"`
	}
	if err := json.Unmarshal(metaRaw, &meta); err != nil {
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

	a.mu.Lock()
	defer a.mu.Unlock()
	a.nodeID = meta.NodeID
	a.role = meta.Role
	a.certPEM = certPEM
	a.keyPEM = keyPEM
	a.caPEM = caPEM
	a.notBefore = cert.NotBefore
	a.notAfter = cert.NotAfter
	return nil
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
