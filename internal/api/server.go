package api

import (
	"crypto/ecdsa"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/prodioslabs/cellar/internal/ca"
	"github.com/prodioslabs/cellar/internal/node"
	"github.com/prodioslabs/cellar/internal/store"
	"github.com/prodioslabs/cellar/internal/token"
)

// Server is the Cellar HTTP API.
type Server struct {
	store store.Store
	mux   *http.ServeMux
}

// New creates an API server backed by the given store.
func New(s store.Store) *Server {
	srv := &Server{store: s, mux: http.NewServeMux()}
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
	s.mux.HandleFunc("GET /api/v1/cluster/token", s.handleToken)
	s.mux.HandleFunc("POST /api/v1/cluster/rotate-token", s.handleRotateToken)
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

type initRequest struct {
	CertValidityHours int `json:"cert_validity_hours,omitempty"`
}

type initResponse struct {
	ClusterID string `json:"cluster_id"`
	Token     string `json:"token"`
}

func (s *Server) handleInit(w http.ResponseWriter, r *http.Request) {
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

	secret, err := token.GenerateSecret()
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
		JoinSecret:   secret,
	})
	if err != nil {
		if errors.Is(err, store.ErrAlreadyInitialized) {
			writeError(w, http.StatusConflict, err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusCreated, initResponse{
		ClusterID: clusterID,
		Token:     token.Format(root.DigestPrefix(), secret),
	})
}

type certResponse struct {
	Certificate string `json:"certificate"`
}

func (s *Server) handleCACertificate(w http.ResponseWriter, r *http.Request) {
	root, err := s.store.GetRootCA(r.Context())
	if err != nil {
		if errors.Is(err, store.ErrNotInitialized) {
			writeError(w, http.StatusNotFound, err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
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
		if errors.Is(err, store.ErrNotInitialized) {
			writeError(w, http.StatusNotFound, err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	cluster, err := s.store.GetCluster(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
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

	var nodeID string
	role := node.RoleCellarNode

	switch {
	case req.Token != "":
		if err := token.Validate(req.Token, cluster.CADigestPrefix, cluster.JoinSecret); err != nil {
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
			if errors.Is(err, store.ErrNodeNotFound) {
				writeError(w, http.StatusNotFound, err.Error())
				return
			}
			writeError(w, http.StatusInternalServerError, err.Error())
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
		writeError(w, http.StatusInternalServerError, err.Error())
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
		if errors.Is(err, store.ErrNodeNotFound) {
			writeError(w, http.StatusNotFound, err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
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

type tokenResponse struct {
	Token string `json:"token"`
}

func (s *Server) handleToken(w http.ResponseWriter, r *http.Request) {
	cluster, err := s.store.GetCluster(r.Context())
	if err != nil {
		if errors.Is(err, store.ErrNotInitialized) {
			writeError(w, http.StatusNotFound, err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, tokenResponse{
		Token: token.Format(cluster.CADigestPrefix, cluster.JoinSecret),
	})
}

func (s *Server) handleRotateToken(w http.ResponseWriter, r *http.Request) {
	cluster, err := s.store.GetCluster(r.Context())
	if err != nil {
		if errors.Is(err, store.ErrNotInitialized) {
			writeError(w, http.StatusNotFound, err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	secret, err := token.GenerateSecret()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	cluster.JoinSecret = secret
	if err := s.store.SaveCluster(r.Context(), cluster); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, tokenResponse{
		Token: token.Format(cluster.CADigestPrefix, secret),
	})
}

// ListenAndServe is a convenience wrapper.
func (s *Server) ListenAndServe(addr string) error {
	return http.ListenAndServe(addr, s.Handler())
}
