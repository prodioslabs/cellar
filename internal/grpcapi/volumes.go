package grpcapi

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	cellarv1 "github.com/prodioslabs/cellar/api/gen"
	"github.com/prodioslabs/cellar/internal/sandbox"
	"github.com/prodioslabs/cellar/internal/scheduler"
	"github.com/prodioslabs/cellar/internal/store"
)

func (s *SandboxServer) CreateVolume(ctx context.Context, req *cellarv1.VolumeCreateRequest) (*cellarv1.VolumeCreateResponse, error) {
	if err := requireClusterPeer(ctx); err != nil {
		return nil, err
	}
	if err := s.requireLeader(); err != nil {
		return nil, err
	}
	name := req.Name
	if err := sandbox.ValidateVolumeCreate(name, req.CapacityGib); err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	if existing, err := s.store.GetVolumeByName(ctx, name); err == nil && existing != nil {
		return nil, status.Error(codes.AlreadyExists, store.ErrNameExists.Error())
	} else if err != nil && !errors.Is(err, store.ErrVolumeNotFound) {
		return nil, mapStoreErr(err)
	}

	id, err := sandbox.NewID()
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
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
	if nodeID == "" {
		return nil, status.Error(codes.FailedPrecondition, "no schedulable node for volume")
	}

	v := &sandbox.Volume{
		ID:          id,
		Name:        name,
		Kind:        sandbox.VolumeKindNamed,
		Status:      sandbox.VolumeStatusReady,
		NodeID:      nodeID,
		CapacityGiB: req.CapacityGib,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	if len(req.LabelsJson) > 0 {
		_ = json.Unmarshal(req.LabelsJson, &v.Labels)
	}
	if req.CapacityGib != nil {
		b := uint64(*req.CapacityGib) * 1024 * 1024 * 1024
		v.CapacityBytes = &b
	}
	if err := s.store.SaveVolume(ctx, v); err != nil {
		return nil, mapStoreErr(err)
	}
	// Best-effort ensure on owning node (async path: runtime agent can also ensure on mount).
	_ = s.pushEnsureVolume(ctx, v)
	return &cellarv1.VolumeCreateResponse{Volume: sandbox.VolumeToProto(v)}, nil
}

func (s *SandboxServer) ListVolumes(ctx context.Context, _ *cellarv1.VolumeListRequest) (*cellarv1.VolumeListResponse, error) {
	if err := requireClusterPeer(ctx); err != nil {
		return nil, err
	}
	list, err := s.store.ListVolumes(ctx)
	if err != nil {
		return nil, mapStoreErr(err)
	}
	out := &cellarv1.VolumeListResponse{}
	for _, v := range list {
		out.Volumes = append(out.Volumes, sandbox.VolumeToProto(v))
	}
	return out, nil
}

func (s *SandboxServer) GetVolume(ctx context.Context, req *cellarv1.VolumeGetRequest) (*cellarv1.VolumeGetResponse, error) {
	if err := requireClusterPeer(ctx); err != nil {
		return nil, err
	}
	v, err := s.store.GetVolume(ctx, req.VolumeId)
	if err != nil {
		return nil, mapStoreErr(err)
	}
	return &cellarv1.VolumeGetResponse{Volume: sandbox.VolumeToProto(v)}, nil
}

func (s *SandboxServer) GetDefaultVolume(ctx context.Context, _ *cellarv1.VolumeGetDefaultRequest) (*cellarv1.VolumeGetResponse, error) {
	if err := requireClusterPeer(ctx); err != nil {
		return nil, err
	}
	v, err := s.store.GetDefaultVolume(ctx)
	if err == nil {
		return &cellarv1.VolumeGetResponse{Volume: sandbox.VolumeToProto(v)}, nil
	}
	if !errors.Is(err, store.ErrVolumeNotFound) {
		return nil, mapStoreErr(err)
	}
	if err := s.requireLeader(); err != nil {
		return nil, err
	}
	id, err := sandbox.NewID()
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
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
	v = &sandbox.Volume{
		ID:        id,
		Kind:      sandbox.VolumeKindDefault,
		Status:    sandbox.VolumeStatusReady,
		NodeID:    nodeID,
		Name:      "default",
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := s.store.SaveVolume(ctx, v); err != nil {
		return nil, mapStoreErr(err)
	}
	_ = s.pushEnsureVolume(ctx, v)
	return &cellarv1.VolumeGetResponse{Volume: sandbox.VolumeToProto(v)}, nil
}

func (s *SandboxServer) DeleteVolume(ctx context.Context, req *cellarv1.VolumeDeleteRequest) (*cellarv1.VolumeDeleteResponse, error) {
	if err := requireClusterPeer(ctx); err != nil {
		return nil, err
	}
	if err := s.requireLeader(); err != nil {
		return nil, err
	}
	v, err := s.store.GetVolume(ctx, req.VolumeId)
	if err != nil {
		return nil, mapStoreErr(err)
	}
	_ = s.pushDeleteVolume(ctx, v)
	if err := s.store.DeleteVolume(ctx, req.VolumeId); err != nil {
		return nil, mapStoreErr(err)
	}
	return &cellarv1.VolumeDeleteResponse{Message: "volume deleted"}, nil
}

func (s *SandboxServer) pushEnsureVolume(ctx context.Context, v *sandbox.Volume) error {
	return nil // filled by daemon host when IdentityProvider supports runtime dial; gateway dials runtime directly for FS
}

func (s *SandboxServer) pushDeleteVolume(ctx context.Context, v *sandbox.Volume) error {
	return nil
}

// Volume helpers for SandboxAPIServer forwarding.
func (s *SandboxAPIServer) CreateVolume(ctx context.Context, req *cellarv1.VolumeCreateRequest) (*cellarv1.VolumeCreateResponse, error) {
	if s.raft != nil && s.raft.IsLeader() && s.control != nil {
		return s.control.CreateVolume(WithInternalCall(ctx), req)
	}
	conn, err := s.dialLeader(ctx)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	return cellarv1.NewSandboxControlClient(conn).CreateVolume(ctx, req)
}

func (s *SandboxAPIServer) ListVolumes(ctx context.Context, req *cellarv1.VolumeListRequest) (*cellarv1.VolumeListResponse, error) {
	if s.raft != nil && s.raft.IsLeader() && s.control != nil {
		return s.control.ListVolumes(WithInternalCall(ctx), req)
	}
	conn, err := s.dialLeader(ctx)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	return cellarv1.NewSandboxControlClient(conn).ListVolumes(ctx, req)
}

func (s *SandboxAPIServer) GetVolume(ctx context.Context, req *cellarv1.VolumeGetRequest) (*cellarv1.VolumeGetResponse, error) {
	if s.raft != nil && s.raft.IsLeader() && s.control != nil {
		return s.control.GetVolume(WithInternalCall(ctx), req)
	}
	conn, err := s.dialLeader(ctx)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	return cellarv1.NewSandboxControlClient(conn).GetVolume(ctx, req)
}

func (s *SandboxAPIServer) GetDefaultVolume(ctx context.Context, req *cellarv1.VolumeGetDefaultRequest) (*cellarv1.VolumeGetResponse, error) {
	if s.raft != nil && s.raft.IsLeader() && s.control != nil {
		return s.control.GetDefaultVolume(WithInternalCall(ctx), req)
	}
	conn, err := s.dialLeader(ctx)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	return cellarv1.NewSandboxControlClient(conn).GetDefaultVolume(ctx, req)
}

func (s *SandboxAPIServer) DeleteVolume(ctx context.Context, req *cellarv1.VolumeDeleteRequest) (*cellarv1.VolumeDeleteResponse, error) {
	if s.raft != nil && s.raft.IsLeader() && s.control != nil {
		return s.control.DeleteVolume(WithInternalCall(ctx), req)
	}
	conn, err := s.dialLeader(ctx)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	return cellarv1.NewSandboxControlClient(conn).DeleteVolume(ctx, req)
}
