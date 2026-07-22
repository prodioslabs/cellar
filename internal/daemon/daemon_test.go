package daemon_test

import (
	"context"
	"net"
	"path/filepath"
	"testing"
	"time"

	cellarv1 "github.com/prodioslabs/cellar/api/gen"
	"github.com/prodioslabs/cellar/internal/ca"
	"github.com/prodioslabs/cellar/internal/daemon"
	"github.com/prodioslabs/cellar/internal/grpcapi"
	"github.com/prodioslabs/cellar/internal/node"
)

func freePort(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	ln.Close()
	return addr
}

func startDaemon(t *testing.T, dir, socket, listen, raft string) context.CancelFunc {
	t.Helper()
	d := daemon.New(daemon.Config{
		DataDir:    dir,
		SocketPath: socket,
		ListenAddr: listen,
		RaftAddr:   raft,
	})
	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() { errCh <- d.Run(ctx) }()

	deadline := time.Now().Add(5 * time.Second)
	for {
		conn, err := daemon.DialLocal(socket)
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
	t.Cleanup(func() {
		cancel()
		select {
		case <-errCh:
		case <-time.After(5 * time.Second):
		}
	})
	return cancel
}

func TestInitJoinTokenWorkerJoin(t *testing.T) {
	base := t.TempDir()
	mgrListen := freePort(t)
	mgrRaft := freePort(t)
	mgrSock := filepath.Join(base, "mgr.sock")

	startDaemon(t, filepath.Join(base, "mgr"), mgrSock, mgrListen, mgrRaft)

	conn, err := daemon.DialLocal(mgrSock)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	ctrl := cellarv1.NewControlClient(conn)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	initResp, err := ctrl.Init(ctx, &cellarv1.InitRequest{
		AdvertiseAddr: mgrListen,
		ListenAddr:    mgrListen,
		RaftAddr:      mgrRaft,
	})
	if err != nil {
		t.Fatalf("init: %v", err)
	}
	if initResp.WorkerToken == "" || initResp.ManagerToken == "" {
		t.Fatal("missing tokens")
	}

	tokResp, err := ctrl.JoinToken(ctx, &cellarv1.JoinTokenRequest{Role: "worker"})
	if err != nil {
		t.Fatalf("join-token: %v", err)
	}
	if tokResp.JoinCommand == "" || tokResp.Token == "" {
		t.Fatal("empty join command")
	}

	workerSock := filepath.Join(base, "worker.sock")
	startDaemon(t, filepath.Join(base, "worker"), workerSock, freePort(t), freePort(t))

	wconn, err := daemon.DialLocal(workerSock)
	if err != nil {
		t.Fatal(err)
	}
	defer wconn.Close()
	wctrl := cellarv1.NewControlClient(wconn)

	joinResp, err := wctrl.Join(ctx, &cellarv1.JoinRequest{
		Token:      tokResp.Token,
		RemoteAddr: mgrListen,
	})
	if err != nil {
		t.Fatalf("worker join: %v", err)
	}
	if joinResp.Role != string(node.RoleWorker) {
		t.Fatalf("role=%s", joinResp.Role)
	}

	_, err = grpcapi.DownloadRootCA(ctx, mgrListen, "CLLRN-1-0000000000000000000000000-deadbeef")
	if err == nil {
		t.Fatal("expected digest mismatch")
	}
}

func TestManagerJoinReplicatesCA(t *testing.T) {
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

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	initResp, err := ctrlA.Init(ctx, &cellarv1.InitRequest{
		AdvertiseAddr: listenA,
		ListenAddr:    listenA,
		RaftAddr:      raftA,
	})
	if err != nil {
		t.Fatalf("init: %v", err)
	}

	joinManager := func(name, remote string) (listen, sock string, cancelFn context.CancelFunc) {
		t.Helper()
		listen = freePort(t)
		raft := freePort(t)
		sock = filepath.Join(base, name+".sock")
		cancelFn = startDaemon(t, filepath.Join(base, name), sock, listen, raft)
		conn, err := daemon.DialLocal(sock)
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		ctrl := cellarv1.NewControlClient(conn)
		joinResp, err := ctrl.Join(ctx, &cellarv1.JoinRequest{
			Token:         initResp.ManagerToken,
			RemoteAddr:    remote,
			AdvertiseAddr: listen,
			ListenAddr:    listen,
			RaftAddr:      raft,
		})
		if err != nil {
			t.Fatalf("manager %s join: %v", name, err)
		}
		if joinResp.Role != string(node.RoleManager) {
			t.Fatalf("role=%s", joinResp.Role)
		}
		return listen, sock, cancelFn
	}

	listenB, sockB, _ := joinManager("b", listenA)
	listenC, _, _ := joinManager("c", listenA)

	connB, err := daemon.DialLocal(sockB)
	if err != nil {
		t.Fatal(err)
	}
	defer connB.Close()
	ctrlB := cellarv1.NewControlClient(connB)

	deadline := time.Now().Add(20 * time.Second)
	for {
		st, err := ctrlB.Status(ctx, &cellarv1.StatusRequest{})
		if err == nil && st.ClusterId != "" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("manager B not ready: %v", err)
		}
		time.Sleep(100 * time.Millisecond)
	}

	caPEM, err := grpcapi.DownloadRootCA(ctx, listenA, initResp.WorkerToken)
	if err != nil {
		t.Fatalf("download ca: %v", err)
	}
	_, csrPEM, _, err := ca.GenerateKeyAndCSR("")
	if err != nil {
		t.Fatal(err)
	}
	issued, err := grpcapi.IssueWithToken(ctx, listenA, caPEM, initResp.WorkerToken, csrPEM)
	if err != nil {
		t.Fatalf("issue via A: %v", err)
	}
	if issued.NodeId == "" {
		t.Fatal("empty node id")
	}

	caPEMB, err := grpcapi.DownloadRootCA(ctx, listenB, initResp.WorkerToken)
	if err != nil {
		t.Fatalf("download ca from B: %v", err)
	}
	_ = listenC

	// Failover with 3 managers: stop A, B or C can form quorum and sign.
	cancelA()
	time.Sleep(3 * time.Second)

	deadline = time.Now().Add(45 * time.Second)
	var issuedB *cellarv1.IssueNodeCertificateResponse
	for {
		_, csr2, _, err := ca.GenerateKeyAndCSR("")
		if err != nil {
			t.Fatal(err)
		}
		issuedB, err = grpcapi.IssueWithToken(ctx, listenB, caPEMB, initResp.WorkerToken, csr2)
		if err == nil {
			break
		}
		// Try C as well in case B is not leader.
		issuedB, err = grpcapi.IssueWithToken(ctx, listenC, caPEMB, initResp.WorkerToken, csr2)
		if err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("issue after failover: %v", err)
		}
		time.Sleep(300 * time.Millisecond)
	}
	if issuedB.NodeId == "" {
		t.Fatal("empty node id after failover")
	}
}
