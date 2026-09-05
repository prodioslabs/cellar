package daemon

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	cellarv1 "github.com/prodioslabs/cellar/api/gen"
	"github.com/prodioslabs/cellar/internal/grpcapi"
	"github.com/prodioslabs/cellar/internal/node"
	"github.com/prodioslabs/cellar/internal/raftstore"
	"github.com/prodioslabs/cellar/internal/runtime"
	"github.com/prodioslabs/cellar/internal/sandbox"
	"github.com/prodioslabs/cellar/internal/scheduler"
)

// heartbeatSource is the agent's AssignmentSource. Create does not push a
// VM to the assigned node: SaveSandbox only commits desired state.
// The agent pulls assignments here on each reconcile tick.
type heartbeatSource struct {
	d *Daemon
}

func (h *heartbeatSource) ListAssigned(ctx context.Context) ([]*sandbox.Sandbox, error) {
	return h.d.fetchAssignments(ctx)
}

type statusReporter struct {
	d *Daemon
}

func (r *statusReporter) UpdateStatus(ctx context.Context, sandboxID string, generation int64, st sandbox.Status) error {
	return r.d.reportSandboxStatus(ctx, sandboxID, generation, st)
}

func (d *Daemon) startRuntimeLocked(ctx context.Context) error {
	if d.agent != nil {
		return nil
	}
	if err := runtime.EnsureInstalled(ctx); err != nil {
		d.runtimeErr = err
		log.Printf("microsandbox runtime unavailable: %v (sandbox create will fail on this node)", err)
		return nil
	}
	drv := runtime.NewDriver()
	mat := d.idStore.Material()
	if mat == nil {
		_ = drv.Close()
		return fmt.Errorf("no identity for runtime agent")
	}
	agent := runtime.NewAgent(mat.NodeID, drv, &heartbeatSource{d: d}, &statusReporter{d: d}, d.cfg.DataDir)
	d.driver = drv
	d.agent = agent
	d.runtimeErr = nil
	d.runtimeSrv = grpcapi.NewRuntimeServer(drv)

	d.clusterWG.Add(1)
	go func() {
		defer d.clusterWG.Done()
		agent.Run(ctx)
	}()
	d.clusterWG.Add(1)
	go func() {
		defer d.clusterWG.Done()
		d.heartbeatLoop(ctx)
	}()
	log.Printf("runtime agent started for node %s", mat.NodeID)
	return nil
}

// ensureSandboxCreateRuntime refuses create when no node can run sandboxes.
func (d *Daemon) ensureSandboxCreateRuntime(ctx context.Context, raft *raftstore.Store, drv *runtime.Driver, runtimeErr error) error {
	now := time.Now().UTC()
	nodes, err := raft.ListNodes(ctx)
	if err != nil {
		return err
	}
	for _, n := range nodes {
		if scheduler.IsNodeLive(n, now) {
			return nil
		}
	}
	if drv != nil {
		return nil
	}
	if runtimeErr != nil {
		return status.Error(codes.FailedPrecondition, runtimeErr.Error())
	}
	return status.Error(codes.FailedPrecondition, "no node with a live sandbox runtime")
}

// fetchAssignments returns sandboxes currently assigned to this node.
func (d *Daemon) fetchAssignments(ctx context.Context) ([]*sandbox.Sandbox, error) {
	d.mu.Lock()
	raft := d.raft
	cached := d.lastAssigned
	d.mu.Unlock()

	if raft != nil && raft.IsLeader() {
		mat := d.idStore.Material()
		if mat == nil {
			return nil, fmt.Errorf("no identity")
		}
		return raft.ListSandboxesByNode(ctx, mat.NodeID)
	}

	out := make([]*sandbox.Sandbox, 0, len(cached))
	for _, sb := range cached {
		out = append(out, sandbox.Clone(sb))
	}
	return out, nil
}

func (d *Daemon) heartbeatLoop(ctx context.Context) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		_ = d.sendHeartbeat(ctx)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (d *Daemon) sendHeartbeat(ctx context.Context) error {
	mat := d.idStore.Material()
	if mat == nil {
		return nil
	}
	state := d.idStore.State()
	grpcAddr := state.AdvertiseAddr
	if grpcAddr == "" {
		grpcAddr = state.ListenAddr
	}
	count := 0
	if d.agent != nil {
		assigned, _ := d.fetchAssignments(ctx)
		count = len(assigned)
	}

	d.mu.Lock()
	raft := d.raft
	d.mu.Unlock()

	if raft != nil && raft.IsLeader() {
		n, err := raft.GetNode(ctx, mat.NodeID)
		if err != nil {
			n = &node.Node{
				ID:         mat.NodeID,
				Role:       mat.Role,
				Membership: node.MembershipAccepted,
			}
		}
		n.RuntimeGRPCAddr = grpcAddr
		n.RuntimeHeartbeatAt = time.Now().UTC()
		n.RuntimeSandboxCount = count
		if err := raft.SaveNode(ctx, n); err != nil {
			return err
		}
		d.mu.Lock()
		sbSrv := d.sandboxServer
		d.mu.Unlock()
		if sbSrv != nil {
			sbSrv.Quarantine().NoteHeartbeat(mat.NodeID)
		}
		assigned, err := raft.ListSandboxesByNode(ctx, mat.NodeID)
		if err != nil {
			return err
		}
		protos := make([]*cellarv1.Sandbox, 0, len(assigned))
		for _, sb := range assigned {
			protos = append(protos, sandbox.ToProto(sb))
		}
		d.cacheAssigned(protos)
		d.maybeApplyDesiredRole(ctx, string(n.Role), false)
		return nil
	}

	var resp *cellarv1.RuntimeHeartbeatResponse
	err := d.forEachManager(func(addr string) error {
		var herr error
		resp, herr = grpcapi.RuntimeHeartbeatRemote(ctx, addr, mat.Certificate, mat.PrivateKey, mat.CACert, &cellarv1.RuntimeHeartbeatRequest{
			NodeId:       mat.NodeID,
			GrpcAddr:     grpcAddr,
			SandboxCount: int32(count),
		})
		return herr
	})
	if err != nil {
		return err
	}
	d.applyManagerEndpoints(resp.GetLeaderGrpc(), resp.GetManagerAddrs())
	d.cacheAssigned(resp.Assigned)
	d.maybeApplyDesiredRole(ctx, resp.DesiredRole, resp.Removed)
	return nil
}

func (d *Daemon) cacheAssigned(list []*cellarv1.Sandbox) {
	out := make([]*sandbox.Sandbox, 0, len(list))
	for _, p := range list {
		out = append(out, sandbox.FromProto(p))
	}
	d.mu.Lock()
	d.lastAssigned = out
	d.mu.Unlock()
}

func (d *Daemon) reportSandboxStatus(ctx context.Context, sandboxID string, generation int64, st sandbox.Status) error {
	mat := d.idStore.Material()
	if mat == nil {
		return fmt.Errorf("no identity")
	}

	d.mu.Lock()
	raft := d.raft
	d.mu.Unlock()

	if raft != nil && raft.IsLeader() {
		sb, err := raft.GetSandbox(ctx, sandboxID)
		if err != nil {
			return err
		}
		if sb.NodeID != "" && sb.NodeID != mat.NodeID {
			return fmt.Errorf("sandbox not assigned to this node")
		}
		if err := sandbox.CheckAssignmentGeneration(sb.AssignmentGeneration, generation); err != nil {
			return err
		}
		st.UpdatedAt = time.Now().UTC()
		sb.Status = st
		sb.UpdatedAt = st.UpdatedAt
		return raft.SaveSandbox(ctx, sb)
	}
	return d.forEachManager(func(addr string) error {
		return grpcapi.UpdateSandboxStatusRemote(ctx, addr, mat.Certificate, mat.PrivateKey, mat.CACert, &cellarv1.UpdateSandboxStatusRequest{
			SandboxId:            sandboxID,
			Status:               sandbox.StatusToProto(st),
			AssignmentGeneration: generation,
		})
	})
}

func (d *Daemon) withManagerControl(fn func(addr string, cert, key, ca []byte) error) error {
	mat := d.idStore.Material()
	if mat == nil {
		return fmt.Errorf("node not joined")
	}
	return d.forEachManager(func(addr string) error {
		return fn(addr, mat.Certificate, mat.PrivateKey, mat.CACert)
	})
}

func (d *Daemon) SandboxCreate(ctx context.Context, req *cellarv1.SandboxCreateRequest) (*cellarv1.SandboxCreateResponse, error) {
	d.mu.Lock()
	raft := d.raft
	sb := d.sandboxServer
	drv := d.driver
	runtimeErr := d.runtimeErr
	d.mu.Unlock()
	if raft != nil && raft.IsLeader() && sb != nil {
		if err := d.ensureSandboxCreateRuntime(ctx, raft, drv, runtimeErr); err != nil {
			return nil, err
		}
		return sb.Create(grpcapi.WithInternalCall(ctx), req)
	}
	var resp *cellarv1.SandboxCreateResponse
	err := d.withManagerControl(func(addr string, cert, key, ca []byte) error {
		var cerr error
		resp, cerr = grpcapi.SandboxCreateRemote(ctx, addr, cert, key, ca, req)
		return cerr
	})
	return resp, err
}

func (d *Daemon) SandboxStart(ctx context.Context, req *cellarv1.SandboxStartRequest) (*cellarv1.SandboxStartResponse, error) {
	d.mu.Lock()
	raft := d.raft
	sb := d.sandboxServer
	d.mu.Unlock()
	if raft != nil && raft.IsLeader() && sb != nil {
		return sb.Start(grpcapi.WithInternalCall(ctx), req)
	}
	var resp *cellarv1.SandboxStartResponse
	err := d.withManagerControl(func(addr string, cert, key, ca []byte) error {
		var cerr error
		resp, cerr = grpcapi.SandboxStartRemote(ctx, addr, cert, key, ca, req.SandboxId)
		return cerr
	})
	return resp, err
}

func (d *Daemon) SandboxStop(ctx context.Context, req *cellarv1.SandboxStopRequest) (*cellarv1.SandboxStopResponse, error) {
	d.mu.Lock()
	raft := d.raft
	sb := d.sandboxServer
	d.mu.Unlock()
	if raft != nil && raft.IsLeader() && sb != nil {
		return sb.Stop(grpcapi.WithInternalCall(ctx), req)
	}
	var resp *cellarv1.SandboxStopResponse
	err := d.withManagerControl(func(addr string, cert, key, ca []byte) error {
		var cerr error
		resp, cerr = grpcapi.SandboxStopRemote(ctx, addr, cert, key, ca, req.SandboxId)
		return cerr
	})
	return resp, err
}

func (d *Daemon) SandboxRemove(ctx context.Context, req *cellarv1.SandboxRemoveRequest) (*cellarv1.SandboxRemoveResponse, error) {
	d.mu.Lock()
	raft := d.raft
	sb := d.sandboxServer
	d.mu.Unlock()
	if raft != nil && raft.IsLeader() && sb != nil {
		return sb.Remove(grpcapi.WithInternalCall(ctx), req)
	}
	err := d.withManagerControl(func(addr string, cert, key, ca []byte) error {
		return grpcapi.SandboxRemoveRemote(ctx, addr, cert, key, ca, req.SandboxId)
	})
	if err != nil {
		return nil, err
	}
	return &cellarv1.SandboxRemoveResponse{}, nil
}

func (d *Daemon) SandboxGet(ctx context.Context, req *cellarv1.SandboxGetRequest) (*cellarv1.SandboxGetResponse, error) {
	d.mu.Lock()
	raft := d.raft
	sb := d.sandboxServer
	d.mu.Unlock()
	if raft != nil && sb != nil {
		return sb.Get(grpcapi.WithInternalCall(ctx), req)
	}
	var resp *cellarv1.SandboxGetResponse
	err := d.withManagerControl(func(addr string, cert, key, ca []byte) error {
		var cerr error
		resp, cerr = grpcapi.SandboxGetRemote(ctx, addr, cert, key, ca, req.SandboxId)
		return cerr
	})
	return resp, err
}

func (d *Daemon) SandboxGetByName(ctx context.Context, req *cellarv1.SandboxGetByNameRequest) (*cellarv1.SandboxGetResponse, error) {
	d.mu.Lock()
	raft := d.raft
	sb := d.sandboxServer
	d.mu.Unlock()
	if raft != nil && sb != nil {
		return sb.GetByName(grpcapi.WithInternalCall(ctx), req)
	}
	var resp *cellarv1.SandboxGetResponse
	err := d.withManagerControl(func(addr string, cert, key, ca []byte) error {
		var cerr error
		resp, cerr = grpcapi.SandboxGetByNameRemote(ctx, addr, cert, key, ca, req.Name)
		return cerr
	})
	return resp, err
}

func (d *Daemon) SandboxList(ctx context.Context, req *cellarv1.SandboxListRequest) (*cellarv1.SandboxListResponse, error) {
	d.mu.Lock()
	raft := d.raft
	sb := d.sandboxServer
	d.mu.Unlock()
	if raft != nil && sb != nil {
		return sb.List(grpcapi.WithInternalCall(ctx), req)
	}
	var resp *cellarv1.SandboxListResponse
	err := d.withManagerControl(func(addr string, cert, key, ca []byte) error {
		var cerr error
		resp, cerr = grpcapi.SandboxListRemote(ctx, addr, cert, key, ca)
		return cerr
	})
	return resp, err
}

func (d *Daemon) SandboxLogs(req *cellarv1.SandboxLogsRequest, stream cellarv1.Control_SandboxLogsServer) error {
	sbResp, err := d.SandboxGet(stream.Context(), &cellarv1.SandboxGetRequest{SandboxId: req.SandboxId})
	if err != nil {
		return err
	}
	sb := sbResp.Sandbox
	mat := d.idStore.Material()
	if mat != nil && sb.NodeId == mat.NodeID && d.driver != nil {
		var sources []string
		if req.Sources != "" {
			for _, s := range strings.Split(req.Sources, ",") {
				if t := strings.TrimSpace(s); t != "" {
					sources = append(sources, t)
				}
			}
		}
		return d.driver.FollowLogs(stream.Context(), req.SandboxId, runtime.LogFollowOptions{
			Follow:     req.Follow,
			Sources:    sources,
			FromCursor: req.LastEventId,
		}, func(e runtime.LogEntry) error {
			return stream.Send(&cellarv1.SandboxLogsChunk{
				Id:         e.ID,
				Source:     e.Source,
				TsUnixNano: e.Timestamp.UnixNano(),
				Text:       e.Text,
			})
		})
	}

	addr, err := d.lookupNodeRuntimeAddr(stream.Context(), sb.NodeId)
	if err != nil {
		return err
	}
	if mat == nil {
		return fmt.Errorf("no identity")
	}
	return grpcapi.StreamRemoteLogs(stream.Context(), addr, mat.Certificate, mat.PrivateKey, mat.CACert, req, stream.Send)
}

func (d *Daemon) lookupNodeRuntimeAddr(ctx context.Context, nodeID string) (string, error) {
	d.mu.Lock()
	raft := d.raft
	d.mu.Unlock()
	if raft != nil {
		n, err := raft.GetNode(ctx, nodeID)
		if err == nil && n.RuntimeGRPCAddr != "" {
			return n.RuntimeGRPCAddr, nil
		}
		for _, p := range raft.ListPeers() {
			if p.NodeID == nodeID && p.GRPCAddr != "" {
				return p.GRPCAddr, nil
			}
		}
	}
	return "", fmt.Errorf("runtime address for node %s unknown (waiting for heartbeat)", nodeID)
}
