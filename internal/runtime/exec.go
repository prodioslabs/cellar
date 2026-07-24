package runtime

import (
	"context"
	"fmt"
	"io"
	"log"
	"strings"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
)

// ExecSession is a bidirectional exec attach stream.
type ExecSession interface {
	Read(p []byte) (int, error)
	Write(p []byte) (int, error)
	Close() error
	CloseWrite() error
	Wait() (exitCode int, errMsg string)
}

type dockerExecSession struct {
	hijacked types.HijackedResponse
	cli      *Driver
	execID   string
	ctx      context.Context
}

func (s *dockerExecSession) Read(p []byte) (int, error)  { return s.hijacked.Reader.Read(p) }
func (s *dockerExecSession) Write(p []byte) (int, error) { return s.hijacked.Conn.Write(p) }
func (s *dockerExecSession) Close() error {
	s.hijacked.Close()
	return nil
}
func (s *dockerExecSession) CloseWrite() error {
	if cw, ok := s.hijacked.Conn.(interface{ CloseWrite() error }); ok {
		return cw.CloseWrite()
	}
	return nil
}
func (s *dockerExecSession) Wait() (int, string) {
	if s.execID == "" {
		return 0, ""
	}
	code, err := s.cli.ExecInspectExit(s.ctx, s.execID)
	if err != nil {
		return -1, err.Error()
	}
	return code, ""
}

// ExecSession starts docker exec for a container.
func (d *Driver) ExecSession(ctx context.Context, containerID string, cmd []string, tty, stdin bool) (ExecSession, string, error) {
	if len(cmd) == 0 {
		return nil, "", fmt.Errorf("command required")
	}
	execID, err := d.cli.ContainerExecCreate(ctx, containerID, container.ExecOptions{
		Cmd:          cmd,
		AttachStdin:  stdin,
		AttachStdout: true,
		AttachStderr: true,
		Tty:          tty,
	})
	if err != nil {
		return nil, "", err
	}
	hj, err := d.cli.ContainerExecAttach(ctx, execID.ID, container.ExecStartOptions{Tty: tty})
	if err != nil {
		return nil, "", err
	}
	return &dockerExecSession{hijacked: hj, cli: d, execID: execID.ID, ctx: ctx}, execID.ID, nil
}

// AgentExec implements LocalRuntime.Exec for the agent.
func (a *Agent) Exec(ctx context.Context, sandboxID string, cmd []string, tty, stdin bool) (ExecSession, error) {
	sock := AgentSockPath(a.DataDir, sandboxID)
	token, err := ReadAgentToken(a.DataDir, sandboxID)
	if err == nil {
		sess, aerr := ExecViaAgent(ctx, sock, strings.TrimSpace(token), cmd, tty, stdin)
		if aerr == nil {
			return sess, nil
		}
		// Fall back to docker exec if the agent socket is unavailable.
		log.Printf("sandbox %s agent exec: %v; falling back to docker exec", sandboxID, aerr)
	}

	cid := a.LocalContainerID(sandboxID)
	if cid == "" {
		var err error
		cid, err = a.Driver.FindBySandboxID(ctx, sandboxID)
		if err != nil {
			return nil, err
		}
		if cid == "" {
			return nil, fmt.Errorf("sandbox container not found locally")
		}
	}
	sess, _, err := a.Driver.ExecSession(ctx, cid, cmd, tty, stdin)
	return sess, err
}

// Discard unused import guard
var _ io.Reader
