package daemon

import (
	"context"
	"errors"
	"strings"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	cellarv1 "github.com/prodioslabs/cellar/api/gen"
	"github.com/prodioslabs/cellar/internal/node"
	"github.com/prodioslabs/cellar/internal/raftstore"
	"github.com/prodioslabs/cellar/internal/scheduler"
	"github.com/prodioslabs/cellar/internal/store"
)

func (d *Daemon) NodeList(ctx context.Context, _ *cellarv1.NodeListRequest) (*cellarv1.NodeListResponse, error) {
	raft, err := d.managerRaft()
	if err != nil {
		return nil, err
	}
	nodes, err := raft.ListNodes(ctx)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	leaderID := raft.LeaderID()
	peers := peerIDSet(raft)
	now := time.Now().UTC()
	out := &cellarv1.NodeListResponse{}
	for _, n := range nodes {
		out.Nodes = append(out.Nodes, nodeToInfo(n, leaderID, peers, now))
	}
	return out, nil
}

func (d *Daemon) NodeInspect(ctx context.Context, req *cellarv1.NodeInspectRequest) (*cellarv1.NodeInspectResponse, error) {
	raft, err := d.managerRaft()
	if err != nil {
		return nil, err
	}
	if req.NodeId == "" {
		return nil, status.Error(codes.InvalidArgument, "node_id is required")
	}
	n, err := resolveNode(ctx, raft, req.NodeId)
	if err != nil {
		return nil, err
	}
	return &cellarv1.NodeInspectResponse{
		Node: nodeToInfo(n, raft.LeaderID(), peerIDSet(raft), time.Now().UTC()),
	}, nil
}

func (d *Daemon) NodePromote(ctx context.Context, req *cellarv1.NodePromoteRequest) (*cellarv1.NodePromoteResponse, error) {
	raft, err := d.managerLeader()
	if err != nil {
		return nil, err
	}
	if req.NodeId == "" {
		return nil, status.Error(codes.InvalidArgument, "node_id is required")
	}
	n, err := resolveNode(ctx, raft, req.NodeId)
	if err != nil {
		return nil, err
	}
	if n.Role == node.RoleManager {
		return nil, status.Errorf(codes.FailedPrecondition, "node %s is already a manager", shortID(n.ID))
	}
	n.Role = node.RoleManager
	if err := raft.SaveNode(ctx, n); err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	if d.isLocalNode(n.ID) {
		if err := d.applyRoleChange(ctx, node.RoleManager); err != nil {
			return nil, status.Errorf(codes.Internal, "apply promote locally: %v", err)
		}
	}
	return &cellarv1.NodePromoteResponse{}, nil
}

func (d *Daemon) NodeDemote(ctx context.Context, req *cellarv1.NodeDemoteRequest) (*cellarv1.NodeDemoteResponse, error) {
	raft, err := d.managerLeader()
	if err != nil {
		return nil, err
	}
	if req.NodeId == "" {
		return nil, status.Error(codes.InvalidArgument, "node_id is required")
	}
	n, err := resolveNode(ctx, raft, req.NodeId)
	if err != nil {
		return nil, err
	}
	if n.Role != node.RoleManager {
		return nil, status.Errorf(codes.FailedPrecondition, "node %s is not a manager", shortID(n.ID))
	}
	managers, err := countManagers(ctx, raft)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	if managers <= 1 {
		return nil, status.Error(codes.FailedPrecondition, "cannot demote the last manager")
	}
	if raft.NumVoters() <= 1 {
		return nil, status.Error(codes.FailedPrecondition, "cannot demote the sole raft voter")
	}

	if d.isLocalNode(n.ID) && raft.IsLeader() {
		if err := raft.LeadershipTransfer(); err != nil {
			return nil, status.Errorf(codes.Unavailable, "transfer leadership before demote: %v", err)
		}
		return nil, status.Error(codes.Unavailable, "leadership transferred; re-run node demote on the new leader")
	}

	if raft.IsVoter(n.ID) {
		if err := raft.RemoveServer(n.ID); err != nil {
			return nil, status.Errorf(codes.Internal, "remove raft voter: %v", err)
		}
	}
	if err := raft.DeletePeer(ctx, n.ID); err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	n.Role = node.RoleWorker
	if err := raft.SaveNode(ctx, n); err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	if d.isLocalNode(n.ID) {
		if err := d.applyRoleChange(ctx, node.RoleWorker); err != nil {
			return nil, status.Errorf(codes.Internal, "apply demote locally: %v", err)
		}
	}
	return &cellarv1.NodeDemoteResponse{}, nil
}

func (d *Daemon) NodeRemove(ctx context.Context, req *cellarv1.NodeRemoveRequest) (*cellarv1.NodeRemoveResponse, error) {
	raft, err := d.managerLeader()
	if err != nil {
		return nil, err
	}
	if req.NodeId == "" {
		return nil, status.Error(codes.InvalidArgument, "node_id is required")
	}
	n, err := resolveNode(ctx, raft, req.NodeId)
	if err != nil {
		return nil, err
	}
	if d.isLocalNode(n.ID) {
		return nil, status.Error(codes.FailedPrecondition, "cannot remove this node; use cellar leave")
	}
	now := time.Now().UTC()
	if scheduler.IsNodeLive(n, now) && !req.Force {
		return nil, status.Error(codes.FailedPrecondition, "node appears live; pass --force to remove")
	}
	if n.Role == node.RoleManager {
		managers, err := countManagers(ctx, raft)
		if err != nil {
			return nil, status.Error(codes.Internal, err.Error())
		}
		if managers <= 1 {
			return nil, status.Error(codes.FailedPrecondition, "cannot remove the last manager")
		}
		if raft.IsVoter(n.ID) {
			if err := raft.RemoveServer(n.ID); err != nil {
				return nil, status.Errorf(codes.Internal, "remove raft voter: %v", err)
			}
		}
		if err := raft.DeletePeer(ctx, n.ID); err != nil {
			return nil, status.Error(codes.Internal, err.Error())
		}
	}
	if err := raft.DeleteNode(ctx, n.ID); err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	return &cellarv1.NodeRemoveResponse{}, nil
}

func (d *Daemon) NodeUpdate(ctx context.Context, req *cellarv1.NodeUpdateRequest) (*cellarv1.NodeUpdateResponse, error) {
	raft, err := d.managerLeader()
	if err != nil {
		return nil, err
	}
	if req.NodeId == "" {
		return nil, status.Error(codes.InvalidArgument, "node_id is required")
	}
	n, err := resolveNode(ctx, raft, req.NodeId)
	if err != nil {
		return nil, err
	}

	if req.Role != "" {
		role := node.Role(req.Role)
		switch role {
		case node.RoleManager:
			if n.Role != node.RoleManager {
				if _, err := d.NodePromote(ctx, &cellarv1.NodePromoteRequest{NodeId: n.ID}); err != nil {
					return nil, err
				}
			}
		case node.RoleWorker:
			if n.Role != node.RoleWorker {
				if _, err := d.NodeDemote(ctx, &cellarv1.NodeDemoteRequest{NodeId: n.ID}); err != nil {
					return nil, err
				}
			}
		default:
			return nil, status.Error(codes.InvalidArgument, "role must be worker or manager")
		}
		n, err = raft.GetNode(ctx, n.ID)
		if err != nil {
			return nil, mapNodeErr(err)
		}
	}

	changed := false
	if req.Availability != "" {
		av, err := node.ParseAvailability(req.Availability)
		if err != nil {
			return nil, status.Error(codes.InvalidArgument, err.Error())
		}
		if n.Availability != av {
			n.Availability = av
			changed = true
		}
	}
	if len(req.LabelAdd) > 0 || len(req.LabelRm) > 0 {
		labels := node.CloneLabels(n.Labels)
		if labels == nil {
			labels = map[string]string{}
		}
		for k, v := range req.LabelAdd {
			labels[k] = v
		}
		for _, k := range req.LabelRm {
			delete(labels, k)
		}
		if len(labels) == 0 {
			labels = nil
		}
		n.Labels = labels
		changed = true
	}
	if changed {
		if err := raft.SaveNode(ctx, n); err != nil {
			return nil, status.Error(codes.Internal, err.Error())
		}
	}
	return &cellarv1.NodeUpdateResponse{
		Node: nodeToInfo(n, raft.LeaderID(), peerIDSet(raft), time.Now().UTC()),
	}, nil
}

func (d *Daemon) managerRaft() (*raftstore.Store, error) {
	d.mu.Lock()
	raft := d.raft
	d.mu.Unlock()
	if raft == nil {
		return nil, status.Error(codes.FailedPrecondition, "node management requires a manager node")
	}
	return raft, nil
}

func (d *Daemon) managerLeader() (*raftstore.Store, error) {
	raft, err := d.managerRaft()
	if err != nil {
		return nil, err
	}
	if !raft.IsLeader() {
		return nil, status.Error(codes.Unavailable, "not the raft leader; run this command on the leader")
	}
	return raft, nil
}

func (d *Daemon) isLocalNode(nodeID string) bool {
	mat := d.idStore.Material()
	return mat != nil && mat.NodeID == nodeID
}

func resolveNode(ctx context.Context, raft *raftstore.Store, idOrPrefix string) (*node.Node, error) {
	if n, err := raft.GetNode(ctx, idOrPrefix); err == nil {
		return n, nil
	} else if !errors.Is(err, store.ErrNodeNotFound) {
		return nil, status.Error(codes.Internal, err.Error())
	}
	nodes, err := raft.ListNodes(ctx)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	var matches []*node.Node
	for _, n := range nodes {
		if strings.HasPrefix(n.ID, idOrPrefix) {
			matches = append(matches, n)
		}
	}
	switch len(matches) {
	case 0:
		return nil, status.Errorf(codes.NotFound, "node %q not found", idOrPrefix)
	case 1:
		return matches[0], nil
	default:
		return nil, status.Errorf(codes.FailedPrecondition, "node id prefix %q is ambiguous (%d matches)", idOrPrefix, len(matches))
	}
}

func countManagers(ctx context.Context, raft *raftstore.Store) (int, error) {
	nodes, err := raft.ListNodes(ctx)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, nd := range nodes {
		if nd.Role == node.RoleManager {
			n++
		}
	}
	return n, nil
}

func peerIDSet(raft *raftstore.Store) map[string]struct{} {
	peers := raft.ListPeers()
	out := make(map[string]struct{}, len(peers))
	for _, p := range peers {
		out[p.NodeID] = struct{}{}
	}
	return out
}

func nodeToInfo(n *node.Node, leaderID string, peers map[string]struct{}, now time.Time) *cellarv1.NodeInfo {
	if n == nil {
		return nil
	}
	info := &cellarv1.NodeInfo{
		NodeId:              n.ID,
		Role:                string(n.Role),
		Membership:          string(n.Membership),
		Availability:        string(n.Availability.Effective()),
		Labels:              node.CloneLabels(n.Labels),
		RuntimeGrpcAddr:     n.RuntimeGRPCAddr,
		RuntimeSandboxCount: int32(n.RuntimeSandboxCount),
		PubKeyFingerprint:   n.PubKeyFingerprint,
		IssuedAtUnixNano:    n.IssuedAt.UnixNano(),
		ExpiresAtUnixNano:   n.ExpiresAt.UnixNano(),
	}
	if !n.RuntimeHeartbeatAt.IsZero() {
		info.RuntimeHeartbeatUnixNano = n.RuntimeHeartbeatAt.UnixNano()
	}
	if scheduler.IsNodeLive(n, now) {
		info.Status = string(node.StatusReady)
	} else {
		info.Status = string(node.StatusDown)
	}
	if n.Role == node.RoleManager {
		switch {
		case n.ID == leaderID:
			info.ManagerStatus = "leader"
		case peers != nil:
			if _, ok := peers[n.ID]; ok {
				info.ManagerStatus = "reachable"
			} else {
				info.ManagerStatus = "unreachable"
			}
		}
	}
	return info
}

func mapNodeErr(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, store.ErrNodeNotFound) {
		return status.Error(codes.NotFound, err.Error())
	}
	return status.Error(codes.Internal, err.Error())
}

func shortID(id string) string {
	if len(id) > 12 {
		return id[:12]
	}
	return id
}
