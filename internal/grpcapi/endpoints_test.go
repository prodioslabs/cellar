package grpcapi

import (
	"crypto/x509"
	"crypto/x509/pkix"
	"context"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/prodioslabs/cellar/internal/node"
	"github.com/prodioslabs/cellar/internal/raftstore"
)

type fakeRaftAdmin struct {
	leader      bool
	leaderGRPC  string
	advertise   string
	peers       []raftstore.PeerInfo
}

func (f *fakeRaftAdmin) IsLeader() bool                              { return f.leader }
func (f *fakeRaftAdmin) LeaderGRPC() string                          { return f.leaderGRPC }
func (f *fakeRaftAdmin) ListPeers() []raftstore.PeerInfo             { return f.peers }
func (f *fakeRaftAdmin) AddVoter(context.Context, raftstore.PeerInfo) error {
	return nil
}
func (f *fakeRaftAdmin) RemoveServer(string) error                   { return nil }
func (f *fakeRaftAdmin) IsVoter(string) bool                         { return true }
func (f *fakeRaftAdmin) DeletePeer(context.Context, string) error    { return nil }
func (f *fakeRaftAdmin) GRPCAdvertise() string                       { return f.advertise }

func TestManagerEndpoints(t *testing.T) {
	raft := &fakeRaftAdmin{
		leader:     true,
		leaderGRPC: "leader:17946",
		advertise:  "self:17946",
		peers: []raftstore.PeerInfo{
			{NodeID: "a", GRPCAddr: "leader:17946"},
			{NodeID: "b", GRPCAddr: "peer:17946"},
			{NodeID: "c", GRPCAddr: ""},
		},
	}
	leader, addrs := managerEndpoints(raft)
	if leader != "leader:17946" {
		t.Fatalf("leader=%q", leader)
	}
	want := []string{"leader:17946", "peer:17946", "self:17946"}
	if len(addrs) != len(want) {
		t.Fatalf("addrs=%v want %v", addrs, want)
	}
	for i := range want {
		if addrs[i] != want[i] {
			t.Fatalf("addrs[%d]=%q want %q", i, addrs[i], want[i])
		}
	}
}

func TestMergeManagerAddrs(t *testing.T) {
	got := MergeManagerAddrs("a:1", []string{"b:1", "a:1", ""}, []string{"c:1", "b:1"})
	want := []string{"a:1", "b:1", "c:1"}
	if len(got) != len(want) {
		t.Fatalf("got=%v want=%v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got=%v want=%v", got, want)
		}
	}
}

func TestRequireNodeOrManagerPeer(t *testing.T) {
	worker := &x509.Certificate{
		Subject: pkix.Name{
			CommonName:         "worker-1",
			OrganizationalUnit: []string{node.RoleWorker.OU()},
		},
	}
	manager := &x509.Certificate{
		Subject: pkix.Name{
			CommonName:         "mgr-1",
			OrganizationalUnit: []string{node.RoleManager.OU()},
		},
	}

	if err := requireNodeOrManagerPeer(worker, "worker-1"); err != nil {
		t.Fatalf("matching worker: %v", err)
	}
	if err := requireNodeOrManagerPeer(manager, "worker-1"); err != nil {
		t.Fatalf("manager forwarder: %v", err)
	}
	err := requireNodeOrManagerPeer(worker, "other-worker")
	if err == nil {
		t.Fatal("expected permission denied for mismatched worker")
	}
	st, ok := status.FromError(err)
	if !ok || st.Code() != codes.PermissionDenied {
		t.Fatalf("got %v", err)
	}
	if err := requireNodeOrManagerPeer(nil, "worker-1"); err == nil {
		t.Fatal("expected unauthenticated")
	}
}
