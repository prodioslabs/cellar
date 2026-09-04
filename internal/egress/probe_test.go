package egress

import (
	"context"
	"net"
	"testing"
	"time"

	"google.golang.org/grpc"

	cellarv1 "github.com/prodioslabs/cellar/api/gen"
)

// startControlOnly serves just the gRPC control API (no :53/:80/:443 data
// plane) so the readiness probe can be exercised without privileges.
func startControlOnly(t *testing.T, token string) string {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	s := NewServer()
	s.controlToken = token
	g := grpc.NewServer(grpc.UnaryInterceptor(s.authUnary), grpc.StreamInterceptor(s.authStream))
	cellarv1.RegisterEgressGatewayControlServer(g, s)
	go func() { _ = g.Serve(lis) }()
	t.Cleanup(g.Stop)
	return lis.Addr().String()
}

func TestProbeControlAuthenticatedRoundTrip(t *testing.T) {
	addr := startControlOnly(t, "tok")
	if err := probeControl(context.Background(), addr, "tok"); err != nil {
		t.Fatalf("probe against live gateway: %v", err)
	}
	if err := probeControl(context.Background(), addr, "wrong"); err == nil {
		t.Fatal("probe with wrong token should fail")
	}
}

// A listener that accepts and immediately closes mimics Docker's port proxy
// fronting a container whose process never came up: TCP connects succeed,
// but no gRPC server answers. The probe must treat that as not ready.
func TestProbeControlRejectsAcceptAndClose(t *testing.T) {
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer lis.Close()
	go func() {
		for {
			c, err := lis.Accept()
			if err != nil {
				return
			}
			_ = c.Close()
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := probeControl(ctx, lis.Addr().String(), "tok"); err == nil {
		t.Fatal("probe should fail when nothing speaks gRPC behind the port")
	}
}
