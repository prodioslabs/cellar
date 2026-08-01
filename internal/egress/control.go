package egress

import (
	"context"
	"crypto/subtle"
	"fmt"
	"log"
	"net"
	"strings"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	cellarv1 "github.com/prodioslabs/cellar/api/gen"
)

// ControlConfig configures the gRPC control listener.
type ControlConfig struct {
	// Addr is the TCP listen address (e.g. "0.0.0.0:17948").
	Addr string
	// Token is the bearer token required on every control RPC.
	Token string
}

// Start binds data-plane listeners and the token-authenticated control TCP port.
func (s *Server) Start(ctx context.Context, ctrl ControlConfig) error {
	if strings.TrimSpace(ctrl.Addr) == "" {
		return fmt.Errorf("control addr required")
	}
	if strings.TrimSpace(ctrl.Token) == "" {
		return fmt.Errorf("control token required")
	}
	s.controlToken = ctrl.Token

	httpLis, err := net.Listen("tcp", "0.0.0.0:80")
	if err != nil {
		return fmt.Errorf("listen :80: %w", err)
	}
	tlsLis, err := net.Listen("tcp", "0.0.0.0:443")
	if err != nil {
		_ = httpLis.Close()
		return fmt.Errorf("listen :443: %w", err)
	}
	otherLis, err := net.Listen("tcp", fmt.Sprintf("0.0.0.0:%d", catchAllPort))
	if err != nil {
		_ = httpLis.Close()
		_ = tlsLis.Close()
		return fmt.Errorf("listen :%d: %w", catchAllPort, err)
	}
	s.httpLis, s.tlsLis, s.otherLis = httpLis, tlsLis, otherLis

	ctrlLis, err := net.Listen("tcp", ctrl.Addr)
	if err != nil {
		s.closeListeners()
		return fmt.Errorf("listen control %s: %w", ctrl.Addr, err)
	}
	s.controlLis = ctrlLis

	s.grpcSrv = grpc.NewServer(
		grpc.UnaryInterceptor(s.authUnary),
		grpc.StreamInterceptor(s.authStream),
	)
	cellarv1.RegisterEgressGatewayControlServer(s.grpcSrv, s)

	go s.serveTCP(ctx, s.httpLis, kindHTTP)
	go s.serveTCP(ctx, s.tlsLis, kindTLS)
	go s.serveTCP(ctx, s.otherLis, kindOther)
	go func() {
		if err := s.grpcSrv.Serve(ctrlLis); err != nil {
			log.Printf("egress-gateway control: %v", err)
		}
	}()
	log.Printf("egress-gateway listening http=:80 tls=:443 other=:%d control=%s", catchAllPort, ctrl.Addr)
	return nil
}

func (s *Server) authUnary(ctx context.Context, req any, _ *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
	if err := s.checkAuth(ctx); err != nil {
		return nil, err
	}
	return handler(ctx, req)
}

func (s *Server) authStream(srv any, ss grpc.ServerStream, _ *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
	if err := s.checkAuth(ss.Context()); err != nil {
		return err
	}
	return handler(srv, ss)
}

func (s *Server) checkAuth(ctx context.Context) error {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return status.Error(codes.Unauthenticated, "missing metadata")
	}
	vals := md.Get("authorization")
	if len(vals) == 0 {
		return status.Error(codes.Unauthenticated, "missing authorization")
	}
	const prefix = "Bearer "
	raw := vals[0]
	if !strings.HasPrefix(raw, prefix) {
		return status.Error(codes.Unauthenticated, "invalid authorization")
	}
	token := strings.TrimSpace(strings.TrimPrefix(raw, prefix))
	if subtle.ConstantTimeCompare([]byte(token), []byte(s.controlToken)) != 1 {
		return status.Error(codes.Unauthenticated, "invalid token")
	}
	return nil
}
