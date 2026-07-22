package raftstore

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"path/filepath"
	"testing"
	"time"

	"github.com/hashicorp/raft"

	"github.com/prodioslabs/cellar/internal/ca"
	"github.com/prodioslabs/cellar/internal/node"
	"github.com/prodioslabs/cellar/internal/store"
	"github.com/prodioslabs/cellar/internal/token"
)

func TestFSMInitClusterRootCA(t *testing.T) {
	fsm := NewFSM()
	root, err := ca.GenerateRootCA("cellar", ca.DefaultCAValidity)
	if err != nil {
		t.Fatal(err)
	}
	secrets, err := token.GenerateSecrets()
	if err != nil {
		t.Fatal(err)
	}

	data, err := encodeCommand(opCreateCluster, createClusterPayload{
		Cluster: store.Cluster{
			ClusterID: "cluster-1",
			RootCA: store.RootCAMaterial{
				CAKey:       root.KeyPEM,
				CACert:      root.CertPEM,
				CACertHash:  root.DigestPrefix(),
				JoinSecrets: secrets,
			},
			CertValidity: ca.DefaultNodeValidity,
			CreatedAt:    time.Now().UTC(),
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	if resp := fsm.Apply(&raft.Log{Data: data}); resp != nil {
		t.Fatalf("apply: %v", resp)
	}

	got, err := fsm.getRootCA()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got.CertPEM, root.CertPEM) || !bytes.Equal(got.KeyPEM, root.KeyPEM) {
		t.Fatal("RootCA PEMs not preserved in FSM")
	}
	cluster, err := fsm.getCluster()
	if err != nil {
		t.Fatal(err)
	}
	if cluster.RootCA.JoinSecrets.Worker != secrets.Worker || cluster.RootCA.JoinSecrets.Manager != secrets.Manager {
		t.Fatal("join secrets not preserved")
	}

	if resp := fsm.Apply(&raft.Log{Data: data}); resp != store.ErrAlreadyInitialized {
		t.Fatalf("second init: got %v want ErrAlreadyInitialized", resp)
	}
}

func TestFSMSnapshotRestore(t *testing.T) {
	fsm := NewFSM()
	root, err := ca.GenerateRootCA("cellar", ca.DefaultCAValidity)
	if err != nil {
		t.Fatal(err)
	}
	secrets, err := token.GenerateSecrets()
	if err != nil {
		t.Fatal(err)
	}
	data, err := encodeCommand(opCreateCluster, createClusterPayload{
		Cluster: store.Cluster{
			ClusterID: "c1",
			RootCA: store.RootCAMaterial{
				CAKey:       root.KeyPEM,
				CACert:      root.CertPEM,
				CACertHash:  root.DigestPrefix(),
				JoinSecrets: secrets,
			},
			CertValidity: time.Hour,
			CreatedAt:    time.Now().UTC(),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp := fsm.Apply(&raft.Log{Data: data}); resp != nil {
		t.Fatal(resp)
	}
	n := &node.Node{ID: "n1", Role: node.RoleWorker, Membership: node.MembershipAccepted}
	ndata, err := encodeCommand(opSaveNode, saveNodePayload{Node: n})
	if err != nil {
		t.Fatal(err)
	}
	if resp := fsm.Apply(&raft.Log{Data: ndata}); resp != nil {
		t.Fatal(resp)
	}

	snap, err := fsm.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	sink := &memSink{buf: &buf}
	if err := snap.Persist(sink); err != nil {
		t.Fatal(err)
	}

	fsm2 := NewFSM()
	if err := fsm2.Restore(io.NopCloser(bytes.NewReader(buf.Bytes()))); err != nil {
		t.Fatal(err)
	}
	got, err := fsm2.getRootCA()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got.KeyPEM, root.KeyPEM) {
		t.Fatal("restored key mismatch")
	}
	if _, err := fsm2.getNode("n1"); err != nil {
		t.Fatal(err)
	}
}

type memSink struct {
	buf    *bytes.Buffer
	closed bool
}

func (m *memSink) Write(p []byte) (int, error) { return m.buf.Write(p) }
func (m *memSink) Close() error                { m.closed = true; return nil }
func (m *memSink) ID() string                  { return "mem" }
func (m *memSink) Cancel() error               { return nil }

func freeAddr(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	ln.Close()
	return addr
}

func TestRaftStoreRootCAReplication(t *testing.T) {
	dir := t.TempDir()
	addrA := freeAddr(t)
	addrB := freeAddr(t)

	a, err := Open(Config{
		DataDir:       filepath.Join(dir, "a"),
		NodeID:        "manager-a",
		RaftAddr:      addrA,
		GRPCAdvertise: "127.0.0.1:7946",
		Bootstrap:     true,
	})
	if err != nil {
		t.Fatalf("open a: %v", err)
	}
	defer a.Close()

	root, err := ca.GenerateRootCA("cellar", ca.DefaultCAValidity)
	if err != nil {
		t.Fatal(err)
	}
	secrets, err := token.GenerateSecrets()
	if err != nil {
		t.Fatal(err)
	}
	if err := a.CreateCluster(context.Background(), store.ClusterConfig{
		ClusterID:    "cid",
		CertValidity: ca.DefaultNodeValidity,
		RootCA:       root,
		JoinSecrets:  secrets,
	}); err != nil {
		t.Fatalf("init: %v", err)
	}

	b, err := Open(Config{
		DataDir:       filepath.Join(dir, "b"),
		NodeID:        "manager-b",
		RaftAddr:      addrB,
		GRPCAdvertise: "127.0.0.1:7948",
		Bootstrap:     false,
	})
	if err != nil {
		t.Fatalf("open b: %v", err)
	}
	defer b.Close()

	if err := a.AddVoter(context.Background(), PeerInfo{
		NodeID:   "manager-b",
		RaftAddr: b.RaftAddr(),
		GRPCAddr: "127.0.0.1:7948",
	}); err != nil {
		t.Fatalf("add voter: %v", err)
	}

	deadline := time.Now().Add(15 * time.Second)
	for {
		got, err := b.GetRootCA(context.Background())
		if err == nil {
			if !bytes.Equal(got.CertPEM, root.CertPEM) || !bytes.Equal(got.KeyPEM, root.KeyPEM) {
				t.Fatal("follower RootCA PEMs do not match leader")
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("follower never received RootCA: %v", err)
		}
		time.Sleep(50 * time.Millisecond)
	}

	worker := &node.Node{
		ID:         "worker-1",
		Role:       node.RoleWorker,
		Membership: node.MembershipAccepted,
	}
	if err := a.SaveNode(context.Background(), worker); err != nil {
		t.Fatal(err)
	}
	deadline = time.Now().Add(10 * time.Second)
	for {
		n, err := b.GetNode(context.Background(), "worker-1")
		if err == nil {
			if n.Role != node.RoleWorker {
				t.Fatalf("role=%s", n.Role)
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("worker node not replicated: %v", err)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func TestRaftStoreNotLeader(t *testing.T) {
	dir := t.TempDir()
	a, err := Open(Config{
		DataDir:   filepath.Join(dir, "a"),
		NodeID:    "a",
		RaftAddr:  freeAddr(t),
		Bootstrap: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()

	b, err := Open(Config{
		DataDir:   filepath.Join(dir, "b"),
		NodeID:    "b",
		RaftAddr:  freeAddr(t),
		Bootstrap: false,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()

	if err := a.AddVoter(context.Background(), PeerInfo{
		NodeID:   "b",
		RaftAddr: b.RaftAddr(),
	}); err != nil {
		t.Fatal(err)
	}
	if err := b.WaitForLeader(10 * time.Second); err != nil {
		t.Fatal(err)
	}

	if b.IsLeader() {
		if !a.IsLeader() {
			root, _ := ca.GenerateRootCA("cellar", time.Hour)
			secrets, _ := token.GenerateSecrets()
			err := a.CreateCluster(context.Background(), store.ClusterConfig{
				ClusterID: "x", RootCA: root, JoinSecrets: secrets,
			})
			if err != store.ErrNotLeader {
				t.Fatalf("want ErrNotLeader, got %v", err)
			}
		}
		return
	}
	root, err := ca.GenerateRootCA("cellar", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	secrets, err := token.GenerateSecrets()
	if err != nil {
		t.Fatal(err)
	}
	err = b.CreateCluster(context.Background(), store.ClusterConfig{
		ClusterID: "x", RootCA: root, JoinSecrets: secrets,
	})
	if err != store.ErrNotLeader {
		t.Fatalf("want ErrNotLeader, got %v", err)
	}
}

func TestEncodeDecodeCommand(t *testing.T) {
	raw, err := encodeCommand(opSavePeer, savePeerPayload{Peer: PeerInfo{NodeID: "m1", RaftAddr: "1:2"}})
	if err != nil {
		t.Fatal(err)
	}
	cmd, err := decodeCommand(raw)
	if err != nil {
		t.Fatal(err)
	}
	if cmd.Op != opSavePeer {
		t.Fatalf("op=%s", cmd.Op)
	}
	var p savePeerPayload
	if err := json.Unmarshal(cmd.Payload, &p); err != nil {
		t.Fatal(err)
	}
	if p.Peer.NodeID != "m1" {
		t.Fatal(p.Peer)
	}
}

func TestClusterRedact(t *testing.T) {
	c := &store.Cluster{
		ClusterID: "c",
		RootCA: store.RootCAMaterial{
			CAKey:      []byte("secret"),
			CACert:     []byte("cert"),
			CACertHash: "hash",
			JoinSecrets: token.Secrets{Worker: "w", Manager: "m"},
		},
	}
	r := c.Redact()
	if len(r.RootCA.CAKey) != 0 {
		t.Fatal("CAKey not redacted")
	}
	if len(c.RootCA.CAKey) == 0 {
		t.Fatal("original mutated")
	}
}
