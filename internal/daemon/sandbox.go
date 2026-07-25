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
	"github.com/prodioslabs/cellar/internal/runtime"
	"github.com/prodioslabs/cellar/internal/sandbox"
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

func (r *statusReporter) UpdateStatus(ctx context.Context, sandboxID string, st sandbox.Status) error {
	return r.d.reportSandboxStatus(ctx, sandboxID, st)
}

func (d *Daemon) startRuntimeLocked(ctx context.Context) error {
	if d.agent != nil {
		return nil
	}
	drv, err := runtime.NewDriver()
	if err != nil {
		log.Printf("docker runtime unavailable: %v (sandbox create will fail on this node)", err)
		return nil
	}
	if err := drv.DefaultOCIRuntimeAvailable(ctx); err != nil {
		_ = drv.Close()
		log.Printf("oci runtime unavailable: %v (sandbox create will fail on this node)", err)
		return nil
	}
	proxy := egress.NewProxy()
	if err := proxy.SetPrivateExceptions(d.cfg.EgressAllowPrivate); err != nil {
		return fmt.Errorf("egress-allow-private-cidrs: %w", err)
	}
	if err := proxy.Start(ctx); err != nil {
		log.Printf("egress proxy: %v", err)
	}
	redir := egress.NewRedirectManager(proxy.HTTPPort, proxy.TLSPort, proxy.OtherPort, proxy.UDPPort)
	mat := d.idStore.Material()
	if mat == nil {
		return fmt.Errorf("no identity for runtime agent")
	}
	agent := runtime.NewAgent(mat.NodeID, drv, proxy, redir, &heartbeatSource{d: d}, &statusReporter{d: d}, d.cfg.DataDir, "")
	d.driver = drv
	d.proxy = proxy
	d.redirect = redir
	d.agent = agent
	d.runtimeSrv = grpcapi.NewRuntimeServer(agent)

	d.wg.Add(1)
	go func() {
		defer d.wg.Done()
		agent.Run(ctx)
	}()
	d.wg.Add(1)
	go func() {
		defer d.wg.Done()
		d.heartbeatLoop(ctx)
	}()
	log.Printf("runtime agent started for node %s", mat.NodeID)
	return nil
}

func (d *Daemon) managerDialAddr() string {
	state := d.idStore.State()
	if state.ManagerAddr != "" {
		return state.ManagerAddr
	}
	if d.raft != nil {
		if a := d.raft.LeaderGRPC(); a != "" {
			return a
		}
		return d.raft.GRPCAdvertise()
	}
	return state.AdvertiseAddr
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
		assigned, err := raft.ListSandboxesByNode(ctx, mat.NodeID)
		if err != nil {
			return err
		}
		protos := make([]*cellarv1.Sandbox, 0, len(assigned))
		for _, sb := range assigned {
			protos = append(protos, sandbox.ToProto(sb))
		}
		d.cacheAssigned(protos)
		return nil
	}

	addr := d.managerDialAddr()
	if addr == "" {
		return fmt.Errorf("no manager address")
	}
	resp, err := grpcapi.RuntimeHeartbeatRemote(ctx, addr, mat.Certificate, mat.PrivateKey, mat.CACert, &cellarv1.RuntimeHeartbeatRequest{
		NodeId:       mat.NodeID,
		GrpcAddr:     grpcAddr,
		SandboxCount: int32(count),
	})
	if err != nil {
		return err
	}
	d.cacheAssigned(resp.Assigned)
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

func (d *Daemon) reportSandboxStatus(ctx context.Context, sandboxID string, st sandbox.Status) error {
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
		st.UpdatedAt = time.Now().UTC()
		sb.Status = st
		sb.UpdatedAt = st.UpdatedAt
		return raft.SaveSandbox(ctx, sb)
	}
	addr := d.managerDialAddr()
	return grpcapi.UpdateSandboxStatusRemote(ctx, addr, mat.Certificate, mat.PrivateKey, mat.CACert, &cellarv1.UpdateSandboxStatusRequest{
		SandboxId:   sandboxID,
		Status:      sandbox.StatusToProto(st),
		ContainerId: st.ContainerID,
	})
}

func (d *Daemon) dialLeaderControl(ctx context.Context) (addr string, cert, key, ca []byte, err error) {
	mat := d.idStore.Material()
	if mat == nil {
		return "", nil, nil, nil, fmt.Errorf("node not joined")
	}
	addr = d.managerDialAddr()
	if addr == "" {
		return "", nil, nil, nil, fmt.Errorf("no manager address")
	}
	return addr, mat.Certificate, mat.PrivateKey, mat.CACert, nil
}

func (d *Daemon) SandboxCreate(ctx context.Context, req *cellarv1.SandboxCreateRequest) (*cellarv1.SandboxCreateResponse, error) {
	d.mu.Lock()
	raft := d.raft
	sb := d.sandboxServer
	d.mu.Unlock()
	if raft != nil && raft.IsLeader() && sb != nil {
		return sb.Create(ctx, req)
	}
	addr, cert, key, ca, err := d.dialLeaderControl(ctx)
	if err != nil {
		return nil, err
	}
	return grpcapi.SandboxCreateRemote(ctx, addr, cert, key, ca, req)
}

func (d *Daemon) SandboxStop(ctx context.Context, req *cellarv1.SandboxStopRequest) (*cellarv1.SandboxStopResponse, error) {
	d.mu.Lock()
	raft := d.raft
	sb := d.sandboxServer
	d.mu.Unlock()
	if raft != nil && raft.IsLeader() && sb != nil {
		return sb.Stop(ctx, req)
	}
	addr, cert, key, ca, err := d.dialLeaderControl(ctx)
	if err != nil {
		return nil, err
	}
	return grpcapi.SandboxStopRemote(ctx, addr, cert, key, ca, req.SandboxId)
}

func (d *Daemon) SandboxRemove(ctx context.Context, req *cellarv1.SandboxRemoveRequest) (*cellarv1.SandboxRemoveResponse, error) {
	d.mu.Lock()
	raft := d.raft
	sb := d.sandboxServer
	d.mu.Unlock()
	if raft != nil && raft.IsLeader() && sb != nil {
		return sb.Remove(ctx, req)
	}
	addr, cert, key, ca, err := d.dialLeaderControl(ctx)
	if err != nil {
		return nil, err
	}
	if err := grpcapi.SandboxRemoveRemote(ctx, addr, cert, key, ca, req.SandboxId); err != nil {
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
		return sb.Get(ctx, req)
	}
	addr, cert, key, ca, err := d.dialLeaderControl(ctx)
	if err != nil {
		return nil, err
	}
	return grpcapi.SandboxGetRemote(ctx, addr, cert, key, ca, req.SandboxId)
}

func (d *Daemon) SandboxList(ctx context.Context, req *cellarv1.SandboxListRequest) (*cellarv1.SandboxListResponse, error) {
	d.mu.Lock()
	raft := d.raft
	sb := d.sandboxServer
	d.mu.Unlock()
	if raft != nil && sb != nil {
		return sb.List(ctx, req)
	}
	addr, cert, key, ca, err := d.dialLeaderControl(ctx)
	if err != nil {
		return nil, err
	}
	return grpcapi.SandboxListRemote(ctx, addr, cert, key, ca)
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
		resp, err = srv.UpdateNetwork(ctx, req)
	} else {
		var addr string
		var cert, key, ca []byte
		addr, cert, key, ca, err = d.dialLeaderControl(ctx)
		if err != nil {
			return nil, err
		}
		resp, err = grpcapi.SandboxUpdateNetworkRemote(ctx, addr, cert, key, ca, req)
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
