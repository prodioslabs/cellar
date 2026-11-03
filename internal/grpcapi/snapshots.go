package grpcapi

import (
	"context"
	"errors"
	"log"
	"sort"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	cellarv1 "github.com/prodioslabs/cellar/api/gen"
	"github.com/prodioslabs/cellar/internal/node"
	"github.com/prodioslabs/cellar/internal/sandbox"
	"github.com/prodioslabs/cellar/internal/scheduler"
	"github.com/prodioslabs/cellar/internal/store"
)

// SnapshotNodeClient RPCs filesystem snapshot I/O on the owning node.
type SnapshotNodeClient interface {
	CaptureFilesystemSnapshot(ctx context.Context, nodeID, sandboxID, snapshotID, location string) (digest string, size int64, err error)
	RestoreFilesystemSnapshot(ctx context.Context, nodeID, sandboxID, snapshotID, location string) error
	DeleteSnapshotBlob(ctx context.Context, nodeID, location string) error
}

// SetSnapshotRuntime wires node snapshot RPCs and the configured blob backend name.
func (s *SandboxServer) SetSnapshotRuntime(c SnapshotNodeClient, backend string) {
	if s == nil {
		return
	}
	s.snapshots = c
	if backend == "" {
		backend = string(sandbox.FSSnapshotBackendLocal)
	}
	s.blobBackend = backend
}

func (s *SandboxServer) Update(ctx context.Context, req *cellarv1.SandboxUpdateRequest) (*cellarv1.SandboxUpdateResponse, error) {
	if err := requireClusterPeer(ctx); err != nil {
		return nil, err
	}
	if err := s.requireLeader(); err != nil {
		return nil, err
	}
	if req.SandboxId == "" {
		return nil, status.Error(codes.InvalidArgument, "sandbox_id required")
	}
	sb, err := s.store.GetSandbox(ctx, req.SandboxId)
	if err != nil {
		return nil, mapStoreErr(err)
	}
	if req.SnapshotExpirationMs != nil {
		v := *req.SnapshotExpirationMs
		if v < 0 {
			return nil, status.Error(codes.InvalidArgument, "snapshot_expiration_ms must be >= 0")
		}
		sb.Spec.Lifecycle.SnapshotExpirationMs = &v
	}
	if req.KeepLastSnapshots != nil || req.KeepLastExpirationMs != nil || req.KeepLastDeleteEvicted != nil {
		k := sandbox.CloneKeepLastSnapshots(sb.Spec.Lifecycle.KeepLastSnapshots)
		if k == nil {
			k = &sandbox.KeepLastSnapshots{Count: 1}
		}
		if req.KeepLastSnapshots != nil {
			k.Count = *req.KeepLastSnapshots
		}
		if req.KeepLastExpirationMs != nil {
			v := *req.KeepLastExpirationMs
			k.ExpirationMs = &v
		}
		if req.KeepLastDeleteEvicted != nil {
			v := *req.KeepLastDeleteEvicted
			k.DeleteEvicted = &v
		}
		if err := sandbox.ValidateKeepLastSnapshots(k); err != nil {
			return nil, status.Error(codes.InvalidArgument, err.Error())
		}
		sb.Spec.Lifecycle.KeepLastSnapshots = k
	}
	sb.UpdatedAt = time.Now().UTC()
	if err := s.store.SaveSandbox(ctx, sb); err != nil {
		return nil, mapStoreErr(err)
	}
	return &cellarv1.SandboxUpdateResponse{Sandbox: sandbox.ToProto(sb)}, nil
}

func (s *SandboxServer) CreateSnapshot(ctx context.Context, req *cellarv1.SnapshotCreateRequest) (*cellarv1.SnapshotCreateResponse, error) {
	if err := requireClusterPeer(ctx); err != nil {
		return nil, err
	}
	if err := s.requireLeader(); err != nil {
		return nil, err
	}
	if !req.Stop {
		return nil, status.Error(codes.InvalidArgument, "--stop is required to snapshot a sandbox")
	}
	if req.SandboxId == "" {
		return nil, status.Error(codes.InvalidArgument, "sandbox_id required")
	}
	sb, err := s.resolveSandbox(ctx, req.SandboxId)
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	exp := sb.Spec.Lifecycle.EffectiveSnapshotExpirationMs()
	if req.ExpirationMs != nil {
		if *req.ExpirationMs < 0 {
			return nil, status.Error(codes.InvalidArgument, "expiration_ms must be >= 0")
		}
		exp = *req.ExpirationMs
	}
	snap, err := s.newFSSnapshot(sb, sandbox.FSSnapshotKindManual, exp, now)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	sb.DesiredState = sandbox.DesiredStopped
	sb.UpdatedAt = now
	if err := s.store.SaveSandbox(ctx, sb); err != nil {
		return nil, mapStoreErr(err)
	}
	if err := s.store.SaveFSSnapshot(ctx, snap); err != nil {
		return nil, mapStoreErr(err)
	}
	if sb.Status.Phase == sandbox.PhaseStopped {
		s.kickCapture(sb, snap)
	}
	return &cellarv1.SnapshotCreateResponse{Snapshot: sandbox.FSSnapshotToProto(snap)}, nil
}

func (s *SandboxServer) GetSnapshot(ctx context.Context, req *cellarv1.SnapshotGetRequest) (*cellarv1.SnapshotGetResponse, error) {
	if err := requireClusterPeer(ctx); err != nil {
		return nil, err
	}
	if req.SnapshotId == "" {
		return nil, status.Error(codes.InvalidArgument, "snapshot_id required")
	}
	snap, err := s.store.GetFSSnapshot(ctx, req.SnapshotId)
	if err != nil {
		return nil, mapStoreErr(err)
	}
	return &cellarv1.SnapshotGetResponse{Snapshot: sandbox.FSSnapshotToProto(snap)}, nil
}

func (s *SandboxServer) ListSnapshots(ctx context.Context, req *cellarv1.SnapshotListRequest) (*cellarv1.SnapshotListResponse, error) {
	if err := requireClusterPeer(ctx); err != nil {
		return nil, err
	}
	var list []*sandbox.FSSnapshot
	var err error
	if req.SandboxId != "" {
		list, err = s.store.ListFSSnapshotsBySandbox(ctx, req.SandboxId)
	} else {
		list, err = s.store.ListFSSnapshots(ctx)
	}
	if err != nil {
		return nil, mapStoreErr(err)
	}
	out := &cellarv1.SnapshotListResponse{}
	for _, snap := range list {
		if req.SandboxName != "" && snap.SandboxName != req.SandboxName {
			continue
		}
		if !req.IncludeDeleted && snap.Status == sandbox.FSSnapshotStatusDeleted {
			continue
		}
		out.Snapshots = append(out.Snapshots, sandbox.FSSnapshotToProto(snap))
	}
	sort.Slice(out.Snapshots, func(i, j int) bool {
		return out.Snapshots[i].CreatedAtUnixNano > out.Snapshots[j].CreatedAtUnixNano
	})
	return out, nil
}

func (s *SandboxServer) DeleteSnapshot(ctx context.Context, req *cellarv1.SnapshotDeleteRequest) (*cellarv1.SnapshotDeleteResponse, error) {
	if err := requireClusterPeer(ctx); err != nil {
		return nil, err
	}
	if err := s.requireLeader(); err != nil {
		return nil, err
	}
	if req.SnapshotId == "" {
		return nil, status.Error(codes.InvalidArgument, "snapshot_id required")
	}
	snap, err := s.store.GetFSSnapshot(ctx, req.SnapshotId)
	if err != nil {
		return nil, mapStoreErr(err)
	}
	s.deleteSnapshotBlob(ctx, snap)
	now := time.Now().UTC()
	snap.Status = sandbox.FSSnapshotStatusDeleted
	snap.UpdatedAt = now
	if err := s.store.SaveFSSnapshot(ctx, snap); err != nil {
		return nil, mapStoreErr(err)
	}
	return &cellarv1.SnapshotDeleteResponse{Message: "snapshot deleted"}, nil
}

func (s *SandboxServer) resolveSandbox(ctx context.Context, idOrName string) (*sandbox.Sandbox, error) {
	sb, err := s.store.GetSandbox(ctx, idOrName)
	if err == nil {
		return sb, nil
	}
	if !errors.Is(err, store.ErrSandboxNotFound) {
		return nil, mapStoreErr(err)
	}
	sb, err = s.store.GetSandboxByName(ctx, idOrName)
	if err != nil {
		return nil, mapStoreErr(err)
	}
	return sb, nil
}

func (s *SandboxServer) newFSSnapshot(sb *sandbox.Sandbox, kind sandbox.FSSnapshotKind, expirationMs int64, now time.Time) (*sandbox.FSSnapshot, error) {
	id, err := sandbox.NewID()
	if err != nil {
		return nil, err
	}
	specJSON, err := sandbox.SpecToJSON(sb.Spec)
	if err != nil {
		return nil, err
	}
	backend := sandbox.FSSnapshotBackend(s.blobBackend)
	if backend == "" {
		backend = sandbox.FSSnapshotBackendLocal
	}
	name := sb.Name
	if name == "" {
		name = sb.Spec.Name
	}
	snap := &sandbox.FSSnapshot{
		ID:           id,
		SandboxID:    sb.ID,
		SandboxName:  name,
		NodeID:       sb.NodeID,
		Kind:         kind,
		Status:       sandbox.FSSnapshotStatusCreating,
		Backend:      backend,
		Location:     sandbox.SnapshotObjectKey(name, id),
		SpecJSON:     specJSON,
		ExpirationMs: expirationMs,
		LastUsedAt:   now,
		ExpiresAt:    sandbox.ComputeExpiresAt(now, expirationMs),
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	return snap, nil
}

// ApplyObservedStatus saves agent-reported status and may flip desired_state
// and start an automatic snapshot. Used by UpdateSandboxStatus and the local
// leader path in cellard.
func (s *SandboxServer) ApplyObservedStatus(ctx context.Context, sb *sandbox.Sandbox, st sandbox.Status) error {
	if sb == nil {
		return status.Error(codes.InvalidArgument, "sandbox is required")
	}
	prevPhase := sb.Status.Phase
	st.UpdatedAt = time.Now().UTC()
	if st.StartedAt.IsZero() && !sb.Status.StartedAt.IsZero() && st.Phase == sandbox.PhaseRunning {
		st.StartedAt = sb.Status.StartedAt
	}
	sb.Status = st
	sb.UpdatedAt = st.UpdatedAt

	if st.Phase == sandbox.PhaseStopped && sb.DesiredState == sandbox.DesiredRunning && sb.Spec.Lifecycle.HasSessionTimers() {
		sb.DesiredState = sandbox.DesiredStopped
	}

	if err := s.store.SaveSandbox(ctx, sb); err != nil {
		return mapStoreErr(err)
	}
	s.maybeSnapshotAfterStop(ctx, sb, prevPhase)
	return nil
}

func (s *SandboxServer) maybeSnapshotAfterStop(ctx context.Context, sb *sandbox.Sandbox, prevPhase sandbox.StatusPhase) {
	if sb.Status.Phase != sandbox.PhaseStopped {
		return
	}
	pending, err := s.creatingSnapshot(ctx, sb.ID)
	if err != nil {
		log.Printf("snapshots: list creating: %v", err)
		return
	}
	if pending != nil {
		s.kickCapture(sb, pending)
		return
	}
	if prevPhase == sandbox.PhaseStopped {
		return
	}
	if sb.Ephemeral || sb.Spec.Lifecycle.Ephemeral {
		return
	}
	now := time.Now().UTC()
	snap, err := s.newFSSnapshot(sb, sandbox.FSSnapshotKindAutomatic, sb.Spec.Lifecycle.EffectiveSnapshotExpirationMs(), now)
	if err != nil {
		log.Printf("snapshots: allocate automatic: %v", err)
		return
	}
	if err := s.store.SaveFSSnapshot(ctx, snap); err != nil {
		log.Printf("snapshots: save automatic: %v", err)
		return
	}
	s.kickCapture(sb, snap)
}

func (s *SandboxServer) creatingSnapshot(ctx context.Context, sandboxID string) (*sandbox.FSSnapshot, error) {
	list, err := s.store.ListFSSnapshotsBySandbox(ctx, sandboxID)
	if err != nil {
		return nil, err
	}
	var newest *sandbox.FSSnapshot
	for _, snap := range list {
		if snap.Status != sandbox.FSSnapshotStatusCreating {
			continue
		}
		if newest == nil || snap.CreatedAt.After(newest.CreatedAt) {
			newest = snap
		}
	}
	return newest, nil
}

func (s *SandboxServer) kickCapture(sb *sandbox.Sandbox, snap *sandbox.FSSnapshot) {
	if s.snapshots == nil || sb == nil || snap == nil {
		return
	}
	nodeID := snap.NodeID
	if nodeID == "" {
		nodeID = sb.NodeID
	}
	if nodeID == "" {
		s.failSnapshot(context.Background(), snap, "sandbox has no owning node")
		return
	}
	go s.runCapture(nodeID, sb.ID, snap)
}

func (s *SandboxServer) runCapture(nodeID, sandboxID string, snap *sandbox.FSSnapshot) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	digest, size, err := s.snapshots.CaptureFilesystemSnapshot(ctx, nodeID, sandboxID, snap.ID, snap.Location)
	now := time.Now().UTC()
	if err != nil {
		s.failSnapshot(ctx, snap, err.Error())
		return
	}
	cur, getErr := s.store.GetFSSnapshot(ctx, snap.ID)
	if getErr != nil {
		log.Printf("snapshots: get after capture %s: %v", snap.ID, getErr)
		return
	}
	cur.Status = sandbox.FSSnapshotStatusCreated
	cur.Digest = digest
	cur.SizeBytes = size
	cur.NodeID = nodeID
	cur.UpdatedAt = now
	if err := s.store.SaveFSSnapshot(ctx, cur); err != nil {
		log.Printf("snapshots: save created %s: %v", snap.ID, err)
		return
	}
	s.applyRetention(ctx, cur.SandboxID)
}

func (s *SandboxServer) failSnapshot(ctx context.Context, snap *sandbox.FSSnapshot, msg string) {
	if snap == nil {
		return
	}
	cur, err := s.store.GetFSSnapshot(ctx, snap.ID)
	if err != nil {
		cur = snap
	}
	cur.Status = sandbox.FSSnapshotStatusFailed
	cur.Message = msg
	cur.UpdatedAt = time.Now().UTC()
	if err := s.store.SaveFSSnapshot(ctx, cur); err != nil {
		log.Printf("snapshots: save failed %s: %v", snap.ID, err)
	}
}

func (s *SandboxServer) applyRetention(ctx context.Context, sandboxID string) {
	sb, err := s.store.GetSandbox(ctx, sandboxID)
	if err != nil {
		return
	}
	keep := sb.Spec.Lifecycle.KeepLastSnapshots
	if keep == nil || keep.Count == 0 {
		return
	}
	list, err := s.store.ListFSSnapshotsBySandbox(ctx, sandboxID)
	if err != nil {
		return
	}
	var live []*sandbox.FSSnapshot
	for _, snap := range list {
		if snap.Status == sandbox.FSSnapshotStatusCreated || snap.Status == sandbox.FSSnapshotStatusCreating {
			live = append(live, snap)
		}
	}
	sort.Slice(live, func(i, j int) bool {
		return live[i].CreatedAt.After(live[j].CreatedAt)
	})
	if uint32(len(live)) <= keep.Count {
		return
	}
	evicted := live[keep.Count:]
	if !keep.ShouldDeleteEvicted() {
		return
	}
	now := time.Now().UTC()
	for _, snap := range evicted {
		s.deleteSnapshotBlob(ctx, snap)
		snap.Status = sandbox.FSSnapshotStatusDeleted
		snap.Message = "evicted by keep_last_snapshots"
		snap.UpdatedAt = now
		_ = s.store.SaveFSSnapshot(ctx, snap)
	}
}

func (s *SandboxServer) deleteSnapshotBlob(ctx context.Context, snap *sandbox.FSSnapshot) {
	if s.snapshots == nil || snap == nil || snap.Location == "" {
		return
	}
	nodeID := snap.NodeID
	if err := s.snapshots.DeleteSnapshotBlob(ctx, nodeID, snap.Location); err != nil {
		log.Printf("snapshots: delete blob %s: %v", snap.ID, err)
	}
}

// ReapExpiredFSSnapshots marks expired created snapshots deleted and removes tars.
func (s *SandboxServer) ReapExpiredFSSnapshots(ctx context.Context) {
	if s == nil {
		return
	}
	list, err := s.store.ListFSSnapshots(ctx)
	if err != nil {
		log.Printf("snapshots: reaper list: %v", err)
		return
	}
	now := time.Now().UTC()
	for _, snap := range list {
		if snap.Status != sandbox.FSSnapshotStatusCreated {
			continue
		}
		if !snap.IsExpired(now) {
			continue
		}
		s.deleteSnapshotBlob(ctx, snap)
		snap.Status = sandbox.FSSnapshotStatusDeleted
		snap.Message = "expired"
		snap.UpdatedAt = now
		if err := s.store.SaveFSSnapshot(ctx, snap); err != nil {
			log.Printf("snapshots: reaper save %s: %v", snap.ID, err)
		}
	}
}

func (s *SandboxServer) applyCreateFromSnapshot(ctx context.Context, req *cellarv1.SandboxCreateRequest, spec sandbox.Spec) (*sandbox.Sandbox, error) {
	snap, err := s.store.GetFSSnapshot(ctx, req.SnapshotId)
	if err != nil {
		return nil, mapStoreErr(err)
	}
	if snap.Status != sandbox.FSSnapshotStatusCreated {
		return nil, status.Errorf(codes.FailedPrecondition, "snapshot %s is %s", snap.ID, snap.Status)
	}
	frozen, err := sandbox.SpecFromJSON(snap.SpecJSON)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	if spec.Name != "" {
		frozen.Name = spec.Name
	}
	if frozen.Name == "" {
		return nil, status.Error(codes.InvalidArgument, "name is required")
	}
	if existing, err := s.store.GetSandboxByName(ctx, frozen.Name); err == nil && existing != nil {
		return nil, status.Error(codes.AlreadyExists, sandbox.ErrNameExists.Error())
	} else if err != nil && !errors.Is(err, store.ErrSandboxNotFound) {
		return nil, mapStoreErr(err)
	}
	frozen = sandbox.NormalizeSpec(frozen)
	if err := sandbox.ValidateSpec(frozen); err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}

	now := time.Now().UTC()
	nodes, err := s.store.ListNodes(ctx)
	if err != nil {
		return nil, mapStoreErr(err)
	}
	all, err := s.store.ListSandboxes(ctx)
	if err != nil {
		return nil, mapStoreErr(err)
	}
	nodeID := scheduler.SelectNodeOpts(nodes, all, now, scheduler.SelectOpts{
		ExcludeNodeIDs: s.quarantine.Excluded(),
	})
	if snap.Backend == sandbox.FSSnapshotBackendLocal {
		if snap.NodeID == "" || !nodeLive(nodes, snap.NodeID, now) {
			return nil, status.Error(codes.FailedPrecondition, "local snapshot can only be restored on the capturing node")
		}
		nodeID = snap.NodeID
	}

	id := req.SandboxId
	if id == "" {
		id, err = sandbox.NewID()
		if err != nil {
			return nil, status.Error(codes.Internal, err.Error())
		}
	}
	desired := sandbox.DesiredStopped
	phase := sandbox.PhaseCreated
	if req.Start {
		desired = sandbox.DesiredRunning
	}
	sb := &sandbox.Sandbox{
		ID:                   id,
		Name:                 frozen.Name,
		Slug:                 frozen.Name,
		Spec:                 frozen,
		NodeID:               nodeID,
		DesiredState:         desired,
		AssignmentGeneration: 1,
		Ephemeral:            frozen.Lifecycle.Ephemeral,
		Labels:               frozen.Labels,
		FromSnapshotID:       snap.ID,
		FromSnapshotLocation: snap.Location,
		Status: sandbox.Status{
			Phase:     phase,
			UpdatedAt: now,
		},
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := s.store.SaveSandbox(ctx, sb); err != nil {
		return nil, mapStoreErr(err)
	}
	snap.TouchUsage(now)
	if err := s.store.SaveFSSnapshot(ctx, snap); err != nil {
		log.Printf("snapshots: touch last used %s: %v", snap.ID, err)
	}
	if s.snapshots != nil && nodeID != "" {
		go func() {
			rctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
			defer cancel()
			if err := s.snapshots.RestoreFilesystemSnapshot(rctx, nodeID, sb.ID, snap.ID, snap.Location); err != nil {
				log.Printf("snapshots: restore %s onto %s: %v", snap.ID, sb.ID, err)
			}
		}()
	}
	return sb, nil
}

func nodeLive(nodes []*node.Node, nodeID string, now time.Time) bool {
	for _, n := range nodes {
		if n != nil && n.ID == nodeID && scheduler.IsNodeLive(n, now) {
			return true
		}
	}
	return false
}
