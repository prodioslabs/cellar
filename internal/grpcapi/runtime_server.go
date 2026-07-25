package grpcapi

import (
	"context"
	"io"
	"sync"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	cellarv1 "github.com/prodioslabs/cellar/api/gen"
	"github.com/prodioslabs/cellar/internal/runtime"
	"github.com/prodioslabs/cellar/internal/sandbox"
)

// LocalRuntime resolves a sandbox to a local container and streams logs/exec.
type LocalRuntime interface {
	StreamLogs(ctx context.Context, sandboxID string, follow bool, tail int64, w io.Writer) error
	Exec(ctx context.Context, sandboxID string, cmd []string, tty, stdin bool) (runtime.ExecSession, error)
	ApplyNetworkPolicy(ctx context.Context, sandboxID string, policy sandbox.NetworkPolicy) error
}

// RuntimeServer implements SandboxRuntime on every node.
type RuntimeServer struct {
	cellarv1.UnimplementedSandboxRuntimeServer
	local LocalRuntime
}

func NewRuntimeServer(local LocalRuntime) *RuntimeServer {
	return &RuntimeServer{local: local}
}

func RegisterRuntime(s *grpc.Server, rt *RuntimeServer) {
	cellarv1.RegisterSandboxRuntimeServer(s, rt)
}

func (r *RuntimeServer) Logs(req *cellarv1.SandboxLogsRequest, stream cellarv1.SandboxRuntime_LogsServer) error {
	if r.local == nil {
		return status.Error(codes.Unavailable, "runtime not ready")
	}
	if req.SandboxId == "" {
		return status.Error(codes.InvalidArgument, "sandbox_id required")
	}
	pr, pw := io.Pipe()
	errCh := make(chan error, 1)
	go func() {
		errCh <- r.local.StreamLogs(stream.Context(), req.SandboxId, req.Follow, req.Tail, pw)
		_ = pw.Close()
	}()
	buf := make([]byte, 32*1024)
	for {
		n, err := pr.Read(buf)
		if n > 0 {
			if serr := stream.Send(&cellarv1.SandboxLogsChunk{Data: append([]byte(nil), buf[:n]...)}); serr != nil {
				_ = pr.Close()
				return serr
			}
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
	}
	if err := <-errCh; err != nil && err != io.EOF {
		return status.Error(codes.Internal, err.Error())
	}
	return nil
}

func (r *RuntimeServer) ApplyNetworkPolicy(ctx context.Context, req *cellarv1.ApplyNetworkPolicyRequest) (*cellarv1.ApplyNetworkPolicyResponse, error) {
	if r.local == nil {
		return nil, status.Error(codes.Unavailable, "runtime not ready")
	}
	if req.SandboxId == "" {
		return nil, status.Error(codes.InvalidArgument, "sandbox_id required")
	}
	if err := r.local.ApplyNetworkPolicy(ctx, req.SandboxId, sandbox.NetworkPolicyFromProto(req.Network)); err != nil {
		return nil, status.Error(codes.FailedPrecondition, err.Error())
	}
	return &cellarv1.ApplyNetworkPolicyResponse{}, nil
}

func (r *RuntimeServer) Exec(stream cellarv1.SandboxRuntime_ExecServer) error {
	if r.local == nil {
		return status.Error(codes.Unavailable, "runtime not ready")
	}
	first, err := stream.Recv()
	if err != nil {
		return err
	}
	start := first.GetStart()
	if start == nil {
		return status.Error(codes.InvalidArgument, "first message must be start")
	}
	sess, err := r.local.Exec(stream.Context(), start.SandboxId, start.Command, start.Tty, start.Stdin)
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
			if _, err := sess.Write(b); err != nil {
				break
			}
		}
		if msg.GetStdinClosed() {
			_ = sess.CloseWrite()
		}
	}
	wg.Wait()
	code, errMsg := sess.Wait()
	_ = stream.Send(&cellarv1.SandboxExecMessage{
		Payload: &cellarv1.SandboxExecMessage_Exit{Exit: &cellarv1.SandboxExecExit{
			ExitCode: int32(code),
			Error:    errMsg,
		}},
	})
	return nil
}
