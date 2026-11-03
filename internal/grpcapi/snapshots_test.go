package grpcapi

import (
	"context"
	"testing"
	"time"

	cellarv1 "github.com/prodioslabs/cellar/api/gen"
	"github.com/prodioslabs/cellar/internal/apikey"
	"github.com/prodioslabs/cellar/internal/ca"
	"github.com/prodioslabs/cellar/internal/node"
	"github.com/prodioslabs/cellar/internal/raftstore"
	"github.com/prodioslabs/cellar/internal/sandbox"
	"github.com/prodioslabs/cellar/internal/store"
)

type memStore struct {
	sandboxes   map[string]*sandbox.Sandbox
	fsSnapshots map[string]*sandbox.FSSnapshot
	nodes       map[string]*node.Node
}

func newMemStore() *memStore {
	return &memStore{
		sandboxes:   map[string]*sandbox.Sandbox{},
		fsSnapshots: map[string]*sandbox.FSSnapshot{},
		nodes:       map[string]*node.Node{},
	}
}

func (m *memStore) CreateCluster(context.Context, store.ClusterConfig) error { return nil }
func (m *memStore) IsInitialized(context.Context) (bool, error)              { return true, nil }
func (m *memStore) GetCluster(context.Context) (*store.Cluster, error) {
	return nil, store.ErrNotInitialized
}
func (m *memStore) UpdateCluster(context.Context, *store.Cluster) error { return nil }
func (m *memStore) GetRootCA(context.Context) (*ca.RootCA, error) {
	return nil, store.ErrNotInitialized
}
func (m *memStore) GetNode(_ context.Context, id string) (*node.Node, error) {
	n, ok := m.nodes[id]
	if !ok {
		return nil, store.ErrNodeNotFound
	}
	return n, nil
}
func (m *memStore) SaveNode(_ context.Context, n *node.Node) error {
	m.nodes[n.ID] = n
	return nil
}
func (m *memStore) DeleteNode(_ context.Context, id string) error { delete(m.nodes, id); return nil }
func (m *memStore) ListNodes(context.Context) ([]*node.Node, error) {
	out := make([]*node.Node, 0, len(m.nodes))
	for _, n := range m.nodes {
		out = append(out, n)
	}
	return out, nil
}
func (m *memStore) SaveSandbox(_ context.Context, sb *sandbox.Sandbox) error {
	m.sandboxes[sb.ID] = sandbox.Clone(sb)
	return nil
}
func (m *memStore) DeleteSandbox(_ context.Context, id string) error {
	delete(m.sandboxes, id)
	return nil
}
func (m *memStore) GetSandbox(_ context.Context, id string) (*sandbox.Sandbox, error) {
	sb, ok := m.sandboxes[id]
	if !ok {
		return nil, store.ErrSandboxNotFound
	}
	return sandbox.Clone(sb), nil
}
func (m *memStore) GetSandboxByName(_ context.Context, name string) (*sandbox.Sandbox, error) {
	for _, sb := range m.sandboxes {
		if sb.Name == name {
			return sandbox.Clone(sb), nil
		}
	}
	return nil, store.ErrSandboxNotFound
}
func (m *memStore) ListSandboxes(context.Context) ([]*sandbox.Sandbox, error) {
	out := make([]*sandbox.Sandbox, 0, len(m.sandboxes))
	for _, sb := range m.sandboxes {
		out = append(out, sandbox.Clone(sb))
	}
	return out, nil
}
func (m *memStore) ListSandboxesByNode(_ context.Context, nodeID string) ([]*sandbox.Sandbox, error) {
	var out []*sandbox.Sandbox
	for _, sb := range m.sandboxes {
		if sb.NodeID == nodeID {
			out = append(out, sandbox.Clone(sb))
		}
	}
	return out, nil
}
func (m *memStore) SaveVolume(context.Context, *sandbox.Volume) error { return nil }
func (m *memStore) DeleteVolume(context.Context, string) error        { return nil }
func (m *memStore) GetVolume(context.Context, string) (*sandbox.Volume, error) {
	return nil, store.ErrVolumeNotFound
}
func (m *memStore) GetVolumeByName(context.Context, string) (*sandbox.Volume, error) {
	return nil, store.ErrVolumeNotFound
}
func (m *memStore) GetDefaultVolume(context.Context) (*sandbox.Volume, error) {
	return nil, store.ErrVolumeNotFound
}
func (m *memStore) ListVolumes(context.Context) ([]*sandbox.Volume, error) { return nil, nil }
func (m *memStore) SaveFSSnapshot(_ context.Context, snap *sandbox.FSSnapshot) error {
	m.fsSnapshots[snap.ID] = sandbox.CloneFSSnapshot(snap)
	return nil
}
func (m *memStore) DeleteFSSnapshot(_ context.Context, id string) error {
	delete(m.fsSnapshots, id)
	return nil
}
func (m *memStore) GetFSSnapshot(_ context.Context, id string) (*sandbox.FSSnapshot, error) {
	s, ok := m.fsSnapshots[id]
	if !ok {
		return nil, store.ErrFSSnapshotNotFound
	}
	return sandbox.CloneFSSnapshot(s), nil
}
func (m *memStore) ListFSSnapshots(context.Context) ([]*sandbox.FSSnapshot, error) {
	out := make([]*sandbox.FSSnapshot, 0, len(m.fsSnapshots))
	for _, s := range m.fsSnapshots {
		out = append(out, sandbox.CloneFSSnapshot(s))
	}
	return out, nil
}
func (m *memStore) ListFSSnapshotsBySandbox(_ context.Context, sandboxID string) ([]*sandbox.FSSnapshot, error) {
	var out []*sandbox.FSSnapshot
	for _, s := range m.fsSnapshots {
		if s.SandboxID == sandboxID {
			out = append(out, sandbox.CloneFSSnapshot(s))
		}
	}
	return out, nil
}
func (m *memStore) SaveAPIKey(context.Context, *apikey.Key) error { return nil }
func (m *memStore) DeleteAPIKey(context.Context, string) error    { return nil }
func (m *memStore) GetAPIKey(context.Context, string) (*apikey.Key, error) {
	return nil, store.ErrAPIKeyNotFound
}
func (m *memStore) GetAPIKeyByHash(context.Context, string) (*apikey.Key, error) {
	return nil, store.ErrAPIKeyNotFound
}
func (m *memStore) ListAPIKeys(context.Context) ([]*apikey.Key, error) { return nil, nil }

type fakeRaft struct{}

func (fakeRaft) IsLeader() bool                                     { return true }
func (fakeRaft) LeaderGRPC() string                                 { return "" }
func (fakeRaft) ListPeers() []raftstore.PeerInfo                    { return nil }
func (fakeRaft) AddVoter(context.Context, raftstore.PeerInfo) error { return nil }
func (fakeRaft) RemoveServer(string) error                          { return nil }
func (fakeRaft) IsVoter(string) bool                                { return true }
func (fakeRaft) DeletePeer(context.Context, string) error           { return nil }
func (fakeRaft) GRPCAdvertise() string                              { return "" }

type captureCall struct {
	nodeID, sandboxID, snapshotID, location string
}

type fakeSnapClient struct {
	calls []captureCall
}

func (f *fakeSnapClient) CaptureFilesystemSnapshot(_ context.Context, nodeID, sandboxID, snapshotID, location string) (string, int64, error) {
	f.calls = append(f.calls, captureCall{nodeID, sandboxID, snapshotID, location})
	return "sha256:abc", 12, nil
}
func (f *fakeSnapClient) RestoreFilesystemSnapshot(context.Context, string, string, string, string) error {
	return nil
}
func (f *fakeSnapClient) DeleteSnapshotBlob(context.Context, string, string) error { return nil }

func TestApplyObservedStatusCreatesAutomaticSnapshot(t *testing.T) {
	ms := newMemStore()
	sb := &sandbox.Sandbox{
		ID:           "sb1",
		Name:         "demo",
		NodeID:       "n1",
		DesiredState: sandbox.DesiredStopped,
		Status:       sandbox.Status{Phase: sandbox.PhaseRunning},
		Spec:         sandbox.Spec{Name: "demo", Image: sandbox.OCIImage("alpine"), Resources: sandbox.Resources{VCPUs: 1, MemoryMiB: 512}},
	}
	ms.sandboxes[sb.ID] = sandbox.Clone(sb)
	client := &fakeSnapClient{}
	srv := NewSandboxServer(ms, fakeRaft{}, nil)
	srv.SetSnapshotRuntime(client, "local")

	if err := srv.ApplyObservedStatus(context.Background(), sandbox.Clone(sb), sandbox.Status{
		Phase:     sandbox.PhaseStopped,
		StoppedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	if len(ms.fsSnapshots) != 1 {
		t.Fatalf("snapshots=%d", len(ms.fsSnapshots))
	}
	time.Sleep(20 * time.Millisecond)
	if len(client.calls) != 1 {
		t.Fatalf("capture calls=%d", len(client.calls))
	}

	// already stopped: no second snapshot
	cur, _ := ms.GetSandbox(context.Background(), "sb1")
	if err := srv.ApplyObservedStatus(context.Background(), cur, sandbox.Status{Phase: sandbox.PhaseStopped}); err != nil {
		t.Fatal(err)
	}
	if len(ms.fsSnapshots) != 1 {
		t.Fatalf("second snapshot created: %d", len(ms.fsSnapshots))
	}
}

func TestApplyObservedStatusSkipsEphemeral(t *testing.T) {
	ms := newMemStore()
	sb := &sandbox.Sandbox{
		ID:           "sb1",
		Name:         "eph",
		NodeID:       "n1",
		DesiredState: sandbox.DesiredStopped,
		Ephemeral:    true,
		Spec:         sandbox.Spec{Lifecycle: sandbox.LifecyclePolicy{Ephemeral: true}},
		Status:       sandbox.Status{Phase: sandbox.PhaseRunning},
	}
	ms.sandboxes[sb.ID] = sandbox.Clone(sb)
	srv := NewSandboxServer(ms, fakeRaft{}, nil)
	if err := srv.ApplyObservedStatus(context.Background(), sandbox.Clone(sb), sandbox.Status{Phase: sandbox.PhaseStopped}); err != nil {
		t.Fatal(err)
	}
	if len(ms.fsSnapshots) != 0 {
		t.Fatal("ephemeral should not snapshot")
	}
}

func TestApplyObservedStatusFlipsDesiredOnSessionTimeout(t *testing.T) {
	ms := newMemStore()
	idle := uint64(5)
	sb := &sandbox.Sandbox{
		ID:           "sb1",
		Name:         "demo",
		NodeID:       "n1",
		DesiredState: sandbox.DesiredRunning,
		Spec:         sandbox.Spec{Name: "demo", Lifecycle: sandbox.LifecyclePolicy{IdleTimeoutSecs: &idle}},
		Status:       sandbox.Status{Phase: sandbox.PhaseRunning},
	}
	ms.sandboxes[sb.ID] = sandbox.Clone(sb)
	client := &fakeSnapClient{}
	srv := NewSandboxServer(ms, fakeRaft{}, nil)
	srv.SetSnapshotRuntime(client, "local")
	if err := srv.ApplyObservedStatus(context.Background(), sandbox.Clone(sb), sandbox.Status{
		Phase:   sandbox.PhaseStopped,
		Message: "idle_timeout",
	}); err != nil {
		t.Fatal(err)
	}
	got, _ := ms.GetSandbox(context.Background(), "sb1")
	if got.DesiredState != sandbox.DesiredStopped {
		t.Fatalf("desired=%s", got.DesiredState)
	}
}

func TestApplyRetentionKeepsNewest(t *testing.T) {
	ms := newMemStore()
	count := uint32(1)
	del := true
	sb := &sandbox.Sandbox{
		ID:   "sb1",
		Name: "demo",
		Spec: sandbox.Spec{Lifecycle: sandbox.LifecyclePolicy{KeepLastSnapshots: &sandbox.KeepLastSnapshots{Count: count, DeleteEvicted: &del}}},
	}
	ms.sandboxes[sb.ID] = sb
	old := time.Now().UTC().Add(-time.Hour)
	newer := time.Now().UTC()
	ms.fsSnapshots["old"] = &sandbox.FSSnapshot{ID: "old", SandboxID: "sb1", Status: sandbox.FSSnapshotStatusCreated, CreatedAt: old}
	ms.fsSnapshots["new"] = &sandbox.FSSnapshot{ID: "new", SandboxID: "sb1", Status: sandbox.FSSnapshotStatusCreated, CreatedAt: newer}
	srv := NewSandboxServer(ms, fakeRaft{}, nil)
	srv.SetSnapshotRuntime(&fakeSnapClient{}, "local")
	srv.applyRetention(context.Background(), "sb1")
	if ms.fsSnapshots["old"].Status != sandbox.FSSnapshotStatusDeleted {
		t.Fatalf("old status=%s", ms.fsSnapshots["old"].Status)
	}
	if ms.fsSnapshots["new"].Status != sandbox.FSSnapshotStatusCreated {
		t.Fatalf("new status=%s", ms.fsSnapshots["new"].Status)
	}
}

func TestLocalRestorePin(t *testing.T) {
	ms := newMemStore()
	ms.fsSnapshots["snap"] = &sandbox.FSSnapshot{
		ID:       "snap",
		Status:   sandbox.FSSnapshotStatusCreated,
		Backend:  sandbox.FSSnapshotBackendLocal,
		NodeID:   "gone",
		SpecJSON: []byte(`{"name":"orig","image":{"type":"oci","reference":"alpine"},"resources":{"vcpus":1,"memory_mib":512}}`),
	}
	srv := NewSandboxServer(ms, fakeRaft{}, nil)
	_, err := srv.applyCreateFromSnapshot(context.Background(), &cellarv1.SandboxCreateRequest{
		SnapshotId: "snap",
	}, sandbox.Spec{Name: "new"})
	if err == nil {
		t.Fatal("expected FailedPrecondition when capturing node is gone")
	}
}
