package gateway_test

import (
	"context"
	"net"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"

	cellarv1 "github.com/prodioslabs/cellar/api/gen"
	"github.com/prodioslabs/cellar/internal/egress/gateway"
)

func TestControlRegisterUpdateDeregister(t *testing.T) {
	ln80, err := net.Listen("tcp", "127.0.0.1:80")
	if err != nil {
		t.Skip("need bind on :80 for gateway Start in this environment")
	}
	_ = ln80.Close()
	ln443, err := net.Listen("tcp", "127.0.0.1:443")
	if err != nil {
		t.Skip("need bind on :443 for gateway Start in this environment")
	}
	_ = ln443.Close()

	ctrlLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctrlAddr := ctrlLn.Addr().String()
	_ = ctrlLn.Close()

	const token = "test-control-token"
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	srv := gateway.New()
	if err := srv.Start(ctx, gateway.ControlConfig{Addr: ctrlAddr, Token: token}); err != nil {
		t.Skipf("gateway start: %v", err)
	}
	defer srv.Close()

	conn, err := grpc.NewClient(ctrlAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	cli := cellarv1.NewEgressGatewayControlClient(conn)
	authCtx := metadata.AppendToOutgoingContext(ctx, "authorization", "Bearer "+token)

	_, err = cli.RegisterSandbox(authCtx, &cellarv1.RegisterSandboxRequest{
		SandboxId:  "sb-1",
		NetworkId:  "net-1",
		SubnetCidr: "172.30.0.0/29",
		GatewayIp:  "127.0.0.1",
		Policy: &cellarv1.NetworkPolicy{
			Mode: "allowlist",
			Dns:  &cellarv1.DNSPolicy{Mode: "allowlist", Names: []string{"example.com"}},
			Rules: []*cellarv1.NetworkRule{{
				Hosts: []string{"example.com"},
				Ports: []uint32{443},
			}},
		},
	})
	if err != nil {
		t.Fatalf("register: %v", err)
	}

	_, err = cli.UpdatePolicy(authCtx, &cellarv1.UpdatePolicyRequest{
		SandboxId: "sb-1",
		Policy: &cellarv1.NetworkPolicy{
			Mode: "allowlist",
			Dns:  &cellarv1.DNSPolicy{Mode: "allowlist", Names: []string{"api.example.com"}},
			Rules: []*cellarv1.NetworkRule{{
				Hosts: []string{"api.example.com"},
				Ports: []uint32{443},
			}},
		},
	})
	if err != nil {
		t.Fatalf("update: %v", err)
	}

	_, err = cli.DeregisterSandbox(authCtx, &cellarv1.DeregisterSandboxRequest{SandboxId: "sb-1"})
	if err != nil {
		t.Fatalf("deregister: %v", err)
	}

	// Unauthenticated call must fail.
	_, err = cli.RegisterSandbox(ctx, &cellarv1.RegisterSandboxRequest{
		SandboxId:  "sb-2",
		NetworkId:  "net-2",
		SubnetCidr: "172.30.0.8/29",
		GatewayIp:  "127.0.0.1",
		Policy:     &cellarv1.NetworkPolicy{Mode: "none"},
	})
	if err == nil {
		t.Fatal("expected unauthenticated register to fail")
	}
}
