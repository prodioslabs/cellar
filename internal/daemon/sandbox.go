package daemon

import (
	"context"
	"fmt"
	"io"
	"log"
	"sync"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	cellarv1 "github.com/prodioslabs/cellar/api/gen"
	"github.com/prodioslabs/cellar/internal/egress"
	"github.com/prodioslabs/cellar/internal/grpcapi"
	"github.com/prodioslabs/cellar/internal/node"
	"github.com/prodioslabs/cellar/internal/raftstore"
	"github.com/prodioslabs/cellar/internal/runtime"
	"github.com/prodioslabs/cellar/internal/sandbox"
	"github.com/prodioslabs/cellar/internal/scheduler"
)

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
	drv, err := runtime.NewDriver()
	if err != nil {
		d.runtimeErr = err
		log.Printf("docker runtime unavailable: %v (sandbox create will fail on this node)", err)
		return nil
	}
	if err := drv.Ping(ctx); err != nil {
		_ = drv.Close()
		d.runtimeErr = err
		log.Printf("docker ping failed: %v (sandbox create will fail on this node)", err)
		return nil
	}
	allocator, err := egress.NewAllocator(d.cfg.DataDir, d.cfg.EgressSupernet)
	if err != nil {
		_ = drv.Close()
		return fmt.Errorf("egress ipam: %w", err)
	}
	gwPool := egress.NewPool(drv.Client(), egress.PoolConfig{
		DataDir:           d.cfg.DataDir,
		Image:             d.cfg.EgressGatewayImage,
		MaxLegs:           d.cfg.EgressGatewayMaxLegs,
		PrivateExceptions: d.cfg.EgressAllowPrivate,
	})
	if err := gwPool.EnsureReady(ctx); err != nil {
		_ = drv.Close()
		d.runtimeErr = err
		log.Printf("egress gateway pool: %v (sandbox create will fail on this node)", err)
		return nil
	}
	mat := d.idStore.Material()
	if mat == nil {
		_ = drv.Close()
		return fmt.Errorf("no identity for runtime agent")
	}
	agent := runtime.NewAgent(mat.NodeID, drv, gwPool, allocator, &heartbeatSource{d: d}, &statusReporter{d: d}, d.cfg.DataDir, "")
	d.driver = drv
	d.gwPool = gwPool
	d.ipam = allocator
	d.agent = agent
	d.runtimeErr = nil
	d.runtimeSrv = grpcapi.NewRuntimeServer(agent)

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

// ensureSandboxCreateRuntime refuses create when no node can run sandboxes,
// surfacing Docker errors instead of leaving a pending sandbox.
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
		if err := drv.Ping(ctx); err != nil {
			return status.Error(codes.FailedPrecondition, err.Error())
		}
		// Local Docker is reachable; allow create before the first heartbeat.
		return nil
	}
	if runtimeErr != nil {
		return status.Error(codes.FailedPrecondition, runtimeErr.Error())
	}
	return status.Error(codes.FailedPrecondition, "no node with a live sandbox runtime")
}

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

	// Use last heartbeat response cache; heartbeatLoop refreshes it.
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
			ContainerId:          st.ContainerID,
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

// SandboxUpdateNetwork commits a new network policy and then pushes it to the
// node running the sandbox so it takes effect immediately.
func (d *Daemon) SandboxUpdateNetwork(ctx context.Context, req *cellarv1.SandboxUpdateNetworkRequest) (*cellarv1.SandboxUpdateNetworkResponse, error) {
	d.mu.Lock()
	raft := d.raft
	srv := d.sandboxServer
	d.mu.Unlock()

	var resp *cellarv1.SandboxUpdateNetworkResponse
	var err error
	if raft != nil && raft.IsLeader() && srv != nil {
		resp, err = srv.UpdateNetwork(grpcapi.WithInternalCall(ctx), req)
	} else {
		err = d.withManagerControl(func(addr string, cert, key, ca []byte) error {
			var cerr error
			resp, cerr = grpcapi.SandboxUpdateNetworkRemote(ctx, addr, cert, key, ca, req)
			return cerr
		})
	}
	if err != nil {
		return nil, err
	}
	d.pushNetworkPolicy(ctx, resp.Sandbox)
	return resp, nil
}

// pushNetworkPolicy applies a committed policy on the owning node. Failures are
// logged only: the agent's reconcile loop re-applies desired state every tick,
// so the push is a latency optimization, not the correctness path.
func (d *Daemon) pushNetworkPolicy(ctx context.Context, sb *cellarv1.Sandbox) {
	if sb == nil || sb.NodeId == "" {
		return
	}
	policy := sb.Spec.GetNetwork()
	mat := d.idStore.Material()
	if mat != nil && sb.NodeId == mat.NodeID {
		if d.agent != nil {
			if err := d.agent.ApplyNetworkPolicy(ctx, sb.Id, sandbox.NetworkPolicyFromProto(policy)); err != nil {
				log.Printf("sandbox %s: apply network policy: %v", sb.Id, err)
			}
		}
		return
	}
	if mat == nil {
		return
	}
	addr, err := d.lookupNodeRuntimeAddr(ctx, sb.NodeId)
	if err != nil {
		log.Printf("sandbox %s: apply network policy: %v", sb.Id, err)
		return
	}
	err = grpcapi.ApplyNetworkPolicyRemote(ctx, addr, mat.Certificate, mat.PrivateKey, mat.CACert,
		&cellarv1.ApplyNetworkPolicyRequest{SandboxId: sb.Id, Network: policy})
	if err != nil {
		log.Printf("sandbox %s: apply network policy on %s: %v", sb.Id, sb.NodeId, err)
	}
}

func (d *Daemon) SandboxLogs(req *cellarv1.SandboxLogsRequest, stream cellarv1.Control_SandboxLogsServer) error {
	sbResp, err := d.SandboxGet(stream.Context(), &cellarv1.SandboxGetRequest{SandboxId: req.SandboxId})
	if err != nil {
		return err
	}
	sb := sbResp.Sandbox
	mat := d.idStore.Material()
	if mat != nil && sb.NodeId == mat.NodeID && d.agent != nil {
		pr, pw := io.Pipe()
		errCh := make(chan error, 1)
		go func() {
			errCh <- d.agent.StreamLogs(stream.Context(), req.SandboxId, req.Follow, req.Tail, pw)
			_ = pw.Close()
		}()
		buf := make([]byte, 32*1024)
		for {
			n, rerr := pr.Read(buf)
			if n > 0 {
				if serr := stream.Send(&cellarv1.SandboxLogsChunk{Data: append([]byte(nil), buf[:n]...)}); serr != nil {
					return serr
				}
			}
			if rerr == io.EOF {
				break
			}
			if rerr != nil {
				return rerr
			}
		}
		if e := <-errCh; e != nil && e != io.EOF {
			return status.Error(codes.Internal, e.Error())
		}
		return nil
	}

	// Resolve owning node runtime addr from store/heartbeat.
	// After failover, Raft node_id is the fence for exec/logs: traffic goes only
	// to the current assignee. UpdateSandboxStatus additionally checks
	// assignment_generation so a returning former owner cannot overwrite status.
	addr, err := d.lookupNodeRuntimeAddr(stream.Context(), sb.NodeId)
	if err != nil {
		return err
	}
	if mat == nil {
		return fmt.Errorf("no identity")
	}
	pr, pw := io.Pipe()
	errCh := make(chan error, 1)
	go func() {
		errCh <- grpcapi.StreamRemoteLogs(stream.Context(), addr, mat.Certificate, mat.PrivateKey, mat.CACert, req, pw)
		_ = pw.Close()
	}()
	buf := make([]byte, 32*1024)
	for {
		n, rerr := pr.Read(buf)
		if n > 0 {
			if serr := stream.Send(&cellarv1.SandboxLogsChunk{Data: append([]byte(nil), buf[:n]...)}); serr != nil {
				return serr
			}
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			return rerr
		}
	}
	if e := <-errCh; e != nil && e != io.EOF {
		return e
	}
	return nil
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
		// Fall back to peer grpc for managers.
		for _, p := range raft.ListPeers() {
			if p.NodeID == nodeID && p.GRPCAddr != "" {
				return p.GRPCAddr, nil
			}
		}
	}
	// Ask leader via get — we need node list; use SandboxGet path's manager.
	return "", fmt.Errorf("runtime address for node %s unknown (waiting for heartbeat)", nodeID)
}

func (d *Daemon) SandboxExec(stream cellarv1.Control_SandboxExecServer) error {
	first, err := stream.Recv()
	if err != nil {
		return err
	}
	start := first.GetStart()
	if start == nil {
		return status.Error(codes.InvalidArgument, "first message must be start")
	}
	sbResp, err := d.SandboxGet(stream.Context(), &cellarv1.SandboxGetRequest{SandboxId: start.SandboxId})
	if err != nil {
		return err
	}
	mat := d.idStore.Material()
	if mat != nil && sbResp.Sandbox.NodeId == mat.NodeID && d.agent != nil {
		return d.proxyLocalExec(stream, first, start)
	}
	addr, err := d.lookupNodeRuntimeAddr(stream.Context(), sbResp.Sandbox.NodeId)
	if err != nil {
		return err
	}
	if mat == nil {
		return fmt.Errorf("no identity")
	}
	conn, err := grpcapi.DialRuntime(addr, mat.Certificate, mat.PrivateKey, mat.CACert)
	if err != nil {
		return err
	}
	defer conn.Close()
	remote, err := cellarv1.NewSandboxRuntimeClient(conn).Exec(stream.Context())
	if err != nil {
		return err
	}
	if err := remote.Send(first); err != nil {
		return err
	}
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			msg, err := remote.Recv()
			if err != nil {
				return
			}
			if err := stream.Send(msg); err != nil {
				return
			}
			if msg.GetExit() != nil {
				return
			}
		}
	}()
	for {
		msg, err := stream.Recv()
		if err == io.EOF {
			_ = remote.CloseSend()
			break
		}
		if err != nil {
			break
		}
		if err := remote.Send(msg); err != nil {
			break
		}
	}
	wg.Wait()
	return nil
}

func (d *Daemon) proxyLocalExec(stream cellarv1.Control_SandboxExecServer, first *cellarv1.SandboxExecMessage, start *cellarv1.SandboxExecStart) error {
	_ = first
	sess, err := d.agent.Exec(stream.Context(), start.SandboxId, start.Command, start.Tty, start.Stdin)
	if err != nil {
		return status.Error(codes.Internal, err.Error())
	}
	defer sess.Close()

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		buf := make([]byte, 32*1024)
		for {
			n, err := sess.Read(buf)
			if n > 0 {
				if serr := stream.Send(&cellarv1.SandboxExecMessage{
					Payload: &cellarv1.SandboxExecMessage_Stdout{Stdout: append([]byte(nil), buf[:n]...)},
				}); serr != nil {
					return
				}
			}
			if err != nil {
				return
			}
		}
	}()
	for {
		msg, err := stream.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			break
		}
		if b := msg.GetStdin(); len(b) > 0 {
			_, _ = sess.Write(b)
		}
		if msg.GetStdinClosed() {
			_ = sess.CloseWrite()
		}
	}
	wg.Wait()
	code, errMsg := sess.Wait()
	return stream.Send(&cellarv1.SandboxExecMessage{
		Payload: &cellarv1.SandboxExecMessage_Exit{Exit: &cellarv1.SandboxExecExit{
			ExitCode: int32(code),
			Error:    errMsg,
		}},
	})
}
