package daemon

import (
	"context"
	"fmt"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	cellarv1 "github.com/prodioslabs/cellar/api/gen"
	"github.com/prodioslabs/cellar/internal/grpcapi"
)

// IdentityPEMs implements grpcapi.SandboxAPIHost.
func (d *Daemon) IdentityPEMs() (cert, key, ca []byte, err error) {
	mat := d.idStore.Material()
	if mat == nil {
		return nil, nil, nil, fmt.Errorf("no identity")
	}
	return mat.Certificate, mat.PrivateKey, mat.CACert, nil
}

// LeaderAddr implements grpcapi.SandboxAPIHost.
func (d *Daemon) LeaderAddr() string {
	return d.managerDialAddr()
}

// RuntimeAddr implements grpcapi.SandboxAPIHost.
func (d *Daemon) RuntimeAddr(ctx context.Context, nodeID string) (string, error) {
	return d.lookupNodeRuntimeAddr(ctx, nodeID)
}

func (d *Daemon) APIKeyCreate(ctx context.Context, req *cellarv1.APIKeyCreateRequest) (*cellarv1.APIKeyCreateResponse, error) {
	d.mu.Lock()
	raft := d.raft
	d.mu.Unlock()
	if raft == nil {
		return nil, status.Error(codes.FailedPrecondition, "API keys require a manager node")
	}
	if !raft.IsLeader() {
		return nil, status.Error(codes.Unavailable, "not the raft leader; run api-key create on the leader")
	}
	g, err := grpcapi.CreateAPIKeyOnStore(ctx, raft, req.Name)
	if err != nil {
		return nil, err
	}
	return grpcapi.CreateAPIKeyResponse(g), nil
}

func (d *Daemon) APIKeyList(ctx context.Context, _ *cellarv1.APIKeyListRequest) (*cellarv1.APIKeyListResponse, error) {
	d.mu.Lock()
	raft := d.raft
	d.mu.Unlock()
	if raft == nil {
		return nil, status.Error(codes.FailedPrecondition, "API keys require a manager node")
	}
	keys, err := raft.ListAPIKeys(ctx)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	out := &cellarv1.APIKeyListResponse{}
	for _, k := range keys {
		out.Keys = append(out.Keys, grpcapi.APIKeyInfoFromKey(k))
	}
	return out, nil
}

func (d *Daemon) APIKeyDelete(ctx context.Context, req *cellarv1.APIKeyDeleteRequest) (*cellarv1.APIKeyDeleteResponse, error) {
	d.mu.Lock()
	raft := d.raft
	d.mu.Unlock()
	if raft == nil {
		return nil, status.Error(codes.FailedPrecondition, "API keys require a manager node")
	}
	if !raft.IsLeader() {
		return nil, status.Error(codes.Unavailable, "not the raft leader; run api-key rm on the leader")
	}
	if req.Id == "" {
		return nil, status.Error(codes.InvalidArgument, "id is required")
	}
	if err := raft.DeleteAPIKey(ctx, req.Id); err != nil {
		return nil, grpcapi.MapStoreErr(err)
	}
	return &cellarv1.APIKeyDeleteResponse{}, nil
}
