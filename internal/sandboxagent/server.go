package sandboxagent

import (
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"sync"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	agentv1 "github.com/prodioslabs/cellar/api/gen/agent"
)

// Server implements SandboxAgent.
type Server struct {
	agentv1.UnimplementedSandboxAgentServer
	cfg Config
}

// NewServer constructs the in-sandbox agent service.
func NewServer(cfg Config) *Server {
	return &Server{cfg: cfg}
}

// Health returns sandbox identity.
func (s *Server) Health(_ context.Context, _ *agentv1.HealthRequest) (*agentv1.HealthResponse, error) {
	return &agentv1.HealthResponse{
		SandboxId: s.cfg.SandboxID,
		Version:   Version,
	}, nil
}

// RunCommand starts a process and streams stdio.
func (s *Server) RunCommand(stream agentv1.SandboxAgent_RunCommandServer) error {
	first, err := stream.Recv()
	if err != nil {
		return err
	}
	start := first.GetStart()
	if start == nil {
		return status.Error(codes.InvalidArgument, "first message must be start")
	}
	if len(start.Command) == 0 {
		return status.Error(codes.InvalidArgument, "command required")
	}

	ctx := stream.Context()
	cmd := exec.CommandContext(ctx, start.Command[0], start.Command[1:]...)
	if start.Cwd != "" {
		cmd.Dir = start.Cwd
	}
	if len(start.Env) > 0 {
		cmd.Env = append(os.Environ(), start.Env...)
	}

	var stdin io.WriteCloser
	if start.Stdin {
		stdin, err = cmd.StdinPipe()
		if err != nil {
			return status.Errorf(codes.Internal, "stdin pipe: %v", err)
		}
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return status.Errorf(codes.Internal, "stdout pipe: %v", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return status.Errorf(codes.Internal, "stderr pipe: %v", err)
	}

	if err := cmd.Start(); err != nil {
		return status.Errorf(codes.Internal, "start: %v", err)
	}

	var wg sync.WaitGroup
	copyOut := func(r io.Reader, send func([]byte) *agentv1.RunCommandMessage) {
		defer wg.Done()
		buf := make([]byte, 32*1024)
		for {
			n, rerr := r.Read(buf)
			if n > 0 {
				if serr := stream.Send(send(append([]byte(nil), buf[:n]...))); serr != nil {
					return
				}
			}
			if rerr != nil {
				return
			}
		}
	}
	wg.Add(2)
	go copyOut(stdout, func(b []byte) *agentv1.RunCommandMessage {
		return &agentv1.RunCommandMessage{Payload: &agentv1.RunCommandMessage_Stdout{Stdout: b}}
	})
	go copyOut(stderr, func(b []byte) *agentv1.RunCommandMessage {
		return &agentv1.RunCommandMessage{Payload: &agentv1.RunCommandMessage_Stderr{Stderr: b}}
	})

	go func() {
		if stdin == nil {
			for {
				if _, rerr := stream.Recv(); rerr != nil {
					return
				}
			}
		}
		defer stdin.Close()
		for {
			msg, rerr := stream.Recv()
			if rerr != nil {
				return
			}
			if b := msg.GetStdin(); len(b) > 0 {
				if _, werr := stdin.Write(b); werr != nil {
					return
				}
			}
			if msg.GetStdinClosed() {
				return
			}
		}
	}()

	waitErr := cmd.Wait()
	wg.Wait()

	exit := &agentv1.RunCommandExit{}
	if waitErr != nil {
		if ee, ok := waitErr.(*exec.ExitError); ok {
			exit.ExitCode = int32(ee.ExitCode())
		} else {
			exit.ExitCode = -1
			exit.Error = waitErr.Error()
		}
	}
	return stream.Send(&agentv1.RunCommandMessage{
		Payload: &agentv1.RunCommandMessage_Exit{Exit: exit},
	})
}

// ListenAndServe binds the UDS, serves gRPC until ctx is cancelled, then stops.
func ListenAndServe(ctx context.Context, cfg Config) error {
	if err := os.Remove(cfg.SockPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove stale socket: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(cfg.SockPath), 0o755); err != nil {
		return fmt.Errorf("mkdir socket dir: %w", err)
	}

	lis, err := net.Listen("unix", cfg.SockPath)
	if err != nil {
		return fmt.Errorf("listen %s: %w", cfg.SockPath, err)
	}
	defer lis.Close()
	if err := os.Chmod(cfg.SockPath, 0o600); err != nil {
		return fmt.Errorf("chmod socket: %w", err)
	}

	srv := grpc.NewServer(
		grpc.UnaryInterceptor(UnaryAuthInterceptor(cfg.Token)),
		grpc.StreamInterceptor(StreamAuthInterceptor(cfg.Token)),
	)
	agentv1.RegisterSandboxAgentServer(srv, NewServer(cfg))

	errCh := make(chan error, 1)
	go func() {
		errCh <- srv.Serve(lis)
	}()

	select {
	case <-ctx.Done():
		srv.GracefulStop()
		<-errCh
		return nil
	case err := <-errCh:
		if err != nil {
			return err
		}
		return nil
	}
}
