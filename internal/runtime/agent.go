package runtime

import (
	"context"
	"fmt"
	"io"
	"log"
	"strconv"
	"sync"
	"time"

	"github.com/prodioslabs/cellar/internal/egress"
	"github.com/prodioslabs/cellar/internal/sandbox"
)

// StatusReporter pushes observed status to the control plane.
type StatusReporter interface {
	UpdateStatus(ctx context.Context, sandboxID string, st sandbox.Status) error
}

// AssignmentSource yields sandboxes assigned to this node.
type AssignmentSource interface {
	ListAssigned(ctx context.Context) ([]*sandbox.Sandbox, error)
}

// Agent reconciles local Docker containers with assigned sandboxes.
type Agent struct {
	NodeID      string
	Driver      *Driver
	Proxy       *egress.Proxy
	Redirect    *egress.RedirectManager
	Source      AssignmentSource
	Report      StatusReporter
	DataDir     string
	AgentBinary string

	mu       sync.Mutex
	local    map[string]string // sandboxID -> containerID
	interval time.Duration

	// stopRemove overrides Driver for Stop/Remove when non-nil (unit tests).
	stopRemove stopRemover
}

// stopRemover is the Stop/Remove surface used by TeardownLocal and unassign.
type stopRemover interface {
	Stop(ctx context.Context, containerID string, timeoutSec int) error
	Remove(ctx context.Context, containerID string) error
}

// NewAgent constructs a runtime agent.
func NewAgent(nodeID string, drv *Driver, proxy *egress.Proxy, redir *egress.RedirectManager, src AssignmentSource, rep StatusReporter, dataDir, agentBinary string) *Agent {
	return &Agent{
		NodeID:      nodeID,
		Driver:      drv,
		Proxy:       proxy,
		Redirect:    redir,
		Source:      src,
		Report:      rep,
		DataDir:     dataDir,
		AgentBinary: agentBinary,
		local:       make(map[string]string),
		interval:    3 * time.Second,
	}
}

func (a *Agent) stopper() stopRemover {
	if a.stopRemove != nil {
		return a.stopRemove
	}
	if a.Driver != nil {
		return a.Driver
	}
	return nil
}

// Run loops until ctx is done.
func (a *Agent) Run(ctx context.Context) {
	ticker := time.NewTicker(a.interval)
	defer ticker.Stop()
	for {
		if err := a.reconcile(ctx); err != nil {
			log.Printf("runtime agent: %v", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// TeardownLocal stops and removes every container this node manages.
// Cluster desired state is untouched; a later start recreates them via reconcile.
func (a *Agent) TeardownLocal(ctx context.Context) {
	a.mu.Lock()
	locals := make(map[string]string, len(a.local))
	for id, cid := range a.local {
		locals[id] = cid
	}
	a.local = make(map[string]string)
	a.mu.Unlock()

	if len(locals) == 0 {
		return
	}

	var wg sync.WaitGroup
	for id, cid := range locals {
		wg.Add(1)
		go func(id, cid string) {
			defer wg.Done()
			if s := a.stopper(); s != nil {
				_ = s.Stop(ctx, cid, 5)
				_ = s.Remove(ctx, cid)
			}
			_ = CleanupSandboxDir(a.DataDir, id)
			if a.Redirect != nil {
				_ = a.Redirect.RemoveSandbox(id)
			}
			if a.Proxy != nil {
				a.Proxy.RemovePolicy(id)
			}
		}(id, cid)
	}

	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-ctx.Done():
		log.Printf("runtime agent: TeardownLocal timed out: %v", ctx.Err())
	}
}

func (a *Agent) reconcile(ctx context.Context) error {
	assigned, err := a.Source.ListAssigned(ctx)
	if err != nil {
		return err
	}
	want := make(map[string]*sandbox.Sandbox, len(assigned))
	for _, sb := range assigned {
		if sb == nil {
			continue
		}
		want[sb.ID] = sb
	}

	a.mu.Lock()
	defer a.mu.Unlock()

	for id, sb := range want {
		if err := a.reconcileOne(ctx, sb); err != nil {
			log.Printf("sandbox %s: %v", id, err)
			_ = a.report(ctx, id, sandbox.Status{
				Phase:     sandbox.PhaseFailed,
				Message:   err.Error(),
				UpdatedAt: time.Now().UTC(),
			})
		}
	}

	// Tear down locals no longer assigned (removed from raft).
	for id, cid := range a.local {
		if _, ok := want[id]; ok {
			continue
		}
		if s := a.stopper(); s != nil {
			_ = s.Stop(ctx, cid, 10)
			_ = s.Remove(ctx, cid)
		}
		_ = CleanupSandboxDir(a.DataDir, id)
		if a.Redirect != nil {
			_ = a.Redirect.RemoveSandbox(id)
		}
		if a.Proxy != nil {
			a.Proxy.RemovePolicy(id)
		}
		delete(a.local, id)
	}
	return nil
}

func (a *Agent) reconcileOne(ctx context.Context, sb *sandbox.Sandbox) error {
	cid := a.local[sb.ID]
	if cid == "" {
		found, err := a.Driver.FindBySandboxID(ctx, sb.ID)
		if err != nil {
			return err
		}
		cid = found
		if cid != "" {
			a.local[sb.ID] = cid
		}
	}

	switch sb.DesiredState {
	case sandbox.DesiredRemoved, sandbox.DesiredStopped:
		if cid != "" {
			if s := a.stopper(); s != nil {
				_ = s.Stop(ctx, cid, 10)
				if sb.DesiredState == sandbox.DesiredRemoved {
					_ = s.Remove(ctx, cid)
				}
			}
			if sb.DesiredState == sandbox.DesiredRemoved {
				_ = CleanupSandboxDir(a.DataDir, sb.ID)
				delete(a.local, sb.ID)
				if a.Redirect != nil {
					_ = a.Redirect.RemoveSandbox(sb.ID)
				}
				if a.Proxy != nil {
					a.Proxy.RemovePolicy(sb.ID)
				}
			}
			phase := sandbox.PhaseStopped
			if sb.DesiredState == sandbox.DesiredRemoved {
				phase = sandbox.PhaseStopped
			}
			return a.report(ctx, sb.ID, sandbox.Status{
				Phase:       phase,
				ContainerID: cid,
				UpdatedAt:   time.Now().UTC(),
				FinishedAt:  time.Now().UTC(),
			})
		}
		return a.report(ctx, sb.ID, sandbox.Status{
			Phase:     sandbox.PhaseStopped,
			UpdatedAt: time.Now().UTC(),
		})

	case sandbox.DesiredRunning:
		if a.Proxy != nil && sb.Spec.Network.Mode != sandbox.NetworkNone && sb.Spec.Network.Mode != "" {
			a.Proxy.SetPolicy(sb.ID, sb.Spec.Network)
		}
		if cid == "" {
			_ = a.report(ctx, sb.ID, sandbox.Status{
				Phase:     sandbox.PhaseStarting,
				UpdatedAt: time.Now().UTC(),
			})
			newID, err := a.Driver.CreateAndStart(ctx, sb, CreateOpts{
				DataDir:     a.DataDir,
				AgentBinary: a.AgentBinary,
			})
			if err != nil {
				return err
			}
			a.local[sb.ID] = newID
			cid = newID
			if err := a.setupEgress(ctx, sb, cid); err != nil {
				log.Printf("sandbox %s egress setup: %v", sb.ID, err)
			}
			return a.report(ctx, sb.ID, sandbox.Status{
				Phase:       sandbox.PhaseRunning,
				ContainerID: cid,
				StartedAt:   time.Now().UTC(),
				UpdatedAt:   time.Now().UTC(),
			})
		}
		phase, exit, err := a.Driver.InspectPhase(ctx, cid)
		if err != nil {
			// container gone — recreate
			delete(a.local, sb.ID)
			return fmt.Errorf("inspect: %w", err)
		}
		st := sandbox.Status{
			Phase:       phase,
			ContainerID: cid,
			ExitCode:    exit,
			UpdatedAt:   time.Now().UTC(),
		}
		if phase == sandbox.PhaseRunning {
			_ = a.setupEgress(ctx, sb, cid)
		}
		return a.report(ctx, sb.ID, st)
	}
	return nil
}

func (a *Agent) setupEgress(ctx context.Context, sb *sandbox.Sandbox, cid string) error {
	if sb.Spec.Network.Mode == sandbox.NetworkNone || sb.Spec.Network.Mode == "" {
		return nil
	}
	if a.Redirect == nil {
		return nil
	}
	ip, err := a.Driver.ContainerIP(ctx, cid)
	if err != nil || ip == "" {
		return err
	}
	return a.Redirect.EnsureSandbox(sb.ID, ip)
}

func (a *Agent) report(ctx context.Context, id string, st sandbox.Status) error {
	if a.Report == nil {
		return nil
	}
	return a.Report.UpdateStatus(ctx, id, st)
}

// LocalContainerID returns the cached container id for a sandbox.
func (a *Agent) LocalContainerID(sandboxID string) string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.local[sandboxID]
}

// StreamLogs writes demuxed logs to w.
func (a *Agent) StreamLogs(ctx context.Context, sandboxID string, follow bool, tail int64, w io.Writer) error {
	cid := a.LocalContainerID(sandboxID)
	if cid == "" {
		var err error
		cid, err = a.Driver.FindBySandboxID(ctx, sandboxID)
		if err != nil {
			return err
		}
		if cid == "" {
			return fmt.Errorf("sandbox container not found locally")
		}
	}
	tailStr := "all"
	if tail > 0 {
		tailStr = strconv.FormatInt(tail, 10)
	}
	rc, err := a.Driver.Logs(ctx, cid, follow, tailStr)
	if err != nil {
		return err
	}
	defer rc.Close()
	return DemuxLogs(rc, w, w)
}
