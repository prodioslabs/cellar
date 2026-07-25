package daemon

import (
	"context"
	"net"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	cellarv1 "github.com/prodioslabs/cellar/api/gen"
)

func TestGracefulStopForcesAfterTimeout(t *testing.T) {
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer lis.Close()

	entered := make(chan struct{})
	block := make(chan struct{})
	s := grpc.NewServer()
	cellarv1.RegisterControlServer(s, &blockingControl{
		entered: entered,
		block:   block,
	})
	go s.Serve(lis)
	defer s.Stop()

	conn, err := grpc.NewClient(lis.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	client := cellarv1.NewControlClient(conn)

	errCh := make(chan error, 1)
	go func() {
		_, err := client.Status(context.Background(), &cellarv1.StatusRequest{})
		errCh <- err
	}()
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("RPC did not start")
	}

	start := time.Now()
	gracefulStop(s, 200*time.Millisecond)
	elapsed := time.Since(start)
	if elapsed < 150*time.Millisecond {
		t.Fatalf("expected gracefulStop to wait for timeout, took %v", elapsed)
	}
	if elapsed > 2*time.Second {
		t.Fatalf("gracefulStop took too long: %v", elapsed)
	}
	close(block)

	select {
	case <-errCh:
	case <-time.After(2 * time.Second):
		t.Fatal("RPC did not finish after Stop")
	}
}

func TestShutdownUnlocksBeforeGracefulStop(t *testing.T) {
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer lis.Close()

	d := &Daemon{}
	entered := make(chan struct{})
	s := grpc.NewServer()
	cellarv1.RegisterControlServer(s, &contendingControl{
		d:       d,
		entered: entered,
	})
	d.localGRPC = s
	go s.Serve(lis)

	conn, err := grpc.NewClient(lis.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	client := cellarv1.NewControlClient(conn)

	rpcDone := make(chan error, 1)
	go func() {
		_, err := client.Status(context.Background(), &cellarv1.StatusRequest{})
		rpcDone <- err
	}()
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("RPC did not start")
	}

	// Old shutdown held d.mu across GracefulStop: Status sleeping then locking
	// mu deadlocked with GracefulStop waiting for Status. New shutdown must
	// return promptly.
	done := make(chan struct{})
	go func() {
		d.shutdown()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("shutdown deadlocked holding mu across GracefulStop")
	}

	select {
	case <-rpcDone:
	case <-time.After(2 * time.Second):
		t.Fatal("contending RPC did not finish")
	}
}

func TestShutdownReturnsPromptly(t *testing.T) {
	base := t.TempDir()
	sock := filepath.Join(base, "cellar.sock")
	d := New(Config{
		DataDir:    filepath.Join(base, "data"),
		SocketPath: sock,
		ListenAddr: "127.0.0.1:0",
		RaftAddr:   "127.0.0.1:0",
	})
	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() { errCh <- d.Run(ctx) }()

	deadline := time.Now().Add(5 * time.Second)
	for {
		conn, err := DialLocal(sock)
		if err == nil {
			_ = conn.Close()
			break
		}
		if time.Now().After(deadline) {
			cancel()
			t.Fatalf("daemon socket not ready: %v", err)
		}
		time.Sleep(20 * time.Millisecond)
	}

	cancel()
	select {
	case err := <-errCh:
		if err != nil && err != context.Canceled {
			t.Fatalf("Run: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("shutdown did not return promptly")
	}
}

type blockingControl struct {
	cellarv1.UnimplementedControlServer
	entered chan struct{}
	block   chan struct{}
	once    sync.Once
}

func (b *blockingControl) Status(ctx context.Context, _ *cellarv1.StatusRequest) (*cellarv1.StatusResponse, error) {
	b.once.Do(func() { close(b.entered) })
	select {
	case <-b.block:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	return &cellarv1.StatusResponse{}, nil
}

type contendingControl struct {
	cellarv1.UnimplementedControlServer
	d       *Daemon
	entered chan struct{}
	once    sync.Once
}

func (c *contendingControl) Status(ctx context.Context, _ *cellarv1.StatusRequest) (*cellarv1.StatusResponse, error) {
	c.once.Do(func() { close(c.entered) })
	// Let shutdown acquire mu (old bug) or begin GracefulStop (fixed path).
	time.Sleep(100 * time.Millisecond)
	c.d.mu.Lock()
	defer c.d.mu.Unlock()
	return &cellarv1.StatusResponse{}, nil
}
