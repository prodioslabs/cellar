// Package runtime is the node-local sandbox executor.
//
// Agent is the per-node reconciler: it turns Raft-assigned sandbox objects
// into Docker containers, optional egress topology, and status reports back
// to the control plane. Driver talks to the host Docker Engine. Exec, logs,
// and detached jobs are implemented against those containers.
//
// Agent is not the in-guest cellar-agent binary. That binary is bind-mounted
// into each sandbox and runs as PID 1; Driver injects it at create time.
package runtime

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"strconv"
	"sync"
	"time"

	"github.com/prodioslabs/cellar/internal/egress"
	"github.com/prodioslabs/cellar/internal/sandbox"
)

// StatusReporter pushes observed sandbox status to the control plane.
// cellard implements this by calling RuntimeAgent.UpdateSandboxStatus on the
// Raft leader (or applying locally when this node is the leader).
type StatusReporter interface {
	UpdateStatus(ctx context.Context, sandboxID string, generation int64, st sandbox.Status) error
}

// AssignmentSource yields sandboxes currently assigned to this node.
// cellard implements this from Raft when it is the leader, otherwise from
// the last RuntimeAgent.Heartbeat response.
type AssignmentSource interface {
	ListAssigned(ctx context.Context) ([]*sandbox.Sandbox, error)
}

// restartBackoff tracks exponential wait before the next container recreate.
type restartBackoff struct {
	attempts  int
	notBefore time.Time
	// reason is the failure that scheduled this backoff, surfaced in the
	// sandbox status while waiting so `cellar sandbox inspect` shows the
	// cause rather than only the countdown.
	reason string
}

// Agent reconciles this node's Docker containers with the sandboxes the
// control plane has assigned to it.
//
// cellard starts one Agent per node after Docker and the egress pool are
// ready, then calls [Agent.Run]. Each tick, Agent lists assigned sandboxes
// and drives local state toward DesiredState:
//
//   - DesiredRunning: create and start the container (and a private Docker
//     network plus egress-gateway leg when the spec is not NetworkNone),
//     report PhaseRunning, and keep egress policy in sync.
//   - DesiredStopped: stop the container and report PhaseStopped.
//   - DesiredRemoved: stop and remove the container, tear down egress, and
//     delete the host sandbox directory.
//
// Create and inspect failures use exponential backoff (5s–60s) before the
// next recreate. Unexpected container exits are reaped and retried the same
// way. Status is reported with the assignment generation so the leader can
// ignore stale updates after a reschedule.
//
// Agent is the host-side reconciler. The in-guest cellar-agent binary
// (PID 1 / job supervisor) is a separate process; AgentBinary is only the
// host path Driver bind-mounts into new containers.
type Agent struct {
	// NodeID is this node's cluster identity.
	NodeID string
	// Driver talks to the host Docker Engine.
	Driver *Driver
	// Pool is the shared egress-gateway container pool. Nil skips egress setup.
	Pool *egress.Pool
	// IPAM allocates per-sandbox /29 networks from the node supernet.
	IPAM *egress.Allocator
	// Source lists sandboxes assigned to this node.
	Source AssignmentSource
	// Report pushes observed status to the control plane.
	Report StatusReporter
	// DataDir holds per-sandbox host state (resolv.conf, egress.json, jobs).
	DataDir string
	// AgentBinary is the host path of the in-guest cellar-agent binary.
	// Empty lets Driver resolve it (CELLAR_AGENT_BINARY, data dir, or next
	// to the cellard executable).
	AgentBinary string

	mu       sync.Mutex
	local    map[string]string // sandboxID -> containerID
	restarts map[string]restartBackoff
	interval time.Duration

	// stopRemove overrides Driver for Stop/Remove when non-nil (unit tests).
	stopRemove stopRemover
}

// stopRemover is the Stop/Remove surface used by TeardownLocal and unassign.
type stopRemover interface {
	Stop(ctx context.Context, containerID string, timeoutSec int) error
	Remove(ctx context.Context, containerID string) error
}

// NewAgent constructs an Agent. Call [Agent.Run] to start reconciling.
// agentBinary may be empty; Driver then resolves the in-guest cellar-agent.
func NewAgent(nodeID string, drv *Driver, gwPool *egress.Pool, allocator *egress.Allocator, src AssignmentSource, rep StatusReporter, dataDir, agentBinary string) *Agent {
	return &Agent{
		NodeID:      nodeID,
		Driver:      drv,
		Pool:        gwPool,
		IPAM:        allocator,
		Source:      src,
		Report:      rep,
		DataDir:     dataDir,
		AgentBinary: agentBinary,
		local:       make(map[string]string),
		restarts:    make(map[string]restartBackoff),
		interval:    3 * time.Second,
	}
}

// nextRestartDelay returns the wait before the Nth recreate attempt (0-based).
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

// stopper returns the Stop/Remove implementation (test stub or Driver).
func (a *Agent) stopper() stopRemover {
	if a.stopRemove != nil {
		return a.stopRemove
	}
	if a.Driver != nil {
		return a.Driver
	}
	return nil
}

// Run reconciles once immediately, then on a 3s ticker until ctx is done.
// The first pass GCs labeled sandbox networks that are no longer allocated
// so IPAM matches Docker after a crash or restart.
func (a *Agent) Run(ctx context.Context) {
	if err := a.reconcileOrphans(ctx); err != nil {
		log.Printf("runtime agent orphan reconcile: %v", err)
	}
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

// reconcileOrphans syncs IPAM from live Docker sandbox networks so allocations
// match what survived a process restart.
func (a *Agent) reconcileOrphans(ctx context.Context) error {
	if a.Driver == nil || a.IPAM == nil {
		return nil
	}
	live, err := a.Driver.ListManagedSandboxNetworks(ctx)
	if err != nil {
		return err
	}
	return a.IPAM.SyncFromDocker(live)
}

// TeardownLocal stops and removes every container this node currently
// tracks, tears down their egress legs, and deletes host sandbox dirs.
// cellard calls this on drain, leave, and shutdown. It returns when every
// teardown finishes or ctx is cancelled.
func (a *Agent) TeardownLocal(ctx context.Context) {
	a.mu.Lock()
	locals := make(map[string]string, len(a.local))
	for id, cid := range a.local {
		locals[id] = cid
	}
	a.local = make(map[string]string)
	a.restarts = make(map[string]restartBackoff)
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
			a.teardownEgress(ctx, id)
			_ = CleanupSandboxDir(a.DataDir, id)
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

// reconcile applies DesiredState for every assigned sandbox and tears down
// local containers that are no longer assigned to this node.
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

	for id, cid := range a.local {
		if _, ok := want[id]; ok {
			continue
		}
		if s := a.stopper(); s != nil {
			_ = s.Stop(ctx, cid, 10)
			_ = s.Remove(ctx, cid)
		}
		a.teardownEgress(ctx, id)
		_ = CleanupSandboxDir(a.DataDir, id)
		delete(a.local, id)
		delete(a.restarts, id)
	}
	return nil
}

// reconcileOne drives one sandbox toward its DesiredState and reports status.
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
		delete(a.restarts, sb.ID)
		if cid != "" {
			if s := a.stopper(); s != nil {
				_ = s.Stop(ctx, cid, 10)
				if sb.DesiredState == sandbox.DesiredRemoved {
					_ = s.Remove(ctx, cid)
				}
			}
			if sb.DesiredState == sandbox.DesiredRemoved {
				a.teardownEgress(ctx, sb.ID)
				_ = CleanupSandboxDir(a.DataDir, sb.ID)
				delete(a.local, sb.ID)
			}
			return a.report(ctx, sb.ID, sb.AssignmentGeneration, sandbox.Status{
				Phase:       sandbox.PhaseStopped,
				ContainerID: cid,
				UpdatedAt:   time.Now().UTC(),
				FinishedAt:  time.Now().UTC(),
			})
		}
		if sb.DesiredState == sandbox.DesiredRemoved {
			a.teardownEgress(ctx, sb.ID)
		}
		return a.report(ctx, sb.ID, sb.AssignmentGeneration, sandbox.Status{
			Phase:     sandbox.PhaseStopped,
			UpdatedAt: time.Now().UTC(),
		})

	case sandbox.DesiredRunning:
		if cid == "" {
			return a.createDesiredRunning(ctx, sb)
		}
		phase, exit, err := a.Driver.InspectPhase(ctx, cid)
		if err != nil {
			delete(a.local, sb.ID)
			err = fmt.Errorf("inspect: %w", err)
			a.noteRestart(sb.ID, err)
			return err
		}
		if phase == sandbox.PhaseRunning {
			_ = a.ensurePolicy(ctx, sb)
			return a.report(ctx, sb.ID, sb.AssignmentGeneration, sandbox.Status{
				Phase:       sandbox.PhaseRunning,
				ContainerID: cid,
				UpdatedAt:   time.Now().UTC(),
			})
		}

		msg := fmt.Sprintf("container exited with code %d", exit)
		if phase == sandbox.PhaseStopped && exit == 0 {
			msg = "container stopped"
		}
		_ = a.report(ctx, sb.ID, sb.AssignmentGeneration, sandbox.Status{
			Phase:       phase,
			ContainerID: cid,
			ExitCode:    exit,
			Message:     msg,
			UpdatedAt:   time.Now().UTC(),
			FinishedAt:  time.Now().UTC(),
		})
		a.reapContainer(ctx, sb.ID, cid, errors.New(msg))
		return nil
	}
	return nil
}

// createDesiredRunning creates the container (and egress topology if needed)
// after honoring restart backoff. Failures schedule the next recreate.
func (a *Agent) createDesiredRunning(ctx context.Context, sb *sandbox.Sandbox) error {
	now := time.Now()
	if rb, ok := a.restarts[sb.ID]; ok && now.Before(rb.notBefore) {
		msg := fmt.Sprintf("waiting %s before recreate", rb.notBefore.Sub(now).Round(time.Second))
		if rb.reason != "" {
			msg = rb.reason + "; " + msg
		}
		return a.report(ctx, sb.ID, sb.AssignmentGeneration, sandbox.Status{
			Phase:     sandbox.PhaseFailed,
			Message:   msg,
			UpdatedAt: now.UTC(),
		})
	}

	_ = a.report(ctx, sb.ID, sb.AssignmentGeneration, sandbox.Status{
		Phase:     sandbox.PhaseStarting,
		UpdatedAt: now.UTC(),
	})

	opts := CreateOpts{
		DataDir:     a.DataDir,
		AgentBinary: a.AgentBinary,
	}

	if sb.Spec.Network.Mode != sandbox.NetworkNone && sb.Spec.Network.Mode != "" {
		egressOpts, err := a.setupTopology(ctx, sb)
		if err != nil {
			a.noteRestart(sb.ID, err)
			return err
		}
		opts.NetworkName = egressOpts.NetworkName
		opts.DNSServer = egressOpts.DNSServer
		opts.SandboxIP = egressOpts.SandboxIP
		opts.EgressImage = egressOpts.EgressImage
	}

	newID, err := a.Driver.CreateAndStart(ctx, sb, opts)
	if err != nil {
		if sb.Spec.Network.Mode != sandbox.NetworkNone && sb.Spec.Network.Mode != "" {
			a.teardownEgress(ctx, sb.ID)
		}
		a.noteRestart(sb.ID, err)
		return err
	}
	delete(a.restarts, sb.ID)
	a.local[sb.ID] = newID
	return a.report(ctx, sb.ID, sb.AssignmentGeneration, sandbox.Status{
		Phase:       sandbox.PhaseRunning,
		ContainerID: newID,
		StartedAt:   time.Now().UTC(),
		UpdatedAt:   time.Now().UTC(),
	})
}

// topologyOpts is the Docker create config produced by setupTopology.
type topologyOpts struct {
	NetworkName string
	DNSServer   string
	SandboxIP   string
	EgressImage string
}

// setupTopology allocates a /29, creates the sandbox Docker network, attaches
// an egress-gateway leg, and registers the sandbox's network policy.
func (a *Agent) setupTopology(ctx context.Context, sb *sandbox.Sandbox) (topologyOpts, error) {
	if a.Pool == nil || a.IPAM == nil {
		return topologyOpts{}, fmt.Errorf("egress pool/ipam not configured")
	}
	subnet, err := a.IPAM.Allocate(sb.ID)
	if err != nil {
		return topologyOpts{}, err
	}
	gwIP := egress.GatewayIP(subnet)
	sbIP := egress.SandboxIP(subnet)
	netID, err := a.Driver.CreateSandboxNetwork(ctx, sb.ID, subnet.String(), gwIP.String())
	if err != nil {
		_ = a.IPAM.Free(sb.ID)
		return topologyOpts{}, fmt.Errorf("create sandbox network: %w", err)
	}
	// Prefer the live Docker subnet when reusing an existing network so IPAM
	// and ConnectSandbox agree after a partial teardown left the net behind.
	if liveCIDR, err := a.Driver.SandboxNetworkSubnet(ctx, sb.ID); err == nil && liveCIDR != "" && liveCIDR != subnet.String() {
		if err := a.IPAM.Adopt(sb.ID, liveCIDR); err != nil {
			_ = a.Driver.RemoveSandboxNetwork(ctx, sb.ID)
			_ = a.IPAM.Free(sb.ID)
			return topologyOpts{}, fmt.Errorf("adopt live subnet: %w", err)
		}
		_, subnet, err = net.ParseCIDR(liveCIDR)
		if err != nil {
			return topologyOpts{}, err
		}
		gwIP = egress.GatewayIP(subnet)
		sbIP = egress.SandboxIP(subnet)
	}
	gw, err := a.Pool.Assign(ctx, sb.ID)
	if err != nil {
		_ = a.Driver.RemoveSandboxNetwork(ctx, sb.ID)
		_ = a.IPAM.Free(sb.ID)
		return topologyOpts{}, fmt.Errorf("assign gateway: %w", err)
	}
	if err := a.Pool.ConnectSandbox(ctx, gw, netID, gwIP.String()); err != nil {
		a.Pool.Release(sb.ID)
		_ = a.Driver.RemoveSandboxNetwork(ctx, sb.ID)
		_ = a.IPAM.Free(sb.ID)
		return topologyOpts{}, fmt.Errorf("connect gateway: %w", err)
	}
	if err := a.Pool.RegisterSandbox(ctx, gw, sb.ID, netID, subnet.String(), gwIP.String(), sb.Spec.Network); err != nil {
		_ = a.Pool.DisconnectSandbox(ctx, gw, netID)
		a.Pool.Release(sb.ID)
		_ = a.Driver.RemoveSandboxNetwork(ctx, sb.ID)
		_ = a.IPAM.Free(sb.ID)
		return topologyOpts{}, fmt.Errorf("register sandbox: %w", err)
	}
	_ = WriteEgressState(a.DataDir, sb.ID, EgressState{
		GatewayID:  gw.ID,
		SubnetCIDR: subnet.String(),
		NetworkID:  netID,
		GatewayIP:  gwIP.String(),
		SandboxIP:  sbIP.String(),
	})
	return topologyOpts{
		NetworkName: SandboxNetworkName(sb.ID),
		DNSServer:   gwIP.String(),
		SandboxIP:   sbIP.String(),
		EgressImage: a.Pool.Image(),
	}, nil
}

// teardownEgress deregisters the sandbox from its gateway, removes the
// Docker network, and frees the IPAM allocation.
func (a *Agent) teardownEgress(ctx context.Context, sandboxID string) {
	st, ok, _ := ReadEgressState(a.DataDir, sandboxID)
	var gw *egress.Instance
	if a.Pool != nil {
		if g, found := a.Pool.GatewayFor(sandboxID); found {
			gw = g
		} else if ok && st.GatewayID != "" {
			gw, _ = a.Pool.GatewayFor(sandboxID)
		}
		if gw != nil {
			_ = a.Pool.DeregisterSandbox(ctx, gw, sandboxID)
			netID := st.NetworkID
			if netID == "" && a.Driver != nil {
				netID, _ = a.Driver.FindSandboxNetworkID(ctx, sandboxID)
			}
			if netID != "" {
				_ = a.Pool.DisconnectSandbox(ctx, gw, netID)
			}
		}
		a.Pool.Release(sandboxID)
	}
	if a.Driver != nil {
		_ = a.Driver.RemoveSandboxNetwork(ctx, sandboxID)
	}
	if a.IPAM != nil {
		_ = a.IPAM.Free(sandboxID)
	}
}

// ensurePolicy refreshes the egress-gateway policy for a running sandbox so
// committed spec changes take effect even if ApplyNetworkPolicy was missed.
func (a *Agent) ensurePolicy(ctx context.Context, sb *sandbox.Sandbox) error {
	if sb.Spec.Network.Mode == sandbox.NetworkNone || sb.Spec.Network.Mode == "" {
		return nil
	}
	if a.Pool == nil {
		return nil
	}
	gw, ok := a.Pool.GatewayFor(sb.ID)
	if !ok {
		return nil
	}
	return a.Pool.UpdatePolicy(ctx, gw, sb.ID, sb.Spec.Network)
}

// reapContainer removes an exited container, tears down egress, and starts
// restart backoff so the next tick can recreate it.
func (a *Agent) reapContainer(ctx context.Context, sandboxID, cid string, reason error) {
	if s := a.stopper(); s != nil {
		_ = s.Remove(ctx, cid)
	}
	a.teardownEgress(ctx, sandboxID)
	delete(a.local, sandboxID)
	a.noteRestart(sandboxID, reason)
}

// noteRestart records a failed or unexpected exit and sets the next recreate
// time. reason is kept so the status reported during the wait explains it.
func (a *Agent) noteRestart(sandboxID string, reason error) {
	prev := a.restarts[sandboxID]
	attempts := prev.attempts
	var why string
	if reason != nil {
		why = reason.Error()
	}
	a.restarts[sandboxID] = restartBackoff{
		attempts:  attempts + 1,
		notBefore: time.Now().Add(nextRestartDelay(attempts)),
		reason:    why,
	}
}

// ApplyNetworkPolicy installs an already-committed policy on a locally running
// sandbox so it takes effect without waiting for the next reconcile tick.
func (a *Agent) ApplyNetworkPolicy(ctx context.Context, sandboxID string, policy sandbox.NetworkPolicy) error {
	if a.Pool == nil {
		return fmt.Errorf("egress pool not running on this node")
	}
	a.mu.Lock()
	_, local := a.local[sandboxID]
	a.mu.Unlock()
	if !local {
		return fmt.Errorf("sandbox %s is not running on this node", sandboxID)
	}
	if policy.Mode == sandbox.NetworkNone || policy.Mode == "" {
		return nil
	}
	gw, ok := a.Pool.GatewayFor(sandboxID)
	if !ok {
		return fmt.Errorf("sandbox %s has no egress gateway assignment", sandboxID)
	}
	return a.Pool.UpdatePolicy(ctx, gw, sandboxID, policy)
}

// report forwards observed status to the control plane when a reporter is set.
func (a *Agent) report(ctx context.Context, id string, generation int64, st sandbox.Status) error {
	if a.Report == nil {
		return nil
	}
	return a.Report.UpdateStatus(ctx, id, generation, st)
}

// LocalContainerID returns the cached Docker container ID for a sandbox
// this Agent currently tracks, or empty if it is not local.
func (a *Agent) LocalContainerID(sandboxID string) string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.local[sandboxID]
}

// StreamLogs copies demuxed stdout/stderr from the sandbox container to w.
// follow keeps the stream open; tail limits how many historical lines to
// include (0 means all).
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
