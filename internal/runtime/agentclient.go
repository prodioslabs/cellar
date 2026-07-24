package runtime

import (
	"context"
	"fmt"
	"io"
	"net"
	"sync"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	agentv1 "github.com/prodioslabs/cellar/api/gen/agent"
	"github.com/prodioslabs/cellar/internal/sandboxagent"
)

// DialSandboxAgent connects to cellar-agent over a Unix socket with bearer auth.
func DialSandboxAgent(ctx context.Context, sockPath, token string) (*grpc.ClientConn, agentv1.SandboxAgentClient, error) {
	dialer := func(ctx context.Context, _ string) (net.Conn, error) {
		var d net.Dialer
		return d.DialContext(ctx, "unix", sockPath)
	}
	conn, err := grpc.NewClient("passthrough:///cellar-agent",
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithContextDialer(dialer),
	)
	if err != nil {
		return nil, nil, err
	}
	return conn, agentv1.NewSandboxAgentClient(conn), nil
}

// WaitAgentHealthy polls Health until success or timeout.
func WaitAgentHealthy(ctx context.Context, sockPath, token, wantSandboxID string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	var lastErr error
	for time.Now().Before(deadline) {
		if err := ctx.Err(); err != nil {
			return err
		}
		cctx, cancel := context.WithTimeout(ctx, 2*time.Second)
		conn, client, err := DialSandboxAgent(cctx, sockPath, token)
		if err != nil {
			cancel()
			lastErr = err
			time.Sleep(100 * time.Millisecond)
			continue
		}
		resp, err := client.Health(sandboxagent.WithBearer(cctx, token), &agentv1.HealthRequest{})
		conn.Close()
		cancel()
		if err != nil {
			lastErr = err
			time.Sleep(100 * time.Millisecond)
			continue
		}
		if wantSandboxID != "" && resp.SandboxId != wantSandboxID {
			lastErr = fmt.Errorf("agent sandbox_id %q != %q", resp.SandboxId, wantSandboxID)
			time.Sleep(100 * time.Millisecond)
			continue
		}
		return nil
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("timeout")
	}
	return fmt.Errorf("agent health: %w", lastErr)
}

type agentExecSession struct {
	conn   *grpc.ClientConn
	stream agentv1.SandboxAgent_RunCommandClient
	out    io.ReadCloser
	pw     *io.PipeWriter

	mu       sync.Mutex
	exitCode int
	exitErr  string
	done     chan struct{}
}

// ExecViaAgent starts RunCommand on cellar-agent and returns an ExecSession.
func ExecViaAgent(ctx context.Context, sockPath, token string, cmd []string, tty, stdin bool) (ExecSession, error) {
	conn, client, err := DialSandboxAgent(ctx, sockPath, token)
	if err != nil {
		return nil, err
	}
	stream, err := client.RunCommand(sandboxagent.WithBearer(ctx, token))
	if err != nil {
		conn.Close()
		return nil, err
	}
	if err := stream.Send(&agentv1.RunCommandMessage{
		Payload: &agentv1.RunCommandMessage_Start{Start: &agentv1.RunCommandStart{
			Command: cmd,
			Tty:     tty,
			Stdin:   stdin,
		}},
	}); err != nil {
		conn.Close()
		return nil, err
	}

	pr, pw := io.Pipe()
	sess := &agentExecSession{
		conn:   conn,
		stream: stream,
		out:    pr,
		pw:     pw,
		done:   make(chan struct{}),
	}
	go sess.recvLoop()
	return sess, nil
}

func (s *agentExecSession) recvLoop() {
	defer close(s.done)
	defer s.pw.Close()
	for {
		msg, err := s.stream.Recv()
		if err != nil {
			if err != io.EOF {
				s.mu.Lock()
				if s.exitErr == "" {
					s.exitCode = -1
					s.exitErr = err.Error()
				}
				s.mu.Unlock()
			}
			return
		}
		switch {
		case len(msg.GetStdout()) > 0:
			if _, err := s.pw.Write(msg.GetStdout()); err != nil {
				return
			}
		case len(msg.GetStderr()) > 0:
			if _, err := s.pw.Write(msg.GetStderr()); err != nil {
				return
			}
		case msg.GetExit() != nil:
			ex := msg.GetExit()
			s.mu.Lock()
			s.exitCode = int(ex.ExitCode)
			s.exitErr = ex.Error
			s.mu.Unlock()
			return
		}
	}
}

func (s *agentExecSession) Read(p []byte) (int, error) { return s.out.Read(p) }

func (s *agentExecSession) Write(p []byte) (int, error) {
	if err := s.stream.Send(&agentv1.RunCommandMessage{
		Payload: &agentv1.RunCommandMessage_Stdin{Stdin: p},
	}); err != nil {
		return 0, err
	}
	return len(p), nil
}

func (s *agentExecSession) CloseWrite() error {
	return s.stream.Send(&agentv1.RunCommandMessage{
		Payload: &agentv1.RunCommandMessage_StdinClosed{StdinClosed: true},
	})
}

func (s *agentExecSession) Close() error {
	_ = s.stream.CloseSend()
	<-s.done
	return s.conn.Close()
}

func (s *agentExecSession) Wait() (int, string) {
	<-s.done
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.exitCode, s.exitErr
}
