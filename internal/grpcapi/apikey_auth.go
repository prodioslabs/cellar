package grpcapi

import (
	"context"
	"strings"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	"github.com/prodioslabs/cellar/internal/apikey"
	"github.com/prodioslabs/cellar/internal/store"
)

type apiKeyCtxKey struct{}

// APIKeyFromContext returns the authenticated API key, if any.
func APIKeyFromContext(ctx context.Context) *apikey.Key {
	k, _ := ctx.Value(apiKeyCtxKey{}).(*apikey.Key)
	return k
}

func extractAPIKey(ctx context.Context) (string, error) {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return "", status.Error(codes.Unauthenticated, "missing metadata")
	}
	if vals := md.Get("x-api-key"); len(vals) > 0 && vals[0] != "" {
		return vals[0], nil
	}
	for _, v := range md.Get("authorization") {
		const bearer = "bearer "
		if len(v) > len(bearer) && strings.EqualFold(v[:len(bearer)], bearer) {
			return strings.TrimSpace(v[len(bearer):]), nil
		}
	}
	return "", status.Error(codes.Unauthenticated, "API key required")
}

func authenticateAPIKey(ctx context.Context, s store.Store) (context.Context, error) {
	raw, err := extractAPIKey(ctx)
	if err != nil {
		return ctx, err
	}
	raw, err = apikey.ParseRaw(raw)
	if err != nil {
		return ctx, status.Error(codes.Unauthenticated, "invalid API key")
	}
	k, err := s.GetAPIKeyByHash(ctx, apikey.Hash(raw))
	if err != nil {
		return ctx, status.Error(codes.Unauthenticated, "invalid API key")
	}
	if k.Disabled {
		return ctx, status.Error(codes.PermissionDenied, "API key disabled")
	}
	return context.WithValue(ctx, apiKeyCtxKey{}, k), nil
}

func isSandboxAPIMethod(fullMethod string) bool {
	return strings.HasPrefix(fullMethod, "/cellar.v1.SandboxAPI/")
}

// APIKeyUnaryInterceptor authenticates SandboxAPI unary RPCs.
func APIKeyUnaryInterceptor(s store.Store, touch func(id string)) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		if !isSandboxAPIMethod(info.FullMethod) {
			return handler(ctx, req)
		}
		ctx, err := authenticateAPIKey(ctx, s)
		if err != nil {
			return nil, err
		}
		if k := APIKeyFromContext(ctx); k != nil && touch != nil {
			touch(k.ID)
		}
		return handler(ctx, req)
	}
}

// APIKeyStreamInterceptor authenticates SandboxAPI streaming RPCs.
func APIKeyStreamInterceptor(s store.Store, touch func(id string)) grpc.StreamServerInterceptor {
	return func(srv any, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		if !isSandboxAPIMethod(info.FullMethod) {
			return handler(srv, ss)
		}
		ctx, err := authenticateAPIKey(ss.Context(), s)
		if err != nil {
			return err
		}
		if k := APIKeyFromContext(ctx); k != nil && touch != nil {
			touch(k.ID)
		}
		return handler(srv, &ctxServerStream{ServerStream: ss, ctx: ctx})
	}
}

type ctxServerStream struct {
	grpc.ServerStream
	ctx context.Context
}

func (s *ctxServerStream) Context() context.Context { return s.ctx }

// TouchAPIKeyBestEffort updates last_used on the leader; ignores errors.
func TouchAPIKeyBestEffort(s store.Store, raft RaftAdmin, id string) {
	if raft == nil || !raft.IsLeader() || id == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	k, err := s.GetAPIKey(ctx, id)
	if err != nil {
		return
	}
	k.LastUsed = time.Now().UTC()
	_ = s.SaveAPIKey(ctx, k)
}
