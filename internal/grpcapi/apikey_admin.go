package grpcapi

import (
	"context"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	cellarv1 "github.com/prodioslabs/cellar/api/gen"
	"github.com/prodioslabs/cellar/internal/apikey"
	"github.com/prodioslabs/cellar/internal/store"
)

// CreateAPIKeyOnStore mints and persists a key (caller must be Raft leader).
func CreateAPIKeyOnStore(ctx context.Context, s store.Store, name string) (*apikey.Generated, error) {
	g, err := apikey.Generate(name)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	if err := s.SaveAPIKey(ctx, g.Key); err != nil {
		return nil, mapStoreErr(err)
	}
	return g, nil
}

// APIKeyInfoFromKey converts a store key to the Control list shape.
func APIKeyInfoFromKey(k *apikey.Key) *cellarv1.APIKeyInfo {
	if k == nil {
		return nil
	}
	info := &cellarv1.APIKeyInfo{
		Id:                k.ID,
		Name:              k.Name,
		Mask:              k.Mask,
		CreatedAtUnixNano: k.CreatedAt.UnixNano(),
		Disabled:          k.Disabled,
	}
	if !k.LastUsed.IsZero() {
		info.LastUsedUnixNano = k.LastUsed.UnixNano()
	}
	return info
}

// CreateAPIKeyResponse builds the one-time create response.
func CreateAPIKeyResponse(g *apikey.Generated) *cellarv1.APIKeyCreateResponse {
	return &cellarv1.APIKeyCreateResponse{
		Id:                g.Key.ID,
		Name:              g.Key.Name,
		Key:               g.Raw,
		Mask:              g.Key.Mask,
		CreatedAtUnixNano: g.Key.CreatedAt.UnixNano(),
	}
}
