package grpcapi

import (
	"context"
	"errors"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	cellarv1 "github.com/prodioslabs/cellar/api/gen"
	"github.com/prodioslabs/cellar/internal/node"
	"github.com/prodioslabs/cellar/internal/sandbox"
	"github.com/prodioslabs/cellar/internal/scheduler"
	"github.com/prodioslabs/cellar/internal/store"
)

// SandboxServer implements SandboxControl and RuntimeAgent on managers.
type SandboxServer struct {
	cellarv1.UnimplementedSandboxControlServer
	cellarv1.UnimplementedRuntimeAgentServer

	store      store.Store
	raft       RaftAdmin
	host       IdentityProvider
	quarantine *scheduler.Quarantine
}

func NewSandboxServer(s store.Store, raft RaftAdmin, host IdentityProvider) *SandboxServer {
	return &SandboxServer{store: s, raft: raft, host: host, quarantine: scheduler.NewQuarantine()}
}

// Quarantine returns the post-eviction placement quarantine tracker.
func (s *SandboxServer) Quarantine() *scheduler.Quarantine {
	if s == nil {
		return nil
	}
	return s.quarantine
}

func (s *SandboxServer) requireLeader() error {
	if s.raft != nil && !s.raft.IsLeader() {
		return status.Error(codes.Unavailable, store.ErrNotLeader.Error())
	}
	return nil
}

type internalCallKey struct{}

// WithInternalCall marks ctx as an in-process control-plane call (skips peer cert check).
func WithInternalCall(ctx context.Context) context.Context {
	return context.WithValue(ctx, internalCallKey{}, true)
}

func requireClusterPeer(ctx context.Context) error {
	if ctx.Value(internalCallKey{}) != nil {
		return nil
	}
	if peerCertificate(ctx) == nil {
		return status.Error(codes.Unauthenticated, "cluster client certificate required")
	}
	return nil
}

func (s *SandboxServer) Create(ctx context.Context, req *cellarv1.SandboxCreateRequest) (*cellarv1.SandboxCreateResponse, error) {
	if err := requireClusterPeer(ctx); err != nil {
		return nil, err
	}
	if err := s.requireLeader(); err != nil {
		return nil, err
	}
	spec := sandbox.SpecFromProto(req.Spec)
	var netProto *cellarv1.NetworkPolicy
	if req.Spec != nil {
		netProto = req.Spec.Network
	}
	np, err := sandbox.ResolveNetworkPolicyFromProto(netProto)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	spec.Network = np
	if err := sandbox.ValidateSpec(spec); err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	id := req.SandboxId
	if id == "" {
		var err error
		id, err = sandbox.NewID()
		if err != nil {
			return nil, status.Error(codes.Internal, err.Error())
		}
	}
	now := time.Now().UTC()
	nodes, err := s.store.ListNodes(ctx)
	if err != nil {
		return nil, mapStoreErr(err)
	}
	all, err := s.store.ListSandboxes(ctx)
	if err != nil {
		return nil, mapStoreErr(err)
	}
	nodeID := scheduler.SelectNodeOpts(nodes, all, now, scheduler.SelectOpts{
		ExcludeNodeIDs: s.quarantine.Excluded(),
	})
	sb := &sandbox.Sandbox{
		ID:                   id,
		Spec:                 spec,
		NodeID:               nodeID,
		DesiredState:         sandbox.DesiredRunning,
		AssignmentGeneration: 1,
		Status: sandbox.Status{
			Phase:     sandbox.PhasePending,
			UpdatedAt: now,
		},
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := s.store.SaveSandbox(ctx, sb); err != nil {
		return nil, mapStoreErr(err)
	}
	return &cellarv1.SandboxCreateResponse{Sandbox: sandbox.ToProto(sb)}, nil
}

func (s *SandboxServer) Stop(ctx context.Context, req *cellarv1.SandboxStopRequest) (*cellarv1.SandboxStopResponse, error) {
	if err := requireClusterPeer(ctx); err != nil {
		return nil, err
	}
	if err := s.requireLeader(); err != nil {
		return nil, err
	}
	sb, err := s.store.GetSandbox(ctx, req.SandboxId)
	if err != nil {
		return nil, mapStoreErr(err)
	}
	sb.DesiredState = sandbox.DesiredStopped
	sb.UpdatedAt = time.Now().UTC()
	if err := s.store.SaveSandbox(ctx, sb); err != nil {
		return nil, mapStoreErr(err)
	}
	return &cellarv1.SandboxStopResponse{Sandbox: sandbox.ToProto(sb)}, nil
}

func (s *SandboxServer) Remove(ctx context.Context, req *cellarv1.SandboxRemoveRequest) (*cellarv1.SandboxRemoveResponse, error) {
	if err := requireClusterPeer(ctx); err != nil {
		return nil, err
	}
	if err := s.requireLeader(); err != nil {
		return nil, err
	}
	if _, err := s.store.GetSandbox(ctx, req.SandboxId); err != nil {
		return nil, mapStoreErr(err)
	}
	// Delete from Raft; owning agent tears down when the assignment disappears.
	if err := s.store.DeleteSandbox(ctx, req.SandboxId); err != nil {
		return nil, mapStoreErr(err)
	}
	return &cellarv1.SandboxRemoveResponse{}, nil
}

func (s *SandboxServer) Get(ctx context.Context, req *cellarv1.SandboxGetRequest) (*cellarv1.SandboxGetResponse, error) {
	if err := requireClusterPeer(ctx); err != nil {
		return nil, err
	}
	sb, err := s.store.GetSandbox(ctx, req.SandboxId)
	if err != nil {
		return nil, mapStoreErr(err)
	}
	return &cellarv1.SandboxGetResponse{Sandbox: sandbox.ToProto(sb)}, nil
}

func (s *SandboxServer) List(ctx context.Context, _ *cellarv1.SandboxListRequest) (*cellarv1.SandboxListResponse, error) {
	if err := requireClusterPeer(ctx); err != nil {
		return nil, err
	}
	list, err := s.store.ListSandboxes(ctx)
	if err != nil {
		return nil, mapStoreErr(err)
	}
	out := &cellarv1.SandboxListResponse{}
	for _, sb := range list {
		out.Sandboxes = append(out.Sandboxes, sandbox.ToProto(sb))
	}
	return out, nil
}

// UpdateNetwork replaces the network policy of an existing sandbox. The write
// is the source of truth; pushing it to the owning node is the caller's job.
func (s *SandboxServer) UpdateNetwork(ctx context.Context, req *cellarv1.SandboxUpdateNetworkRequest) (*cellarv1.SandboxUpdateNetworkResponse, error) {
	if err := requireClusterPeer(ctx); err != nil {
		return nil, err
	}
	if err := s.requireLeader(); err != nil {
		return nil, err
	}
	if req.SandboxId == "" {
		return nil, status.Error(codes.InvalidArgument, "sandbox_id required")
	}
	np, err := sandbox.ResolveNetworkPolicyFromProto(req.Network)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	sb, err := s.store.GetSandbox(ctx, req.SandboxId)
	if err != nil {
		return nil, mapStoreErr(err)
	}
	// Mode none is decided when the container is created: no bridge, no
	// resolv.conf mount, no REDIRECT rules. Neither direction can be toggled
	// on a live container. blockall keeps topology, so it may change live.
	if (sb.Spec.Network.Mode == sandbox.NetworkNone) != (np.Mode == sandbox.NetworkNone) {
		return nil, status.Errorf(codes.FailedPrecondition,
			"cannot change network mode %q -> %q on a running sandbox; recreate it instead",
			sb.Spec.Network.Mode, np.Mode)
	}
	sb.Spec.Network = np
	sb.UpdatedAt = time.Now().UTC()
	if err := s.store.SaveSandbox(ctx, sb); err != nil {
		return nil, mapStoreErr(err)
	}
	return &cellarv1.SandboxUpdateNetworkResponse{Sandbox: sandbox.ToProto(sb)}, nil
}

func (s *SandboxServer) Heartbeat(ctx context.Context, req *cellarv1.RuntimeHeartbeatRequest) (*cellarv1.RuntimeHeartbeatResponse, error) {
	if s.raft != nil && !s.raft.IsLeader() {
		return s.forwardHeartbeat(ctx, req)
	}
	if req.NodeId == "" {
		return nil, status.Error(codes.InvalidArgument, "node_id required")
	}
	if err := requireNodeOrManagerPeer(peerCertificate(ctx), req.NodeId); err != nil {
		return nil, err
	}

	n, err := s.store.GetNode(ctx, req.NodeId)
	if err != nil {
		if errors.Is(err, store.ErrNodeNotFound) {
			return &cellarv1.RuntimeHeartbeatResponse{Removed: true}, nil
		}
		return nil, mapStoreErr(err)
	}
	n.RuntimeGRPCAddr = req.GrpcAddr
	n.RuntimeHeartbeatAt = time.Now().UTC()
	n.RuntimeSandboxCount = int(req.SandboxCount)
	if err := s.store.SaveNode(ctx, n); err != nil {
		return nil, mapStoreErr(err)
	}
	s.quarantine.NoteHeartbeat(req.NodeId)
	assigned, err := s.store.ListSandboxesByNode(ctx, req.NodeId)
	if err != nil {
		return nil, mapStoreErr(err)
	}
	resp := &cellarv1.RuntimeHeartbeatResponse{
		DesiredRole: string(n.Role),
	}
	for _, sb := range assigned {
		resp.Assigned = append(resp.Assigned, sandbox.ToProto(sb))
	}
	s.attachManagerEndpoints(resp)
	return resp, nil
}

func (s *SandboxServer) attachManagerEndpoints(resp *cellarv1.RuntimeHeartbeatResponse) {
	leader, addrs := managerEndpoints(s.raft)
	resp.LeaderGrpc = leader
	resp.ManagerAddrs = addrs
}

func (s *SandboxServer) forwardHeartbeat(ctx context.Context, req *cellarv1.RuntimeHeartbeatRequest) (*cellarv1.RuntimeHeartbeatResponse, error) {
	conn, err := s.dialLeader(ctx)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	return cellarv1.NewRuntimeAgentClient(conn).Heartbeat(ctx, req)
}

func (s *SandboxServer) dialLeader(ctx context.Context) (*grpc.ClientConn, error) {
	_ = ctx
	addr := ""
	if s.raft != nil {
		addr = s.raft.LeaderGRPC()
		if addr == "" {
			addr = s.raft.GRPCAdvertise()
		}
	}
	if addr == "" {
		return nil, status.Error(codes.Unavailable, "no raft leader")
	}
	if s.host == nil {
		return nil, status.Error(codes.FailedPrecondition, "no identity for leader forward")
	}
	cert, key, caPEM, err := s.host.IdentityPEMs()
	if err != nil {
		return nil, status.Error(codes.FailedPrecondition, err.Error())
	}
	return DialMTLS(addr, cert, key, caPEM)
}

func (s *SandboxServer) UpdateSandboxStatus(ctx context.Context, req *cellarv1.UpdateSandboxStatusRequest) (*cellarv1.UpdateSandboxStatusResponse, error) {
	if s.raft != nil && !s.raft.IsLeader() {
		return s.forwardUpdateSandboxStatus(ctx, req)
	}
	cert := peerCertificate(ctx)
	if cert == nil {
		return nil, status.Error(codes.Unauthenticated, "client certificate required")
	}
	sb, err := s.store.GetSandbox(ctx, req.SandboxId)
	if err != nil {
		return nil, mapStoreErr(err)
	}
	if sb.NodeID != "" {
		if err := requireNodeOrManagerPeer(cert, sb.NodeID); err != nil {
			return nil, err
		}
	}
	// Fencing: reject status from a stale assignment after reschedule. Generation 0
	// on the store means legacy records; any non-zero store generation must match.
	// Exec/logs are fenced by resolving the current Raft node_id (see daemon proxy).
	if err := sandbox.CheckAssignmentGeneration(sb.AssignmentGeneration, req.AssignmentGeneration); err != nil {
		return nil, status.Error(codes.FailedPrecondition, err.Error())
	}
	st := sandbox.StatusFromProto(req.Status)
	if req.ContainerId != "" {
		st.ContainerID = req.ContainerId
	}
	st.UpdatedAt = time.Now().UTC()
	sb.Status = st
	sb.UpdatedAt = st.UpdatedAt
	if err := s.store.SaveSandbox(ctx, sb); err != nil {
		return nil, mapStoreErr(err)
	}
	return &cellarv1.UpdateSandboxStatusResponse{}, nil
}

func (s *SandboxServer) forwardUpdateSandboxStatus(ctx context.Context, req *cellarv1.UpdateSandboxStatusRequest) (*cellarv1.UpdateSandboxStatusResponse, error) {
	conn, err := s.dialLeader(ctx)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	return cellarv1.NewRuntimeAgentClient(conn).UpdateSandboxStatus(ctx, req)
}

func (s *SandboxServer) ListNodeSandboxes(ctx context.Context, req *cellarv1.ListNodeSandboxesRequest) (*cellarv1.ListNodeSandboxesResponse, error) {
	cert := peerCertificate(ctx)
	if cert == nil {
		return nil, status.Error(codes.Unauthenticated, "client certificate required")
	}
	nodeID := req.NodeId
	if nodeID == "" {
		nodeID = cert.Subject.CommonName
	}
	if nodeID != cert.Subject.CommonName {
		role, err := node.RoleFromCertificate(cert)
		if err != nil || !role.CanAccessControlPlane() {
			return nil, status.Error(codes.PermissionDenied, "cannot list other node sandboxes")
		}
	}
	list, err := s.store.ListSandboxesByNode(ctx, nodeID)
	if err != nil {
		return nil, mapStoreErr(err)
	}
	resp := &cellarv1.ListNodeSandboxesResponse{}
	for _, sb := range list {
		resp.Sandboxes = append(resp.Sandboxes, sandbox.ToProto(sb))
	}
	return resp, nil
}

// RegisterSandboxServices registers SandboxControl + RuntimeAgent.
func RegisterSandboxServices(s *grpc.Server, sb *SandboxServer) {
	cellarv1.RegisterSandboxControlServer(s, sb)
	cellarv1.RegisterRuntimeAgentServer(s, sb)
}