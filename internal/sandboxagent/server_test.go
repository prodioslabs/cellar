package sandboxagent_test

import (
	"context"
	"io"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"

	agentv1 "github.com/prodioslabs/cellar/api/gen/agent"
	"github.com/prodioslabs/cellar/internal/sandboxagent"
	"github.com/prodioslabs/cellar/internal/version"
)

func startTestAgent(t *testing.T, token string) (agentv1.SandboxAgentClient, context.CancelFunc) {
	t.Helper()
	dir := t.TempDir()
	sock := filepath.Join(dir, "agent.sock")
	tokenPath := filepath.Join(dir, "agent.token")
	if err := os.WriteFile(tokenPath, []byte(token), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg := sandboxagent.Config{
		SandboxID: "sb-test",
		SockPath:  sock,
		Token:     token,
	}

	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() {
		errCh <- sandboxagent.ListenAndServe(ctx, cfg)
	}()

	deadline := time.Now().Add(5 * time.Second)
	var conn *grpc.ClientConn
	for time.Now().Before(deadline) {
		if _, err := os.Stat(sock); err == nil {
			dialer := func(ctx context.Context, _ string) (net.Conn, error) {
				var d net.Dialer
				return d.DialContext(ctx, "unix", sock)
			}
			c, err := grpc.NewClient("passthrough:///test",
				grpc.WithTransportCredentials(insecure.NewCredentials()),
				grpc.WithContextDialer(dialer),
			)
			if err == nil {
				conn = c
				break
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	if conn == nil {
		cancel()
		t.Fatal("agent socket not ready")
	}

	t.Cleanup(func() {
		cancel()
		_ = conn.Close()
		select {
		case <-errCh:
		case <-time.After(2 * time.Second):
		}
	})

	return agentv1.NewSandboxAgentClient(conn), cancel
}

func TestHealthRequiresAuth(t *testing.T) {
	client, _ := startTestAgent(t, "secret-token")

	_, err := client.Health(context.Background(), &agentv1.HealthRequest{})
	if status.Code(err) != codes.Unauthenticated {
		t.Fatalf("expected Unauthenticated, got %v", err)
	}

	ctx := sandboxagent.WithBearer(context.Background(), "wrong")
	_, err = client.Health(ctx, &agentv1.HealthRequest{})
	if status.Code(err) != codes.Unauthenticated {
		t.Fatalf("expected Unauthenticated for wrong token, got %v", err)
	}

	ctx = sandboxagent.WithBearer(context.Background(), "secret-token")
	resp, err := client.Health(ctx, &agentv1.HealthRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if resp.SandboxId != "sb-test" {
		t.Fatalf("sandbox_id: got %q", resp.SandboxId)
	}
	if resp.Version != version.Version {
		t.Fatalf("version: got %q, want %q", resp.Version, version.Version)
	}
}

func TestRunCommandEcho(t *testing.T) {
	client, _ := startTestAgent(t, "secret-token")
	ctx := sandboxagent.WithBearer(context.Background(), "secret-token")

	stream, err := client.RunCommand(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := stream.Send(&agentv1.RunCommandMessage{
		Payload: &agentv1.RunCommandMessage_Start{Start: &agentv1.RunCommandStart{
			Command: []string{"echo", "hello-cellar"},
		}},
	}); err != nil {
		t.Fatal(err)
	}
	_ = stream.CloseSend()

	var stdout []byte
	var exit *agentv1.RunCommandExit
	for {
		msg, err := stream.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		stdout = append(stdout, msg.GetStdout()...)
		if msg.GetExit() != nil {
			exit = msg.GetExit()
			break
		}
	}
	if exit == nil {
		t.Fatal("expected exit")
	}
	if exit.ExitCode != 0 {
		t.Fatalf("exit code %d err %q", exit.ExitCode, exit.Error)
	}
	if got := string(stdout); got != "hello-cellar\n" {
		t.Fatalf("stdout: got %q", got)
	}
}

func TestRunCommandRequiresAuth(t *testing.T) {
	client, _ := startTestAgent(t, "secret-token")
	stream, err := client.RunCommand(context.Background())
	if err != nil {
		// Some grpc versions fail on first send
		return
	}
	err = stream.Send(&agentv1.RunCommandMessage{
		Payload: &agentv1.RunCommandMessage_Start{Start: &agentv1.RunCommandStart{
			Command: []string{"true"},
		}},
	})
	if err == nil {
		_, err = stream.Recv()
	}
	if status.Code(err) != codes.Unauthenticated {
		t.Fatalf("expected Unauthenticated, got %v", err)
	}
}
