package daemon_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	cellarv1 "github.com/prodioslabs/cellar/api/gen"
	"github.com/prodioslabs/cellar/internal/daemon"
	"github.com/prodioslabs/cellar/internal/gateway"
	"github.com/prodioslabs/cellar/pkg/client"
)

func TestSandboxAPIClientViaGatewayFailover(t *testing.T) {
	base := t.TempDir()
	listenA := freePort(t)
	raftA := freePort(t)
	sockA := filepath.Join(base, "a.sock")
	cancelA := startDaemon(t, filepath.Join(base, "a"), sockA, listenA, raftA)

	connA, err := daemon.DialLocal(sockA)
	if err != nil {
		t.Fatal(err)
	}
	defer connA.Close()
	ctrlA := cellarv1.NewControlClient(connA)

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	initResp, err := ctrlA.Init(ctx, &cellarv1.InitRequest{
		AdvertiseAddr: listenA,
		ListenAddr:    listenA,
		RaftAddr:      raftA,
	})
	if err != nil {
		t.Fatalf("init: %v", err)
	}

	joinManager := func(name string) (listen, sock string) {
		t.Helper()
		listen = freePort(t)
		raft := freePort(t)
		sock = filepath.Join(base, name+".sock")
		startDaemon(t, filepath.Join(base, name), sock, listen, raft)
		conn, err := daemon.DialLocal(sock)
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		ctrl := cellarv1.NewControlClient(conn)
		_, err = ctrl.Join(ctx, &cellarv1.JoinRequest{
			Token:         initResp.ManagerToken,
			RemoteAddr:    listenA,
			AdvertiseAddr: listen,
			ListenAddr:    listen,
			RaftAddr:      raft,
		})
		if err != nil {
			t.Fatalf("manager %s join: %v", name, err)
		}
		return listen, sock
	}

	listenB, sockB := joinManager("b")
	listenC, sockC := joinManager("c")

	waitReady := func(sock string) {
		t.Helper()
		conn, err := daemon.DialLocal(sock)
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		ctrl := cellarv1.NewControlClient(conn)
		deadline := time.Now().Add(20 * time.Second)
		for {
			st, err := ctrl.Status(ctx, &cellarv1.StatusRequest{})
			if err == nil && st.ClusterId != "" {
				return
			}
			if time.Now().After(deadline) {
				t.Fatalf("manager not ready: %v", err)
			}
			time.Sleep(100 * time.Millisecond)
		}
	}
	waitReady(sockB)
	waitReady(sockC)

	keyResp, err := ctrlA.APIKeyCreate(ctx, &cellarv1.APIKeyCreateRequest{Name: "test"})
	if err != nil {
		t.Fatalf("api-key create: %v", err)
	}
	if keyResp.Key == "" {
		t.Fatal("empty raw key")
	}

	deadline := time.Now().Add(15 * time.Second)
	for {
		conn, err := daemon.DialLocal(sockB)
		if err != nil {
			t.Fatal(err)
		}
		list, err := cellarv1.NewControlClient(conn).APIKeyList(ctx, &cellarv1.APIKeyListRequest{})
		_ = conn.Close()
		if err == nil && len(list.Keys) == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("api key not replicated: %v", err)
		}
		time.Sleep(100 * time.Millisecond)
	}

	gwListen := freePort(t)
	gw, err := gateway.New(gateway.Config{
		ListenAddr: gwListen,
		DataDir:    filepath.Join(base, "a"),
		Upstreams:  []string{listenA, listenB, listenC},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	gwCtx, gwCancel := context.WithCancel(context.Background())
	defer gwCancel()
	go func() { _ = gw.Run(gwCtx) }()

	// Wait for gateway readiness.
	deadline = time.Now().Add(15 * time.Second)
	for {
		cli, err := client.New(client.Config{
			Endpoint: "http://" + gwListen,
			APIKey:   keyResp.Key,
		})
		if err != nil {
			t.Fatal(err)
		}
		_, err = cli.List(ctx)
		if err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("gateway not ready: %v", err)
		}
		time.Sleep(100 * time.Millisecond)
	}

	cli, err := client.New(client.Config{
		Endpoint: "http://" + gwListen,
		APIKey:   keyResp.Key,
	})
	if err != nil {
		t.Fatal(err)
	}

	sb, err := cli.Create(ctx, &cellarv1.SandboxCreateRequest{
		Spec: &cellarv1.SandboxSpec{Image: "alpine:3.20"},
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if sb.Id == "" {
		t.Fatal("empty sandbox id")
	}

	deadline = time.Now().Add(15 * time.Second)
	for {
		conn, err := daemon.DialLocal(sockB)
		if err != nil {
			t.Fatal(err)
		}
		got, err := cellarv1.NewControlClient(conn).SandboxGet(ctx, &cellarv1.SandboxGetRequest{SandboxId: sb.Id})
		_ = conn.Close()
		if err == nil && got.Sandbox != nil && got.Sandbox.Id == sb.Id {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("sandbox not replicated to B: %v", err)
		}
		time.Sleep(100 * time.Millisecond)
	}

	cancelA()

	deadline = time.Now().Add(45 * time.Second)
	var leaderSock string
	for {
		for _, sock := range []string{sockB, sockC} {
			conn, err := daemon.DialLocal(sock)
			if err != nil {
				continue
			}
			st, err := cellarv1.NewControlClient(conn).Status(ctx, &cellarv1.StatusRequest{})
			_ = conn.Close()
			if err == nil && st.IsLeader {
				leaderSock = sock
				break
			}
		}
		if leaderSock != "" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("no leader after killing A")
		}
		time.Sleep(200 * time.Millisecond)
	}

	got, err := cli.Get(ctx, sb.Id)
	if err != nil {
		t.Fatalf("get after failover: %v", err)
	}
	if got.Id != sb.Id {
		t.Fatalf("id=%q", got.Id)
	}

	sb2, err := cli.Create(ctx, &cellarv1.SandboxCreateRequest{
		Spec: &cellarv1.SandboxSpec{Image: "alpine:3.20"},
	})
	if err != nil {
		t.Fatalf("create after failover: %v", err)
	}
	if sb2.Id == "" || sb2.Id == sb.Id {
		t.Fatalf("unexpected sandbox2 id=%q", sb2.Id)
	}

	if err := cli.Remove(ctx, sb.Id); err != nil {
		t.Fatalf("remove: %v", err)
	}
	if err := cli.Remove(ctx, sb2.Id); err != nil {
		t.Fatalf("remove2: %v", err)
	}

	connL, err := daemon.DialLocal(leaderSock)
	if err != nil {
		t.Fatal(err)
	}
	defer connL.Close()
	list, err := cellarv1.NewControlClient(connL).APIKeyList(ctx, &cellarv1.APIKeyListRequest{})
	if err != nil {
		t.Fatalf("api-key list: %v", err)
	}
	if len(list.Keys) != 1 || list.Keys[0].Mask == "" {
		t.Fatalf("keys=%v", list.Keys)
	}
}
