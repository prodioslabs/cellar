package runtime

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/prodioslabs/cellar/internal/egress"
	"github.com/prodioslabs/cellar/internal/sandbox"
)

type fakeStopRemove struct {
	mu      sync.Mutex
	stopped []string
	removed []string
}

func (f *fakeStopRemove) Stop(_ context.Context, containerID string, _ int) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.stopped = append(f.stopped, containerID)
	return nil
}

func (f *fakeStopRemove) Remove(_ context.Context, containerID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.removed = append(f.removed, containerID)
	return nil
}

func TestTeardownLocal(t *testing.T) {
	dataDir := t.TempDir()
	proxy := egress.NewProxy()
	redir := egress.NewRedirectManager(1234, 5678, 9012, 3456)

	fake := &fakeStopRemove{}
	a := NewAgent("node-1", nil, proxy, redir, nil, nil, dataDir, "")
	a.stopRemove = fake
	a.local = map[string]string{
		"sb-a": "cid-a",
		"sb-b": "cid-b",
	}

	for _, id := range []string{"sb-a", "sb-b"} {
		if _, err := PrepareSandboxDir(dataDir, id); err != nil {
			t.Fatal(err)
		}
		proxy.SetPolicy(id, sandbox.NetworkPolicy{Mode: sandbox.NetworkAllowlist})
	}
	proxy.BindSandboxIP("sb-a", "10.0.0.1")
	proxy.BindSandboxIP("sb-b", "10.0.0.2")
	redir.SeedSandbox("sb-a", "10.0.0.1")
	redir.SeedSandbox("sb-b", "10.0.0.2")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	a.TeardownLocal(ctx)

	fake.mu.Lock()
	defer fake.mu.Unlock()
	if len(fake.stopped) != 2 || len(fake.removed) != 2 {
		t.Fatalf("stopped=%v removed=%v", fake.stopped, fake.removed)
	}
	stopped := map[string]bool{}
	for _, id := range fake.stopped {
		stopped[id] = true
	}
	removed := map[string]bool{}
	for _, id := range fake.removed {
		removed[id] = true
	}
	for _, cid := range []string{"cid-a", "cid-b"} {
		if !stopped[cid] || !removed[cid] {
			t.Fatalf("missing stop/remove for %s: stopped=%v removed=%v", cid, fake.stopped, fake.removed)
		}
	}

	if len(a.local) != 0 {
		t.Fatalf("local not emptied: %v", a.local)
	}
	for _, id := range []string{"sb-a", "sb-b"} {
		if _, err := os.Stat(SandboxHostDir(dataDir, id)); !os.IsNotExist(err) {
			t.Fatalf("sandbox dir %s still present: %v", id, err)
		}
		if proxy.HasPolicy(id) {
			t.Fatalf("proxy policy still present for %s", id)
		}
		if _, ok := proxy.SandboxIP(id); ok {
			t.Fatalf("proxy IP binding still present for %s", id)
		}
		if redir.HasSandbox(id) {
			t.Fatalf("redirect rules still present for %s", id)
		}
	}

	// Ensure dirs were under dataDir (sanity).
	if entries, err := os.ReadDir(filepath.Join(dataDir, "sandboxes")); err == nil && len(entries) != 0 {
		t.Fatalf("sandboxes dir not empty: %v", entries)
	}
}
