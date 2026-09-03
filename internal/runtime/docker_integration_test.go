package runtime

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/network"

	"github.com/prodioslabs/cellar/internal/egress"
	"github.com/prodioslabs/cellar/internal/sandbox"
)

func TestCreateAndStartWithNonRootImage(t *testing.T) {
	if os.Getenv("CELLAR_INTEGRATION") != "1" {
		t.Skip("set CELLAR_INTEGRATION=1 to run Docker integration tests")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()

	driver, err := NewDriver()
	if err != nil {
		t.Fatal(err)
	}
	defer driver.Close()

	agentBinary, err := filepath.Abs("../../bin/cellar-agent")
	if err != nil {
		t.Fatal(err)
	}
	sb := &sandbox.Sandbox{
		ID: "integration-curl-agent",
		Spec: sandbox.Spec{
			Image: "curlimages/curl",
			Network: sandbox.NetworkPolicy{
				Mode: sandbox.NetworkNone,
			},
		},
	}

	containerID, err := driver.CreateAndStart(ctx, sb, CreateOpts{
		DataDir:     t.TempDir(),
		AgentBinary: agentBinary,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = driver.Remove(context.Background(), containerID)
	})

	inspect, err := driver.cli.ContainerInspect(ctx, containerID)
	if err != nil {
		t.Fatal(err)
	}
	if inspect.Config == nil {
		t.Fatal("container config is missing")
	}
	if inspect.Config.User != "0:0" {
		t.Fatalf("container user: got %q, want 0:0", inspect.Config.User)
	}
	if inspect.State == nil || !inspect.State.Running {
		t.Fatalf("container is not running: %#v", inspect.State)
	}
}

type memAssignments struct {
	mu sync.Mutex
	sb []*sandbox.Sandbox
}

func (m *memAssignments) ListAssigned(_ context.Context) ([]*sandbox.Sandbox, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]*sandbox.Sandbox, len(m.sb))
	copy(out, m.sb)
	return out, nil
}

type memReporter struct {
	mu       sync.Mutex
	statuses []sandbox.Status
}

func (m *memReporter) UpdateStatus(_ context.Context, _ string, _ int64, st sandbox.Status) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.statuses = append(m.statuses, st)
	return nil
}

func (m *memReporter) phases() []sandbox.Phase {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]sandbox.Phase, len(m.statuses))
	for i, st := range m.statuses {
		out[i] = st.Phase
	}
	return out
}

func TestReconcileReapsDeadContainer(t *testing.T) {
	if os.Getenv("CELLAR_INTEGRATION") != "1" {
		t.Skip("set CELLAR_INTEGRATION=1 to run Docker integration tests")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	driver, err := NewDriver()
	if err != nil {
		t.Fatal(err)
	}
	defer driver.Close()

	agentBinary, err := filepath.Abs("../../bin/cellar-agent")
	if err != nil {
		t.Fatal(err)
	}
	if err := driver.pullIfMissing(ctx, "alpine"); err != nil {
		t.Fatal(err)
	}

	const sandboxID = "integration-reap-dead"
	_ = driver.Remove(ctx, "cellar-sb-"+sandboxID)

	dead, err := driver.cli.ContainerCreate(ctx, &container.Config{
		Image:      "alpine",
		Entrypoint: []string{"/bin/true"},
		Labels:     map[string]string{labelSandboxID: sandboxID},
	}, &container.HostConfig{
		NetworkMode: "none",
	}, &network.NetworkingConfig{}, nil, "cellar-sb-"+sandboxID)
	if err != nil {
		t.Fatal(err)
	}
	if err := driver.cli.ContainerStart(ctx, dead.ID, container.StartOptions{}); err != nil {
		_ = driver.Remove(ctx, dead.ID)
		t.Fatal(err)
	}
	// Wait until exited.
	for i := 0; i < 50; i++ {
		phase, _, err := driver.InspectPhase(ctx, dead.ID)
		if err == nil && phase != sandbox.PhaseRunning {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}

	sb := &sandbox.Sandbox{
		ID:           sandboxID,
		DesiredState: sandbox.DesiredRunning,
		Spec: sandbox.Spec{
			Image: "alpine",
			Network: sandbox.NetworkPolicy{
				Mode: sandbox.NetworkNone,
			},
		},
	}
	src := &memAssignments{sb: []*sandbox.Sandbox{sb}}
	rep := &memReporter{}
	dataDir := t.TempDir()
	agent := NewAgent("node-test", driver, nil, nil, src, rep, dataDir, agentBinary)

	if err := agent.reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	if agent.LocalContainerID(sandboxID) != "" {
		t.Fatalf("expected dead cid cleared after reap, got %q", agent.LocalContainerID(sandboxID))
	}
	phases := rep.phases()
	foundFailed := false
	for _, p := range phases {
		if p == sandbox.PhaseFailed || p == sandbox.PhaseStopped {
			foundFailed = true
			break
		}
	}
	if !foundFailed {
		t.Fatalf("expected failed/stopped status before recreate, got %v", phases)
	}

	// Skip backoff so the next tick can recreate immediately.
	agent.mu.Lock()
	delete(agent.restarts, sandboxID)
	agent.mu.Unlock()

	if err := agent.reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	newID := agent.LocalContainerID(sandboxID)
	if newID == "" {
		t.Fatal("expected recreated container")
	}
	t.Cleanup(func() {
		_ = driver.Remove(context.Background(), newID)
	})
	if newID == dead.ID {
		t.Fatalf("expected a new container id, still %s", newID)
	}
	phase, _, err := driver.InspectPhase(ctx, newID)
	if err != nil {
		t.Fatal(err)
	}
	if phase != sandbox.PhaseRunning {
		t.Fatalf("phase: got %s want running", phase)
	}
	phases = rep.phases()
	if phases[len(phases)-1] != sandbox.PhaseRunning {
		t.Fatalf("last reported phase: got %v", phases)
	}
}

func TestReconcileEssentialServicesCreate(t *testing.T) {
	if os.Getenv("CELLAR_INTEGRATION") != "1" {
		t.Skip("set CELLAR_INTEGRATION=1 to run Docker integration tests")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	driver, err := NewDriver()
	if err != nil {
		t.Fatal(err)
	}
	defer driver.Close()

	if _, _, err := driver.cli.ImageInspectWithRaw(ctx, "cellar/egress-gateway"); err != nil {
		t.Skip("cellar/egress-gateway image not loaded (make egress-gateway-image)")
	}

	agentBinary, err := filepath.Abs("../../bin/cellar-agent")
	if err != nil {
		t.Fatal(err)
	}
	if err := driver.pullIfMissing(ctx, "alpine"); err != nil {
		t.Fatal(err)
	}

	const sandboxID = "integration-essentials"
	_ = driver.Remove(ctx, "cellar-sb-"+sandboxID)
	_ = driver.RemoveSandboxNetwork(ctx, sandboxID)

	dataDir := t.TempDir()
	ipam, err := egress.NewAllocator(dataDir, "172.31.0.0/16")
	if err != nil {
		t.Fatal(err)
	}
	pool := egress.NewPool(driver.Client(), egress.PoolConfig{
		DataDir: dataDir,
		Image:   "cellar/egress-gateway",
	})
	if err := pool.EnsureReady(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { pool.Close(context.Background()) })

	sb := &sandbox.Sandbox{
		ID:                   sandboxID,
		DesiredState:         sandbox.DesiredRunning,
		AssignmentGeneration: 1,
		Spec: sandbox.Spec{
			Image: "alpine",
			Network: sandbox.NetworkPolicy{
				Mode:              sandbox.NetworkBlockAll,
				EssentialServices: true,
				DNS:               sandbox.DNSPolicy{Mode: sandbox.DNSNone},
			},
		},
	}
	src := &memAssignments{sb: []*sandbox.Sandbox{sb}}
	rep := &memReporter{}
	agent := NewAgent("node-test", driver, pool, ipam, src, rep, dataDir, agentBinary)

	if err := agent.reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	cid := agent.LocalContainerID(sandboxID)
	if cid == "" {
		t.Fatalf("expected container, phases=%v", rep.phases())
	}
	t.Cleanup(func() {
		_ = driver.Remove(context.Background(), cid)
		_ = driver.RemoveSandboxNetwork(context.Background(), sandboxID)
	})
	phase, _, err := driver.InspectPhase(ctx, cid)
	if err != nil {
		t.Fatal(err)
	}
	if phase != sandbox.PhaseRunning {
		t.Fatalf("phase: got %s want running (status=%v)", phase, rep.phases())
	}
	phases := rep.phases()
	if phases[len(phases)-1] != sandbox.PhaseRunning {
		t.Fatalf("last reported phase: got %v", phases)
	}
}
