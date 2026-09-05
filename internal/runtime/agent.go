// Package runtime is the node-local sandbox executor.
//
// Agent is the per-node reconciler: it turns Raft-assigned sandbox objects
// into local microsandbox VMs and status reports back to the control plane.
package runtime

import (
	"context"
	"errors"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/prodioslabs/cellar/internal/sandbox"
)

// StatusReporter pushes observed sandbox status to the control plane.
type StatusReporter interface {
	UpdateStatus(ctx context.Context, sandboxID string, generation int64, st sandbox.Status) error
}

// AssignmentSource yields sandboxes currently assigned to this node.
type AssignmentSource interface {
	ListAssigned(ctx context.Context) ([]*sandbox.Sandbox, error)
}

type restartBackoff struct {
	attempts  int
	notBefore time.Time
	reason    string
}

// Agent reconciles this node's microsandbox VMs with assigned sandboxes.
type Agent struct {
	NodeID  string
	Driver  *Driver
	Source  AssignmentSource
	Report  StatusReporter
	DataDir string

	mu       sync.Mutex
	local    map[string]struct{} // sandboxIDs tracked locally
	restarts map[string]restartBackoff
	interval time.Duration

	stopRemove stopRemover
}

type stopRemover interface {
	Stop(ctx context.Context, sandboxID string, timeoutSec int) error
	Remove(ctx context.Context, sandboxID string) error
}

// NewAgent constructs an Agent.
func NewAgent(nodeID string, drv *Driver, src AssignmentSource, rep StatusReporter, dataDir string) *Agent {
	return &Agent{
		NodeID:   nodeID,
		Driver:   drv,
		Source:   src,
		Report:   rep,
		DataDir:  dataDir,
		local:    make(map[string]struct{}),
		restarts: make(map[string]restartBackoff),
		interval: 3 * time.Second,
	}
}

func nextRestartDelay(attempts int) time.Duration {
	const (
		base = 5 * time.Second
		cap  = 60 * time.Second
	)
	if attempts < 0 {
		attempts = 0
	}
	d := base
	for i := 0; i < attempts; i++ {
		if d >= cap {
			return cap
		}
		d *= 2
	}
	if d > cap {
		return cap
	}
	return d
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

// Run reconciles until ctx is done.
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

// TeardownLocal stops and destroys every sandbox this node tracks.
func (a *Agent) TeardownLocal(ctx context.Context) {
	a.mu.Lock()
	ids := make([]string, 0, len(a.local))
	for id := range a.local {
		ids = append(ids, id)
	}
	a.local = make(map[string]struct{})
	a.restarts = make(map[string]restartBackoff)
	a.mu.Unlock()

	if len(ids) == 0 {
		return
	}
	var wg sync.WaitGroup
	for _, id := range ids {
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			if s := a.stopper(); s != nil {
				_ = s.Stop(ctx, id, 5)
				_ = s.Remove(ctx, id)
			}
		}(id)
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
			_ = a.report(ctx, id, sb.AssignmentGeneration, sandbox.Status{
				Phase:     sandbox.PhaseFailed,
				Message:   err.Error(),
				UpdatedAt: time.Now().UTC(),
			})
		}
	}

	for id := range a.local {
		if _, ok := want[id]; ok {
			continue
		}
		if s := a.stopper(); s != nil {
			_ = s.Stop(ctx, id, 10)
			_ = s.Remove(ctx, id)
		}
		delete(a.local, id)
		delete(a.restarts, id)
	}
	return nil
}

func (a *Agent) reconcileOne(ctx context.Context, sb *sandbox.Sandbox) error {
	_, tracked := a.local[sb.ID]
	if !tracked && a.Driver != nil {
		if found, err := a.Driver.FindBySandboxID(ctx, sb.ID); err == nil && found != "" {
			tracked = true
			a.local[sb.ID] = struct{}{}
		}
	}

	switch sb.DesiredState {
	case sandbox.DesiredRemoved, sandbox.DesiredStopped:
		delete(a.restarts, sb.ID)
		if tracked {
			if s := a.stopper(); s != nil {
				_ = s.Stop(ctx, sb.ID, 10)
				if sb.DesiredState == sandbox.DesiredRemoved {
					_ = s.Remove(ctx, sb.ID)
					delete(a.local, sb.ID)
				}
			}
			return a.report(ctx, sb.ID, sb.AssignmentGeneration, sandbox.Status{
				Phase:     sandbox.PhaseStopped,
				LocalName: LocalName(sb.ID),
				UpdatedAt: time.Now().UTC(),
				StoppedAt: time.Now().UTC(),
			})
		}
		return a.report(ctx, sb.ID, sb.AssignmentGeneration, sandbox.Status{
			Phase:     sandbox.PhaseStopped,
			UpdatedAt: time.Now().UTC(),
		})

	case sandbox.DesiredRunning:
		if !tracked {
			return a.createDesiredRunning(ctx, sb)
		}
		phase, err := a.Driver.InspectPhase(ctx, sb.ID)
		if err != nil {
			delete(a.local, sb.ID)
			err = fmt.Errorf("inspect: %w", err)
			a.noteRestart(sb.ID, err)
			return err
		}
		if phase == sandbox.PhaseRunning {
			return a.report(ctx, sb.ID, sb.AssignmentGeneration, sandbox.Status{
				Phase:     sandbox.PhaseRunning,
				LocalName: LocalName(sb.ID),
				UpdatedAt: time.Now().UTC(),
				StartedAt: time.Now().UTC(),
			})
		}
		msg := "sandbox not running"
		_ = a.report(ctx, sb.ID, sb.AssignmentGeneration, sandbox.Status{
			Phase:     phase,
			LocalName: LocalName(sb.ID),
			Message:   msg,
			UpdatedAt: time.Now().UTC(),
			StoppedAt: time.Now().UTC(),
		})
		a.reap(ctx, sb.ID, errors.New(msg))
		return nil
	}
	return nil
}

func (a *Agent) createDesiredRunning(ctx context.Context, sb *sandbox.Sandbox) error {
	now := time.Now()
	if rb, ok := a.restarts[sb.ID]; ok && now.Before(rb.notBefore) {
		msg := fmt.Sprintf("waiting %s before recreate", rb.notBefore.Sub(now).Round(time.Second))
		if rb.reason != "" {
			msg = rb.reason + "; " + msg
		}
		return a.report(ctx, sb.ID, sb.AssignmentGeneration, sandbox.Status{
			Phase:     sandbox.PhaseStarting,
			Message:   msg,
			UpdatedAt: now.UTC(),
		})
	}
	_ = a.report(ctx, sb.ID, sb.AssignmentGeneration, sandbox.Status{
		Phase:     sandbox.PhaseStarting,
		UpdatedAt: now.UTC(),
	})
	if a.Driver == nil {
		return fmt.Errorf("no runtime driver")
	}
	if err := a.Driver.CreateOrConnect(ctx, sb, true); err != nil {
		a.noteRestart(sb.ID, err)
		return err
	}
	a.local[sb.ID] = struct{}{}
	delete(a.restarts, sb.ID)
	return a.report(ctx, sb.ID, sb.AssignmentGeneration, sandbox.Status{
		Phase:     sandbox.PhaseRunning,
		LocalName: LocalName(sb.ID),
		StartedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
	})
}

func (a *Agent) noteRestart(id string, err error) {
	rb := a.restarts[id]
	rb.reason = err.Error()
	rb.notBefore = time.Now().Add(nextRestartDelay(rb.attempts))
	rb.attempts++
	a.restarts[id] = rb
}

func (a *Agent) reap(ctx context.Context, sandboxID string, cause error) {
	if s := a.stopper(); s != nil {
		_ = s.Stop(ctx, sandboxID, 5)
		_ = s.Remove(ctx, sandboxID)
	}
	delete(a.local, sandboxID)
	a.noteRestart(sandboxID, cause)
}

func (a *Agent) report(ctx context.Context, sandboxID string, gen int64, st sandbox.Status) error {
	if a.Report == nil {
		return nil
	}
	return a.Report.UpdateStatus(ctx, sandboxID, gen, st)
}
