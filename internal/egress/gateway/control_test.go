package gateway_test

import (
	"context"
	"net"
	"path/filepath"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	cellarv1 "github.com/prodioslabs/cellar/api/gen"
	"github.com/prodioslabs/cellar/internal/egress"
	"github.com/prodioslabs/cellar/internal/egress/gateway"
	"github.com/prodioslabs/cellar/internal/sandbox"
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

	dir := t.TempDir()
	sock := filepath.Join(dir, "control.sock")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	srv := gateway.New()
	if err := srv.Start(ctx, sock); err != nil {
		t.Skipf("gateway start: %v", err)
	}
	defer srv.Close()

	conn, err := grpc.NewClient("unix://"+sock, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	cli := cellarv1.NewEgressGatewayControlClient(conn)

	_, err = cli.RegisterSandbox(ctx, &cellarv1.RegisterSandboxRequest{
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

	_, err = cli.UpdatePolicy(ctx, &cellarv1.UpdatePolicyRequest{
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

	_, err = cli.DeregisterSandbox(ctx, &cellarv1.DeregisterSandboxRequest{SandboxId: "sb-1"})
	if err != nil {
		t.Fatalf("deregister: %v", err)
	}
}

func TestEvaluatorAllowDNS(t *testing.T) {
	ev := egress.NewEvaluator(sandbox.NetworkPolicy{
		Mode: sandbox.NetworkAllowlist,
		DNS:  sandbox.DNSPolicy{Mode: sandbox.DNSAllowlist, Names: []string{"example.com"}},
		Rules: []sandbox.NetworkRule{{
			Hosts: []string{"example.com"},
			Ports: []uint32{443},
		}},
	})
	if ev.AllowDNS("example.com") != egress.Allow {
		t.Fatal("expected allow")
	}
	if ev.AllowDNS("google.com") != egress.Deny {
		t.Fatal("expected deny")
	}
}
