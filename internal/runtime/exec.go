package runtime

import (
	"context"
	"fmt"
	"io"

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

// ExecDetached starts a detached docker exec and returns the exec ID.
func (d *Driver) ExecDetached(ctx context.Context, containerID string, cmd []string) (string, error) {
	if len(cmd) == 0 {
		return "", fmt.Errorf("command required")
	}
	execID, err := d.cli.ContainerExecCreate(ctx, containerID, container.ExecOptions{
		Cmd:          cmd,
		AttachStdin:  false,
		AttachStdout: false,
		AttachStderr: false,
		Tty:          false,
	})
	if err != nil {
		return "", err
	}
	if err := d.cli.ContainerExecStart(ctx, execID.ID, container.ExecStartOptions{Detach: true}); err != nil {
		return "", err
	}
	return execID.ID, nil
}

// ExecInspectRunning returns whether the exec is still running and its exit code.
func (d *Driver) ExecInspectRunning(ctx context.Context, execID string) (running bool, exitCode int, err error) {
	ins, err := d.cli.ContainerExecInspect(ctx, execID)
	if err != nil {
		return false, -1, err
	}
	return ins.Running, ins.ExitCode, nil
}

// AgentExec implements LocalRuntime.Exec via docker exec.
func (a *Agent) Exec(ctx context.Context, sandboxID string, cmd []string, tty, stdin bool) (ExecSession, error) {
	cid, err := a.resolveContainerID(ctx, sandboxID)
	if err != nil {
		return nil, err
	}
	sess, _, err := a.Driver.ExecSession(ctx, cid, cmd, tty, stdin)
	return sess, err
}

func (a *Agent) resolveContainerID(ctx context.Context, sandboxID string) (string, error) {
	cid := a.LocalContainerID(sandboxID)
	if cid != "" {
		return cid, nil
	}
	cid, err := a.Driver.FindBySandboxID(ctx, sandboxID)
	if err != nil {
		return "", err
	}
	if cid == "" {
		return "", fmt.Errorf("sandbox container not found locally")
	}
	return cid, nil
}

// Discard unused import guard
var _ io.Reader
