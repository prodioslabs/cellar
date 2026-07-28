package grpcapi

import (
	"context"
	"crypto/ecdsa"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"sync"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"

	cellarv1 "github.com/prodioslabs/cellar/api/gen"
	"github.com/prodioslabs/cellar/internal/ca"
	"github.com/prodioslabs/cellar/internal/node"
	"github.com/prodioslabs/cellar/internal/raftstore"
	"github.com/prodioslabs/cellar/internal/store"
	"github.com/prodioslabs/cellar/internal/token"
)

// RaftAdmin is the optional Raft membership surface used by managers.
type RaftAdmin interface {
	IsLeader() bool
	LeaderGRPC() string
	AddVoter(ctx context.Context, peer raftstore.PeerInfo) error
	RemoveServer(nodeID string) error
	GRPCAdvertise() string
}

// CAServer implements CA, NodeCA, and RaftMembership with leader-gated signing.
type CAServer struct {
	cellarv1.UnimplementedCAServer
	cellarv1.UnimplementedNodeCAServer
	cellarv1.UnimplementedRaftMembershipServer

	mu     sync.RWMutex
	store  store.Store
	raft   RaftAdmin
	root   *ca.RootCA
	ready  bool
}

func NewCAServer(s store.Store, raft RaftAdmin) *CAServer {
	return &CAServer{store: s, raft: raft}
}

// UpdateRootCA loads signing material from the raft Cluster object.
func (s *CAServer) UpdateRootCA(ctx context.Context) error {
	cluster, err := s.store.GetCluster(ctx)
	if err != nil {
		return err
	}
	root, err := cluster.LoadRootCA()
	if err != nil {
		return err
	}
	s.mu.Lock()
	s.root = root
	s.ready = true
	s.mu.Unlock()
	return nil
}

// Stop clears signing state (follower demotion).
func (s *CAServer) Stop() {
	s.mu.Lock()
	s.root = nil
	s.ready = false
	s.mu.Unlock()
}

func (s *CAServer) signingRoot() (*ca.RootCA, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if !s.ready || s.root == nil {
		return nil, status.Error(codes.Unavailable, "CA not ready")
	}
	return s.root, nil
}

func (s *CAServer) requireLeader() error {
	if s.raft != nil && !s.raft.IsLeader() {
		return status.Error(codes.Unavailable, store.ErrNotLeader.Error())
	}
	return nil
}

func (s *CAServer) GetRootCACertificate(ctx context.Context, _ *cellarv1.GetRootCACertificateRequest) (*cellarv1.GetRootCACertificateResponse, error) {
	cluster, err := s.store.GetCluster(ctx)
	if err != nil {
		return nil, mapStoreErr(err)
	}
	return &cellarv1.GetRootCACertificateResponse{Certificate: cluster.RootCA.CACert}, nil
}

func (s *CAServer) IssueNodeCertificate(ctx context.Context, req *cellarv1.IssueNodeCertificateRequest) (*cellarv1.IssueNodeCertificateResponse, error) {
	if err := s.requireLeader(); err != nil {
		return nil, err
	}
	if len(req.Csr) == 0 {
		return nil, status.Error(codes.InvalidArgument, "csr is required")
	}

	root, err := s.signingRoot()
	if err != nil {
		// Try to load from store if not yet warm.
		if uerr := s.UpdateRootCA(ctx); uerr != nil {
			return nil, err
		}
		root, err = s.signingRoot()
		if err != nil {
			return nil, err
		}
	}

	cluster, err := s.store.GetCluster(ctx)
	if err != nil {
		return nil, mapStoreErr(err)
	}

	csr, err := ca.ParseCSR(req.Csr)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	pub, ok := csr.PublicKey.(*ecdsa.PublicKey)
	if !ok {
		return nil, status.Error(codes.InvalidArgument, "CSR public key must be ECDSA")
	}
	fp, err := ca.PublicKeyFingerprint(pub)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}

	var (
		nodeID string
		role   node.Role
	)

	switch {
	case req.Token != "":
		role, err = token.Validate(req.Token, cluster.RootCA.CACertHash, cluster.RootCA.JoinSecrets)
		if err != nil {
			return nil, status.Error(codes.Unauthenticated, err.Error())
		}
		nodeID, err = node.NewID()
		if err != nil {
			return nil, status.Error(codes.Internal, err.Error())
		}
	case req.NodeId != "":
		// Prefer mTLS peer identity when present.
		if peerCert := peerCertificate(ctx); peerCert != nil {
			if peerCert.Subject.CommonName != req.NodeId {
				return nil, status.Error(codes.PermissionDenied, "client cert CN does not match node_id")
			}
		}
		existing, err := s.store.GetNode(ctx, req.NodeId)
		if err != nil {
			return nil, mapStoreErr(err)
		}
		if csr.Subject.CommonName != "" && csr.Subject.CommonName != existing.ID {
			return nil, status.Error(codes.PermissionDenied, "CSR CN does not match node ID")
		}
		nodeID = existing.ID
		role = existing.Role
	default:
		return nil, status.Error(codes.InvalidArgument, "token or node_id is required")
	}

	issued, err := root.SignNodeCSR(ca.IssueRequest{
		CSR:      csr,
		NodeID:   nodeID,
		Role:     role,
		Validity: cluster.CertValidity,
	})
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
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
	if err := s.store.SaveNode(ctx, rec); err != nil {
		return nil, mapStoreErr(err)
	}

	return &cellarv1.IssueNodeCertificateResponse{
		NodeId:             nodeID,
		Role:               string(role),
		Membership:         string(node.MembershipAccepted),
		Certificate:        issued.CertPEM,
		ExpiresAtUnixNano:  issued.Cert.NotAfter.UTC().UnixNano(),
	}, nil
}

func (s *CAServer) NodeCertificateStatus(ctx context.Context, req *cellarv1.NodeCertificateStatusRequest) (*cellarv1.NodeCertificateStatusResponse, error) {
	if req.NodeId == "" {
		return nil, status.Error(codes.InvalidArgument, "node_id is required")
	}
	n, err := s.store.GetNode(ctx, req.NodeId)
	if err != nil {
		return nil, mapStoreErr(err)
	}
	return &cellarv1.NodeCertificateStatusResponse{
		Node: &cellarv1.NodeRecord{
			NodeId:             n.ID,
			Role:               string(n.Role),
			Membership:         string(n.Membership),
			PubKeyFingerprint:  n.PubKeyFingerprint,
			IssuedAtUnixNano:   n.IssuedAt.UnixNano(),
			ExpiresAtUnixNano:  n.ExpiresAt.UnixNano(),
			Certificate:        []byte(n.CertificatePEM),
		},
	}, nil
}

func (s *CAServer) Join(ctx context.Context, req *cellarv1.RaftJoinRequest) (*cellarv1.RaftJoinResponse, error) {
	if err := requireManagerPeer(ctx); err != nil {
		return nil, err
	}
	if s.raft == nil {
		return nil, status.Error(codes.Unimplemented, "raft is not enabled")
	}
	if err := s.requireLeader(); err != nil {
		return nil, err
	}
	if req.NodeId == "" || req.RaftAddr == "" {
		return nil, status.Error(codes.InvalidArgument, "node_id and raft_addr are required")
	}
	if err := s.raft.AddVoter(ctx, raftstore.PeerInfo{
		NodeID:   req.NodeId,
		RaftAddr: req.RaftAddr,
		GRPCAddr: req.GrpcAddr,
	}); err != nil {
		return nil, mapStoreErr(err)
	}
	return &cellarv1.RaftJoinResponse{}, nil
}

func (s *CAServer) Leave(ctx context.Context, req *cellarv1.RaftLeaveRequest) (*cellarv1.RaftLeaveResponse, error) {
	if err := requireManagerPeer(ctx); err != nil {
		return nil, err
	}
	if s.raft == nil {
		return nil, status.Error(codes.Unimplemented, "raft is not enabled")
	}
	if err := s.requireLeader(); err != nil {
		return nil, err
	}
	if req.NodeId == "" {
		return nil, status.Error(codes.InvalidArgument, "node_id is required")
	}
	if err := s.raft.RemoveServer(req.NodeId); err != nil {
		return nil, mapStoreErr(err)
	}
	return &cellarv1.RaftLeaveResponse{}, nil
}

func RegisterRemote(s *grpc.Server, ca *CAServer, sb *SandboxServer) {
	cellarv1.RegisterCAServer(s, ca)
	cellarv1.RegisterNodeCAServer(s, ca)
	cellarv1.RegisterRaftMembershipServer(s, ca)
	if sb != nil {
		RegisterSandboxServices(s, sb)
	}
}

func mapStoreErr(err error) error {
	switch {
	case errors.Is(err, store.ErrNotLeader):
		return status.Error(codes.Unavailable, err.Error())
	case errors.Is(err, store.ErrAlreadyInitialized):
		return status.Error(codes.AlreadyExists, err.Error())
	case errors.Is(err, store.ErrNotInitialized):
		return status.Error(codes.FailedPrecondition, err.Error())
	case errors.Is(err, store.ErrNodeNotFound):
		return status.Error(codes.NotFound, err.Error())
	case errors.Is(err, store.ErrSandboxNotFound):
		return status.Error(codes.NotFound, err.Error())
	case errors.Is(err, store.ErrAPIKeyNotFound):
		return status.Error(codes.NotFound, err.Error())
	default:
		return status.Error(codes.Internal, err.Error())
	}
}

// MapStoreErr maps store errors to gRPC status errors.
func MapStoreErr(err error) error {
	return mapStoreErr(err)
}

func peerCertificate(ctx context.Context) *x509.Certificate {
	p, ok := peer.FromContext(ctx)
	if !ok || p.AuthInfo == nil {
		return nil
	}
	tlsInfo, ok := p.AuthInfo.(credentials.TLSInfo)
	if !ok || len(tlsInfo.State.PeerCertificates) == 0 {
		return nil
	}
	return tlsInfo.State.PeerCertificates[0]
}

func requireManagerPeer(ctx context.Context) error {
	cert := peerCertificate(ctx)
	if cert == nil {
		return status.Error(codes.Unauthenticated, "manager client certificate required")
	}
	role, err := node.RoleFromCertificate(cert)
	if err != nil {
		return status.Error(codes.Unauthenticated, "invalid client certificate")
	}
	if !role.CanAccessControlPlane() {
		return status.Error(codes.PermissionDenied, "workers cannot access raft membership")
	}
	return nil
}

// InsecureTLSConfig returns a TLS config that skips verify (bootstrap root download).
func InsecureTLSConfig() *tls.Config {
	return &tls.Config{
		InsecureSkipVerify: true, //nolint:gosec // digest-pinned bootstrap
		MinVersion:         tls.VersionTLS12,
	}
}

// ServerTLSFromPEMs builds a server TLS config from PEM material.
func ServerTLSFromPEMs(certPEM, keyPEM, caPEM []byte) (*tls.Config, error) {
	cert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		return nil, err
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(caPEM) {
		return nil, fmt.Errorf("failed to parse CA certificate")
	}
	return &tls.Config{
		Certificates: []tls.Certificate{cert},
		ClientCAs:    pool,
		ClientAuth:   tls.VerifyClientCertIfGiven,
		MinVersion:   tls.VersionTLS12,
	}, nil
}

// ClientTLSFromPEMs builds a client TLS config verifying the server against caPEM.
func ClientTLSFromPEMs(certPEM, keyPEM, caPEM []byte, serverName string) (*tls.Config, error) {
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(caPEM) {
		return nil, fmt.Errorf("failed to parse CA certificate")
	}
	cfg := &tls.Config{
		RootCAs:    pool,
		ServerName: serverName,
		MinVersion: tls.VersionTLS12,
	}
	if len(certPEM) > 0 && len(keyPEM) > 0 {
		cert, err := tls.X509KeyPair(certPEM, keyPEM)
		if err != nil {
			return nil, err
		}
		cfg.Certificates = []tls.Certificate{cert}
	}
	return cfg, nil
}
