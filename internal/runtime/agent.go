package runtime

import (
	"context"
	"fmt"
	"io"
	"log"
	"net"
	"strconv"
	"sync"
	"time"

	"github.com/prodioslabs/cellar/internal/egress/ipam"
	"github.com/prodioslabs/cellar/internal/egress/pool"
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

type restartBackoff struct {
	attempts  int
	notBefore time.Time
}

// Agent reconciles local Docker containers with assigned sandboxes.
type Agent struct {
	NodeID      string
	Driver      *Driver
	Pool        *pool.Pool
	IPAM        *ipam.Allocator
	Source      AssignmentSource
	Report      StatusReporter
	DataDir     string
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

// NewAgent constructs a runtime agent.
func NewAgent(nodeID string, drv *Driver, gwPool *pool.Pool, allocator *ipam.Allocator, src AssignmentSource, rep StatusReporter, dataDir, agentBinary string) *Agent {
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

// reconcileOrphans GCs labeled sandbox networks not in desired set and syncs IPAM.
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

// TeardownLocal stops and removes every container this node manages.
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
			return a.report(ctx, sb.ID, sandbox.Status{
				Phase:       sandbox.PhaseStopped,
				ContainerID: cid,
				UpdatedAt:   time.Now().UTC(),
				FinishedAt:  time.Now().UTC(),
			})
		}
		if sb.DesiredState == sandbox.DesiredRemoved {
			a.teardownEgress(ctx, sb.ID)
		}
		return a.report(ctx, sb.ID, sandbox.Status{
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
			a.noteRestart(sb.ID)
			return fmt.Errorf("inspect: %w", err)
		}
		if phase == sandbox.PhaseRunning {
			_ = a.ensurePolicy(ctx, sb)
			return a.report(ctx, sb.ID, sandbox.Status{
				Phase:       sandbox.PhaseRunning,
				ContainerID: cid,
				UpdatedAt:   time.Now().UTC(),
			})
		}

		msg := fmt.Sprintf("container exited with code %d", exit)
		if phase == sandbox.PhaseStopped && exit == 0 {
			msg = "container stopped"
		}
		_ = a.report(ctx, sb.ID, sandbox.Status{
			Phase:       phase,
			ContainerID: cid,
			ExitCode:    exit,
			Message:     msg,
			UpdatedAt:   time.Now().UTC(),
			FinishedAt:  time.Now().UTC(),
		})
		a.reapContainer(ctx, sb.ID, cid)
		return nil
	}
	return nil
}

func (a *Agent) createDesiredRunning(ctx context.Context, sb *sandbox.Sandbox) error {
	now := time.Now()
	if rb, ok := a.restarts[sb.ID]; ok && now.Before(rb.notBefore) {
		return a.report(ctx, sb.ID, sandbox.Status{
			Phase:     sandbox.PhaseFailed,
			Message:   fmt.Sprintf("waiting %s before recreate", rb.notBefore.Sub(now).Round(time.Second)),
			UpdatedAt: now.UTC(),
		})
	}

	_ = a.report(ctx, sb.ID, sandbox.Status{
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
			a.noteRestart(sb.ID)
			return err
		}
		opts.NetworkName = egressOpts.NetworkName
		opts.DNSServer = egressOpts.DNSServer
		opts.SandboxIP = egressOpts.SandboxIP
	}

	newID, err := a.Driver.CreateAndStart(ctx, sb, opts)
	if err != nil {
		if sb.Spec.Network.Mode != sandbox.NetworkNone && sb.Spec.Network.Mode != "" {
			a.teardownEgress(ctx, sb.ID)
		}
		a.noteRestart(sb.ID)
		return err
	}
	delete(a.restarts, sb.ID)
	a.local[sb.ID] = newID
	return a.report(ctx, sb.ID, sandbox.Status{
		Phase:       sandbox.PhaseRunning,
		ContainerID: newID,
		StartedAt:   time.Now().UTC(),
		UpdatedAt:   time.Now().UTC(),
	})
}

type topologyOpts struct {
	NetworkName string
	DNSServer   string
	SandboxIP   string
}

func (a *Agent) setupTopology(ctx context.Context, sb *sandbox.Sandbox) (topologyOpts, error) {
	if a.Pool == nil || a.IPAM == nil {
		return topologyOpts{}, fmt.Errorf("egress pool/ipam not configured")
	}
	subnet, err := a.IPAM.Allocate(sb.ID)
	if err != nil {
		return topologyOpts{}, err
	}
	gwIP := ipam.GatewayIP(subnet)
	sbIP := ipam.SandboxIP(subnet)
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
		gwIP = ipam.GatewayIP(subnet)
		sbIP = ipam.SandboxIP(subnet)
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
	}, nil
}

func (a *Agent) teardownEgress(ctx context.Context, sandboxID string) {
	st, ok, _ := ReadEgressState(a.DataDir, sandboxID)
	var gw *pool.Instance
	if a.Pool != nil {
		if g, found := a.Pool.GatewayFor(sandboxID); found {
			gw = g
		} else if ok && st.GatewayID != "" {
			// Best-effort: try GatewayFor after SetAssignment recovery
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

func (a *Agent) reapContainer(ctx context.Context, sandboxID, cid string) {
	if s := a.stopper(); s != nil {
		_ = s.Remove(ctx, cid)
	}
	a.teardownEgress(ctx, sandboxID)
	delete(a.local, sandboxID)
	a.noteRestart(sandboxID)
}

func (a *Agent) noteRestart(sandboxID string) {
	prev := a.restarts[sandboxID]
	attempts := prev.attempts
	a.restarts[sandboxID] = restartBackoff{
		attempts:  attempts + 1,
		notBefore: time.Now().Add(nextRestartDelay(attempts)),
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
