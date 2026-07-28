package grpcapi

import (
	"context"
	"io"
	"sync"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	cellarv1 "github.com/prodioslabs/cellar/api/gen"
	"github.com/prodioslabs/cellar/internal/store"
)

// SandboxAPIHost provides manager identity and node lookup for SandboxAPI.
type SandboxAPIHost interface {
	// IdentityPEMs returns this node's mTLS material for dialing peers.
	IdentityPEMs() (cert, key, ca []byte, err error)
	// LeaderAddr returns the current Raft leader gRPC address.
	LeaderAddr() string
	// RuntimeAddr returns the SandboxRuntime address for nodeID.
	RuntimeAddr(ctx context.Context, nodeID string) (string, error)
}

// SandboxAPIServer implements the public SandboxAPI on managers.
type SandboxAPIServer struct {
	cellarv1.UnimplementedSandboxAPIServer

	store   store.Store
	raft    RaftAdmin
	control *SandboxServer
	host    SandboxAPIHost
}

// NewSandboxAPIServer wires public client RPCs onto the control-plane store.
func NewSandboxAPIServer(s store.Store, raft RaftAdmin, control *SandboxServer, host SandboxAPIHost) *SandboxAPIServer {
	return &SandboxAPIServer{store: s, raft: raft, control: control, host: host}
}

func (s *SandboxAPIServer) dialLeader(ctx context.Context) (*grpc.ClientConn, error) {
	addr := ""
	if s.raft != nil {
		addr = s.raft.LeaderGRPC()
		if addr == "" {
			addr = s.raft.GRPCAdvertise()
		}
	}
	if addr == "" && s.host != nil {
		addr = s.host.LeaderAddr()
	}
	if addr == "" {
		return nil, status.Error(codes.Unavailable, "no raft leader")
	}
	cert, key, ca, err := s.host.IdentityPEMs()
	if err != nil {
		return nil, status.Error(codes.FailedPrecondition, err.Error())
	}
	return DialMTLS(addr, cert, key, ca)
}

func (s *SandboxAPIServer) forwardCreate(ctx context.Context, req *cellarv1.SandboxCreateRequest) (*cellarv1.SandboxCreateResponse, error) {
	conn, err := s.dialLeader(ctx)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	return cellarv1.NewSandboxControlClient(conn).Create(ctx, req)
}

func (s *SandboxAPIServer) Create(ctx context.Context, req *cellarv1.SandboxCreateRequest) (*cellarv1.SandboxCreateResponse, error) {
	if s.raft != nil && s.raft.IsLeader() && s.control != nil {
		return s.control.Create(WithInternalCall(ctx), req)
	}
	return s.forwardCreate(ctx, req)
}

func (s *SandboxAPIServer) Stop(ctx context.Context, req *cellarv1.SandboxStopRequest) (*cellarv1.SandboxStopResponse, error) {
	if s.raft != nil && s.raft.IsLeader() && s.control != nil {
		return s.control.Stop(WithInternalCall(ctx), req)
	}
	conn, err := s.dialLeader(ctx)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	return cellarv1.NewSandboxControlClient(conn).Stop(ctx, req)
}

func (s *SandboxAPIServer) Remove(ctx context.Context, req *cellarv1.SandboxRemoveRequest) (*cellarv1.SandboxRemoveResponse, error) {
	if s.raft != nil && s.raft.IsLeader() && s.control != nil {
		return s.control.Remove(WithInternalCall(ctx), req)
	}
	conn, err := s.dialLeader(ctx)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	return cellarv1.NewSandboxControlClient(conn).Remove(ctx, req)
}

func (s *SandboxAPIServer) Get(ctx context.Context, req *cellarv1.SandboxGetRequest) (*cellarv1.SandboxGetResponse, error) {
	if s.control != nil {
		return s.control.Get(WithInternalCall(ctx), req)
	}
	conn, err := s.dialLeader(ctx)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	return cellarv1.NewSandboxControlClient(conn).Get(ctx, req)
}

func (s *SandboxAPIServer) List(ctx context.Context, req *cellarv1.SandboxListRequest) (*cellarv1.SandboxListResponse, error) {
	if s.control != nil {
		return s.control.List(WithInternalCall(ctx), req)
	}
	conn, err := s.dialLeader(ctx)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	return cellarv1.NewSandboxControlClient(conn).List(ctx, req)
}

func (s *SandboxAPIServer) UpdateNetwork(ctx context.Context, req *cellarv1.SandboxUpdateNetworkRequest) (*cellarv1.SandboxUpdateNetworkResponse, error) {
	if s.raft != nil && s.raft.IsLeader() && s.control != nil {
		return s.control.UpdateNetwork(WithInternalCall(ctx), req)
	}
	conn, err := s.dialLeader(ctx)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	return cellarv1.NewSandboxControlClient(conn).UpdateNetwork(ctx, req)
}

func (s *SandboxAPIServer) Logs(req *cellarv1.SandboxLogsRequest, stream cellarv1.SandboxAPI_LogsServer) error {
	sbResp, err := s.Get(stream.Context(), &cellarv1.SandboxGetRequest{SandboxId: req.SandboxId})
	if err != nil {
		return err
	}
	if sbResp.Sandbox == nil || sbResp.Sandbox.NodeId == "" {
		return status.Error(codes.FailedPrecondition, "sandbox has no owning node")
	}
	addr, err := s.host.RuntimeAddr(stream.Context(), sbResp.Sandbox.NodeId)
	if err != nil {
		return status.Error(codes.Unavailable, err.Error())
	}
	cert, key, ca, err := s.host.IdentityPEMs()
	if err != nil {
		return status.Error(codes.FailedPrecondition, err.Error())
	}
	pr, pw := io.Pipe()
	errCh := make(chan error, 1)
	go func() {
		errCh <- StreamRemoteLogs(stream.Context(), addr, cert, key, ca, req, pw)
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

func (s *SandboxAPIServer) Exec(stream cellarv1.SandboxAPI_ExecServer) error {
	first, err := stream.Recv()
	if err != nil {
		return err
	}
	start := first.GetStart()
	if start == nil {
		return status.Error(codes.InvalidArgument, "first message must be start")
	}
	sbResp, err := s.Get(stream.Context(), &cellarv1.SandboxGetRequest{SandboxId: start.SandboxId})
	if err != nil {
		return err
	}
	if sbResp.Sandbox == nil || sbResp.Sandbox.NodeId == "" {
		return status.Error(codes.FailedPrecondition, "sandbox has no owning node")
	}
	addr, err := s.host.RuntimeAddr(stream.Context(), sbResp.Sandbox.NodeId)
	if err != nil {
		return status.Error(codes.Unavailable, err.Error())
	}
	cert, key, ca, err := s.host.IdentityPEMs()
	if err != nil {
		return status.Error(codes.FailedPrecondition, err.Error())
	}
	conn, err := DialRuntime(addr, cert, key, ca)
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

// RegisterSandboxAPI registers the public SandboxAPI service.
func RegisterSandboxAPI(s *grpc.Server, api *SandboxAPIServer) {
	if api != nil {
		cellarv1.RegisterSandboxAPIServer(s, api)
	}
}
