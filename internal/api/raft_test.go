package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/prodioslabs/cellar/internal/api"
	"github.com/prodioslabs/cellar/internal/raftstore"
	"github.com/prodioslabs/cellar/pkg/agent"
)

func freeTCP(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	ln.Close()
	return addr
}

func TestRaftManagerJoinReplicatesCA(t *testing.T) {
	dir := t.TempDir()
	addrA := freeTCP(t)
	addrB := freeTCP(t)

	a, err := raftstore.Open(raftstore.Config{
		DataDir:       filepath.Join(dir, "a"),
		NodeID:        "a",
		RaftAddr:      addrA,
		HTTPAdvertise: "http://leader-a",
		Bootstrap:     true,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()

	srvA := httptest.NewServer(api.NewWithRaft(a, a).Handler())
	defer srvA.Close()

	resp, err := http.Post(srvA.URL+"/api/v1/cluster/init", "application/json", bytes.NewReader([]byte("{}")))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("init status=%d", resp.StatusCode)
	}
	var initOut struct {
		Tokens struct {
			Worker  string `json:"worker"`
			Manager string `json:"manager"`
		} `json:"tokens"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&initOut); err != nil {
		t.Fatal(err)
	}

	b, err := raftstore.Open(raftstore.Config{
		DataDir:       filepath.Join(dir, "b"),
		NodeID:        "b",
		RaftAddr:      addrB,
		HTTPAdvertise: "http://leader-b",
		Bootstrap:     false,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()

	body, _ := json.Marshal(map[string]string{
		"node_id":   "b",
		"raft_addr": b.RaftAddr(),
		"http_addr": "http://leader-b",
	})
	addResp, err := http.Post(srvA.URL+"/api/v1/cluster/managers", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	addResp.Body.Close()
	if addResp.StatusCode != http.StatusOK {
		t.Fatalf("add manager status=%d", addResp.StatusCode)
	}

	if err := b.WaitInitialized(15 * time.Second); err != nil {
		t.Fatal(err)
	}
	rootA, err := a.GetRootCA(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	rootB, err := b.GetRootCA(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if string(rootA.KeyPEM) != string(rootB.KeyPEM) {
		t.Fatal("manager B did not receive RootCA private key via Raft")
	}

	// Worker joins via token only — agent stores public CA cert, never KeyPEM.
	worker := agent.New(srvA.URL, filepath.Join(dir, "worker"))
	id, err := worker.Join(t.Context(), initOut.Tokens.Worker)
	if err != nil {
		t.Fatal(err)
	}
	if id.Role != "worker" {
		t.Fatalf("role=%s", id.Role)
	}
}

func TestFollowerRejectsMutations(t *testing.T) {
	dir := t.TempDir()
	a, err := raftstore.Open(raftstore.Config{
		DataDir: filepath.Join(dir, "a"), NodeID: "a", RaftAddr: freeTCP(t), Bootstrap: true,
		HTTPAdvertise: "http://a",
	})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()

	b, err := raftstore.Open(raftstore.Config{
		DataDir: filepath.Join(dir, "b"), NodeID: "b", RaftAddr: freeTCP(t), Bootstrap: false,
		HTTPAdvertise: "http://b",
	})
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()

	if err := a.AddVoter(context.Background(), raftstore.PeerInfo{
		NodeID: "b", RaftAddr: b.RaftAddr(), HTTPAddr: "http://b",
	}); err != nil {
		t.Fatal(err)
	}
	if err := b.WaitForLeader(10 * time.Second); err != nil {
		t.Fatal(err)
	}

	follower := b
	if b.IsLeader() {
		follower = a
	}
	srv := httptest.NewServer(api.NewWithRaft(follower, follower).Handler())
	defer srv.Close()

	resp, err := http.Post(srv.URL+"/api/v1/cluster/init", "application/json", bytes.NewReader([]byte("{}")))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d", resp.StatusCode)
	}
	if resp.Header.Get("X-Cellar-Leader") == "" && follower.LeaderHTTP() != "" {
		t.Fatal("expected X-Cellar-Leader header when leader HTTP is known")
	}
}
