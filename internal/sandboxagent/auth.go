package sandboxagent

import (
	"context"
	"crypto/subtle"
	"strings"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

const authHeader = "authorization"

// BearerToken returns the canonical Authorization metadata value.
func BearerToken(token string) string {
	return "Bearer " + token
}

func extractBearer(md metadata.MD) (string, bool) {
	vals := md.Get(authHeader)
	if len(vals) == 0 {
		return "", false
	}
	v := strings.TrimSpace(vals[0])
	if len(v) < 7 || !strings.EqualFold(v[:7], "bearer ") {
		return "", false
	}
	tok := strings.TrimSpace(v[7:])
	if tok == "" {
		return "", false
	}
	return tok, true
}

func checkToken(ctx context.Context, expected string) error {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return status.Error(codes.Unauthenticated, "missing metadata")
	}
	got, ok := extractBearer(md)
	if !ok {
		return status.Error(codes.Unauthenticated, "missing bearer token")
	}
	if subtle.ConstantTimeCompare([]byte(got), []byte(expected)) != 1 {
		return status.Error(codes.Unauthenticated, "invalid token")
	}
	return nil
}

// UnaryAuthInterceptor validates the bearer token on unary RPCs.
func UnaryAuthInterceptor(expectedToken string) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, _ *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		if err := checkToken(ctx, expectedToken); err != nil {
			return nil, err
		}
		return handler(ctx, req)
	}
}

// StreamAuthInterceptor validates the bearer token on streaming RPCs.
func StreamAuthInterceptor(expectedToken string) grpc.StreamServerInterceptor {
	return func(srv any, ss grpc.ServerStream, _ *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		if err := checkToken(ss.Context(), expectedToken); err != nil {
			return err
		}
		return handler(srv, ss)
	}
}

// WithBearer attaches the bearer token to an outgoing context.
func WithBearer(ctx context.Context, token string) context.Context {
	return metadata.AppendToOutgoingContext(ctx, authHeader, BearerToken(token))
}
