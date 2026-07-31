package client

import (
	"context"
	"fmt"
	"time"
)

const (
	defaultReadyTimeout = 60 * time.Second
	defaultPollInterval = 500 * time.Millisecond
)

// WaitUntilReadyOptions configures Sandbox.WaitUntilReady.
type WaitUntilReadyOptions struct {
	// Timeout is the max time to wait before failing. Defaults to 60s.
	Timeout time.Duration
	// PollInterval is the delay between status polls. Defaults to 500ms.
	PollInterval time.Duration
}

// Sandbox is an operational handle returned by Client.Create, Client.Get, and
// Client.List. Creation is asynchronous — call WaitUntilReady before Exec when
// you need the container to be running.
type Sandbox struct {
	client *Client
	data   SandboxSnapshot
}

// ID returns the sandbox identifier.
func (s *Sandbox) ID() string { return s.data.ID }

// Spec returns the sandbox spec snapshot.
func (s *Sandbox) Spec() *SandboxSpec { return s.data.Spec }

// NodeID returns the assigned node id.
func (s *Sandbox) NodeID() string { return s.data.NodeID }

// DesiredState returns the desired lifecycle state.
func (s *Sandbox) DesiredState() string { return s.data.DesiredState }

// Status returns the last observed status snapshot.
func (s *Sandbox) Status() *SandboxStatus { return s.data.Status }

// CreatedAtUnixNano returns the creation timestamp.
func (s *Sandbox) CreatedAtUnixNano() int64 { return s.data.CreatedAtUnixNano.Int64() }

// UpdatedAtUnixNano returns the last update timestamp.
func (s *Sandbox) UpdatedAtUnixNano() int64 { return s.data.UpdatedAtUnixNano.Int64() }

// Snapshot returns a defensive copy of the current gateway snapshot.
func (s *Sandbox) Snapshot() SandboxSnapshot {
	return cloneSnapshot(s.data)
}

// GetStatus refreshes from GET /v1/sandboxes/:id and returns the status.
func (s *Sandbox) GetStatus(ctx context.Context) (*SandboxStatus, error) {
	data, err := s.client.fetchSnapshot(ctx, s.data.ID)
	if err != nil {
		return nil, err
	}
	s.data = data
	return s.data.Status, nil
}

// WaitUntilReady polls the gateway until status.phase == "running".
// Continues through pending, starting, and retryable failed while
// desiredState remains running. Fails on timeout, context cancel, or a
// non-running desired/terminal state.
func (s *Sandbox) WaitUntilReady(ctx context.Context, opt WaitUntilReadyOptions) error {
	timeout := opt.Timeout
	if timeout <= 0 {
		timeout = defaultReadyTimeout
	}
	poll := opt.PollInterval
	if poll <= 0 {
		poll = defaultPollInterval
	}
	deadline := time.Now().Add(timeout)

	for {
		if err := ctx.Err(); err != nil {
			return err
		}

		status, err := s.GetStatus(ctx)
		if err != nil {
			return err
		}

		phase := ""
		if status != nil {
			phase = status.Phase
		}
		desired := s.data.DesiredState

		if phase == string(PhaseRunning) {
			return nil
		}

		if desired == "stopped" || desired == "removed" {
			return fmt.Errorf("sandbox %s will not become ready: desiredState=%s%s",
				s.data.ID, desired, statusMessage(status))
		}

		if phase == string(PhaseStopped) {
			return fmt.Errorf("sandbox %s is stopped%s", s.data.ID, statusMessage(status))
		}

		remaining := time.Until(deadline)
		if remaining <= 0 {
			if phase == "" {
				phase = "unknown"
			}
			return fmt.Errorf("sandbox %s not ready within %s (phase=%s)%s",
				s.data.ID, timeout, phase, statusMessage(status))
		}

		wait := poll
		if wait > remaining {
			wait = remaining
		}
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}

// Exec runs a command in this sandbox and collects output until exit.
func (s *Sandbox) Exec(ctx context.Context, command []string) (*ExecResult, error) {
	return s.client.execCommand(ctx, s.data.ID, command)
}

// StartJob runs a command in the background and returns its job id.
func (s *Sandbox) StartJob(ctx context.Context, command []string) (string, error) {
	return s.client.startJob(ctx, s.data.ID, command)
}

// ListJobs lists background jobs for this sandbox.
func (s *Sandbox) ListJobs(ctx context.Context) ([]JobInfo, error) {
	return s.client.listJobs(ctx, s.data.ID)
}

// GetJob returns status for a background job.
func (s *Sandbox) GetJob(ctx context.Context, jobID string) (*JobInfo, error) {
	return s.client.getJob(ctx, s.data.ID, jobID)
}

// StopJob stops a background job.
func (s *Sandbox) StopJob(ctx context.Context, jobID string) error {
	return s.client.stopJob(ctx, s.data.ID, jobID)
}

// Logs streams sandbox logs as NDJSON chunks until EOF or ctx cancel.
func (s *Sandbox) Logs(ctx context.Context, opt LogsOptions) (<-chan LogsChunk, <-chan error) {
	return s.client.streamLogs(ctx, s.data.ID, opt)
}

// Stop stops this sandbox and refreshes the local snapshot.
func (s *Sandbox) Stop(ctx context.Context) error {
	data, err := s.client.stopSandbox(ctx, s.data.ID)
	if err != nil {
		return err
	}
	s.data = data
	return nil
}

// Remove deletes this sandbox.
func (s *Sandbox) Remove(ctx context.Context) error {
	return s.client.removeSandbox(ctx, s.data.ID)
}

// UpdateNetwork replaces this sandbox's network policy and refreshes the snapshot.
func (s *Sandbox) UpdateNetwork(ctx context.Context, network *NetworkPolicy) error {
	data, err := s.client.updateSandboxNetwork(ctx, s.data.ID, network)
	if err != nil {
		return err
	}
	s.data = data
	return nil
}

func statusMessage(status *SandboxStatus) string {
	if status == nil || status.Message == "" {
		return ""
	}
	return ": " + status.Message
}

func cloneSnapshot(in SandboxSnapshot) SandboxSnapshot {
	out := in
	if in.Spec != nil {
		spec := *in.Spec
		if in.Spec.Command != nil {
			spec.Command = append([]string(nil), in.Spec.Command...)
		}
		if in.Spec.Args != nil {
			spec.Args = append([]string(nil), in.Spec.Args...)
		}
		if in.Spec.Env != nil {
			spec.Env = append([]string(nil), in.Spec.Env...)
		}
		if in.Spec.Mounts != nil {
			spec.Mounts = append([]Mount(nil), in.Spec.Mounts...)
		}
		if in.Spec.Resources != nil {
			r := *in.Spec.Resources
			spec.Resources = &r
		}
		if in.Spec.Network != nil {
			spec.Network = cloneNetwork(in.Spec.Network)
		}
		out.Spec = &spec
	}
	if in.Status != nil {
		st := *in.Status
		out.Status = &st
	}
	return out
}

func cloneNetwork(in *NetworkPolicy) *NetworkPolicy {
	if in == nil {
		return nil
	}
	out := *in
	if in.BlockAll != nil {
		v := *in.BlockAll
		out.BlockAll = &v
	}
	if in.DNS != nil {
		dns := *in.DNS
		if in.DNS.Names != nil {
			dns.Names = append([]string(nil), in.DNS.Names...)
		}
		out.DNS = &dns
	}
	if in.Rules != nil {
		out.Rules = make([]NetworkRule, len(in.Rules))
		for i, r := range in.Rules {
			out.Rules[i] = NetworkRule{
				Hosts:     append([]string(nil), r.Hosts...),
				Ports:     append([]uint32(nil), r.Ports...),
				Protocols: append([]string(nil), r.Protocols...),
			}
		}
	}
	return &out
}
