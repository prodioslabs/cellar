package api

import (
	"context"
	"crypto/ecdsa"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/prodioslabs/cellar/internal/ca"
	"github.com/prodioslabs/cellar/internal/node"
	"github.com/prodioslabs/cellar/internal/raftstore"
	"github.com/prodioslabs/cellar/internal/store"
	"github.com/prodioslabs/cellar/internal/token"
)

// RaftAdmin is the optional Raft membership/leadership surface used by managers.
type RaftAdmin interface {
	IsLeader() bool
	LeaderHTTP() string
	AddVoter(ctx context.Context, peer raftstore.PeerInfo) error
	RemoveServer(nodeID string) error
}

// Server is the Cellar HTTP API.
type Server struct {
	store store.Store
	raft  RaftAdmin
	mux   *http.ServeMux
}

// New creates an API server backed by the given store (FileStore or RaftStore).
func New(s store.Store) *Server {
	return NewWithRaft(s, nil)
}

// NewWithRaft creates an API server with optional Raft admin capabilities.
func NewWithRaft(s store.Store, raft RaftAdmin) *Server {
	srv := &Server{store: s, raft: raft, mux: http.NewServeMux()}
	srv.routes()
	return srv
}

// Handler returns the HTTP handler.
func (s *Server) Handler() http.Handler {
	return s.mux
}

func (s *Server) routes() {
	s.mux.HandleFunc("POST /api/v1/cluster/init", s.handleInit)
	s.mux.HandleFunc("GET /api/v1/ca/certificate", s.handleCACertificate)
	s.mux.HandleFunc("POST /api/v1/ca/issue", s.handleIssue)
	s.mux.HandleFunc("GET /api/v1/ca/status/{node_id}", s.handleStatus)
	s.mux.HandleFunc("GET /api/v1/cluster/tokens", s.handleTokens)
	s.mux.HandleFunc("POST /api/v1/cluster/rotate-tokens", s.handleRotateTokens)
	s.mux.HandleFunc("GET /api/v1/cluster/leader", s.handleLeader)
	s.mux.HandleFunc("POST /api/v1/cluster/managers", s.handleAddManager)
	s.mux.HandleFunc("DELETE /api/v1/cluster/managers/{node_id}", s.handleRemoveManager)
}

type errorBody struct {
	Error string `json:"error"`
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, errorBody{Error: msg})
}

func (s *Server) requireLeader(w http.ResponseWriter) bool {
	if s.raft == nil {
		return true
	}
	if s.raft.IsLeader() {
		return true
	}
	if httpAddr := s.raft.LeaderHTTP(); httpAddr != "" {
		w.Header().Set("Location", httpAddr)
		w.Header().Set("X-Cellar-Leader", httpAddr)
	}
	writeError(w, http.StatusServiceUnavailable, store.ErrNotLeader.Error())
	return false
}

func (s *Server) writeStoreError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, store.ErrNotLeader):
		if s.raft != nil {
			if httpAddr := s.raft.LeaderHTTP(); httpAddr != "" {
				w.Header().Set("Location", httpAddr)
				w.Header().Set("X-Cellar-Leader", httpAddr)
			}
		}
		writeError(w, http.StatusServiceUnavailable, err.Error())
	case errors.Is(err, store.ErrAlreadyInitialized):
		writeError(w, http.StatusConflict, err.Error())
	case errors.Is(err, store.ErrNotInitialized):
		writeError(w, http.StatusNotFound, err.Error())
	case errors.Is(err, store.ErrNodeNotFound):
		writeError(w, http.StatusNotFound, err.Error())
	default:
		writeError(w, http.StatusInternalServerError, err.Error())
	}
}

type initRequest struct {
	CertValidityHours int `json:"cert_validity_hours,omitempty"`
}

type initResponse struct {
	ClusterID string     `json:"cluster_id"`
	Tokens    token.Pair `json:"tokens"`
}

func (s *Server) handleInit(w http.ResponseWriter, r *http.Request) {
	if !s.requireLeader(w) {
		return
	}

	var req initRequest
	if r.Body != nil {
		defer r.Body.Close()
		_ = json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&req)
	}

	root, err := ca.GenerateRootCA("cellar", ca.DefaultCAValidity)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	secrets, err := token.GenerateSecrets()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	clusterID, err := node.NewID()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	validity := ca.DefaultNodeValidity
	if req.CertValidityHours > 0 {
		validity = time.Duration(req.CertValidityHours) * time.Hour
	}

	err = s.store.InitCluster(r.Context(), store.ClusterConfig{
		ClusterID:    clusterID,
		CertValidity: validity,
		RootCA:       root,
		JoinSecrets:  secrets,
	})
	if err != nil {
		s.writeStoreError(w, err)
		return
	}

	pair, err := token.FormatPair(root.DigestPrefix(), secrets)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusCreated, initResponse{
		ClusterID: clusterID,
		Tokens:    pair,
	})
}

type certResponse struct {
	Certificate string `json:"certificate"`
}

func (s *Server) handleCACertificate(w http.ResponseWriter, r *http.Request) {
	root, err := s.store.GetRootCA(r.Context())
	if err != nil {
		s.writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, certResponse{Certificate: string(root.CertPEM)})
}

type issueRequest struct {
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

func (s *Server) handleIssue(w http.ResponseWriter, r *http.Request) {
	if !s.requireLeader(w) {
		return
	}

	defer r.Body.Close()
	var req issueRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if req.CSR == "" {
		writeError(w, http.StatusBadRequest, "csr is required")
		return
	}

	root, err := s.store.GetRootCA(r.Context())
	if err != nil {
		s.writeStoreError(w, err)
		return
	}
	cluster, err := s.store.GetCluster(r.Context())
	if err != nil {
		s.writeStoreError(w, err)
		return
	}

	csr, err := ca.ParseCSR([]byte(req.CSR))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	pub, ok := csr.PublicKey.(*ecdsa.PublicKey)
	if !ok {
		writeError(w, http.StatusBadRequest, "CSR public key must be ECDSA")
		return
	}
	fp, err := ca.PublicKeyFingerprint(pub)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	var (
		nodeID string
		role   node.Role
	)

	switch {
	case req.Token != "":
		role, err = token.Validate(req.Token, cluster.CADigestPrefix, cluster.JoinSecrets)
		if err != nil {
			writeError(w, http.StatusUnauthorized, err.Error())
			return
		}
		nodeID, err = node.NewID()
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
	case req.NodeID != "":
		existing, err := s.store.GetNode(r.Context(), req.NodeID)
		if err != nil {
			s.writeStoreError(w, err)
			return
		}
		if csr.Subject.CommonName != "" && csr.Subject.CommonName != existing.ID {
			writeError(w, http.StatusForbidden, "CSR CN does not match node ID")
			return
		}
		nodeID = existing.ID
		role = existing.Role
	default:
		writeError(w, http.StatusBadRequest, "token or node_id is required")
		return
	}

	issued, err := root.SignNodeCSR(ca.IssueRequest{
		CSR:      csr,
		NodeID:   nodeID,
		Role:     role,
		Validity: cluster.CertValidity,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	rec := &node.Node{
		ID:                nodeID,
		Role:              role,
		Membership:        node.MembershipAccepted,
		PubKeyFingerprint: fp,
		IssuedAt:          issued.Cert.NotBefore.UTC(),
		ExpiresAt:         issued.Cert.NotAfter.UTC(),
		CertificatePEM:    string(issued.CertPEM),
	}
	if err := s.store.SaveNode(r.Context(), rec); err != nil {
		s.writeStoreError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, issueResponse{
		NodeID:      nodeID,
		Role:        role,
		Certificate: string(issued.CertPEM),
		ExpiresAt:   issued.Cert.NotAfter.UTC(),
	})
}

type statusResponse struct {
	NodeID      string          `json:"node_id"`
	Role        node.Role       `json:"role"`
	Membership  node.Membership `json:"membership"`
	IssuedAt    time.Time       `json:"issued_at"`
	ExpiresAt   time.Time       `json:"expires_at"`
	Certificate string          `json:"certificate,omitempty"`
}

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	nodeID := r.PathValue("node_id")
	if nodeID == "" {
		writeError(w, http.StatusBadRequest, "node_id is required")
		return
	}
	n, err := s.store.GetNode(r.Context(), nodeID)
	if err != nil {
		s.writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, statusResponse{
		NodeID:      n.ID,
		Role:        n.Role,
		Membership:  n.Membership,
		IssuedAt:    n.IssuedAt,
		ExpiresAt:   n.ExpiresAt,
		Certificate: n.CertificatePEM,
	})
}

type tokensResponse struct {
	Tokens token.Pair `json:"tokens"`
}

func (s *Server) handleTokens(w http.ResponseWriter, r *http.Request) {
	if !allowControlPlane(w, r) {
		return
	}
	cluster, err := s.store.GetCluster(r.Context())
	if err != nil {
		s.writeStoreError(w, err)
		return
	}
	pair, err := token.FormatPair(cluster.CADigestPrefix, cluster.JoinSecrets)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, tokensResponse{Tokens: pair})
}

func (s *Server) handleRotateTokens(w http.ResponseWriter, r *http.Request) {
	if !allowControlPlane(w, r) {
		return
	}
	if !s.requireLeader(w) {
		return
	}
	cluster, err := s.store.GetCluster(r.Context())
	if err != nil {
		s.writeStoreError(w, err)
		return
	}
	secrets, err := token.GenerateSecrets()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	cluster.JoinSecrets = secrets
	if err := s.store.SaveCluster(r.Context(), cluster); err != nil {
		s.writeStoreError(w, err)
		return
	}
	pair, err := token.FormatPair(cluster.CADigestPrefix, secrets)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, tokensResponse{Tokens: pair})
}

type leaderResponse struct {
	LeaderHTTP string `json:"leader_http,omitempty"`
	IsLeader   bool   `json:"is_leader"`
}

func (s *Server) handleLeader(w http.ResponseWriter, r *http.Request) {
	if s.raft == nil {
		writeJSON(w, http.StatusOK, leaderResponse{IsLeader: true})
		return
	}
	writeJSON(w, http.StatusOK, leaderResponse{
		LeaderHTTP: s.raft.LeaderHTTP(),
		IsLeader:   s.raft.IsLeader(),
	})
}

type addManagerRequest struct {
	NodeID   string `json:"node_id"`
	RaftAddr string `json:"raft_addr"`
	HTTPAddr string `json:"http_addr,omitempty"`
}

func (s *Server) handleAddManager(w http.ResponseWriter, r *http.Request) {
	if !allowControlPlane(w, r) {
		return
	}
	if s.raft == nil {
		writeError(w, http.StatusNotImplemented, "raft is not enabled")
		return
	}
	if !s.requireLeader(w) {
		return
	}

	defer r.Body.Close()
	var req addManagerRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if req.NodeID == "" || req.RaftAddr == "" {
		writeError(w, http.StatusBadRequest, "node_id and raft_addr are required")
		return
	}

	err := s.raft.AddVoter(r.Context(), raftstore.PeerInfo{
		NodeID:   req.NodeID,
		RaftAddr: req.RaftAddr,
		HTTPAddr: req.HTTPAddr,
	})
	if err != nil {
		s.writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, req)
}

func (s *Server) handleRemoveManager(w http.ResponseWriter, r *http.Request) {
	if !allowControlPlane(w, r) {
		return
	}
	if s.raft == nil {
		writeError(w, http.StatusNotImplemented, "raft is not enabled")
		return
	}
	if !s.requireLeader(w) {
		return
	}
	nodeID := r.PathValue("node_id")
	if nodeID == "" {
		writeError(w, http.StatusBadRequest, "node_id is required")
		return
	}
	if err := s.raft.RemoveServer(nodeID); err != nil {
		s.writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"node_id": nodeID})
}

// allowControlPlane enforces manager-only access when a client TLS certificate
// is present. Plain HTTP (no client cert) remains allowed for local bootstrap.
// Workers presenting a client cert are rejected.
func allowControlPlane(w http.ResponseWriter, r *http.Request) bool {
	if r.TLS == nil || len(r.TLS.PeerCertificates) == 0 {
		return true
	}
	role, err := node.RoleFromCertificate(r.TLS.PeerCertificates[0])
	if err != nil {
		writeError(w, http.StatusUnauthorized, "invalid client certificate")
		return false
	}
	if !role.CanAccessControlPlane() {
		writeError(w, http.StatusForbidden, "workers cannot access the control plane")
		return false
	}
	return true
}

// ListenAndServe is a convenience wrapper.
func (s *Server) ListenAndServe(addr string) error {
	return http.ListenAndServe(addr, s.Handler())
}
